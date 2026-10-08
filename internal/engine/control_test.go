package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/damonleelcx/play-with-agents/internal/llm"
)

// DecideAsOwner is the one path for deciding an approval (the mission card's
// buttons and Aoi's chat): only the approval's owner, only pending, and
// someone else's approval looks like a missing one.
func TestDecideAsOwnerAndPendingApprovals(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	ctx := context.Background()
	g := &Goal{UserID: r.userID, ConversationID: r.convID, Title: "Dragon Chess", Objective: "x", Domain: "studio", Skill: "general-task", Language: "en"}
	if err := r.store.CreateGoal(ctx, g); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := r.pool.QueryRow(ctx, `INSERT INTO approvals (goal_id, task_id, user_id, tool, args, call_id, preview)
		SELECT $1, id, $2, 'publish_game', '{}', 'c1', 'publish v1' FROM tasks WHERE goal_id=$1 LIMIT 1 RETURNING id`, g.ID, r.userID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	other, _ := newUser(t, r.pool, nil)

	if ps, _ := r.store.PendingApprovals(ctx, other, ""); len(ps) != 0 {
		t.Fatalf("someone else sees %v", ps)
	}
	ps, err := r.store.PendingApprovals(ctx, r.userID, g.ID)
	if err != nil || len(ps) != 1 || ps[0].ID != id || ps[0].GoalTitle != "Dragon Chess" || ps[0].Preview != "publish v1" {
		t.Fatalf("pending %+v %v", ps, err)
	}
	if err := r.store.DecideAsOwner(ctx, id, other, true, ""); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("someone else decided: %v", err)
	}
	if err := r.store.DecideAsOwner(ctx, "not-a-uuid", r.userID, true, ""); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("bad id: %v", err)
	}
	if err := r.store.DecideAsOwner(ctx, id, r.userID, true, "from chat"); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := r.store.ApprovalDecision(ctx, id); st != "approved" {
		t.Fatalf("status %s", st)
	}
	if err := r.store.DecideAsOwner(ctx, id, r.userID, false, ""); err == nil {
		t.Fatal("decided twice")
	}
}

func TestGoalControl(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	ctx := context.Background()
	g := r.goalWith(t, []PlannedTask{{Key: "a", Title: "A", Instructions: "do a"}}, Limits{})
	if err := r.store.GoalControl(ctx, g, "pause", "test"); err != nil {
		t.Fatal(err)
	}
	g, _ = r.store.Goal(ctx, g.ID)
	if g.Status != "paused" {
		t.Fatalf("status %s", g.Status)
	}
	if err := r.store.GoalControl(ctx, g, "pause", "again"); err != nil { // a no-op
		t.Fatal(err)
	}
	if err := r.store.GoalControl(ctx, g, "resume", "test"); err != nil {
		t.Fatal(err)
	}
	g, _ = r.store.Goal(ctx, g.ID)
	var reviews int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE goal_id=$1 AND spec->>'mode'='review'`, g.ID).Scan(&reviews)
	if g.Status != "active" || reviews != 1 {
		t.Fatalf("status %s reviews %d", g.Status, reviews)
	}
	if err := r.store.GoalControl(ctx, g, "cancel", "test"); err != nil {
		t.Fatal(err)
	}
	g, _ = r.store.Goal(ctx, g.ID)
	if g.Status != "cancelled" {
		t.Fatalf("status %s", g.Status)
	}
	if err := r.store.GoalControl(ctx, g, "explode", ""); err == nil {
		t.Fatal("unknown action accepted")
	}
}
