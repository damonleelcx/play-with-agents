package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/notify"
	"github.com/damonleelcx/play-with-agents/internal/persona"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

type Worker struct {
	ID      string
	Store   *Store
	Model   *Model
	Planner *Planner
	LLM     string
	Mailer  mail.Mailer
	Lease   time.Duration
	// Wake is signalled when new work may exist (LISTEN play_work), so an idle
	// worker does not wait out its poll interval.
	Wake <-chan struct{}
	// Hook, if set, runs after each checkpoint. Tests use it to crash a worker
	// at a precise point.
	Hook func(t *Task, step int)
}

const defaultMaxSteps = 12

func NewWorkerID() string {
	h, _ := os.Hostname()
	return fmt.Sprintf("%s-%d-%04d", h, os.Getpid(), rand.Intn(10000))
}

// Run claims and executes tasks until ctx is cancelled. Shutdown is graceful:
// the task in hand stops at its next step boundary and is released, so the
// next worker resumes from the last checkpoint rather than from scratch.
func (w *Worker) Run(ctx context.Context) {
	slog.Info("worker started", "id", w.ID)
	for ctx.Err() == nil {
		t, err := w.Store.Claim(ctx, w.ID, w.Lease)
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("claim", "err", err)
			}
			sleepCtx(ctx, 2*time.Second)
			continue
		}
		if t == nil {
			select {
			case <-ctx.Done():
			case <-w.Wake:
			case <-time.After(3 * time.Second):
			}
			continue
		}
		w.Execute(ctx, t)
	}
	slog.Info("worker stopped", "id", w.ID)
}

// Execute runs one leased task to a terminal or parked state.
func (w *Worker) Execute(parent context.Context, t *Task) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	g, err := w.Store.Goal(ctx, t.GoalID)
	if err != nil {
		slog.Error("load goal", "task", t.ID, "err", err)
		_, _ = w.Store.Retry(context.WithoutCancel(ctx), t, err, 5*time.Second)
		return
	}

	// Heartbeat: extend the lease while we work; if it is lost, stop — the
	// task belongs to someone else now, and every fenced write would fail.
	lost := make(chan struct{})
	go func() {
		tick := time.NewTicker(w.Lease / 3)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := w.Store.Heartbeat(ctx, t.ID, t.LeaseEpoch, w.Lease); err != nil {
					if errors.Is(err, ErrLeaseLost) {
						close(lost)
						cancel()
						return
					}
				}
			}
		}
	}()

	w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.started", map[string]any{"task": t.Title, "attempt": t.Attempts, "worker": w.ID})
	start := time.Now()

	var out map[string]any
	handled := false
	if h := planHook(g.Skill); h != nil && (t.Kind == "finish" || (t.Kind == "plan" && t.Spec.Mode != "initial")) {
		handled, err = h(ctx, w.Planner, g, t)
	}
	switch {
	case handled:
	case err != nil:
	case t.Kind == "plan":
		switch t.Spec.Mode {
		case "initial":
			err = w.Planner.Initial(ctx, g, t)
		default:
			err = w.Planner.Replan(ctx, g, t)
		}
	case t.Kind == "finish":
		err = w.Planner.Finish(ctx, g, t)
	case t.Kind == "wait":
		// A wait task is claimable only once its run_after passed; being here
		// IS the wake-up.
		err = w.Store.Complete(ctx, t, map[string]any{"summary": "waited " + fmt.Sprint(t.Spec.WaitDays) + " days"})
	case t.Kind == "llm":
		out, err = w.runLLM(ctx, g, t)
		if err == nil && out != nil {
			err = w.Store.Complete(ctx, t, out)
		}
	case t.Kind == "tool":
		out, err = w.runTool(ctx, g, t)
		if err == nil {
			err = w.Store.Complete(ctx, t, out)
		}
	default:
		err = fmt.Errorf("unknown task kind %q", t.Kind)
	}

	bg := context.WithoutCancel(ctx)
	select {
	case <-lost:
		w.Store.Event(bg, g.UserID, g.ID, t.ID, "task.lease_lost", map[string]any{"task": t.Title, "worker": w.ID,
			"why": "lease expired or was taken over; another worker resumes from the last checkpoint"})
		return
	default:
	}
	w.settle(bg, parent, g, t, out, err, time.Since(start))
}

