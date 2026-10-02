package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
)

// Role is a specialist a task can name in TaskSpec.Role (the studio's
// designer, engineer, critic …). Every field is optional: an unregistered
// role, or a nil field, falls back to the generic worker behaviour.
type Role struct {
	Name string
	// Model overrides the worker's model for this role's tasks.
	Model string
	// System replaces the worker's system prompt. A critic that must be
	// independent of the engineer gets its own here.
	System func(g *Goal, t *Task) string
	// Context replaces BuildTaskContext: it decides exactly what the role
	// sees. A role that must not see the rest of the mission (the critic
	// sees only rules, source and playtest report) builds it here.
	Context func(ctx context.Context, s *Store, g *Goal, t *Task, c *Client) (string, error)
	// Temperature for this role's model calls (0 keeps the worker's).
	Temperature float64
}

var (
	rolesMu sync.RWMutex
	roles   = map[string]*Role{}
)

// RegisterRole adds a role. Duplicate names are a programming error.
func RegisterRole(r Role) {
	rolesMu.Lock()
	defer rolesMu.Unlock()
	if r.Name == "" {
		panic("role without a name")
	}
	if _, dup := roles[r.Name]; dup {
		panic("role registered twice: " + r.Name)
	}
	roles[r.Name] = &r
}

func lookupRole(name string) *Role {
	if name == "" {
		return nil
	}
	rolesMu.RLock()
	defer rolesMu.RUnlock()
	return roles[name]
}

// PlanHook lets a skill decide some planning moments deterministically
// instead of asking the model: the studio turns "the critic asked for a
// revision" into a fixed revise → playtest → critic → publish loop. It runs
// for replan/review/change/cycle and finish tasks of goals with that skill
// (never for the initial plan). handled=false falls through to the generic
// planner. A hook that handles the task must settle it (Complete, ApplyEdit).
type PlanHook func(ctx context.Context, p *Planner, g *Goal, t *Task) (handled bool, err error)

var (
	hooksMu   sync.RWMutex
	planHooks = map[string]PlanHook{}
)

// RegisterPlanHook installs the hook for one skill.
func RegisterPlanHook(skill string, h PlanHook) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	if _, dup := planHooks[skill]; dup {
		panic("plan hook registered twice: " + skill)
	}
	planHooks[skill] = h
}

func planHook(skill string) PlanHook {
	hooksMu.RLock()
	defer hooksMu.RUnlock()
	return planHooks[skill]
}

// PlanEdit is a deterministic change to a goal's plan, made by a PlanHook.
type PlanEdit struct {
	// Supersede retires tasks a new round replaces: unstarted ones are
	// cancelled, failed ones become skipped (so the goal is not "stuck" on a
	// failure that a later round answered). Guards do not protect them: the
	// new round carries the same guards. Leased or finished tasks are left
	// alone.
	Supersede []string
	Add       []PlannedTask
	Why       string
	// Data is recorded on the goal.replanned event.
	Data map[string]any
}

// ApplyEdit validates and writes a PlanEdit, counts it as a replan, completes
// the plan task t and promotes what became runnable — in one transaction.
func (p *Planner) ApplyEdit(ctx context.Context, g *Goal, t *Task, e PlanEdit) error {
	all, err := p.Store.Tasks(ctx, g.ID)
	if err != nil {
		return err
	}
	existing := map[string]int{}
	byKey := map[string]*Task{}
	for _, x := range all {
		existing[x.Key] = x.Depth
		byKey[x.Key] = x
	}
	var depth map[string]int
	if len(e.Add) > 0 {
		if depth, err = validate(e.Add, existing, g.Limits); err != nil {
			return fmt.Errorf("plan edit rejected: %w", err)
		}
	}
	pv := g.PlanVersion + 1
	return pgx.BeginFunc(ctx, p.Store.Pool, func(tx pgx.Tx) error {
		// Cancel the unstarted first, then skip the failed: skipping counts
		// as done for promotion, and the dependents must already be gone.
		for _, k := range e.Supersede {
			if x := byKey[k]; x != nil && (x.Status == "blocked" || x.Status == "ready") {
				if _, err := tx.Exec(ctx, `UPDATE tasks SET status='cancelled', error=$2, finished_at=now(), updated_at=now()
					WHERE id=$1 AND status IN ('blocked','ready')`, x.ID, "superseded: "+e.Why); err != nil {
					return err
				}
			}
		}
		for _, k := range e.Supersede {
			if x := byKey[k]; x != nil && x.Status == "failed" {
				if _, err := tx.Exec(ctx, `UPDATE tasks SET status='skipped', error=$2, updated_at=now()
					WHERE id=$1 AND status='failed'`, x.ID, truncate(x.Error, 1500)+" (superseded: "+e.Why+")"); err != nil {
					return err
				}
			}
		}
		if len(e.Add) > 0 {
			if err := insertTasks(ctx, tx, g.ID, pv, e.Add, depth); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE goals SET plan_version=$2, replans=replans+1, next_review_at=now()+interval '1 day', updated_at=now() WHERE id=$1`, g.ID, pv); err != nil {
			return err
		}
		if err := completePlanTx(ctx, tx, t, map[string]any{"summary": e.Why, "added": len(e.Add)}); err != nil {
			return err
		}
		if err := promoteTx(ctx, tx, g.ID); err != nil {
			return err
		}
		keys := make([]string, len(e.Add))
		for i, a := range e.Add {
			keys[i] = a.Key
		}
		data := map[string]any{"mode": t.Spec.Mode, "reason": t.Spec.Reason, "why": e.Why, "added": len(e.Add),
			"keys": keys, "superseded": e.Supersede, "deterministic": true}
		for k, v := range e.Data {
			data[k] = v
		}
		return eventTx(ctx, tx, g.UserID, g.ID, t.ID, "goal.replanned", data)
	})
}

// SetLimits replaces a goal's limits (e.g. the owner granting another
// revision round).
func (s *Store) SetLimits(ctx context.Context, goalID string, l Limits) error {
	raw, _ := json.Marshal(l)
	_, err := s.Pool.Exec(ctx, `UPDATE goals SET limits=$2, updated_at=now() WHERE id=$1`, goalID, raw)
	return err
}
