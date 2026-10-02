package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/llm"
)

func init() {
	RegisterVerifier(Verifier{Name: "test_final", Final: true, Check: func(context.Context, *Store, *Task) []string {
		return []string{"the reviewer said no"}
	}})
	RegisterRole(Role{Name: "test_role", Model: "role-model",
		System: func(*Goal, *Task) string { return "ROLE SYSTEM" },
		Context: func(_ context.Context, _ *Store, _ *Goal, t *Task, _ *Client) (string, error) {
			return "ROLE CONTEXT for " + t.Key, nil
		}})
}

// A tool task runs one tool with fixed arguments and no model; a tool that
// needs approval cannot be a tool task.
func TestToolTask(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	ctx := context.Background()
	if _, err := validate([]PlannedTask{{Key: "p", Title: "P", Tool: "test_publish"}}, nil, Limits{}.WithDefaults()); err == nil {
		t.Fatal("a G1 tool was accepted as an unattended tool task")
	}
	if _, err := validate([]PlannedTask{{Key: "p", Title: "P", Tool: "nope"}}, nil, Limits{}.WithDefaults()); err == nil {
		t.Fatal("an unknown tool was accepted")
	}
	g := r.goalWith(t, []PlannedTask{{Key: "count", Title: "Count", Role: "playtester", Tool: "test_count",
		Args: map[string]any{"n": 3}, Verify: []string{"called:test_count"}}}, Limits{})
	if x := r.task(t, g.ID, "count"); x.Kind != "tool" || x.Spec.Role != "playtester" {
		t.Fatalf("kind %s role %s", x.Kind, x.Spec.Role)
	}
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	if x := r.task(t, g.ID, "count"); x.Status != "succeeded" || !contains(x.Output["summary"].(string), `"n":3`) {
		t.Fatalf("tool task: %s %v %s", x.Status, x.Output, x.Error)
	}
	if n := r.fake.calls.Load(); n != 0 {
		t.Fatalf("a tool task called the model %d times", n)
	}
}

// A Final verifier is a verdict: the task fails at once, with no correction
// round for the model.
func TestFinalVerifierFailsWithoutCorrection(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	ctx := context.Background()
	g := r.goalWith(t, []PlannedTask{{Key: "review", Title: "Review", Instructions: "review", Verify: []string{"test_final"}}}, Limits{})
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	x := r.task(t, g.ID, "review")
	if x.Status != "failed" || !contains(x.Error, "the reviewer said no") {
		t.Fatalf("status %s error %q", x.Status, x.Error)
	}
	if n := r.fake.calls.Load(); n != 1 {
		t.Fatalf("%d model calls; a verdict must not be argued with", n)
	}
}

// A registered role supplies the system prompt, the context and the model.
func TestRoleShapesTheCall(t *testing.T) {
	var seen fakeReq
	r := newRig(t, func(req fakeReq) llm.Message { seen = req; return finish })
	ctx := context.Background()
	r.goalWith(t, []PlannedTask{{Key: "work", Title: "Work", Role: "test_role", Instructions: "do it"}}, Limits{})
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	if seen.Model != "role-model" || seen.Messages[0].Content != "ROLE SYSTEM" || seen.Messages[1].Content != "ROLE CONTEXT for work" {
		t.Fatalf("model %q messages %q / %q", seen.Model, seen.Messages[0].Content, seen.Messages[1].Content)
	}
}

// The same approved call again in the same task returns its recorded result
// instead of asking the owner a second time.
func TestRepeatedApprovedCallIsNotReAsked(t *testing.T) {
	r := newRig(t, func(req fakeReq) llm.Message {
		if toolResults(req) < 2 {
			return llm.Message{ToolCalls: []llm.ToolCall{call("p", "test_publish", map[string]any{"v": 1})}}
		}
		return finish
	})
	ctx := context.Background()
	g := r.goalWith(t, []PlannedTask{{Key: "pub", Title: "Publish", Instructions: "publish", Tools: []string{"test_publish"}}}, Limits{})
	w := r.worker("w")
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	w.Execute(ctx, task)
	var apID string
	_ = r.pool.QueryRow(ctx, `SELECT id FROM approvals WHERE goal_id=$1`, g.ID).Scan(&apID)
	if err := r.store.Decide(ctx, apID, r.userID, true, ""); err != nil {
		t.Fatal(err)
	}
	task, _ = r.store.Claim(ctx, "w", time.Minute)
	w.Execute(ctx, task)
	var n int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE goal_id=$1`, g.ID).Scan(&n)
	if x := r.task(t, g.ID, "pub"); x.Status != "succeeded" || n != 1 {
		t.Fatalf("status %s, %d approvals", x.Status, n)
	}
}

// ApplyEdit supersedes a failed gate and its blocked dependents and adds the
// next round, counted as a replan.
func TestApplyEdit(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	ctx := context.Background()
	g := r.goalWith(t, []PlannedTask{
		{Key: "gate", Title: "Gate", Instructions: "x"},
		{Key: "after", Title: "After", Instructions: "x", Deps: []string{"gate"}},
	}, Limits{})
	_, _ = r.pool.Exec(ctx, `UPDATE tasks SET status='failed', error='no' WHERE goal_id=$1 AND key='gate'`, g.ID)
	if _, err := r.store.EnqueuePlan(ctx, g.ID, "replan-x", "replan", "gate failed"); err != nil {
		t.Fatal(err)
	}
	pt, _ := r.store.Claim(ctx, "w", time.Minute)
	err := r.planner.ApplyEdit(ctx, g, pt, PlanEdit{Supersede: []string{"after", "gate"}, Why: "round 2",
		Add: []PlannedTask{{Key: "gate-2", Title: "Gate again", Instructions: "x"}, {Key: "after-2", Title: "After", Instructions: "x", Deps: []string{"gate-2"}}}})
	if err != nil {
		t.Fatal(err)
	}
	st := map[string]string{}
	ts, _ := r.store.Tasks(ctx, g.ID)
	for _, x := range ts {
		st[x.Key] = x.Status
	}
	if st["gate"] != "skipped" || st["after"] != "cancelled" || st["gate-2"] != "ready" || st["after-2"] != "blocked" || st["replan-x"] != "succeeded" {
		t.Fatalf("statuses %v", st)
	}
	if g2, _ := r.store.Goal(ctx, g.ID); g2.Replans != 1 || g2.PlanVersion != 2 {
		t.Fatalf("replans %d plan_version %d", g2.Replans, g2.PlanVersion)
	}
	if x := r.task(t, g.ID, "gate"); !strings.Contains(x.Error, "superseded") {
		t.Fatalf("error %q", x.Error)
	}
}