// settle records the outcome and decides retry / fail / park.
func (w *Worker) settle(ctx, parent context.Context, g *Goal, t *Task, out map[string]any, err error, took time.Duration) {
	var parked *errParked
	var budget *ErrBudget
	switch {
	case err == nil:
		if out != nil || (t.Kind != "llm" && t.Kind != "tool") {
			data := map[string]any{"task": t.Title, "ms": took.Milliseconds()}
			if sm, ok := out["summary"].(string); ok && sm != "" {
				data["summary"] = truncate(sm, 300)
			}
			w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.succeeded", data)
		}
	case errors.As(err, &parked):
		// Already persisted; nothing to settle.
	case errors.Is(err, ErrLeaseLost):
		w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.lease_lost", map[string]any{"task": t.Title})
	case errors.Is(err, errStopped):
		// Pause, cancel or shutdown: hand the task back without counting the
		// attempt. A cancelled goal already cancelled the task row.
		_ = w.Store.Release(ctx, t)
		why := "worker shutting down"
		if parent.Err() == nil {
			why = "goal paused or cancelled"
		}
		w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.released", map[string]any{"task": t.Title, "why": why})
	case errors.As(err, &budget):
		_ = w.Store.Release(ctx, t)
		_ = w.Store.SetGoalStatus(ctx, g.ID, "needs_attention", budget.Reason)
		w.Store.Event(ctx, g.UserID, g.ID, t.ID, "budget.exceeded", map[string]any{"task": t.Title, "why": budget.Reason})
	case errors.Is(err, tools.ErrAmbiguous):
		_ = w.Store.Fail(ctx, t, err)
		_ = w.Store.SetGoalStatus(ctx, g.ID, "needs_attention", "a side effect may or may not have happened in "+t.Title+"; check before retrying")
		w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.failed", map[string]any{"task": t.Title, "error": err.Error(), "retry": false})
	default:
		permanent := errors.Is(err, llm.ErrPermanent) || errors.Is(err, errPermanentTask)
		delay := time.Duration(1<<min(t.Attempts, 9)) * 2 * time.Second // 2s … ~17min
		if delay > 10*time.Minute {
			delay = 10 * time.Minute
		}
		delay += time.Duration(rand.Int63n(int64(delay / 2)))
		retrying := false
		if !permanent {
			retrying, _ = w.Store.Retry(ctx, t, err, delay)
		} else {
			_ = w.Store.Fail(ctx, t, err)
		}
		w.Store.Event(ctx, g.UserID, g.ID, t.ID, map[bool]string{true: "task.retry", false: "task.failed"}[retrying],
			map[string]any{"task": t.Title, "error": truncate(err.Error(), 500), "attempt": t.Attempts, "next_in_s": int(delay.Seconds())})
		if !retrying {
			// A failed task does not fail the goal: the replanner decides —
			// retry differently, route around it, or ask a person.
			if t.Kind == "plan" && t.Spec.Mode == "initial" {
				_ = w.Store.SetGoalStatus(ctx, g.ID, "needs_attention", "could not plan: "+truncate(err.Error(), 200))
			} else {
				_, _ = w.Store.EnqueuePlan(ctx, g.ID, fmt.Sprintf("replan-%d-%s", g.PlanVersion, t.Key), "replan",
					fmt.Sprintf("task %q failed: %s", t.Title, truncate(err.Error(), 300)))
			}
		}
	}
}

var (
	errStopped       = errors.New("stopped at a step boundary")
	errPermanentTask = errors.New("task cannot succeed as specified")
)

