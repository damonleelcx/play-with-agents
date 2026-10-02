package rooms

// Away seats and paused tables.
//
// A person who lets the clock run out awayAfter times in a row is marked
// away: from then on their turns resolve with DefaultMove after AwayGrace
// instead of the full clock, so agents and other people are not kept
// waiting. Their own move, or POST /api/tables/{id}/back, clears it.
//
// When every person at a playing table has been away for PauseAfter, or no
// person has done anything for PauseAfter, the sweep pauses the table:
// status stays "playing", but its pending jobs are retired and none are
// scheduled until a person moves or comes back (which resumes it through an
// ordinary commit). Table talk is not written for paused or all-away tables.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// awayAfter is how many consecutive clock run-outs mark a person away.
const awayAfter = 2

func (s *Service) awayGrace() time.Duration {
	if s.AwayGrace <= 0 {
		return 1500 * time.Millisecond
	}
	return s.AwayGrace
}

func (s *Service) pauseAfter() time.Duration {
	if s.PauseAfter <= 0 {
		return 15 * time.Minute
	}
	return s.PauseAfter
}

// updateAway applies a change's effect on the away bookkeeping: a timeout
// counts towards away, a person's own move or "I'm back" clears it. It
// returns the seats (a copy when anything changed) and the changed ones.
func (s *Service) updateAway(seats []seatRow, c change) ([]seatRow, []seatRow) {
	type edit struct {
		seat    int
		timeout bool
	}
	var edits []edit
	if m := c.move; m != nil {
		switch m.actor {
		case "timeout":
			edits = append(edits, edit{m.seat, true})
		case "human":
			edits = append(edits, edit{m.seat, false})
		}
	}
	if c.back != nil {
		edits = append(edits, edit{*c.back, false})
	}
	var out, touched []seatRow
	for _, e := range edits {
		for i := range seats {
			st := seats[i]
			if st.Seat != e.seat || st.Kind != "human" {
				continue
			}
			if e.timeout {
				st.Timeouts++
				if st.Timeouts >= awayAfter && st.AwaySince == nil {
					now := time.Now()
					st.AwaySince = &now
				}
			} else if st.Timeouts != 0 || st.AwaySince != nil {
				st.Timeouts, st.AwaySince = 0, nil
			} else {
				continue
			}
			if out == nil {
				out = cloneSeats(seats)
			}
			out[i] = st
			touched = append(touched, st)
		}
		if out != nil {
			seats = out
		}
	}
	return seats, touched
}

// allAway reports whether the table has people seated and all of them are
// away. A table with nobody seated (agents only) is never "all away".
func allAway(t *tableRow) bool {
	people := 0
	for _, st := range t.Seats {
		if st.Kind != "human" {
			continue
		}
		people++
		if st.AwaySince == nil {
			return false
		}
	}
	return people > 0
}

// Back is "I'm back": it clears the caller's away mark and resumes a paused
// table. Anyone who may watch the table may resume it (the host of an
// all-agent table has no seat); only a seated person has a mark to clear.
func (s *Service) Back(ctx context.Context, userID, tableID string) (*TableView, error) {
	for attempt := 0; attempt < 5; attempt++ {
		t, err := loadTable(ctx, s.Pool, tableID, false)
		if err != nil {
			return nil, err
		}
		ok, err := s.canWatch(ctx, t, userID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrForbidden
		}
		seat := t.seatOf(userID)
		marked := false
		if st := t.seat(seat); st != nil && (st.AwaySince != nil || st.Timeouts > 0) {
			marked = true
		}
		if t.Status != "playing" || (!marked && t.PausedAt == nil) {
			if t.Status == "lobby" || t.Status == "playing" {
				_, _ = s.Pool.Exec(ctx, `UPDATE tables SET last_human_at=now() WHERE id=$1`, t.ID)
			}
			return s.assemble(ctx, t, userID)
		}
		g, err := s.Game(ctx, t.GameID, t.GameVersion)
		if err != nil {
			return nil, err
		}
		c := change{expected: t.Version, g: g, state: t.State, human: true}
		if seat != games.Spectator {
			c.back = &seat
		}
		_, err = s.commit(ctx, t, c)
		if errors.Is(err, errStale) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return s.View(ctx, tableID, userID)
	}
	return nil, fmt.Errorf("%w: the table is busy, try again", ErrConflict)
}

// pauseIdle pauses playing tables where every person has been away for
// PauseAfter, or no person has acted for PauseAfter. The version bump makes
// any in-flight move job stale; pending jobs (moves, clocks and talk) are
// retired. A person's move or Back resumes the table through commit.
func (s *Service) pauseIdle(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `UPDATE tables t SET paused_at=now(), version=version+1, deadline=NULL, updated_at=now()
		WHERE t.status='playing' AND t.paused_at IS NULL AND (
			t.last_human_at < now() - make_interval(secs => $1::float8)
			OR (EXISTS (SELECT 1 FROM table_seats x WHERE x.table_id=t.id AND x.kind='human')
				AND NOT EXISTS (SELECT 1 FROM table_seats x WHERE x.table_id=t.id AND x.kind='human'
					AND (x.away_since IS NULL OR x.away_since > now() - make_interval(secs => $1::float8)))))
		RETURNING t.id`, s.pauseAfter().Seconds())
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		_, _ = s.Pool.Exec(ctx, `UPDATE table_jobs SET status='done', result='table paused', finished_at=now()
			WHERE table_id=$1 AND status='ready'`, id)
		_, _ = s.Pool.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, id)
	}
	return len(ids), nil
}
