package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/holdem"
)

// What Aoi, the host agent, needs from the tables beyond the table API:
// a player's tables with their seat and pause state, per-table settings for a
// table she sets up, coaching from the caller's own seat, and the player's
// results. Everything here is scoped to the calling user.

// PlayerTable is one of the user's tables as Aoi talks about it.
type PlayerTable struct {
	TableSummary
	GameID       string    `json:"game_id"`
	IsHost       bool      `json:"is_host"`
	MySeat       int       `json:"my_seat"` // -1 when the user only hosts or watches
	Away         bool      `json:"away"`
	Paused       bool      `json:"paused"`
	PausedReason string    `json:"paused_reason,omitempty"`
	RematchID    string    `json:"rematch_id,omitempty"`
	Players      []string  `json:"players"`
	CreatedAt    time.Time `json:"created_at"`
}

// PlayerTables lists the tables the user hosts, sits at or watches: the
// unfinished ones (lobby, playing), plus finished ones when withFinished is
// set; most recently active first, at most 20.
func (s *Service) PlayerTables(ctx context.Context, userID string, withFinished bool) ([]PlayerTable, error) {
	statuses := []string{"lobby", "playing"}
	if withFinished {
		statuses = append(statuses, "finished")
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id, t.name, t.code, g.name, t.status, t.updated_at, t.created_at, t.game_id,
			coalesce(t.host_id::text,'') = $1::text, coalesce(t.rematch_id::text,''), t.paused_at IS NOT NULL, t.fault_at IS NOT NULL,
			coalesce((SELECT x.seat FROM table_seats x WHERE x.table_id=t.id AND x.user_id=$1::uuid LIMIT 1), -1),
			coalesce((SELECT x.away_since IS NOT NULL FROM table_seats x WHERE x.table_id=t.id AND x.user_id=$1::uuid LIMIT 1), false),
			coalesce((SELECT array_agg(CASE WHEN x.kind='open' THEN '' ELSE x.name END ORDER BY x.seat) FROM table_seats x WHERE x.table_id=t.id), '{}')
		FROM tables t JOIN games g ON g.id=t.game_id
		WHERE t.status = ANY($2) AND (t.host_id=$1::uuid
			OR EXISTS (SELECT 1 FROM table_seats x WHERE x.table_id=t.id AND x.user_id=$1::uuid)
			OR EXISTS (SELECT 1 FROM table_spectators x WHERE x.table_id=t.id AND x.user_id=$1::uuid))
		ORDER BY t.updated_at DESC LIMIT 20`, userID, statuses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayerTable{}
	for rows.Next() {
		var p PlayerTable
		var fault bool
		var names []string
		if err := rows.Scan(&p.ID, &p.Name, &p.Code, &p.GameName, &p.Status, &p.UpdatedAt, &p.CreatedAt, &p.GameID,
			&p.IsHost, &p.RematchID, &p.Paused, &fault, &p.MySeat, &p.Away, &names); err != nil {
			return nil, err
		}
		p.SeatsTotal = len(names)
		p.Players = []string{}
		for _, n := range names {
			if n != "" {
				p.SeatsTaken++
				p.Players = append(p.Players, n)
			}
		}
		if p.Status == "playing" && p.Paused {
			p.PausedReason = "idle"
			if fault {
				p.PausedReason = "fault"
			}
		} else {
			p.Paused = false
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LobbySettings are per-table overrides of the host's preferences that Aoi
// applies to a table she sets up ("shark-level bots, 60-second clock"). Empty
// fields keep what the table was created with.
type LobbySettings struct {
	Difficulty  string // casual | regular | shark
	AgentSpeed  string // fast | natural | slow
	TableTalk   string // all | quiet | off
	TurnSeconds *int   // 0 (no clock) or 5..600
}

// ApplyLobbySettings changes a lobby table's settings. Only the host, only in
// the lobby (before the first deal), and only to valid values.
func (s *Service) ApplyLobbySettings(ctx context.Context, userID, tableID string, ls LobbySettings) error {
	if ls.Difficulty != "" && oneOf(ls.Difficulty, "", "casual", "regular", "shark") == "" {
		return badInput("difficulty must be casual, regular or shark")
	}
	if ls.AgentSpeed != "" && oneOf(ls.AgentSpeed, "", "fast", "natural", "slow") == "" {
		return badInput("agent speed must be fast, natural or slow")
	}
	if ls.TableTalk != "" && oneOf(ls.TableTalk, "", "all", "quiet", "off") == "" {
		return badInput("table talk must be all, quiet or off")
	}
	if ls.TurnSeconds != nil && !validTurnSeconds(*ls.TurnSeconds) {
		return badInput("turn_seconds must be 0 (no clock) or 5..600")
	}
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return err
	}
	if !t.hostRights(userID) {
		return ErrForbidden
	}
	if t.Status != "lobby" {
		return fmt.Errorf("%w: settings can only change before the game starts", ErrConflict)
	}
	st := t.Settings
	if ls.Difficulty != "" {
		st.Difficulty = ls.Difficulty
	}
	if ls.AgentSpeed != "" {
		st.AgentSpeed = ls.AgentSpeed
	}
	if ls.TableTalk != "" {
		st.TableTalk = ls.TableTalk
	}
	if ls.TurnSeconds != nil {
		st.TurnSeconds = *ls.TurnSeconds
	}
	raw, _ := json.Marshal(st)
	tag, err := s.Pool.Exec(ctx, `UPDATE tables SET settings=$3, updated_at=now() WHERE id=$1 AND version=$2 AND status='lobby'`, t.ID, t.Version, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: the table changed, try again", ErrConflict)
	}
	return nil
}

// CoachReport is coaching for one person from their own seat. It is built
// only from that seat's view (games.Game.View for the seat, and holdem.Coach,
// which itself works only from the seat's view), so it can never carry
// another seat's hidden information.
type CoachReport struct {
	TableID  string           `json:"table_id"`
	GameID   string           `json:"game_id"`
	GameName string           `json:"game_name"`
	Seat     int              `json:"seat"`
	YourTurn bool             `json:"your_turn"`
	Legal    []string         `json:"legal"`     // the caller's legal moves, labelled
	View     json.RawMessage  `json:"your_view"` // the caller's own seat view
	Holdem   *holdem.CoachTip `json:"holdem,omitempty"`
}

// CoachFor analyses the caller's seat at a playing table. A person who is
// not seated there (a spectator, the host of an all-agent table) gets an
// InputError: there is no seat to coach, and a spectator view would not be
// theirs to reason from anyway.
func (s *Service) CoachFor(ctx context.Context, userID, tableID string) (*CoachReport, error) {
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return nil, err
	}
	ok, err := s.canWatch(ctx, t, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	seat := t.seatOf(userID)
	if seat == games.Spectator {
		return nil, badInput("you are not seated at this table, so there is no hand of yours to coach")
	}
	if t.Status != "playing" {
		return nil, badInput("the game at this table is not in progress")
	}
	g, err := s.Game(ctx, t.GameID, t.GameVersion)
	if err != nil {
		return nil, err
	}
	names := t.names()
	v, err := g.View(t.State, seat)
	if err != nil {
		return nil, err
	}
	sv, err := substView(v, names)
	if err != nil {
		return nil, err
	}
	rep := &CoachReport{TableID: t.ID, GameID: t.GameID, GameName: t.GameName, Seat: seat, YourTurn: contains(t.ToMove, seat), Legal: []string{}}
	rep.View, _ = json.Marshal(sv)
	if rep.YourTurn {
		if legal, err := g.Legal(t.State, seat); err == nil {
			for _, m := range legal {
				label := m.Label
				if label == "" {
					label = m.Type
				}
				rep.Legal = append(rep.Legal, Subst(label, names))
			}
		}
	}
	if t.GameKind == "builtin" && t.GameID == "holdem" {
		if tip, err := holdem.Coach(t.State, seat); err == nil {
			rep.Holdem = &tip
		}
	}
	return rep, nil
}

// GameResult is one finished game the user sat in.
type GameResult struct {
	TableID    string    `json:"table_id"`
	GameID     string    `json:"game_id"`
	GameName   string    `json:"game_name"`
	Place      int       `json:"place"` // 1 = won (ties share a place); 0 unknown
	Players    int       `json:"players"`
	Score      float64   `json:"score"`
	Chips      int       `json:"chips"`     // hold'em: chips won or lost (play money)
	HasChips   bool      `json:"has_chips"` // Chips applies (hold'em)
	FinishedAt time.Time `json:"finished_at"`
}

// PlayerStats summarises the user's finished games.
type PlayerStats struct {
	Played    int            `json:"played"` // finished games they sat in
	Wins      int            `json:"wins"`
	ChipsWon  int            `json:"chips_won"` // net, hold'em only, play money
	InPlay    int            `json:"in_play"`   // unfinished tables they sit at
	ByGame    map[string]int `json:"by_game"`   // game name → games played
	Recent    []GameResult   `json:"recent"`    // newest first, at most recentN
	FirstPlay *time.Time     `json:"first_play,omitempty"`
}

// PlayerResults reads the user's record from the tables they sat in at the
// end of the game (a seat they left mid-game was taken over by an agent and
// is not theirs any more).
func (s *Service) PlayerResults(ctx context.Context, userID string, recentN int) (*PlayerStats, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.id, t.game_id, g.name, x.seat, t.outcome, t.options, t.updated_at,
			(SELECT count(*) FROM table_seats y WHERE y.table_id=t.id)
		FROM tables t JOIN games g ON g.id=t.game_id JOIN table_seats x ON x.table_id=t.id AND x.user_id=$1
		WHERE t.status='finished' AND t.outcome IS NOT NULL
		ORDER BY t.updated_at DESC LIMIT 1000`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	st := &PlayerStats{ByGame: map[string]int{}, Recent: []GameResult{}}
	for rows.Next() {
		var r GameResult
		var seat int
		var outRaw, optRaw []byte
		if err := rows.Scan(&r.TableID, &r.GameID, &r.GameName, &seat, &outRaw, &optRaw, &r.FinishedAt, &r.Players); err != nil {
			return nil, err
		}
		var o games.Outcome
		if json.Unmarshal(outRaw, &o) != nil {
			continue
		}
		if seat >= 0 && seat < len(o.Rank) {
			r.Place = o.Rank[seat]
		}
		if seat >= 0 && seat < len(o.Score) {
			r.Score = o.Score[seat]
		}
		if r.GameID == "holdem" {
			var opts map[string]any
			_ = json.Unmarshal(optRaw, &opts)
			if stack, ok := toFloat(opts["starting_stack"]); ok {
				r.Chips, r.HasChips = int(r.Score-stack), true
				st.ChipsWon += r.Chips
			}
		}
		st.Played++
		if r.Place == 1 {
			st.Wins++
		}
		st.ByGame[r.GameName]++
		if len(st.Recent) < recentN {
			st.Recent = append(st.Recent, r)
		}
		at := r.FinishedAt
		st.FirstPlay = &at
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT count(DISTINCT t.id) FROM tables t JOIN table_seats x ON x.table_id=t.id AND x.user_id=$1
		WHERE t.status IN ('lobby','playing')`, userID).Scan(&st.InPlay)
	return st, err
}

// TopGames returns ByGame's names, most played first.
func (p *PlayerStats) TopGames() []string {
	out := make([]string, 0, len(p.ByGame))
	for k := range p.ByGame {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if p.ByGame[out[i]] != p.ByGame[out[j]] {
			return p.ByGame[out[i]] > p.ByGame[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// IsClientError reports whether err is about the request (bad input, not
// found, forbidden, conflict) rather than the system, and returns a message
// fit for the player.
func IsClientError(err error) (string, bool) {
	var in *InputError
	switch {
	case errors.As(err, &in):
		return in.Msg, true
	case errors.Is(err, ErrNotFound):
		return "not found", true
	case errors.Is(err, ErrForbidden):
		return "only the host can do that", true
	case errors.Is(err, ErrConflict):
		msg := err.Error()
		if p := ErrConflict.Error() + ": "; len(msg) > len(p) && msg[:len(p)] == p {
			msg = msg[len(p):]
		}
		return msg, true
	}
	return "", false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}
