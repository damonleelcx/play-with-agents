package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
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
	ID            string
	Store         *Store
	Model         *Model
	Planner       *Planner
	LLM           string
	Mailer        mail.Mailer
	HTTP          *http.Client
	Lease         time.Duration
	CourtListener string
	OnCallEmail   string
	// Wake is signalled when new work may exist (LISTEN act_work), so an idle
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
	switch t.Kind {
	case "plan":
		switch t.Spec.Mode {
		case "initial":
			err = w.Planner.Initial(ctx, g, t)
		default:
			err = w.Planner.Replan(ctx, g, t)
		}
	case "finish":
		err = w.Planner.Finish(ctx, g, t)
	case "wait":
		// A wait task is claimable only once its run_after passed; being here
		// IS the wake-up.
		err = w.Store.Complete(ctx, t, map[string]any{"summary": "waited " + fmt.Sprint(t.Spec.WaitDays) + " days"})
	case "llm":
		out, err = w.runLLM(ctx, g, t)
		if err == nil && out != nil {
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
		if out != nil || t.Kind != "llm" {
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
	env := &tools.Env{Pool: w.Store.Pool, Mailer: w.Mailer, HTTP: w.HTTP, UserID: g.UserID, UserEmail: client.Email,
		UserName: client.Name, GoalID: g.ID, TaskID: t.ID, ConversationID: g.ConversationID, Lang: g.Language,
		CourtListener: w.CourtListener, OnCallEmail: w.OnCallEmail}

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

	cp, err := w.Store.LatestCheckpoint(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	var msgs []llm.Message
	if cp == nil {
		// Fresh start: the whole working context is rebuilt from the database.
		msgs = []llm.Message{
			{Role: "system", Content: persona.WorkerSystem(g.Language, time.Now())},
			{Role: "user", Content: w.Store.BuildTaskContext(ctx, g, t, client)},
		}
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
			content = "DECLINED by the reviewer. Note: " + note + ". Do not retry the same action; adapt or explain what the client can do instead."
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
			note := fmt.Sprintf("[system] %d step(s) left for this task. Stop gathering; finish the deliverable with what you have now (save it if the task needs a document), then give your final summary.", remaining)
			if remaining <= 1 {
				note = "[system] This is the last step. Tools are no longer available: write your final summary now, stating anything still missing."
				stepDefs = nil
			}
			msgs = append(msgs, llm.Message{Role: "user", Content: note})
		}
		resp, err := w.Model.Chat(ctx, CallMeta{Purpose: "task", UserID: g.UserID, GoalID: g.ID, TaskID: t.ID, CountIteration: true},
			llm.Request{Model: w.LLM, Messages: msgs, Tools: stepDefs, Temperature: 0.3})
		if err != nil {
			return nil, err
		}
		msg := resp.Message
		msg.Role = "assistant"
		msgs = append(msgs, msg)

		if len(msg.ToolCalls) == 0 {
			// The model says it is done. Verify before believing it.
			problems := w.verifyStep(ctx, t)
			if len(problems) == 0 {
				docs := w.Store.TaskDocuments(ctx, t.ID)
				w.Store.Event(ctx, g.UserID, g.ID, t.ID, "task.verified", map[string]any{"task": t.Title, "checks": t.Spec.Verify})
				return map[string]any{"summary": truncate(msg.Content, 4000), "documents": docs}, nil
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
// tool_calls ledger, not the model's account of what it did.
func (w *Worker) verifyStep(ctx context.Context, t *Task) []string {
	var problems []string
	for _, v := range t.Spec.Verify {
		switch v {
		case "document_saved":
			var n int
			_ = w.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE task_id=$1`, t.ID).Scan(&n)
			if n == 0 {
				problems = append(problems, "no document was saved. Call save_document NOW with this task's deliverable. "+
					"If facts are missing, still save it: write what is known, and list what is missing under a clear heading — "+
					"memory_save and notify_user do not count as saving the document")
			}
		case "citations_verified":
			if bad := w.unverifiedCitations(ctx, t.ID); len(bad) > 0 {
				problems = append(problems, "these citations in saved documents are not verified on CourtListener — remove or replace them, save again, and re-verify: "+strings.Join(bad, ", "))
			}
		case "signed_off":
			var n int
			_ = w.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM tool_calls WHERE task_id=$1 AND tool='request_professional_signoff' AND status='succeeded'`, t.ID).Scan(&n)
			if n == 0 {
				problems = append(problems, "no licensed professional has signed off — call request_professional_signoff")
			}
		}
	}
	return problems
}

// unverifiedCitations: every citation in the LATEST version of each document
// this task saved must appear in the verified list of some successful
// legal_verify_citations call made by this task.
func (w *Worker) unverifiedCitations(ctx context.Context, taskID string) []string {
	verified := map[string]bool{}
	rows, err := w.Store.Pool.Query(ctx, `SELECT output FROM tool_calls WHERE task_id=$1 AND tool='legal_verify_citations' AND status='succeeded'`, taskID)
	if err == nil {
		for rows.Next() {
			var raw []byte
			if rows.Scan(&raw) == nil {
				var o struct {
					Verified []struct {
						Citation string `json:"citation"`
					} `json:"verified"`
				}
				_ = json.Unmarshal(raw, &o)
				for _, v := range o.Verified {
					verified[v.Citation] = true
				}
			}
		}
		rows.Close()
	}
	var bad []string
	rows, err = w.Store.Pool.Query(ctx, `SELECT DISTINCT ON (title) content FROM documents WHERE task_id=$1 ORDER BY title, version DESC`, taskID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var content string
		if rows.Scan(&content) != nil {
			continue
		}
		for _, c := range tools.ExtractCitations(content) {
			if !verified[c] {
				bad = append(bad, c)
			}
		}
	}
	return bad
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
			return "BLOCKED: " + err.Error() + " Explain to the client what a licensed professional must do in person."
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
// approval row and the parked task state in ONE transaction.
func (s *Store) RequestApproval(ctx context.Context, g *Goal, t *Task, cp *Checkpoint, pc *PendingCall, need *tools.NeedsApproval) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO approvals (goal_id, task_id, user_id, tool, args, call_id, preview, gate, required_role)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, g.ID, t.ID, g.UserID, pc.Tool, []byte(pc.Args), pc.CallID,
			need.Preview, string(need.Gate), need.Role).Scan(&id); err != nil {
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
		if _, err := notify.ApprovalRequested(ctx, tx, id); err != nil {
			return err
		}
		return eventTx(ctx, tx, g.UserID, g.ID, t.ID, "approval.requested", map[string]any{"task": t.Title, "tool": pc.Tool,
			"gate": need.Gate, "role": need.Role, "why": "this action needs " + map[bool]string{true: "a licensed " + need.Role + "'s", false: "the client's"}[need.Gate == tools.G2] + " approval"})
	})
	return id, err
}

func (s *Store) ApprovalDecision(ctx context.Context, id string) (string, string, error) {
	var status, note string
	err := s.Pool.QueryRow(ctx, `SELECT status, note FROM approvals WHERE id=$1`, id).Scan(&status, &note)
	return status, note, err
}

// Decide records a person's decision and wakes the parked task. The caller
// has already checked the person may decide this gate.
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
		_, _ = tx.Exec(ctx, `SELECT pg_notify('act_work', $1)`, goalID)
		if _, err := notify.ApprovalDecided(ctx, tx, approvalID); err != nil {
			return err
		}
		return eventTx(ctx, tx, userID, goalID, taskID, "approval.decided", map[string]any{"tool": tool, "decision": status, "note": note, "by": deciderID})
	})
}

func (s *Store) TaskDocuments(ctx context.Context, taskID string) []any {
	rows, err := s.Pool.Query(ctx, `SELECT id, title, version FROM documents WHERE task_id=$1 ORDER BY created_at`, taskID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, title string
		var v int
		if rows.Scan(&id, &title, &v) == nil {
			out = append(out, map[string]any{"id": id, "title": title, "version": v})
		}
	}
	return out
}
