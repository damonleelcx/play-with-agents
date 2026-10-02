// Package engine is the durable workflow: goals, task DAGs, a leased job
// queue, a scheduler, planner/replanner and workers. The LLM is called from
// here in bounded steps; everything it needs is rebuilt from Postgres each
// time, and everything it produces is written back before the next step.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Goal struct {
	ID, UserID, ConversationID      string
	Title, Objective, Domain, Skill string
	Status                          string
	Criteria, Milestones            []string
	Limits                          Limits
	Usage                           Usage
	PlanVersion, Replans            int
	AttentionReason, Summary        string
	Language                        string
	CreatedAt, UpdatedAt            time.Time
}

type Task struct {
	ID, GoalID, Key, Title, Kind string
	Spec                         TaskSpec
	Status                       string
	Attempts, MaxAttempts        int
	RunAfter                     time.Time
	LeaseEpoch                   int64
	Output                       map[string]any
	Error                        string
	Depth, PlanVersion           int
	Deps                         []string // keys
	CreatedAt                    time.Time
	StartedAt, FinishedAt        *time.Time
}

type TaskSpec struct {
	Instructions string   `json:"instructions,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	Verify       []string `json:"verify,omitempty"`
	MaxSteps     int      `json:"max_steps,omitempty"`
	WaitDays     float64  `json:"wait_days,omitempty"`
	// Plan tasks
	Mode   string `json:"mode,omitempty"`   // initial | replan | review | change | finish
	Reason string `json:"reason,omitempty"` // why this plan task exists
}

var ErrLeaseLost = errors.New("lease lost: another worker owns this task now")

type Store struct{ Pool *pgxpool.Pool }

const goalCols = `id, user_id, coalesce(conversation_id::text,''), title, objective, domain, skill, status,
	completion_criteria, milestones, limits, usage, plan_version, replans, attention_reason, summary, language, created_at, updated_at`

func scanGoal(row pgx.Row) (*Goal, error) {
	var g Goal
	var crit, ms, lim, use []byte
	err := row.Scan(&g.ID, &g.UserID, &g.ConversationID, &g.Title, &g.Objective, &g.Domain, &g.Skill, &g.Status,
		&crit, &ms, &lim, &use, &g.PlanVersion, &g.Replans, &g.AttentionReason, &g.Summary, &g.Language, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(crit, &g.Criteria)
	_ = json.Unmarshal(ms, &g.Milestones)
	_ = json.Unmarshal(lim, &g.Limits)
	_ = json.Unmarshal(use, &g.Usage)
	g.Limits = g.Limits.WithDefaults()
	return &g, nil
}

func (s *Store) Goal(ctx context.Context, id string) (*Goal, error) {
	return scanGoal(s.Pool.QueryRow(ctx, `SELECT `+goalCols+` FROM goals WHERE id=$1`, id))
}

func (s *Store) GoalForUser(ctx context.Context, id, userID string) (*Goal, error) {
	return scanGoal(s.Pool.QueryRow(ctx, `SELECT `+goalCols+` FROM goals WHERE id=$1 AND user_id=$2`, id, userID))
}

func (s *Store) Goals(ctx context.Context, userID string, limit int) ([]*Goal, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+goalCols+` FROM goals WHERE user_id=$1 ORDER BY updated_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateGoal inserts the goal and its first task — the planning task — in one
// transaction, so a goal can never exist without the work that plans it.
func (s *Store) CreateGoal(ctx context.Context, g *Goal) error {
	crit, _ := json.Marshal(g.Criteria)
	ms, _ := json.Marshal(g.Milestones)
	lim, _ := json.Marshal(g.Limits.WithDefaults())
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO goals (user_id, conversation_id, title, objective, domain, skill, status,
			completion_criteria, milestones, limits, language, next_review_at)
			VALUES ($1, nullif($2,'')::uuid, $3, $4, $5, $6, 'planning', $7, $8, $9, $10, now() + interval '1 day')
			RETURNING id, created_at`, g.UserID, g.ConversationID, g.Title, g.Objective, g.Domain, g.Skill, crit, ms, lim, g.Language).
			Scan(&g.ID, &g.CreatedAt)
		if err != nil {
			return err
		}
		g.Status = "planning"
		spec, _ := json.Marshal(TaskSpec{Mode: "initial", Reason: "new goal"})
		if _, err := tx.Exec(ctx, `INSERT INTO tasks (goal_id, key, title, kind, spec, status, max_attempts)
			VALUES ($1, 'plan-0', 'Plan the work', 'plan', $2, 'ready', 4)`, g.ID, spec); err != nil {
			return err
		}
		return eventTx(ctx, tx, g.UserID, g.ID, "", "goal.created", map[string]any{"title": g.Title, "skill": g.Skill, "why": "client asked: " + truncate(g.Objective, 200)})
	})
}

// EnqueuePlan adds a planning task (replan/review/change/finish). The key makes
// it idempotent: two schedulers noticing the same failure add one task.
func (s *Store) EnqueuePlan(ctx context.Context, goalID, key, mode, reason string) (bool, error) {
	spec, _ := json.Marshal(TaskSpec{Mode: mode, Reason: reason})
	title := map[string]string{"replan": "Re-plan after a problem", "review": "Review progress against the goal",
		"change": "Adapt the plan to new information", "finish": "Check the goal is complete"}[mode]
	if title == "" {
		title = "Plan"
	}
	kind := "plan"
	if mode == "finish" {
		kind = "finish"
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO tasks (goal_id, key, title, kind, spec, status, max_attempts)
		VALUES ($1, $2, $3, $4, $5, 'ready', 4) ON CONFLICT (goal_id, key) DO NOTHING`, goalID, key, title, kind, spec)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		_, _ = s.Pool.Exec(ctx, `SELECT pg_notify('act_work', $1)`, goalID)
	}
	return tag.RowsAffected() == 1, nil
}

