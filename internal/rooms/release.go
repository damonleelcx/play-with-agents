package rooms

// Account deletion: everything a person leaves behind at the tables must
// keep working for everyone else (migration 0013 for the foreign keys).
//
//   - Each seat they hold mid-game is taken over by an agent, as with Leave:
//     chips (or whatever the game holds) stay with the seat. A lobby seat
//     opens again. Finished tables keep their seating as a record.
//   - Host rights pass to the next person seated at the table, or to no one
//     (see tableRow.hostRights). A lobby nobody else is in, and a game nobody
//     else can see any more, are abandoned with a visible event.
//   - Their published, non-private games stay on the shelf, credited to "a
//     former player". Drafts and private games are deleted with their
//     versions, unless someone else has a table record of them: then the live
//     tables are abandoned with a visible event and the game is kept, hidden
//     and ownerless, so those records still render.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

const formerOwner = "a former player"

const gameRemovedText = "The game's creator deleted their account, so this table is closed."

// ReleaseUser hands over everything userID holds at the tables and in the
// catalog. Call it before deleting the account; it is idempotent, so a
// retry after a failure is safe.
func (s *Service) ReleaseUser(ctx context.Context, userID string) error {
	if err := s.releaseGames(ctx, userID); err != nil {
		return fmt.Errorf("release games: %w", err)
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id FROM tables t
		WHERE t.status IN ('lobby','playing','finished') AND (t.host_id=$1
			OR EXISTS (SELECT 1 FROM table_seats x WHERE x.table_id=t.id AND x.user_id=$1))`, userID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.releaseTable(ctx, id, userID); err != nil {
			return fmt.Errorf("release table %s: %w", id, err)
		}
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM table_spectators WHERE user_id=$1`, userID)
	return err
}

