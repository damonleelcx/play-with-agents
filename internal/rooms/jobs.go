package rooms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/agents"
	"github.com/damonleelcx/play-with-agents/internal/games"
)

// job is a leased table job. Epoch is the fencing token: every write the
// worker makes for this job must present it.
type job struct {
	ID           int64
	TableID      string
	Kind         string
	Seat         int
	StateVersion int64
	Epoch        int64
	Attempts     int
	MaxAttempts  int
	Payload      []byte
	CreatedAt    time.Time
}

// claim leases the next due job. SKIP LOCKED lets concurrent workers take
// different rows; the epoch bump fences out whoever held it before.
func (s *Service) claim(ctx context.Context, owner string) (*job, error) {
	var j job
	err := s.Pool.QueryRow(ctx, `
		UPDATE table_jobs SET status='leased', lease_owner=$1, lease_expires_at=now() + make_interval(secs => $2::float8),
			lease_epoch=lease_epoch+1, attempts=attempts+1
		WHERE id = (SELECT id FROM table_jobs WHERE status='ready' AND run_after <= now()
			ORDER BY run_after, id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, table_id, kind, seat, state_version, lease_epoch, attempts, max_attempts, payload, created_at`,
		owner, s.lease().Seconds()).
		Scan(&j.ID, &j.TableID, &j.Kind, &j.Seat, &j.StateVersion, &j.Epoch, &j.Attempts, &j.MaxAttempts, &j.Payload, &j.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Service) heartbeat(ctx context.Context, j *job) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE table_jobs SET lease_expires_at=now() + make_interval(secs => $3::float8)
		WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, j.ID, j.Epoch, s.lease().Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// finish closes a job without a table change (stale, rate limited, ...).
func (s *Service) finish(ctx context.Context, j *job, status, result string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE table_jobs SET status=$3, result=$4, finished_at=now(), lease_owner=NULL, lease_expires_at=NULL
		WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, j.ID, j.Epoch, status, clip(result, 300))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Service) retry(ctx context.Context, j *job, cause error) {
	status := "ready"
	if j.Attempts >= j.MaxAttempts {
		status = "failed"
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE table_jobs SET status=$3, result=$4, run_after=now() + interval '1 second',
		lease_owner=NULL, lease_expires_at=NULL, finished_at = CASE WHEN $3='failed' THEN now() END
		WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, j.ID, j.Epoch, status, clip(cause.Error(), 300))
}

// ── Workers ─────────────────────────────────────────────────────────────────

// RunWorkers runs n workers until ctx is cancelled, plus the sweep.
// They are woken by NOTIFY play_table (see Listen), by commits in this
// process, and otherwise by the next job's due time (polled at most every
// second), so a delayed agent move fires on time.
func (s *Service) RunWorkers(ctx context.Context, n int) {
	if n <= 0 {
		n = 1
	}
	host, _ := os.Hostname()
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-%d-t%d", host, os.Getpid(), i)
		go func() { s.worker(ctx, id); done <- struct{}{} }()
	}
	go func() { s.RunSweeper(ctx); done <- struct{}{} }()
	for i := 0; i < n+1; i++ {
		<-done
	}
}

func (s *Service) worker(ctx context.Context, id string) {
	for ctx.Err() == nil {
		j, err := s.claim(ctx, id)
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("rooms: claim", "err", err)
			}
			sleepCtx(ctx, time.Second)
			continue
		}
		if j == nil {
			wait := s.nextDue(ctx)
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
			case <-s.wake:
			case <-t.C:
			}
			t.Stop()
			continue
		}
		// More work may be due: let an idle sibling look too.
		s.poke()
		s.run(ctx, j)
	}
}

func (s *Service) nextDue(ctx context.Context) time.Duration {
	var secs *float64
	_ = s.Pool.QueryRow(ctx, `SELECT extract(epoch FROM min(run_after) - now())::float8 FROM table_jobs WHERE status='ready'`).Scan(&secs)
	d := time.Second
	if secs != nil {
		d = time.Duration(*secs * float64(time.Second))
	}
	return min(max(d, 10*time.Millisecond), time.Second)
}

// run executes one leased job, heartbeating while it works. If the lease is
// lost the job's context is cancelled and every fenced write fails.
func (s *Service) run(parent context.Context, j *job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		tick := time.NewTicker(s.lease() / 3)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if errors.Is(s.heartbeat(ctx, j), ErrLeaseLost) {
					cancel()
					return
				}
			}
		}
	}()
	err := s.execute(ctx, j)
	switch {
	case err == nil, errors.Is(err, ErrLeaseLost):
		// Done, or someone else owns the job now and will finish it.
	default:
		if parent.Err() == nil {
			slog.Warn("rooms: job failed", "job", j.ID, "kind", j.Kind, "table", j.TableID, "attempt", j.Attempts, "err", err)
		}
		s.retry(context.WithoutCancel(ctx), j, err)
	}
}

func (s *Service) execute(ctx context.Context, j *job) error {
	switch j.Kind {
	case "agent_move", "turn_timeout":
		return s.playJob(ctx, j)
	case "agent_chat":
		return s.chatJob(ctx, j)
	}
	return s.finish(ctx, j, "failed", "unknown job kind")
}

