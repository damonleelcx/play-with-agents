package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// change is one state transition of a table. Everything in it is written in
// a single transaction guarded by the expected version.
type change struct {
	expected int64
	g        games.Game  // nil while the table has no game state (lobby)
	state    games.State // the state to store (the current one if unchanged)
	status   string      // "" keeps the status, except that an outcome finishes the game
	events   []games.Event
	move     *moveRec
	human    bool      // a person acted: resets the idle clock
	job      *job      // the job doing this work; fenced by its lease epoch
	seats    []seatRow // non-nil replaces every seat
	spectate string    // user to add as spectator
	unwatch  string    // user to remove from spectators
	talk     []talkJob // agent table talk to enqueue
	back     *int      // a person at this seat said "I'm back": clear away
	extra    func(ctx context.Context, tx pgx.Tx, version int64) error
}

type moveRec struct {
	seat         int
	actor        string // human | agent | timeout
	userID       string
	clientMoveID string
	move         games.Move
}

var errDuplicateMove = errors.New("duplicate client_move_id")

// commit applies c. It returns the new version, errStale when another
// writer got there first, and ErrLeaseLost when c.job was taken over.
func (s *Service) commit(ctx context.Context, t *tableRow, c change) (int64, error) {
	status := t.Status
	if c.status != "" {
		status = c.status
	}
	toMove := []int{}
	var outcome *games.Outcome
	if c.g != nil && c.state != nil && status == "playing" {
		var err error
		if outcome, err = c.g.Outcome(c.state); err != nil {
			return 0, fmt.Errorf("outcome: %w", err)
		}
		if outcome != nil {
			status = "finished"
		} else if toMove, err = c.g.ToMove(c.state); err != nil {
			return 0, fmt.Errorf("to move: %w", err)
		}
	}
	seats := t.Seats
	if c.seats != nil {
		seats = c.seats
	}
	// Away bookkeeping happens before scheduling, so the next turn of a
	// person who just went away already runs on the short grace.
	seats, touched := s.updateAway(seats, c)
	kindOf := map[int]string{}
	awayOf := map[int]bool{}
	for _, st := range seats {
		kindOf[st.Seat] = st.Kind
		awayOf[st.Seat] = st.away()
	}
	// The clock only runs for people: agents are paced by their think delay.
	// An away person's turn only waits the grace before the default move.
	clock := 0.0
	grace := s.awayGrace()
	if status == "playing" && t.Settings.TurnSeconds > 0 {
		for _, seat := range toMove {
			if kindOf[seat] != "human" {
				continue
			}
			if awayOf[seat] {
				clock = max(clock, grace.Seconds())
			} else {
				clock = max(clock, float64(t.Settings.TurnSeconds))
			}
		}
	}
	var outRaw []byte
	if outcome != nil {
		outRaw, _ = json.Marshal(outcome)
	}
	nMoves := 0
	if c.move != nil {
		nMoves = 1
	}
	afterResult := hasResult(c.events)

	var newVersion int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if c.job != nil {
			tag, err := tx.Exec(ctx, `UPDATE table_jobs SET status='done', result='applied', finished_at=now(),
				lease_owner=NULL, lease_expires_at=NULL WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, c.job.ID, c.job.Epoch)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrLeaseLost
			}
		}
		var moveSeq, eventSeq int
		var deadline *time.Time
		err := tx.QueryRow(ctx, `UPDATE tables SET state=$2, version=version+1, status=$3, to_move=$4,
				deadline = CASE WHEN $5::float8 > 0 THEN now() + make_interval(secs => $5::float8) ELSE NULL END,
				paused_at = NULL, fault_at = NULL,
				outcome=$6, move_seq=move_seq+$7, event_seq=event_seq+$8,
				last_human_at = CASE WHEN $9 THEN now() ELSE last_human_at END,
				started_at = CASE WHEN $3 = 'playing' THEN coalesce(started_at, now()) ELSE started_at END,
				finished_at = CASE WHEN $3 IN ('finished','abandoned') THEN coalesce(finished_at, now()) ELSE finished_at END,
				updated_at = now()
			WHERE id=$1 AND version=$10
			RETURNING version, move_seq, event_seq, deadline`,
			t.ID, []byte(c.state), status, toMove, clock, outRaw, nMoves, len(c.events), c.human, c.expected).
			Scan(&newVersion, &moveSeq, &eventSeq, &deadline)
		if errors.Is(err, pgx.ErrNoRows) {
			return errStale
		}
		if err != nil {
			return err
		}
		if c.seats != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM table_seats WHERE table_id=$1`, t.ID); err != nil {
				return err
			}
			for _, st := range c.seats {
				if _, err := tx.Exec(ctx, `INSERT INTO table_seats (table_id, seat, kind, user_id, agent_id, name, timeouts, away_since)
					VALUES ($1, $2, $3, nullif($4,'')::uuid, nullif($5,''), $6, $7, $8)`,
					t.ID, st.Seat, st.Kind, st.UserID, st.AgentID, st.Name, st.Timeouts, st.AwaySince); err != nil {
					return err
				}
			}
		} else {
			for _, st := range touched {
				if _, err := tx.Exec(ctx, `UPDATE table_seats SET timeouts=$3,
						away_since = CASE WHEN $4 THEN coalesce(away_since, now()) ELSE NULL END
					WHERE table_id=$1 AND seat=$2`, t.ID, st.Seat, st.Timeouts, st.AwaySince != nil); err != nil {
					return err
				}
			}
		}
		if c.spectate != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO table_spectators (table_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, t.ID, c.spectate); err != nil {
				return err
			}
		}
		if c.unwatch != "" {
			if _, err := tx.Exec(ctx, `DELETE FROM table_spectators WHERE table_id=$1 AND user_id=$2`, t.ID, c.unwatch); err != nil {
				return err
			}
		}
		if m := c.move; m != nil {
			raw, _ := json.Marshal(m.move)
			hash := ""
			if m.clientMoveID != "" {
				hash = moveHash(m.move)
			}
			_, err := tx.Exec(ctx, `INSERT INTO table_moves (table_id, seq, seat, actor, user_id, client_move_id, move, version, move_hash)
				VALUES ($1, $2, $3, $4, nullif($5,'')::uuid, nullif($6,''), $7, $8, nullif($9,''))`,
				t.ID, moveSeq, m.seat, m.actor, m.userID, m.clientMoveID, raw, c.expected, hash)
			if isUniqueViolation(err) {
				return errDuplicateMove
			}
			if err != nil {
				return err
			}
		}
		for i, e := range c.events {
			var data []byte
			if e.Data != nil {
				data, _ = json.Marshal(e.Data)
			}
			only := e.Only
			if only == nil {
				only = []int{}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO table_events (table_id, seq, type, seat, text, data, only_seats, version)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				t.ID, eventSeq-len(c.events)+i+1, e.Type, e.Seat, e.Text, data, only, newVersion); err != nil {
				return err
			}
		}
		switch status {
		case "finished":
			if t.Status != "finished" {
				if _, err := tx.Exec(ctx, `UPDATE games SET plays = plays + 1, updated_at = now() WHERE id=$1`, t.GameID); err != nil {
					return err
				}
			}
			fallthrough
		case "abandoned":
			// Pending moves and clocks are stale now; finish them so the
			// queue stays short. Chat may still be said after a game ends.
			if _, err := tx.Exec(ctx, `UPDATE table_jobs SET status='done', result='table closed', finished_at=now()
				WHERE table_id=$1 AND status='ready' AND kind <> 'agent_chat'`, t.ID); err != nil {
				return err
			}
		case "playing":
			for _, seat := range toMove {
				switch kindOf[seat] {
				case "agent":
					delay := s.thinkDelay(t.Settings.AgentSpeed, afterResult)
					if err := enqueue(ctx, tx, t.ID, "agent_move", seat, newVersion, delay, nil); err != nil {
						return err
					}
				case "human":
					if deadline == nil {
						break
					}
					if awayOf[seat] {
						err = enqueue(ctx, tx, t.ID, "turn_timeout", seat, newVersion, grace, nil)
					} else {
						err = enqueueAt(ctx, tx, t.ID, "turn_timeout", seat, newVersion, *deadline, nil)
					}
					if err != nil {
						return err
					}
				}
			}
		}
		for _, tj := range c.talk {
			key := newVersion
			if tj.key != 0 {
				key = tj.key
			}
			payload, _ := json.Marshal(tj)
			if err := enqueue(ctx, tx, t.ID, "agent_chat", tj.Seat, key, tj.delay, payload); err != nil {
				return err
			}
		}
		if c.extra != nil {
			if err := c.extra(ctx, tx, newVersion); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, t.ID)
		return err
	})
	if err != nil {
		return 0, err
	}
	s.poke()
	return newVersion, nil
}

