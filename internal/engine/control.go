package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Owner-facing controls shared by the HTTP API (the buttons on a mission) and
// Aoi's chat ("pause the build", "approve it"), so both take exactly the same
// path and the same checks.

// ErrApprovalNotFound: no such approval, or it belongs to someone else (the
// two are deliberately indistinguishable, so ids reveal nothing).
var ErrApprovalNotFound = errors.New("approval not found")

// ErrNotApprovable: the approval's gate is not one a person may decide (G3).
var ErrNotApprovable = errors.New("this action cannot be approved")

// GoalControl pauses, resumes or cancels a goal the way its owner does from
// the mission page. A control that does not apply to the goal's status is a
// no-op (pausing a paused build, cancelling a finished one).
func (s *Store) GoalControl(ctx context.Context, g *Goal, action, why string) error {
	switch action {
	case "pause":
		if g.Status == "active" || g.Status == "planning" {
			return s.SetGoalStatus(ctx, g.ID, "paused", why)
		}
	case "resume":
		if g.Status == "paused" || g.Status == "needs_attention" {
			if err := s.SetGoalStatus(ctx, g.ID, "active", why); err != nil {
				return err
			}
			_, err := s.EnqueuePlan(ctx, g.ID, fmt.Sprintf("resume-%d", time.Now().Unix()), "review", "the owner resumed the mission; check the plan still fits")
			return err
		}
	case "cancel":
		if g.Status != "completed" && g.Status != "cancelled" && g.Status != "failed" {
			return s.SetGoalStatus(ctx, g.ID, "cancelled", why)
		}
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	return nil
}

// DecideAsOwner is the one way a person decides an approval: it checks, on
// the server, that the stored approval is theirs and is a G1 gate, then
// records the decision through Decide.
func (s *Store) DecideAsOwner(ctx context.Context, approvalID, userID string, approve bool, note string) error {
	var owner, gate string
	err := s.Pool.QueryRow(ctx, `SELECT user_id, gate FROM approvals WHERE id::text=$1`, approvalID).Scan(&owner, &gate)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != userID) {
		return ErrApprovalNotFound
	}
	if err != nil {
		return err
	}
	if gate != "G1" {
		return ErrNotApprovable
	}
	return s.Decide(ctx, approvalID, userID, approve, note)
}

// PendingApproval is an approval waiting on its owner.
type PendingApproval struct {
	ID, GoalID, GoalTitle, Tool, Preview, Gate string
	GameID                                     string
	CreatedAt                                  time.Time
}

// PendingApprovals lists the user's own pending G1 approvals, newest first,
// optionally for one goal.
func (s *Store) PendingApprovals(ctx context.Context, userID, goalID string) ([]PendingApproval, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.id, a.goal_id, g.title, a.tool, a.preview, a.gate, coalesce(g.game_id,''), a.created_at
		FROM approvals a JOIN goals g ON g.id = a.goal_id
		WHERE a.user_id=$1 AND a.status='pending' AND a.gate='G1' AND ($2 = '' OR a.goal_id::text = $2)
		ORDER BY a.created_at DESC LIMIT 20`, userID, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingApproval
	for rows.Next() {
		var p PendingApproval
		if err := rows.Scan(&p.ID, &p.GoalID, &p.GoalTitle, &p.Tool, &p.Preview, &p.Gate, &p.GameID, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
