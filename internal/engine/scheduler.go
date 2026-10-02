package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/notify"
)

// Scheduler is the wake-up machinery. Exactly one runs at a time across all
// pods (a session advisory lock); the others stand by and take over if the
// leader's connection dies.
type Scheduler struct {
	Store        *Store
	Model        *Model
	FastLLM      string
	Mailer       mail.Mailer
	PublicOrigin string
	Interval     time.Duration
}

const schedulerLock = 727275

func (s *Scheduler) Run(ctx context.Context) {
	if s.Interval == 0 {
		s.Interval = 5 * time.Second
	}
	for ctx.Err() == nil {
		conn, err := s.Store.Pool.Acquire(ctx)
		if err != nil {
			sleepCtx(ctx, 5*time.Second)
			continue
		}
		var leader bool
		_ = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, schedulerLock).Scan(&leader)
		if !leader {
			conn.Release()
			sleepCtx(ctx, 10*time.Second)
			continue
		}
		slog.Info("scheduler: leader")
		s.lead(ctx, conn.Conn().Ping)
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, schedulerLock)
		conn.Release()
	}
}

func (s *Scheduler) lead(ctx context.Context, alive func(context.Context) error) {
	tick := time.NewTicker(s.Interval)
	defer tick.Stop()
	slow := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		// Losing the lock connection means losing leadership.
		if err := alive(ctx); err != nil {
			slog.Warn("scheduler: lost leader connection", "err", err)
			return
		}
		s.Tick(ctx)
		slow++
		if slow%12 == 0 { // about once a minute
			s.SlowTick(ctx)
		}
	}
}

// Tick is the fast loop: leases, promotion, completion, failure pickup, timers.
func (s *Scheduler) Tick(ctx context.Context) {
	p := s.Store.Pool
	// 1. Expired leases: the worker died or hung. Back to ready — it resumes
	// from its last checkpoint — or failed if it has used its attempts.
	rows, err := p.Query(ctx, `UPDATE tasks SET
			status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'ready' END,
			error = CASE WHEN attempts >= max_attempts THEN 'lease expired on final attempt' ELSE 'lease expired; resuming from checkpoint' END,
			lease_owner=NULL, lease_expires_at=NULL, run_after=now(), updated_at=now()
		WHERE status='leased' AND lease_expires_at < now()
		RETURNING id, goal_id, title, status, (SELECT user_id FROM goals g WHERE g.id=tasks.goal_id)`)
	if err == nil {
		type rec struct{ id, goal, title, status, user string }
		var rs []rec
		for rows.Next() {
			var r rec
			if rows.Scan(&r.id, &r.goal, &r.title, &r.status, &r.user) == nil {
				rs = append(rs, r)
			}
		}
		rows.Close()
		for _, r := range rs {
			s.Store.Event(ctx, r.user, r.goal, r.id, "lease.reclaimed", map[string]any{"task": r.title, "to": r.status,
				"why": "worker stopped heartbeating (crash, restart or hang)"})
		}
	}

	// 2. Promotion safety net and 3. finish / failure pickup, per active goal.
	goals, err := p.Query(ctx, `SELECT g.id, g.plan_version, g.replans,
			count(*) FILTER (WHERE t.status IN ('blocked','ready','leased','waiting_approval')) AS open,
			count(*) FILTER (WHERE t.status='failed' AND t.kind='llm') AS failed,
			count(*) FILTER (WHERE t.status='blocked') AS blocked
		FROM goals g JOIN tasks t ON t.goal_id=g.id
		WHERE g.status='active' GROUP BY g.id`)
	if err != nil {
		return
	}
	type gs struct {
		id                           string
		pv, replans, open, failed, b int
	}
	var list []gs
	for goals.Next() {
		var x gs
		if goals.Scan(&x.id, &x.pv, &x.replans, &x.open, &x.failed, &x.b) == nil {
			list = append(list, x)
		}
	}
	goals.Close()
	for _, x := range list {
		if x.b > 0 {
			_ = s.Store.Promote(ctx, x.id)
		}
		if x.open == 0 {
			if x.failed > 0 {
				_, _ = s.Store.EnqueuePlan(ctx, x.id, fmt.Sprintf("replan-stuck-%d-%d", x.pv, x.replans), "replan",
					"no task can run: some failed and nothing else is open")
			} else {
				_, _ = s.Store.EnqueuePlan(ctx, x.id, fmt.Sprintf("finish-%d-%d", x.pv, x.replans), "finish", "every task is finished")
			}
		}
	}

	notify.Deliver(ctx, s.Store.Pool, s.Mailer, s.PublicOrigin)
}