func enqueue(ctx context.Context, tx pgx.Tx, tableID, kind string, seat int, version int64, delay time.Duration, payload []byte) error {
	if payload == nil {
		payload = []byte(`{}`)
	}
	_, err := tx.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version, run_after, payload)
		VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5::float8), $6)
		ON CONFLICT (table_id, kind, seat, state_version) DO NOTHING`, tableID, kind, seat, version, delay.Seconds(), payload)
	return err
}

func enqueueAt(ctx context.Context, tx pgx.Tx, tableID, kind string, seat int, version int64, at time.Time, payload []byte) error {
	if payload == nil {
		payload = []byte(`{}`)
	}
	_, err := tx.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version, run_after, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (table_id, kind, seat, state_version) DO NOTHING`, tableID, kind, seat, version, at, payload)
	return err
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// ── Pace ────────────────────────────────────────────────────────────────────

// thinkDelay is how long an agent "thinks". Instant agents feel like a
// machine; a pause after a hand or round lets people read the result.
func (s *Service) thinkDelay(speed string, afterResult bool) time.Duration {
	if s.Think != nil {
		return s.Think(speed, afterResult)
	}
	lo, hi := 1.2, 3.0
	switch speed {
	case "fast":
		lo, hi = 0.4, 0.9
	case "slow":
		lo, hi = 2.5, 5.0
	}
	d := lo + rand.Float64()*(hi-lo)
	if afterResult {
		d += 3.5
	}
	return time.Duration(d * float64(time.Second))
}