const taskCols = `t.id, t.goal_id, t.key, t.title, t.kind, t.spec, t.status, t.attempts, t.max_attempts, t.run_after,
	t.lease_epoch, t.output, t.error, t.depth, t.plan_version, t.created_at, t.started_at, t.finished_at,
	coalesce((SELECT array_agg(d2.key ORDER BY d2.key) FROM task_deps td JOIN tasks d2 ON d2.id = td.depends_on WHERE td.task_id = t.id), '{}')`

func scanTask(row pgx.Row) (*Task, error) {
	var t Task
	var spec, out []byte
	err := row.Scan(&t.ID, &t.GoalID, &t.Key, &t.Title, &t.Kind, &spec, &t.Status, &t.Attempts, &t.MaxAttempts, &t.RunAfter,
		&t.LeaseEpoch, &out, &t.Error, &t.Depth, &t.PlanVersion, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.Deps)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(spec, &t.Spec)
	if len(out) > 0 {
		_ = json.Unmarshal(out, &t.Output)
	}
	return &t, nil
}

func (s *Store) Tasks(ctx context.Context, goalID string) ([]*Task, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+taskCols+` FROM tasks t WHERE t.goal_id=$1 ORDER BY t.created_at, t.key`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Task(ctx context.Context, id string) (*Task, error) {
	return scanTask(s.Pool.QueryRow(ctx, `SELECT `+taskCols+` FROM tasks t WHERE t.id=$1`, id))
}

// Claim leases the next runnable task. FOR UPDATE SKIP LOCKED makes concurrent
// workers pick different rows; the epoch bump is the fencing token every later
// write by this worker must present.
func (s *Store) Claim(ctx context.Context, owner string, lease time.Duration) (*Task, error) {
	row := s.Pool.QueryRow(ctx, `
		UPDATE tasks SET status='leased', lease_owner=$1, lease_expires_at=now()+$2::interval,
			lease_epoch=lease_epoch+1, attempts=attempts+1, started_at=coalesce(started_at, now()), updated_at=now()
		WHERE id = (
			SELECT t.id FROM tasks t JOIN goals g ON g.id = t.goal_id
			WHERE t.status='ready' AND t.run_after <= now() AND g.status IN ('planning','active')
			ORDER BY t.run_after, t.created_at
			LIMIT 1 FOR UPDATE OF t SKIP LOCKED)
		RETURNING id`, owner, fmt.Sprintf("%d seconds", int(lease.Seconds())))
	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s.Task(ctx, id)
}

// Heartbeat extends a lease this worker still holds.
func (s *Store) Heartbeat(ctx context.Context, taskID string, epoch int64, lease time.Duration) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE tasks SET lease_expires_at=now()+$3::interval
		WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, taskID, epoch, fmt.Sprintf("%d seconds", int(lease.Seconds())))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Complete marks a leased task succeeded, fenced by epoch, then promotes any
