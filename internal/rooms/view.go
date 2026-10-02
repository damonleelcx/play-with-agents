package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/agents"
	"github.com/damonleelcx/play-with-agents/internal/games"
)

const (
	logWindow  = 60
	chatWindow = 60
)

var seatRef = regexp.MustCompile(`\{s:(\d+)\}`)

// Subst replaces {s:N} with seat N's display name. Games never know names;
// nothing leaves this package without passing through here.
func Subst(text string, names []string) string {
	if len(text) < 5 {
		return text
	}
	return seatRef.ReplaceAllStringFunc(text, func(m string) string {
		n, _ := strconv.Atoi(m[3 : len(m)-1])
		if n >= 0 && n < len(names) {
			return names[n]
		}
		return "Seat " + strconv.Itoa(n+1)
	})
}

// substDeep applies Subst to every string inside an arbitrary JSON value,
// so generic board views ("message": "{s:1} to move") are covered too.
func substDeep(v any, names []string) any {
	switch x := v.(type) {
	case string:
		return Subst(x, names)
	case []any:
		for i := range x {
			x[i] = substDeep(x[i], names)
		}
		return x
	case map[string]any:
		for k, e := range x {
			x[k] = substDeep(e, names)
		}
		return x
	}
	return v
}

// substView renders a view for clients: a JSON round trip turns typed game
// data into plain values that substDeep can walk.
func substView(v games.View, names []string) (*games.View, error) {
	raw, err := json.Marshal(v.Data)
	if err != nil {
		return nil, err
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	return &games.View{Kind: v.Kind, Data: substDeep(data, names), Status: Subst(v.Status, names)}, nil
}

// View builds the TableView userID may see. Anyone seated, the host and
// registered spectators may look; others get ErrForbidden.
func (s *Service) View(ctx context.Context, tableID, userID string) (*TableView, error) {
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
	return s.assemble(ctx, t, userID)
}

// CanWatch reports whether userID may see the table (and its stream).
func (s *Service) CanWatch(ctx context.Context, tableID, userID string) (bool, error) {
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return false, err
	}
	return s.canWatch(ctx, t, userID)
}

// Version is the table's current version (the stream's first frame).
func (s *Service) Version(ctx context.Context, tableID string) (int64, error) {
	var v int64
	err := s.Pool.QueryRow(ctx, `SELECT version FROM tables WHERE id=$1`, tableID).Scan(&v)
	if err != nil && (errors.Is(err, pgx.ErrNoRows) || isBadUUID(err)) {
		return 0, ErrNotFound
	}
	return v, err
}

func (s *Service) canWatch(ctx context.Context, t *tableRow, userID string) (bool, error) {
	if t.HostID == userID || t.seatOf(userID) != games.Spectator {
		return true, nil
	}
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM table_spectators WHERE table_id=$1 AND user_id=$2)`, t.ID, userID).Scan(&ok)
	return ok, err
}

func avatarFor(st seatRow) string {
	if st.Kind == "agent" {
		if a, ok := agents.Get(st.AgentID); ok {
			return a.Avatar
		}
	}
	return ""
}

func (s *Service) assemble(ctx context.Context, t *tableRow, userID string) (*TableView, error) {
	me := t.seatOf(userID)
	names := t.names()
	v := &TableView{
		ID: t.ID, Name: t.Name, Code: t.Code,
		Game:   GameRef{ID: t.GameID, Name: t.GameName, Kind: t.GameKind},
		Status: t.Status, HostID: t.HostID, IsHost: t.HostID == userID, MySeat: me,
		Version: t.Version, ToMove: t.ToMove, Deadline: t.Deadline, TurnSeconds: t.Settings.TurnSeconds,
		Legal: []games.MoveSpec{}, Log: []LogEntry{}, Chat: []ChatLine{}, RematchID: t.RematchID,
		Paused: t.PausedAt != nil && t.Status == "playing",
	}
	if v.ToMove == nil {
		v.ToMove = []int{}
	}
	for _, st := range t.Seats {
		v.Seats = append(v.Seats, SeatView{Seat: st.Seat, Kind: st.Kind, Name: names[st.Seat], Avatar: avatarFor(st),
			AgentID: st.AgentID, IsMe: st.Kind == "human" && st.UserID == userID, Away: st.away()})
	}
	if t.Outcome != nil {
		o := *t.Outcome
		o.Summary = Subst(o.Summary, names)
		v.Outcome = &o
	}
	if t.State != nil {
		g, err := s.Game(ctx, t.GameID, t.GameVersion)
		if err != nil {
			return nil, err
		}
		gv, err := g.View(t.State, me)
		if err != nil {
			return nil, err
		}
		if v.View, err = substView(gv, names); err != nil {
			return nil, err
		}
		// Legal moves only for my own seat and only on my turn: never
		// another seat's options, which could reveal its hidden state.
		if me != games.Spectator && t.Status == "playing" && contains(t.ToMove, me) {
			legal, err := g.Legal(t.State, me)
			if err != nil {
				return nil, err
			}
			for _, m := range legal {
				m.Label = Subst(m.Label, names)
				v.Legal = append(v.Legal, m)
			}
		}
	}
	var err error
	if v.Log, err = s.log(ctx, t.ID, me, names, logWindow); err != nil {
		return nil, err
	}
	if v.Chat, err = s.recentChat(ctx, t.ID, chatWindow); err != nil {
		return nil, err
	}
	return v, nil
}

// log returns the last n events seat may see, oldest first. A spectator
// (-1) is never in only_seats, so it gets public events only.
func (s *Service) log(ctx context.Context, tableID string, seat int, names []string, n int) ([]LogEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT seq, type, seat, text, at FROM table_events
		WHERE table_id=$1 AND (cardinality(only_seats) = 0 OR $2 = ANY(only_seats))
		ORDER BY seq DESC LIMIT $3`, tableID, seat, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.Seq, &e.Type, &e.Seat, &e.Text, &e.At); err != nil {
			return nil, err
		}
		e.Text = Subst(e.Text, names)
		out = append(out, e)
	}
	reverse(out)
	return out, rows.Err()
}

func (s *Service) recentChat(ctx context.Context, tableID string, n int) ([]ChatLine, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, seat, name, coalesce(agent_id,''), text, at FROM table_chat
		WHERE table_id=$1 ORDER BY id DESC LIMIT $2`, tableID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatLine{}
	for rows.Next() {
		var c ChatLine
		var agentID string
		if err := rows.Scan(&c.ID, &c.Seat, &c.Name, &agentID, &c.Text, &c.At); err != nil {
			return nil, err
		}
		c.Agent = agentID != ""
		if a, ok := agents.Get(agentID); ok {
			c.Avatar = a.Avatar
		}
		out = append(out, c)
	}
	reverse(out)
	return out, rows.Err()
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func contains(s []int, x int) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