type errParked struct{ approvalID string }

func (e *errParked) Error() string { return "waiting for approval " + e.approvalID }

// runLLM is the bounded agent loop for one task:
// observe (context from DB) → plan+act (model with tools) → verify → persist
// (checkpoint every step) → continue, until the model finishes, a person must
// approve something, or the step budget runs out.
func (w *Worker) runLLM(ctx context.Context, g *Goal, t *Task) (map[string]any, error) {
	client, err := w.Store.Client(ctx, g.UserID)
	if err != nil {
		return nil, err
	}
	env := &tools.Env{Pool: w.Store.Pool, Mailer: w.Mailer, UserID: g.UserID, UserEmail: client.Email,
		UserName: client.Name, GoalID: g.ID, TaskID: t.ID, ConversationID: g.ConversationID, Lang: g.Language}

	maxSteps := t.Spec.MaxSteps
	if maxSteps == 0 {
		maxSteps = defaultMaxSteps
	}
	var defs []llm.ToolDef
	for _, name := range t.Spec.Tools {
		if tl, ok := tools.Get(name); ok {
			defs = append(defs, llm.NewToolDef(tl.Name, tl.Description, tl.Schema()))
		}
	}

	role := lookupRole(t.Spec.Role)
	model, temp := w.LLM, 0.3
	if role != nil && role.Model != "" {
		model = role.Model
	}
	if role != nil && role.Temperature > 0 {
		temp = role.Temperature
	}

	cp, err := w.Store.LatestCheckpoint(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	var msgs []llm.Message
	if cp == nil {
		// Fresh start: the whole working context is rebuilt from the database.
		system := persona.WorkerSystem(g.Language, time.Now())
		if role != nil && role.System != nil {
			system = role.System(g, t)
		}
		var taskCtx string
		if role != nil && role.Context != nil {
			if taskCtx, err = role.Context(ctx, w.Store, g, t, client); err != nil {
				return nil, err
			}
		} else {
			taskCtx = w.Store.BuildTaskContext(ctx, g, t, client)
		}
		msgs = []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: taskCtx}}
		cp = &Checkpoint{}
	} else {
		msgs = decodeMessages(cp.Messages)
		w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.resumed", map[string]any{"task": t.Title, "from_step": cp.Step})
	}

	// A decided approval: execute exactly what was approved, or tell the model
	// it was declined. Never re-ask the model for the arguments.
	if pc := cp.PendingCall; pc != nil {
		status, note, err := w.Store.ApprovalDecision(ctx, pc.ApprovalID)
		if err != nil {
			return nil, err
		}
		var content string
		switch status {
		case "approved":
			res, err := tools.Invoke(ctx, env, pc.Tool, pc.Args, true)
			content = toolResultText(res, err)
			w.Store.Event(ctx, g.UserID, g.ID, t.ID, "tool.called", map[string]any{"tool": pc.Tool, "approved": true, "error": errText(err)})
			if errors.Is(err, tools.ErrAmbiguous) {
				return nil, err
			}
		case "rejected":
			content = "DECLINED by the owner. Note: " + note + ". Do not retry the same action; adapt, or explain what the player can do instead."
		default:
			return nil, &errParked{pc.ApprovalID} // still pending (spurious wake)
		}
		msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: pc.CallID, Content: content})
		cp.PendingCall = nil
		cp.Step++
		cp.Messages = encodeMessages(msgs)
		if err := w.Store.SaveCheckpoint(ctx, t, cp); err != nil {
			return nil, err
		}
	}

	for cp.Step < maxSteps+2*cp.Verifier {
		if err := w.checkGoal(ctx, g.ID); err != nil {
			return nil, err
		}
		// Budget awareness: a model that does not know it is running out keeps
		// researching. Warn it near the end; on the last step take the tools
		// away so it must write up what it has.
		remaining := maxSteps + 2*cp.Verifier - cp.Step
		stepDefs := defs
		if remaining <= 3 && len(msgs) > 0 && msgs[len(msgs)-1].Role == "tool" {
			note := fmt.Sprintf("[system] %d step(s) left for this task. Stop gathering; finish the deliverable with what you have now (save it if the task produces something), then give your final summary.", remaining)
			if remaining <= 1 {
				note = "[system] This is the last step. Tools are no longer available: write your final summary now, stating anything still missing."
				stepDefs = nil
			}
			msgs = append(msgs, llm.Message{Role: "user", Content: note})
		}
		resp, err := w.Model.Chat(ctx, CallMeta{Purpose: "task", UserID: g.UserID, GoalID: g.ID, TaskID: t.ID, CountIteration: true},
			llm.Request{Model: model, Messages: msgs, Tools: stepDefs, Temperature: temp})
		if err != nil {
			return nil, err
		}
		msg := resp.Message
		msg.Role = "assistant"
		msgs = append(msgs, msg)

		if len(msg.ToolCalls) == 0 {
			// The model says it is done. Verify before believing it.
			problems, final := w.verifyStep(ctx, t)
			if len(problems) == 0 {
				w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.verified", map[string]any{"task": t.Title, "checks": t.Spec.Verify})
				return map[string]any{"summary": truncate(msg.Content, 4000)}, nil
			}
			if final {
				// A verdict, not a slip: no correction round.
				return nil, fmt.Errorf("%w: %s", errPermanentTask, strings.Join(problems, "; "))
			}
			if cp.Verifier >= 2 {
				return nil, fmt.Errorf("%w: verification still failing after corrections: %s", errPermanentTask, strings.Join(problems, "; "))
			}
			cp.Verifier++
			w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.verify_failed", map[string]any{"task": t.Title, "why": strings.Join(problems, "; ")})
			msgs = append(msgs, llm.Message{Role: "user", Content: "VERIFICATION FAILED — the task is not done yet:\n- " +
				strings.Join(problems, "\n- ") + "\nFix these, then finish."})
		}

		assistIdx := len(msgs) - 1
		for i, call := range msg.ToolCalls {
			if !allowed(t.Spec.Tools, call.Function.Name) {
				msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: call.ID, Content: "ERROR: tool not available in this task"})
				continue
			}
			_ = w.Store.AddUsage(ctx, g.ID, Usage{ToolCalls: 1})
			res, err := tools.Invoke(ctx, env, call.Function.Name, json.RawMessage(call.Function.Arguments), false)
			var need *tools.NeedsApproval
			if errors.As(err, &need) {
				// Calls after this one in the same message are dropped: the model
				// is asked again once the approval is decided. Calls before it
				// already ran and keep their results, in order.
				done := append([]llm.Message(nil), msgs[assistIdx+1:]...)
				trimmed := msg
				trimmed.ToolCalls = append([]llm.ToolCall(nil), msg.ToolCalls[:i+1]...)
				msgs = append(append(msgs[:assistIdx:assistIdx], trimmed), done...)
				cp.Messages = encodeMessages(msgs)
				pc := &PendingCall{CallID: call.ID, Tool: call.Function.Name, Args: json.RawMessage(call.Function.Arguments)}
				id, err := w.Store.RequestApproval(ctx, g, t, cp, pc, need)
				if err != nil {
					return nil, err
				}
				return nil, &errParked{id}
			}
			if errors.Is(err, tools.ErrAmbiguous) {
				return nil, err
			}
			w.Store.Event(ctx, g.UserID, g.ID, t.ID, "tool.called", map[string]any{"tool": call.Function.Name,
				"error": errText(err), "duplicate": res != nil && res.Duplicate})
			msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: call.ID, Content: toolResultText(res, err)})
		}

		cp.Step++
		cp.Messages = encodeMessages(msgs)
		if err := w.Store.SaveCheckpoint(ctx, t, cp); err != nil {
			return nil, err
		}
		if w.Hook != nil {
			w.Hook(t, cp.Step)
		}
		if ctx.Err() != nil {
			return nil, errStopped
		}
	}
	// Not retryable: a retry resumes from the last checkpoint, which is already
	// at the budget. The replanner decides — rewrite the task (which clears its
	// checkpoint), split it, or ask a person.
	return nil, fmt.Errorf("%w: step budget of %d exhausted without finishing", errPermanentTask, maxSteps)
}

