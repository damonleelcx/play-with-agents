package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/llm"
)

var finish = llm.Message{Content: "Done."}

// Many workers, many tasks: every task is claimed exactly once.
func TestClaimIsExclusiveUnderConcurrency(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	var ts []PlannedTask
	for i := 0; i < 40; i++ {
		ts = append(ts, PlannedTask{Key: "t" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Title: "x", Instructions: "x"})
	}
	r.goalWith(t, ts, Limits{MaxTasksPerPlan: 40, MaxTotalTasks: 50})
	ctx := context.Background()
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				task, err := r.store.Claim(ctx, "w", time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if task == nil {
					return
				}
				mu.Lock()
				seen[task.ID]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(seen) != 40 {
		t.Fatalf("claimed %d distinct tasks, want 40", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("task %s claimed %d times", id, n)
		}
	}
}

// A worker whose lease was taken over cannot write anything: not a checkpoint,
// not a completion.
func TestFencingRejectsStaleWorker(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	g := r.goalWith(t, []PlannedTask{{Key: "a", Title: "A", Instructions: "x"}}, Limits{})
	ctx := context.Background()
	stale, _ := r.store.Claim(ctx, "old", time.Second)
	if stale == nil {
		t.Fatal("nothing claimed")
	}
	time.Sleep(1100 * time.Millisecond)
	(&Scheduler{Store: r.store, Mailer: r.mailer}).Tick(ctx) // reclaims the expired lease
	fresh, _ := r.store.Claim(ctx, "new", time.Minute)
	if fresh == nil || fresh.ID != stale.ID || fresh.LeaseEpoch <= stale.LeaseEpoch {
		t.Fatalf("expected the same task re-claimed with a higher epoch, got %+v", fresh)
	}
	if err := r.store.SaveCheckpoint(ctx, stale, &Checkpoint{Step: 1}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale checkpoint: %v", err)
	}
	if err := r.store.Complete(ctx, stale, map[string]any{"summary": "stale"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale complete: %v", err)
	}
	if err := r.store.Complete(ctx, fresh, map[string]any{"summary": "fresh"}); err != nil {
		t.Fatal(err)
	}
	if got := r.task(t, g.ID, "a"); got.Status != "succeeded" || got.Output["summary"] != "fresh" {
		t.Fatalf("got %s %v", got.Status, got.Output)
	}
}

// Kill a worker mid-task; after the lease expires another resumes from the
// checkpoint instead of starting over.
func TestCrashResumesFromCheckpoint(t *testing.T) {
	var steps sync.Map
	r := newRig(t, func(req fakeReq) llm.Message {
		n := toolResults(req)
		steps.Store(n, true)
		if n < 3 {
			return llm.Message{ToolCalls: []llm.ToolCall{call("c"+string(rune('0'+n)), "legal_deadline_calc",
				map[string]any{"trigger_date": "2026-01-02", "days": 10 + n, "count": "calendar"})}}
		}
		return finish
	})
	g := r.goalWith(t, []PlannedTask{{Key: "a", Title: "A", Instructions: "compute", Tools: []string{"legal_deadline_calc"}}}, Limits{})
	ctx, crash := context.WithCancel(context.Background())
	w1 := r.worker("w1")
	w1.Hook = func(_ *Task, step int) {
		if step == 2 {
			crash() // the process dies right after its second checkpoint
		}
	}
	task, _ := r.store.Claim(context.Background(), "w1", 2*time.Second)
	task.LeaseEpoch = r.task(t, g.ID, "a").LeaseEpoch
	w1.Execute(ctx, task)
	// A crash does not get to release its lease: simulate by re-leasing to w1.
	_, _ = r.pool.Exec(context.Background(), `UPDATE tasks SET status='leased', lease_owner='w1', lease_expires_at=now()-interval '1 second' WHERE goal_id=$1`, g.ID)

	callsBefore := r.fake.calls.Load()
	(&Scheduler{Store: r.store, Mailer: r.mailer}).Tick(context.Background())
	w2 := r.worker("w2")
	t2, _ := r.store.Claim(context.Background(), "w2", time.Minute)
	if t2 == nil {
		t.Fatal("task was not reclaimed")
	}
	w2.Execute(context.Background(), t2)
	if got := r.task(t, g.ID, "a"); got.Status != "succeeded" {
		t.Fatalf("status %s err %s", got.Status, got.Error)
	}
	// Resumed at step 2: one more tool round and the finish = 2 model calls,
	// not the 4 a restart from scratch would need.
	if extra := r.fake.calls.Load() - callsBefore; extra != 2 {
		t.Fatalf("resumed run made %d model calls, want 2", extra)
	}
	var resumed int
	_ = r.pool.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE goal_id=$1 AND type='task.resumed'`, g.ID).Scan(&resumed)
	if resumed != 1 {
		t.Fatalf("task.resumed events: %d", resumed)
	}
}

// A client-gated side effect parks the task; approval runs EXACTLY the
// approved call once; a retry of the same step does not send twice.
func TestApprovalGateAndIdempotentSend(t *testing.T) {
	r := newRig(t, func(req fakeReq) llm.Message {
		if toolResults(req) == 0 {
			return llm.Message{ToolCalls: []llm.ToolCall{call("s1", "email_send", map[string]any{
				"to": "landlord@example.com", "subject": "Demand", "body": "Return my deposit.", "on_behalf_of": "client"})}}
		}
		return finish
	})
	g := r.goalWith(t, []PlannedTask{{Key: "send", Title: "Send", Instructions: "send it", Tools: []string{"email_send"}}}, Limits{})
	ctx := context.Background()
	w := r.worker("w")

	task, _ := r.store.Claim(ctx, "w", time.Minute)
	w.Execute(ctx, task)
	if got := r.task(t, g.ID, "send"); got.Status != "waiting_approval" {
		t.Fatalf("status %s, want waiting_approval", got.Status)
	}
	if r.mailer.count() != 0 {
		t.Fatal("sent before approval")
	}
	var apID string
	_ = r.pool.QueryRow(ctx, `SELECT id FROM approvals WHERE goal_id=$1`, g.ID).Scan(&apID)
	if err := r.store.Decide(ctx, apID, r.userID, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Decide(ctx, apID, r.userID, true, ""); err == nil {
		t.Fatal("an approval was decided twice")
	}
	task, _ = r.store.Claim(ctx, "w", time.Minute)
	w.Execute(ctx, task)
	if got := r.task(t, g.ID, "send"); got.Status != "succeeded" {
		t.Fatalf("status %s: %s", got.Status, got.Error)
	}
	if r.mailer.count() != 1 {
		t.Fatalf("sent %d, want 1", r.mailer.count())
	}

	// Duplicate event: the same step is re-run (e.g. a replayed queue message).
	_, _ = r.pool.Exec(ctx, `UPDATE tasks SET status='ready', attempts=0 WHERE goal_id=$1`, g.ID)
	_, _ = r.pool.Exec(ctx, `DELETE FROM checkpoints WHERE task_id=(SELECT id FROM tasks WHERE goal_id=$1)`, g.ID)
	_, _ = r.pool.Exec(ctx, `UPDATE approvals SET status='approved'`)
	task, _ = r.store.Claim(ctx, "w", time.Minute)
	// Re-run with the approval pre-granted by replaying the pending call.
	cp := &Checkpoint{Step: 1, PendingCall: &PendingCall{CallID: "s1", Tool: "email_send", ApprovalID: apID,
		Args: []byte(`{"to":"landlord@example.com","subject":"Demand","body":"Return my deposit.","on_behalf_of":"client"}`)}}
	_ = r.store.SaveCheckpoint(ctx, task, cp)
	w.Execute(ctx, task)
	if r.mailer.count() != 1 {
		t.Fatalf("duplicate step re-sent: %d sends", r.mailer.count())
	}
}

// A declined approval is fed back to the model, and nothing is sent.
func TestRejectedApprovalIsReportedNotExecuted(t *testing.T) {
	var sawDecline bool
	r := newRig(t, func(req fakeReq) llm.Message {
		for _, m := range req.Messages {
			if m.Role == "tool" && contains(m.Content, "DECLINED") {
				sawDecline = true
				return finish
			}
		}
		return llm.Message{ToolCalls: []llm.ToolCall{call("s1", "email_send", map[string]any{
			"to": "x@example.com", "subject": "s", "body": "b", "on_behalf_of": "counsel"})}}
	})
	g := r.goalWith(t, []PlannedTask{{Key: "send", Title: "Send", Instructions: "send", Tools: []string{"email_send"}}}, Limits{})
	ctx := context.Background()
	w := r.worker("w")
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	w.Execute(ctx, task)
	var apID, role, gate string
	_ = r.pool.QueryRow(ctx, `SELECT id, required_role, gate FROM approvals WHERE goal_id=$1`, g.ID).Scan(&apID, &role, &gate)
	if gate != "G2" || role != "attorney" {
		t.Fatalf("sending as counsel must need a G2 attorney approval, got %s %s", gate, role)
	}
	_ = r.store.Decide(ctx, apID, r.userID, false, "not accurate")
	task, _ = r.store.Claim(ctx, "w", time.Minute)
	w.Execute(ctx, task)
	if !sawDecline || r.mailer.count() != 0 {
		t.Fatalf("decline seen=%v sends=%d", sawDecline, r.mailer.count())
	}
}

// Controlled substances are G3: blocked outright, never queued for approval.
func TestControlledSubstanceIsNeverAutomated(t *testing.T) {
	var blocked bool
	r := newRig(t, func(req fakeReq) llm.Message {
		for _, m := range req.Messages {
			if m.Role == "tool" && contains(m.Content, "BLOCKED") {
				blocked = true
				return finish
			}
		}
		return llm.Message{ToolCalls: []llm.ToolCall{call("rx", "rx_submit", map[string]any{
			"drug": "oxycodone 5mg", "dose": "5 mg", "route": "oral", "frequency": "q6h", "quantity": 20, "refills": 0, "controlled_schedule": "none"})}}
	})
	g := r.goalWith(t, []PlannedTask{{Key: "rx", Title: "Rx", Instructions: "x", Tools: []string{"rx_submit"}}}, Limits{})
	ctx := context.Background()
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	var n int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE goal_id=$1`, g.ID).Scan(&n)
	if !blocked || n != 0 {
		t.Fatalf("blocked=%v approvals=%d (the model lied that it is not scheduled; the deny-list must still catch it)", blocked, n)
	}
}

// Dependencies: B waits for A; a wait task sleeps; C runs after.
func TestDAGPromotionAndWait(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	g := r.goalWith(t, []PlannedTask{
		{Key: "a", Title: "A", Instructions: "x"},
		{Key: "w", Title: "Wait", Deps: []string{"a"}, WaitDays: 1},
		{Key: "b", Title: "B", Instructions: "x", Deps: []string{"w"}},
	}, Limits{})
	ctx := context.Background()
	if r.task(t, g.ID, "b").Status != "blocked" || r.task(t, g.ID, "w").Status != "blocked" {
		t.Fatal("dependents must start blocked")
	}
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	wt := r.task(t, g.ID, "w")
	if wt.Status != "ready" || time.Until(wt.RunAfter) < 23*time.Hour {
		t.Fatalf("wait task: %s run_after in %v", wt.Status, time.Until(wt.RunAfter))
	}
	if next, _ := r.store.Claim(ctx, "w", time.Minute); next != nil {
		t.Fatalf("claimed %s before its timer", next.Key)
	}
	// Time passes.
	_, _ = r.pool.Exec(ctx, `UPDATE tasks SET run_after=now() WHERE id=$1`, wt.ID)
	task, _ = r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	task, _ = r.store.Claim(ctx, "w", time.Minute)
	if task == nil || task.Key != "b" {
		t.Fatalf("expected b, got %+v", task)
	}
}

// A goal that exhausts its budget stops and asks for a person.
func TestBudgetStopsTheGoal(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message {
		return llm.Message{ToolCalls: []llm.ToolCall{call("c", "legal_deadline_calc", map[string]any{"trigger_date": "2026-01-02", "days": 3, "count": "court"})}}
	})
	g := r.goalWith(t, []PlannedTask{{Key: "a", Title: "A", Instructions: "x", Tools: []string{"legal_deadline_calc"}}}, Limits{MaxIterations: 3})
	ctx := context.Background()
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	g, _ = r.store.Goal(ctx, g.ID)
	if g.Status != "needs_attention" || !contains(g.AttentionReason, "iteration") {
		t.Fatalf("goal %s (%s)", g.Status, g.AttentionReason)
	}
	if r.task(t, g.ID, "a").Status != "ready" {
		t.Fatal("the task should be released intact for when the budget is raised")
	}
}