// dependents whose dependencies are now all done — in one transaction.
func (s *Store) Complete(ctx context.Context, t *Task, output map[string]any) error {
	out, _ := json.Marshal(output)
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tasks SET status='succeeded', output=$3, error='', lease_owner=NULL, lease_expires_at=NULL,
			finished_at=now(), updated_at=now() WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, t.ID, t.LeaseEpoch, out)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrLeaseLost
		}
		if err := promoteTx(ctx, tx, t.GoalID); err != nil {
			return err
		}
		return nil
	})
}

// Retry releases the lease and schedules another attempt, or fails the task
// when attempts are exhausted. Returns true if it will run again.
func (s *Store) Retry(ctx context.Context, t *Task, cause error, delay time.Duration) (bool, error) {
	if t.Attempts >= t.MaxAttempts {
		return false, s.Fail(ctx, t, fmt.Errorf("gave up after %d attempts: %w", t.Attempts, cause))
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE tasks SET status='ready', run_after=now()+$3::interval, error=$4,
		lease_owner=NULL, lease_expires_at=NULL, updated_at=now() WHERE id=$1 AND lease_epoch=$2 AND status='leased'`,
		t.ID, t.LeaseEpoch, fmt.Sprintf("%d milliseconds", delay.Milliseconds()), cause.Error())
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, ErrLeaseLost
	}
	return true, nil
}

func (s *Store) Fail(ctx context.Context, t *Task, cause error) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE tasks SET status='failed', error=$3, lease_owner=NULL, lease_expires_at=NULL,
		finished_at=now(), updated_at=now() WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, t.ID, t.LeaseEpoch, cause.Error())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Release hands a task back without counting the attempt (pause, shutdown).
func (s *Store) Release(ctx context.Context, t *Task) error {
	_, err := s.Pool.Exec(ctx, `UPDATE tasks SET status='ready', attempts=greatest(attempts-1,0), lease_owner=NULL,
		lease_expires_at=NULL, updated_at=now() WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, t.ID, t.LeaseEpoch)
	return err
}

// Park moves a leased task to waiting_approval. The attempt is not counted:
// waiting for a person is not a failure.
func (s *Store) Park(ctx context.Context, tx pgx.Tx, t *Task) error {
	tag, err := tx.Exec(ctx, `UPDATE tasks SET status='waiting_approval', attempts=greatest(attempts-1,0), lease_owner=NULL,
		lease_expires_at=NULL, updated_at=now() WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, t.ID, t.LeaseEpoch)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// promoteTx makes blocked tasks ready once every dependency succeeded or was
// skipped. Wait tasks become ready with run_after pushed out by their delay.
func promoteTx(ctx context.Context, tx pgx.Tx, goalID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE tasks t SET status='ready', updated_at=now(),
			run_after = CASE WHEN t.kind='wait' THEN now() + make_interval(secs => coalesce((t.spec->>'wait_days')::float,0)*86400) ELSE now() END
		WHERE t.goal_id=$1 AND t.status='blocked'
		AND NOT EXISTS (SELECT 1 FROM task_deps d JOIN tasks p ON p.id=d.depends_on
			WHERE d.task_id=t.id AND p.status NOT IN ('succeeded','skipped'))`, goalID)
	if err == nil {
		_, _ = tx.Exec(ctx, `SELECT pg_notify('act_work', $1)`, goalID)
	}
	return err
}

func (s *Store) Promote(ctx context.Context, goalID string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error { return promoteTx(ctx, tx, goalID) })
}

// ── Checkpoints ────────────────────────────────────────────────────────────

type Checkpoint struct {
	Step        int            `json:"step"`
	Messages    []any          `json:"messages"`
	PendingCall *PendingCall   `json:"pending_call,omitempty"`
	Verifier    int            `json:"verifier_rounds,omitempty"`
	Notes       map[string]any `json:"notes,omitempty"`
}