func (w *Worker) checkGoal(ctx context.Context, goalID string) error {
	if ctx.Err() != nil {
		return errStopped
	}
	var status string
	if err := w.Store.Pool.QueryRow(ctx, `SELECT status FROM goals WHERE id=$1`, goalID).Scan(&status); err != nil {
		return err
	}
	if status != "active" && status != "planning" {
		return errStopped
	}
	return nil
}

// verifyStep runs the deterministic checks the task declared. It trusts the
// database (the tool_calls ledger and what tools wrote), not the model's
// account of what it did. An unknown verifier is a problem, not a pass: the
// planner validated the name, so it can only be unknown here if the process
// running this task lacks a registration the planner had — fail closed.
//
// final reports that a failing check is a Final verdict (no corrections).
func (w *Worker) verifyStep(ctx context.Context, t *Task) (problems []string, final bool) {
	for _, name := range t.Spec.Verify {
		v, ok := lookupVerifier(name)
		if !ok {
			problems = append(problems, fmt.Sprintf("verifier %q is not available in this worker", name))
			continue
		}
		p := v.Check(ctx, w.Store, t)
		if len(p) > 0 && v.Final {
			final = true
		}
		problems = append(problems, p...)
	}
	return problems, final
}

// runTool executes a deterministic tool task: one tool, fixed arguments, no
// model. Its checks run exactly as for a model task, but there is nobody to
// correct anything, so a failing check fails the task and the replanner
// decides what follows.
func (w *Worker) runTool(ctx context.Context, g *Goal, t *Task) (map[string]any, error) {
	if err := w.checkGoal(ctx, g.ID); err != nil {
		return nil, err
	}
	client, err := w.Store.Client(ctx, g.UserID)
	if err != nil {
		return nil, err
	}
	env := &tools.Env{Pool: w.Store.Pool, Mailer: w.Mailer, UserID: g.UserID, UserEmail: client.Email,
		UserName: client.Name, GoalID: g.ID, TaskID: t.ID, ConversationID: g.ConversationID, Lang: g.Language}
	args, _ := json.Marshal(t.Spec.Args)
	if t.Spec.Args == nil {
		args = []byte("{}")
	}
	_ = w.Store.AddUsage(ctx, g.ID, Usage{ToolCalls: 1})
	res, err := tools.Invoke(ctx, env, t.Spec.Tool, args, false)
	w.Store.Event(ctx, g.UserID, g.ID, t.ID, "tool.called", map[string]any{"tool": t.Spec.Tool, "error": errText(err)})
	var inv *tools.InvalidInput
	var need *tools.NeedsApproval
	switch {
	case err == nil:
	case errors.As(err, &inv), errors.As(err, &need), errors.Is(err, tools.ErrBlocked):
		return nil, fmt.Errorf("%w: %v", errPermanentTask, err)
	default:
		return nil, err // transient: the task retries
	}
	if problems, _ := w.verifyStep(ctx, t); len(problems) > 0 {
		w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.verify_failed", map[string]any{"task": t.Title, "why": strings.Join(problems, "; ")})
		return nil, fmt.Errorf("%w: %s", errPermanentTask, strings.Join(problems, "; "))
	}
	w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.verified", map[string]any{"task": t.Title, "checks": t.Spec.Verify})
	out := map[string]any{"summary": summariseOutput(res.Output, 4000)}
	return out, nil
}