// hasResult spots the end of a hand or round from event types. Games name
// their events freely, so this matches the common vocabulary.
func hasResult(evs []games.Event) bool {
	for _, e := range evs {
		if isResultEvent(e.Type) {
			return true
		}
	}
	return false
}

func isResultEvent(typ string) bool {
	t := strings.ToLower(typ)
	for _, k := range []string{"showdown", "hand_end", "hand_over", "round_end", "round_over", "pot_won", "win", "game_over", "result"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// ── Human moves ─────────────────────────────────────────────────────────────

// Move plays a person's move. A retry with the same client_move_id and the
// same move returns the current view without applying anything twice; the
// same id with a different move is a conflict (409). A stale version
// returns *StaleError; an illegal move returns an error wrapping
// games.ErrIllegal. Access is checked before anything else, retries
// included.
func (s *Service) Move(ctx context.Context, userID, tableID string, req MoveRequest) (*TableView, error) {
	if req.ClientMoveID == "" || len(req.ClientMoveID) > 64 {
		return nil, badInput("client_move_id is required (at most 64 characters)")
	}
	if req.Move.Type == "" || len(req.Move.Type) > 64 {
		return nil, badInput("move.type is required")
	}
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canWatch(ctx, t, userID); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, ErrForbidden
	}
	if done, err := s.alreadyPlayed(ctx, t, userID, req); err != nil || done {
		if err != nil {
			return nil, err
		}
		return s.assemble(ctx, t, userID)
	}
	seat := t.seatOf(userID)
	if seat == games.Spectator {
		return nil, ErrForbidden
	}
	if t.Status != "playing" {
		return nil, fmt.Errorf("%w: the game is not in progress", ErrConflict)
	}
	if t.FaultAt != nil && t.PausedAt != nil {
		return nil, fmt.Errorf("%w: the game hit a problem and is paused; the host can resume it", ErrConflict)
	}
	if req.Version != t.Version {
		return s.stale(ctx, t, userID)
	}
	if !contains(t.ToMove, seat) {
		return nil, ErrNotTurn
	}
	g, err := s.Game(ctx, t.GameID, t.GameVersion)
	if err != nil {
		return nil, err
	}
	st, evs, err := g.Apply(t.State, seat, req.Move)
	if err != nil {
		return nil, err // ErrIllegal (422) or a game bug (500)
	}
	_, err = s.commit(ctx, t, change{expected: req.Version, g: g, state: st, events: evs, human: true,
		move: &moveRec{seat: seat, actor: "human", userID: userID, clientMoveID: req.ClientMoveID, move: req.Move},
		talk: s.talkAfter(t, evs, st, g)})
	switch {
	case errors.Is(err, errDuplicateMove):
		// A concurrent retry of this very move won the race.
	case errors.Is(err, errStale):
		// Either a retry of this move landed first, or someone else moved.
		if done, err2 := s.alreadyPlayed(ctx, t, userID, req); err2 != nil || !done {
			if err2 != nil {
				return nil, err2
			}
			return s.stale(ctx, t, userID)
		}
	case err != nil:
		return nil, err
	}
	return s.View(ctx, tableID, userID)
}

// moveHash fingerprints a move for retry matching. json.Marshal writes map
// keys in sorted order, so equal moves hash equally.
func moveHash(m games.Move) string {
	raw, _ := json.Marshal(struct {
		Type string         `json:"type"`
		Args map[string]any `json:"args"`
	}{m.Type, m.Args})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// alreadyPlayed reports whether req is a retry of a move this user already
// made at the table. The same client_move_id with a different move is a
// client bug and a conflict, never a silent no-op.
func (s *Service) alreadyPlayed(ctx context.Context, t *tableRow, userID string, req MoveRequest) (bool, error) {
	var by, hash string
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(user_id::text,''), coalesce(move_hash,'') FROM table_moves WHERE table_id=$1 AND client_move_id=$2`,
		t.ID, req.ClientMoveID).Scan(&by, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if by != userID {
		// Client ids are random; a collision across users is a client bug,
		// not something to silently treat as "already done".
		return false, fmt.Errorf("%w: client_move_id already used", ErrConflict)
	}
	// Rows from before move hashes were stored carry none: trust them.
	if hash != "" && hash != moveHash(req.Move) {
		return false, fmt.Errorf("%w: client_move_id was already used for a different move", ErrConflict)
	}
	return true, nil
}

func (s *Service) stale(ctx context.Context, t *tableRow, userID string) (*TableView, error) {
	v, err := s.View(ctx, t.ID, userID)
	if err != nil {
		return nil, err
	}
	return nil, &StaleError{Table: v}
}