// SlowTick: reviews, time limits, compaction, stale approvals.
func (s *Scheduler) SlowTick(ctx context.Context) {
	p := s.Store.Pool
	// Time limit.
	rows, err := p.Query(ctx, `SELECT id, (limits->>'max_days')::int FROM goals
		WHERE status IN ('active','planning','paused') AND created_at < now() - make_interval(days => coalesce((limits->>'max_days')::int, 30))`)
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			var d int
			if rows.Scan(&id, &d) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			_ = s.Store.SetGoalStatus(ctx, id, "needs_attention", "the goal has run past its time limit")
		}
	}

	// Periodic review: compare state with the goal even when nothing failed,
	// because a mission can stall quietly (an approval nobody answered, a
	// task that keeps parking).
	rows, err = p.Query(ctx, `UPDATE goals SET next_review_at = now() + interval '1 day'
		WHERE status='active' AND next_review_at < now()
		AND NOT EXISTS (SELECT 1 FROM events e WHERE e.goal_id=goals.id AND e.created_at > now() - interval '12 hours')
		RETURNING id, plan_version, replans`)
	if err == nil {
		type r struct {
			id     string
			pv, rp int
		}
		var rs []r
		for rows.Next() {
			var x r
			if rows.Scan(&x.id, &x.pv, &x.rp) == nil {
				rs = append(rs, x)
			}
		}
		rows.Close()
		for _, x := range rs {
			_, _ = s.Store.EnqueuePlan(ctx, x.id, fmt.Sprintf("review-%s", time.Now().UTC().Format("20060102")), "review",
				"daily review: nothing has happened for 12 hours; check whether the plan still fits the goal and whether anything is stuck")
		}
	}

	s.compact(ctx)
}

// compact folds old events into an episode summary so long-running goals keep
// a bounded prompt. The last 20 events always stay raw.
func (s *Scheduler) compact(ctx context.Context) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT g.id, g.user_id, coalesce(max(es.upto_event_id),0) FROM goals g
		LEFT JOIN episode_summaries es ON es.goal_id=g.id
		WHERE g.status NOT IN ('completed','cancelled','failed')
		GROUP BY g.id HAVING (SELECT count(*) FROM events e WHERE e.goal_id=g.id AND e.id > coalesce(max(es.upto_event_id),0)) > 60
		LIMIT 5`)
	if err != nil {
		return
	}
	type c struct {
		goal, user string
		upto       int64
	}
	var cs []c
	for rows.Next() {
		var x c
		if rows.Scan(&x.goal, &x.user, &x.upto) == nil {
			cs = append(cs, x)
		}
	}
	rows.Close()
	for _, x := range cs {
		s.compactGoal(ctx, x.goal, x.user, x.upto)
	}
}

func (s *Scheduler) compactGoal(ctx context.Context, goalID, userID string, upto int64) {
	evs, err := s.Store.Events(ctx, goalID, upto, 1000)
	if err != nil || len(evs) <= 20 {
		return
	}
	old := evs[:len(evs)-20]
	var prev string
	_ = s.Store.Pool.QueryRow(ctx, `SELECT summary FROM episode_summaries WHERE goal_id=$1 ORDER BY upto_event_id DESC LIMIT 1`, goalID).Scan(&prev)
	var b strings.Builder
	for _, e := range old {
		b.WriteString(fmt.Sprintf("%s %s %s\n", e.CreatedAt.UTC().Format("2006-01-02 15:04"), e.Type, eventLine(e.Data)))
	}
	resp, err := s.Model.Chat(ctx, CallMeta{Purpose: "compact", UserID: userID, GoalID: goalID}, llm.Request{
		Model: s.FastLLM, Temperature: 0, MaxTokens: 600,
		Messages: []llm.Message{{Role: "user", Content: "Compress this execution history of a mission into one factual paragraph (max 180 words): " +
			"what was done, what was decided and why, what failed, what is pending. Keep names of what was produced, check results, decisions and approvals.\n\nPREVIOUS SUMMARY:\n" +
			prev + "\n\nNEW EVENTS:\n" + b.String()}},
	})
	if err != nil {
		return
	}
	_, _ = s.Store.Pool.Exec(ctx, `INSERT INTO episode_summaries (goal_id, upto_event_id, summary) VALUES ($1,$2,$3)`,
		goalID, old[len(old)-1].ID, strings.TrimSpace(resp.Message.Content))
}

// Listen turns Postgres NOTIFY on play_work into worker wake-ups, and on
// play_user into calls to onUser (the web tier's live-update fan-out).
func Listen(ctx context.Context, pool *pgxpool.Pool, wake chan<- struct{}, onUser func(userID string)) {
	for ctx.Err() == nil {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			sleepCtx(ctx, 3*time.Second)
			continue
		}
		_, err = conn.Exec(ctx, `LISTEN play_work; LISTEN play_user`)
		for err == nil {
			n, e := conn.Conn().WaitForNotification(ctx)
			if e != nil {
				err = e
				break
			}
			switch n.Channel {
			case "play_work":
				if wake != nil {
					select {
					case wake <- struct{}{}:
					default:
					}
				}
			case "play_user":
				if onUser != nil {
					onUser(n.Payload)
				}
			}
		}
		conn.Release()
		sleepCtx(ctx, time.Second)
	}
}