func allowed(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

func toolResultText(res *tools.Result, err error) string {
	if err != nil {
		var inv *tools.InvalidInput
		switch {
		case errors.As(err, &inv):
			return "ERROR (fix the arguments): " + err.Error()
		case errors.Is(err, tools.ErrBlocked):
			return "BLOCKED: " + err.Error() + ". Do not try it another way; tell the player plainly what you could not do and what they can do instead."
		default:
			return "ERROR: " + truncate(err.Error(), 800)
		}
	}
	raw, _ := json.Marshal(res.Output)
	s := string(raw)
	if res.Duplicate {
		s = "(already done earlier — this is the recorded result, nothing was repeated) " + s
	}
	return truncate(s, 12000)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return truncate(err.Error(), 300)
}

func encodeMessages(m []llm.Message) []any {
	out := make([]any, len(m))
	for i := range m {
		out[i] = m[i]
	}
	return out
}

func decodeMessages(a []any) []llm.Message {
	raw, _ := json.Marshal(a)
	var m []llm.Message
	_ = json.Unmarshal(raw, &m)
	return m
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// ── Approvals ──────────────────────────────────────────────────────────────

// RequestApproval persists the checkpoint (with the pending call), the
// approval row, the parked task state and the owner's notification in ONE
// transaction.
func (s *Store) RequestApproval(ctx context.Context, g *Goal, t *Task, cp *Checkpoint, pc *PendingCall, need *tools.NeedsApproval) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO approvals (goal_id, task_id, user_id, tool, args, call_id, preview, gate)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, g.ID, t.ID, g.UserID, pc.Tool, []byte(pc.Args), pc.CallID,
			need.Preview, string(need.Gate)).Scan(&id); err != nil {
			return err
		}
		pc.ApprovalID = id
		cp.PendingCall = pc
		cp.Step++
		raw, _ := json.Marshal(cp)
		if _, err := tx.Exec(ctx, `INSERT INTO checkpoints (task_id, step, state) VALUES ($1,$2,$3)
			ON CONFLICT (task_id, step) DO UPDATE SET state=EXCLUDED.state`, t.ID, cp.Step, raw); err != nil {
			return err
		}
		if err := s.Park(ctx, tx, t); err != nil {
			return err
		}
		// Same transaction: the email exists if and only if the approval does.
		if _, err := notify.ApprovalRequested(ctx, tx, id, need.Notify); err != nil {
			return err
		}
		return eventTx(ctx, tx, g.UserID, g.ID, t.ID, "approval.requested", map[string]any{"task": t.Title, "tool": pc.Tool,
			"gate": need.Gate, "why": "this action needs the owner's approval"})
	})
	return id, err
}