// playJob plays an agent's turn or a timed-out person's default move. A
// job for a version the table has left is stale and completes as a no-op.
func (s *Service) playJob(ctx context.Context, j *job) error {
	t, err := loadTable(ctx, s.Pool, j.TableID, false)
	if errors.Is(err, ErrNotFound) {
		return s.finish(ctx, j, "done", "table gone")
	}
	if err != nil {
		return err
	}
	seat := t.seat(j.Seat)
	want := map[string]string{"agent_move": "agent", "turn_timeout": "human"}[j.Kind]
	if t.Status != "playing" || t.Version != j.StateVersion || seat == nil || seat.Kind != want || !contains(t.ToMove, j.Seat) {
		return s.finish(ctx, j, "done", "stale")
	}
	g, err := s.Game(ctx, t.GameID, t.GameVersion)
	if err != nil {
		return err
	}
	var m games.Move
	actor := "timeout"
	if j.Kind == "agent_move" {
		actor = "agent"
		m = s.choose(ctx, g, t, *seat)
	} else if m, err = g.DefaultMove(t.State, j.Seat); err != nil {
		return fmt.Errorf("default move: %w", err)
	}
	st, evs, err := g.Apply(t.State, j.Seat, m)
	if err != nil && actor == "agent" {
		// The Brain's move did not survive Apply: the seat still has to act.
		if m, err = g.DefaultMove(t.State, j.Seat); err == nil {
			st, evs, err = g.Apply(t.State, j.Seat, m)
		}
	}
	if err != nil {
		return fmt.Errorf("apply %s move: %w", actor, err)
	}
	_, err = s.commit(ctx, t, change{expected: j.StateVersion, g: g, state: st, events: evs, job: j,
		move: &moveRec{seat: j.Seat, actor: actor, move: m}, talk: s.talkAfter(t, evs, st, g)})
	if errors.Is(err, errStale) {
		return s.finish(ctx, j, "done", "stale")
	}
	return err
}

// choose asks the Brain for a move within BrainTimeout. Any failure — an
// error, a timeout, a panic, an illegal move — falls back to DefaultMove,
// because the game must go on.
func (s *Service) choose(ctx context.Context, g games.Game, t *tableRow, seat seatRow) games.Move {
	fallback := func() games.Move {
		m, _ := g.DefaultMove(t.State, seat.Seat)
		return m
	}
	var brain games.Brain
	if s.BrainFor != nil {
		brain = s.BrainFor(g)
	}
	if brain == nil {
		return fallback()
	}
	timeout := s.BrainTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Seeded per table, version and seat: a re-run of the same job makes the
	// same choice, which keeps replays and tests reproducible.
	rng := mrand.New(mrand.NewSource(t.Seed ^ (t.Version * 7919) ^ int64(seat.Seat)))
	persona := agents.Persona(seat.AgentID, t.Settings.Difficulty)
	type result struct {
		m   games.Move
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("brain panic: %v", r)}
			}
		}()
		m, err := brain.Choose(cctx, g, t.State, seat.Seat, persona, rng)
		ch <- result{m, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			slog.Debug("rooms: brain error", "table", t.ID, "seat", seat.Seat, "err", r.err)
			return fallback()
		}
		legal, err := g.Legal(t.State, seat.Seat)
		if err != nil || !isListed(legal, r.m) {
			return fallback()
		}
		return r.m
	case <-cctx.Done():
		return fallback()
	}
}

// isListed checks a move against the legal list: the type must be offered
// and a ranged argument must lie inside its range. Apply remains the final
// judge of anything finer.
func isListed(legal []games.MoveSpec, m games.Move) bool {
	for _, spec := range legal {
		if spec.Type != m.Type {
			continue
		}
		if spec.Range == nil {
			return true
		}
		v, ok := m.Args[spec.Range.Arg].(float64)
		if !ok {
			if n, isInt := m.Args[spec.Range.Arg].(int); isInt {
				v, ok = float64(n), true
			}
		}
		if ok && v >= float64(spec.Range.Min) && v <= float64(spec.Range.Max) {
			return true
		}
	}
	return false
}

// ── Sweep ───────────────────────────────────────────────────────────────────

// Sweep reclaims expired leases (the worker died or hung), abandons tables
// with no human activity for IdleAfter, and prunes old finished jobs. Every
// statement is idempotent, so all worker processes may run it.
func (s *Service) Sweep(ctx context.Context) (reclaimed, abandoned int, err error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE table_jobs SET
			status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'ready' END,
			result = 'lease expired', run_after = now(), lease_owner = NULL, lease_expires_at = NULL,
			finished_at = CASE WHEN attempts >= max_attempts THEN now() END
		WHERE status='leased' AND lease_expires_at < now()`)
	if err != nil {
		return 0, 0, err
	}
	reclaimed = int(tag.RowsAffected())
	if reclaimed > 0 {
		s.poke()
	}
	idle := s.IdleAfter
	if idle <= 0 {
		idle = 2 * time.Hour
	}
	rows, err := s.Pool.Query(ctx, `UPDATE tables SET status='abandoned', version=version+1, deadline=NULL, to_move='{}',
			finished_at=now(), updated_at=now()
		WHERE status IN ('lobby','playing') AND last_human_at < now() - make_interval(secs => $1::float8)
		RETURNING id`, idle.Seconds())
	if err != nil {
		return reclaimed, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_, _ = s.Pool.Exec(ctx, `UPDATE table_jobs SET status='done', result='table abandoned', finished_at=now()
			WHERE table_id=$1 AND status='ready'`, id)
		_, _ = s.Pool.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, id)
	}
	_, _ = s.Pool.Exec(ctx, `DELETE FROM table_jobs WHERE status IN ('done','failed') AND finished_at < now() - interval '1 day'`)
	return reclaimed, len(ids), nil
}

// RunSweeper sweeps every 10 seconds until ctx is cancelled.
func (s *Service) RunSweeper(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		if _, n, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("rooms: sweep", "err", err)
		} else if n > 0 {
			slog.Info("rooms: abandoned idle tables", "n", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