type PendingCall struct {
	CallID     string          `json:"call_id"`
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args"`
	ApprovalID string          `json:"approval_id"`
}

func (s *Store) LatestCheckpoint(ctx context.Context, taskID string) (*Checkpoint, error) {
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT state FROM checkpoints WHERE task_id=$1 ORDER BY step DESC LIMIT 1`, taskID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Checkpoint
	return &c, json.Unmarshal(raw, &c)
}

// SaveCheckpoint is fenced like every other write: a worker that lost its
// lease must not overwrite the progress of the one that took over.
func (s *Store) SaveCheckpoint(ctx context.Context, t *Task, c *Checkpoint) error {
	raw, _ := json.Marshal(c)
	tag, err := s.Pool.Exec(ctx, `INSERT INTO checkpoints (task_id, step, state)
		SELECT $1, $3, $4 WHERE EXISTS (SELECT 1 FROM tasks WHERE id=$1 AND lease_epoch=$2 AND status='leased')
		ON CONFLICT (task_id, step) DO UPDATE SET state=EXCLUDED.state, created_at=now()`, t.ID, t.LeaseEpoch, c.Step, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// ── Events ─────────────────────────────────────────────────────────────────

func (s *Store) Event(ctx context.Context, userID, goalID, taskID, typ string, data map[string]any) {
	_ = pgx.BeginFunc(context.WithoutCancel(ctx), s.Pool, func(tx pgx.Tx) error {
		return eventTx(ctx, tx, userID, goalID, taskID, typ, data)
	})
}

func eventTx(ctx context.Context, tx pgx.Tx, userID, goalID, taskID, typ string, data map[string]any) error {
	raw, _ := json.Marshal(data)
	_, err := tx.Exec(ctx, `INSERT INTO events (user_id, goal_id, task_id, type, data)
		VALUES (nullif($1,'')::uuid, nullif($2,'')::uuid, nullif($3,'')::uuid, $4, $5)`, userID, goalID, taskID, typ, raw)
	if err == nil && userID != "" {
		_, _ = tx.Exec(ctx, `SELECT pg_notify('act_user', $1)`, userID)
	}
	return err
}

type Event struct {
	ID        int64          `json:"id"`
	GoalID    string         `json:"goal_id"`
	TaskID    string         `json:"task_id"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
}

func (s *Store) Events(ctx context.Context, goalID string, afterID int64, limit int) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, coalesce(goal_id::text,''), coalesce(task_id::text,''), type, data, created_at
		FROM events WHERE goal_id=$1 AND id>$2 ORDER BY id DESC LIMIT $3`, goalID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var raw []byte
		if err := rows.Scan(&e.ID, &e.GoalID, &e.TaskID, &e.Type, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &e.Data)
		out = append(out, e)
	}
	// Oldest first for readers.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// SetGoalStatus moves a goal and records why. Cancellation also cancels every
// unfinished task and supersedes pending approvals, in the same transaction.
func (s *Store) SetGoalStatus(ctx context.Context, goalID, status, reason string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var userID, prev string
		if err := tx.QueryRow(ctx, `SELECT user_id, status FROM goals WHERE id=$1 FOR UPDATE`, goalID).Scan(&userID, &prev); err != nil {
			return err
		}
		if prev == status {
			return nil
		}
		terminal := status == "completed" || status == "failed" || status == "cancelled"
		attention := ""
		if status == "needs_attention" {
			attention = reason
		}
		if _, err := tx.Exec(ctx, `UPDATE goals SET status=$2, attention_reason=$3, updated_at=now(),
			finished_at = CASE WHEN $4 THEN now() ELSE finished_at END WHERE id=$1`, goalID, status, attention, terminal); err != nil {
			return err
		}
		if terminal {
			if _, err := tx.Exec(ctx, `UPDATE tasks SET status='cancelled', lease_owner=NULL, lease_expires_at=NULL, updated_at=now(), finished_at=now()
				WHERE goal_id=$1 AND status IN ('blocked','ready','leased','waiting_approval')`, goalID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE approvals SET status='superseded' WHERE goal_id=$1 AND status='pending'`, goalID); err != nil {
				return err
			}
		}
		return eventTx(ctx, tx, userID, goalID, "", "goal.status", map[string]any{"from": prev, "to": status, "why": reason})
	})
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