func (s *Store) ApprovalDecision(ctx context.Context, id string) (string, string, error) {
	var status, note string
	err := s.Pool.QueryRow(ctx, `SELECT status, note FROM approvals WHERE id=$1`, id).Scan(&status, &note)
	return status, note, err
}

// Decide records the owner's decision and wakes the parked task. The caller
// has already checked that this person may decide (only the goal's owner can).
func (s *Store) Decide(ctx context.Context, approvalID, deciderID string, approve bool, note string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var goalID, taskID, userID, tool string
		status := map[bool]string{true: "approved", false: "rejected"}[approve]
		err := tx.QueryRow(ctx, `UPDATE approvals SET status=$2, decided_by=$3, decided_at=now(), note=$4
			WHERE id=$1 AND status='pending' RETURNING goal_id, task_id, user_id, tool`, approvalID, status, deciderID, note).
			Scan(&goalID, &taskID, &userID, &tool)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("approval is no longer pending")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE tasks SET status='ready', run_after=now(), updated_at=now() WHERE id=$1 AND status='waiting_approval'`, taskID); err != nil {
			return err
		}
		_, _ = tx.Exec(ctx, `SELECT pg_notify('play_work', $1)`, goalID)
		return eventTx(ctx, tx, userID, goalID, taskID, "approval.decided", map[string]any{"tool": tool, "decision": status, "note": note, "by": deciderID})
	})
}