// releaseTable takes userID out of one table: seat, host rights, or both.
func (s *Service) releaseTable(ctx context.Context, tableID, userID string) error {
	for attempt := 0; attempt < 8; attempt++ {
		t, err := loadTable(ctx, s.Pool, tableID, false)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		seat := t.seatOf(userID)
		hosting := t.HostID == userID
		if seat == games.Spectator && !hosting {
			return nil
		}
		name := ""
		if st := t.seat(seat); st != nil {
			name = st.Name
		} else if u, err := loadUser(ctx, s.Pool, userID); err == nil {
			name = u.Name
		}
		if name == "" {
			name = "A player"
		}

		// Who hosts now: the next person seated, else nobody.
		newHost := t.HostID
		newHostSeat := -1
		if hosting {
			newHost = ""
			for _, st := range t.Seats {
				if st.Kind == "human" && st.UserID != "" && st.UserID != userID {
					newHost, newHostSeat = st.UserID, st.Seat
					break
				}
			}
		}
		// Is anyone else left who can see the table?
		var watchers int
		if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM table_spectators WHERE table_id=$1 AND user_id<>$2`,
			t.ID, userID).Scan(&watchers); err != nil {
			return err
		}
		othersSeated := false
		for _, st := range t.Seats {
			othersSeated = othersSeated || (st.Kind == "human" && st.UserID != "" && st.UserID != userID)
		}
		nobodyLeft := newHost == "" && !othersSeated && watchers == 0

		c := change{expected: t.Version, state: t.State, unwatch: userID}
		seats := cloneSeats(t.Seats)
		switch t.Status {
		case "lobby":
			if seat != games.Spectator {
				seats[seat] = seatRow{Seat: seat, Kind: "open"}
				c.seats = seats
			}
			if newHost == "" {
				// Nobody could start it.
				c.status = "abandoned"
				c.events = append(c.events, games.Event{Type: "closed", Seat: -1,
					Text: fmt.Sprintf("%s left and the table is closed.", name)})
			}
		case "playing":
			g, err := s.Game(ctx, t.GameID, t.GameVersion)
			if err != nil {
				return err
			}
			c.g = g
			if nobodyLeft {
				c.status = "abandoned"
				c.events = append(c.events, games.Event{Type: "closed", Seat: -1,
					Text: fmt.Sprintf("%s left and nobody else is at the table, so it is closed.", name)})
				break
			}
			if seat != games.Spectator {
				others := cloneSeats(t.Seats)
				others[seat] = seatRow{Seat: seat, Kind: "open"}
				st, _ := newPicker(t.Settings.Favorites, others, t.Settings.Language).pick("")
				st.Seat = seat
				seats[seat] = st
				c.seats = seats
				c.events = append(c.events, games.Event{Type: "takeover", Seat: seat,
					Text: fmt.Sprintf("%s left the table; {s:%d} takes over.", name, seat)})
			}
		case "finished":
			// The seating is the record; only host rights (rematch) move.
		default:
			return nil
		}
		if hosting && c.status != "abandoned" {
			text := fmt.Sprintf("%s left; the table has no host now.", name)
			if newHostSeat >= 0 {
				text = fmt.Sprintf("%s left; {s:%d} is the host now.", name, newHostSeat)
			}
			c.events = append(c.events, games.Event{Type: "host", Seat: newHostSeat, Text: text})
		}
		if hosting {
			c.extra = func(ctx context.Context, tx pgx.Tx, _ int64) error {
				_, err := tx.Exec(ctx, `UPDATE tables SET host_id=nullif($2,'')::uuid WHERE id=$1`, t.ID, newHost)
				return err
			}
		}
		_, err = s.commit(ctx, t, c)
		if errors.Is(err, errStale) {
			continue
		}
		return err
	}
	return fmt.Errorf("%w: the table is busy, try again", ErrConflict)
}

// releaseGames credits the user's public games to "a former player" and
// deletes (or, when others hold table records of them, hides) the rest.
func (s *Service) releaseGames(ctx context.Context, userID string) error {
	if _, err := s.Pool.Exec(ctx, `UPDATE games SET owner_gone=true, updated_at=now()
		WHERE owner_id=$1 AND kind='script' AND status='published' AND visibility<>'private'`, userID); err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id FROM games
		WHERE owner_id=$1 AND kind='script' AND NOT (status='published' AND visibility<>'private')`, userID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.abandonTablesOf(ctx, id); err != nil {
			return err
		}
		// Does anyone else hold a record of this game (a seat, a host, a
		// spectator at any of its tables)?
		var shared bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM tables t WHERE t.game_id=$1 AND (
					(t.host_id IS NOT NULL AND t.host_id<>$2)
					OR EXISTS (SELECT 1 FROM table_seats x WHERE x.table_id=t.id AND x.user_id IS NOT NULL AND x.user_id<>$2)
					OR EXISTS (SELECT 1 FROM table_spectators x WHERE x.table_id=t.id AND x.user_id<>$2)))`,
			id, userID).Scan(&shared); err != nil {
			return err
		}
		if shared {
			// Kept so those tables still render; hidden from every list and
			// unplayable (private, and the owner is about to be gone).
			if _, err := s.Pool.Exec(ctx, `UPDATE games SET owner_gone=true, visibility='private', updated_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
			continue
		}
		// Only the user's own tables refer to it: the versions and those
		// tables go with it.
		if _, err := s.Pool.Exec(ctx, `DELETE FROM games WHERE id=$1 AND owner_id=$2`, id, userID); err != nil {
			return err
		}
	}
	return nil
}

// abandonTablesOf closes every live table of a game with a visible event.
func (s *Service) abandonTablesOf(ctx context.Context, gameID string) error {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM tables WHERE game_id=$1 AND status IN ('lobby','playing')`, gameID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.abandon(ctx, id, gameRemovedText); err != nil {
			return err
		}
	}
	return nil
}

// abandon closes a live table with a visible event.
func (s *Service) abandon(ctx context.Context, tableID, text string) error {
	for attempt := 0; attempt < 8; attempt++ {
		t, err := loadTable(ctx, s.Pool, tableID, false)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if t.Status != "lobby" && t.Status != "playing" {
			return nil
		}
		_, err = s.commit(ctx, t, change{expected: t.Version, state: t.State, status: "abandoned",
			events: []games.Event{{Type: "closed", Seat: -1, Text: text}}})
		if errors.Is(err, errStale) {
			continue
		}
		return err
	}
	return fmt.Errorf("%w: the table is busy, try again", ErrConflict)
}