// Cancelling mid-task stops at the next step boundary and supersedes approvals.
func TestCancellationIsSafe(t *testing.T) {
	var r *rig
	var goalID string
	r = newRig(t, func(req fakeReq) llm.Message {
		if toolResults(req) == 1 {
			_ = r.store.SetGoalStatus(context.Background(), goalID, "cancelled", "client cancelled")
		}
		return llm.Message{ToolCalls: []llm.ToolCall{call("c", "legal_deadline_calc", map[string]any{"trigger_date": "2026-01-02", "days": 3, "count": "court"})}}
	})
	g := r.goalWith(t, []PlannedTask{{Key: "a", Title: "A", Instructions: "x", Tools: []string{"legal_deadline_calc"}},
		{Key: "b", Title: "B", Instructions: "x", Deps: []string{"a"}}}, Limits{})
	goalID = g.ID
	ctx := context.Background()
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	for _, k := range []string{"a", "b"} {
		if st := r.task(t, g.ID, k).Status; st != "cancelled" {
			t.Fatalf("%s is %s", k, st)
		}
	}
	if n := r.fake.calls.Load(); n > 3 {
		t.Fatalf("kept calling the model after cancel: %d calls", n)
	}
}

// Deterministic verification: a draft citing an unverified case is not done.
func TestVerifierRejectsUnverifiedCitation(t *testing.T) {
	r := newRig(t, func(req fakeReq) llm.Message {
		if toolResults(req) == 0 {
			return llm.Message{ToolCalls: []llm.ToolCall{call("d", "save_document", map[string]any{
				"title": "Memo", "kind": "memo", "content": "As held in Smith v. Jones, 999 F.3d 12345 (9th Cir. 2031), the deposit must be returned."})}}
		}
		return finish // claims done without verifying
	})
	g := r.goalWith(t, []PlannedTask{{Key: "m", Title: "Memo", Instructions: "x", Tools: []string{"save_document"},
		Verify: []string{"document_saved", "citations_verified"}}}, Limits{})
	ctx := context.Background()
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	got := r.task(t, g.ID, "m")
	if got.Status == "succeeded" {
		t.Fatal("a memo with an unverified citation was accepted")
	}
	var vf int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE goal_id=$1 AND type='task.verify_failed'`, g.ID).Scan(&vf)
	if vf == 0 {
		t.Fatal("verification failure was not recorded on the timeline")
	}
}

func TestPlanValidation(t *testing.T) {
	lim := Limits{}.WithDefaults()
	cases := map[string][]PlannedTask{
		"cycle":        {{Key: "a", Title: "a", Instructions: "x", Deps: []string{"b"}}, {Key: "b", Title: "b", Instructions: "x", Deps: []string{"a"}}},
		"unknown tool": {{Key: "a", Title: "a", Instructions: "x", Tools: []string{"launch_missiles"}}},
		"missing dep":  {{Key: "a", Title: "a", Instructions: "x", Deps: []string{"zzz"}}},
		"bad key":      {{Key: "A B", Title: "a", Instructions: "x"}},
		"long wait":    {{Key: "a", Title: "a", WaitDays: 400}},
	}
	for name, ts := range cases {
		if _, err := validate(ts, map[string]int{}, lim); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	deep := []PlannedTask{{Key: "t0", Title: "x", Instructions: "x"}}
	for i := 1; i <= 8; i++ {
		deep = append(deep, PlannedTask{Key: "t" + string(rune('0'+i)), Title: "x", Instructions: "x", Deps: []string{"t" + string(rune('0'+i-1))}})
	}
	if _, err := validate(deep, map[string]int{}, lim); err == nil {
		t.Error("depth limit not enforced")
	}
	// Dropping the sign-off from a template reverts to the template.
	tmpl := []PlannedTask{{Key: "a", Title: "a", Instructions: "x"}, {Key: "r", Title: "r", Instructions: "x", Verify: []string{"signed_off"}}}
	got, reverted := keepSafetySteps([]PlannedTask{{Key: "a", Title: "a", Instructions: "x"}}, tmpl)
	if len(got) != 2 || !reverted {
		t.Error("a plan without the professional sign-off was accepted")
	}
}

// All tasks done → the scheduler starts the completion check; failed with
// nothing open → a replan. Enqueueing is idempotent under duplicate ticks.
func TestSchedulerFinishAndReplanAreIdempotent(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	g := r.goalWith(t, []PlannedTask{{Key: "a", Title: "A", Instructions: "x"}}, Limits{})
	ctx := context.Background()
	_, _ = r.pool.Exec(ctx, `UPDATE tasks SET status='succeeded' WHERE goal_id=$1`, g.ID)
	s := &Scheduler{Store: r.store, Mailer: r.mailer}
	s.Tick(ctx)
	s.Tick(ctx)
	var n int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE goal_id=$1 AND kind='finish'`, g.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("finish tasks: %d", n)
	}
}

