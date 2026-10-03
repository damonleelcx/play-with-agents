package rooms

// Faults: a table job that fails for good must not freeze the table.
//
// A move or clock job (agent_move, turn_timeout) that has used up its
// attempts, whether by errors or by expired leases, gets one last try: the
// seat's DefaultMove. If even that cannot be played, the table is paused
// with a visible log event ("This game hit a problem and was paused") and
// fault_at set. Its host (or, at a table without a host, any person seated)
// may resume it with POST /back, which retries; a table that faults a
// second time is abandoned. Table talk failing for good changes nothing.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

const (
	faultPausedText    = "This game hit a problem and was paused. The host can resume it."
	faultAbandonedText = "This game hit a problem again and was closed."
)

// jobFailed runs after a job's final failure.
func (s *Service) jobFailed(ctx context.Context, j *job) {
	if j.Kind != "agent_move" && j.Kind != "turn_timeout" {
		return
	}
	if err := s.recoverTable(ctx, j.TableID, j.Seat, j.StateVersion); err != nil && ctx.Err() == nil {
		slog.Error("rooms: could not recover a table after a failed job", "table", j.TableID, "job", j.ID, "err", err)
	}
}

// recoverTable plays the default move for seat at version, or pauses (or,
// on a second fault, abandons) the table when that is impossible. Nothing
// happens when the table has moved on: the failed job was stale anyway.
func (s *Service) recoverTable(ctx context.Context, tableID string, seat int, version int64) error {
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.Status != "playing" || t.PausedAt != nil || t.Version != version || !contains(t.ToMove, seat) {
		return nil
	}
	cause := s.playDefault(ctx, t, seat)
	if cause == nil || errors.Is(cause, errStale) {
		return nil
	}
	slog.Warn("rooms: default move failed after a failed job; pausing the table", "table", t.ID, "seat", seat, "err", cause)
	return s.fault(ctx, t)
}

// playDefault applies seat's DefaultMove as a timeout/agent move.
func (s *Service) playDefault(ctx context.Context, t *tableRow, seat int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("default move panicked: %v", r)
		}
	}()
	g, err := s.Game(ctx, t.GameID, t.GameVersion)
	if err != nil {
		return err
	}
	m, err := g.DefaultMove(t.State, seat)
	if err != nil {
		return fmt.Errorf("default move: %w", err)
	}
	st, evs, err := g.Apply(t.State, seat, m)
	if err != nil {
		return fmt.Errorf("apply default move: %w", err)
	}
	actor := "timeout"
	if sr := t.seat(seat); sr != nil && sr.Kind == "agent" {
		actor = "agent"
	}
	_, err = s.commit(ctx, t, change{expected: t.Version, g: g, state: st, events: evs,
		move: &moveRec{seat: seat, actor: actor, move: m}, talk: s.talkAfter(t, evs, st, g)})
	return err
}

// fault pauses t with a visible event, or abandons it when it has faulted
// before. Version-guarded like every other write; pending jobs are retired.
func (s *Service) fault(ctx context.Context, t *tableRow) error {
	abandon := t.Faults >= 1
	text, typ := faultPausedText, "fault"
	if abandon {
		text, typ = faultAbandonedText, "fault_closed"
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var eventSeq int
		var newVersion int64
		err := tx.QueryRow(ctx, `UPDATE tables SET version=version+1, faults=faults+1, deadline=NULL, event_seq=event_seq+1,
				paused_at = CASE WHEN $3 THEN paused_at ELSE now() END,
				fault_at  = CASE WHEN $3 THEN fault_at ELSE now() END,
				status    = CASE WHEN $3 THEN 'abandoned' ELSE status END,
				to_move   = CASE WHEN $3 THEN '{}' ELSE to_move END,
				finished_at = CASE WHEN $3 THEN now() ELSE finished_at END,
				updated_at = now()
			WHERE id=$1 AND version=$2 RETURNING version, event_seq`, t.ID, t.Version, abandon).Scan(&newVersion, &eventSeq)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // moved on meanwhile: nothing is stuck
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO table_events (table_id, seq, type, seat, text, version)
			VALUES ($1, $2, $3, -1, $4, $5)`, t.ID, eventSeq, typ, text, newVersion); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE table_jobs SET status='done', result='table faulted', finished_at=now()
			WHERE table_id=$1 AND status='ready' AND kind <> 'agent_chat'`, t.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, t.ID)
		return err
	})
}

// mayResume reports whether userID may resume t. A table paused by a fault
// waits for its host (or, without one, any person seated); an idle pause
// may be lifted by anyone who can watch.
func (t *tableRow) mayResume(userID string) bool {
	if t.FaultAt == nil {
		return true
	}
	return t.hostRights(userID)
}