// Reminders are delivered once, even when ticks overlap.
func TestReminderDeliveredOnce(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	ctx := context.Background()
	_, _ = r.pool.Exec(ctx, `INSERT INTO reminders (user_id, due_at, text, key) VALUES ($1, now()-interval '1 minute', 'Hearing tomorrow', 'k1')`, r.userID)
	s := &Scheduler{Store: r.store, Mailer: r.mailer}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.deliverReminders(ctx) }()
	}
	wg.Wait()
	if r.mailer.count() != 1 {
		t.Fatalf("reminder emails: %d", r.mailer.count())
	}
	var msgs int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1`, r.convID).Scan(&msgs)
	if msgs != 1 {
		t.Fatalf("reminder messages: %d", msgs)
	}
}

// The initial planner falls back to the playbook when the model's plan is
// invalid — and the goal still gets a valid DAG.
func TestPlannerFallsBackToPlaybook(t *testing.T) {
	r := newRig(t, func(req fakeReq) llm.Message {
		return llm.Message{Content: `{"title":"x","tasks":[{"key":"a","title":"a","instructions":"x","deps":["a"],"verify":["citations_verified"]}]}`}
	})
	ctx := context.Background()
	g := &Goal{UserID: r.userID, ConversationID: r.convID, Title: "Deposit", Objective: "get deposit back", Domain: "legal",
		Skill: "demand-letter", Language: "en"}
	if err := r.store.CreateGoal(ctx, g); err != nil {
		t.Fatal(err)
	}
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	g, _ = r.store.Goal(ctx, g.ID)
	ts, _ := r.store.Tasks(ctx, g.ID)
	if g.Status != "active" || len(ts) != 5 {
		t.Fatalf("goal %s with %d tasks", g.Status, len(ts))
	}
	var why string
	_ = r.pool.QueryRow(ctx, `SELECT data->>'why' FROM events WHERE goal_id=$1 AND type='goal.planned'`, g.ID).Scan(&why)
	if !contains(why, "playbook used as written") {
		t.Fatalf("why = %q", why)
	}
}
