// Package rooms is the multiplayer table service: creating and joining
// tables, applying moves with optimistic concurrency, the durable table-job
// queue that plays agents' turns and runs the clock, agent table talk, and the
// LISTEN/NOTIFY fan-out that keeps every open table live.
//
// Every state change follows one discipline (docs/00-architecture.md):
//
//	UPDATE tables SET state=$new, version=version+1 WHERE id=$id AND version=$expected
//
// together with the move row, the events and the follow-up jobs in the same
// transaction, then pg_notify('play_table', id), which Postgres delivers only
// on commit. Nothing is held in memory between requests except caches.
package rooms

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// NotifyChannel is the Postgres channel every table change is announced on.
const NotifyChannel = "play_table"

type Service struct {
	Pool *pgxpool.Pool

	// Loader loads a non-built-in game at a version (script games, phase 2).
	// Built-ins come from games.Builtin and never reach it.
	Loader func(ctx context.Context, gameID string, version int) (games.Game, error)
	// BrainFor picks the Brain for a game. Nil, or a nil Brain, plays
	// DefaultMove.
	BrainFor func(g games.Game) games.Brain
	// Chatter writes agents' table talk. Nil uses the canned lines.
	Chatter Chatter

	// Lease is how long a worker owns a claimed job (default 15s).
	Lease time.Duration
	// Think overrides the agents' thinking delay (tests use it to run fast).
	Think func(speed string, afterResult bool) time.Duration
	// BrainTimeout bounds one Brain.Choose call (default 2s).
	BrainTimeout time.Duration
	// IdleAfter is how long without human activity before a table is
	// abandoned (default 2h).
	IdleAfter time.Duration

	hub   *Hub
	wake  chan struct{}
	cache sync.Map // gameKey → games.Game
}

type gameKey struct {
	id      string
	version int
}

// New returns a service with its in-process hub.
func New(pool *pgxpool.Pool) *Service {
	return &Service{Pool: pool, hub: NewHub(), wake: make(chan struct{}, 1)}
}

func (s *Service) lease() time.Duration {
	if s.Lease <= 0 {
		return 15 * time.Second
	}
	return s.Lease
}

func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Hub is the in-process fan-out for this service's SSE subscribers.
func (s *Service) Hub() *Hub { return s.hub }

// ── Games ───────────────────────────────────────────────────────────────────

// Game resolves a game implementation. Built-ins win; anything else goes
// through Loader and is cached per (id, version) because loading a script
// game compiles it.
func (s *Service) Game(ctx context.Context, id string, version int) (games.Game, error) {
	if g, ok := games.Builtin(id); ok {
		return g, nil
	}
	k := gameKey{id, version}
	if g, ok := s.cache.Load(k); ok {
		return g.(games.Game), nil
	}
	if s.Loader == nil {
		return nil, fmt.Errorf("%w: game %q", ErrNotFound, id)
	}
	g, err := s.Loader(ctx, id, version)
	if err != nil {
		return nil, err
	}
	s.cache.Store(k, g)
	return g, nil
}

// ── Rows ────────────────────────────────────────────────────────────────────

type seatRow struct {
	Seat    int
	Kind    string // human | agent | open
	UserID  string
	AgentID string
	Name    string
}

type tableRow struct {
	ID, Code, Name, HostID string
	GameID                 string
	GameVersion            int
	GameName, GameKind     string
	Status                 string
	Options                map[string]any
	Settings               Settings
	Seed                   int64
	State                  games.State
	Version                int64
	ToMove                 []int
	Deadline               *time.Time
	Outcome                *games.Outcome
	RematchID              string
	Seats                  []seatRow
}

func (t *tableRow) seatOf(userID string) int {
	for _, st := range t.Seats {
		if st.Kind == "human" && st.UserID == userID {
			return st.Seat
		}
	}
	return games.Spectator
}

func (t *tableRow) seat(n int) *seatRow {
	for i := range t.Seats {
		if t.Seats[i].Seat == n {
			return &t.Seats[i]
		}
	}
	return nil
}

func (t *tableRow) names() []string {
	out := make([]string, len(t.Seats))
	for i, st := range t.Seats {
		out[i] = st.Name
		if st.Kind == "open" || st.Name == "" {
			out[i] = fmt.Sprintf("Seat %d", st.Seat+1)
		}
	}
	return out
}

const tableCols = `t.id, t.code, t.name, t.host_id, t.game_id, t.game_version, g.name, g.kind, t.status, t.options, t.settings,
	t.seed, t.state, t.version, t.to_move, t.deadline, t.outcome, coalesce(t.rematch_id::text,'')`

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadTable(ctx context.Context, q querier, id string, forUpdate bool) (*tableRow, error) {
	sql := `SELECT ` + tableCols + ` FROM tables t JOIN games g ON g.id = t.game_id WHERE t.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF t`
	}
	var t tableRow
	var opts, sets, state, outcome []byte
	err := q.QueryRow(ctx, sql, id).Scan(&t.ID, &t.Code, &t.Name, &t.HostID, &t.GameID, &t.GameVersion, &t.GameName, &t.GameKind,
		&t.Status, &opts, &sets, &t.Seed, &state, &t.Version, &t.ToMove, &t.Deadline, &outcome, &t.RematchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		if isBadUUID(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal(opts, &t.Options)
	_ = json.Unmarshal(sets, &t.Settings)
	if len(state) > 0 {
		t.State = games.State(state)
	}
	if len(outcome) > 0 && string(outcome) != "null" {
		t.Outcome = new(games.Outcome)
		_ = json.Unmarshal(outcome, t.Outcome)
	}
	rows, err := q.Query(ctx, `SELECT seat, kind, coalesce(user_id::text,''), coalesce(agent_id,''), name
		FROM table_seats WHERE table_id=$1 ORDER BY seat`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st seatRow
		if err := rows.Scan(&st.Seat, &st.Kind, &st.UserID, &st.AgentID, &st.Name); err != nil {
			return nil, err
		}
		t.Seats = append(t.Seats, st)
	}
	return &t, rows.Err()
}

func isBadUUID(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid input syntax for type uuid")
}

// ── Preferences ─────────────────────────────────────────────────────────────

type userInfo struct {
	Name  string
	Prefs map[string]any
}

func loadUser(ctx context.Context, q querier, userID string) (userInfo, error) {
	var u userInfo
	var raw []byte
	err := q.QueryRow(ctx, `SELECT u.name, coalesce(p.data, '{}') FROM users u
		LEFT JOIN user_preferences p ON p.user_id = u.id WHERE u.id = $1`, userID).Scan(&u.Name, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	if err != nil {
		return u, err
	}
	_ = json.Unmarshal(raw, &u.Prefs)
	if dn := prefString(u.Prefs, "display_name", ""); dn != "" {
		u.Name = dn
	}
	if u.Name == "" {
		u.Name = "Player"
	}
	u.Name = clip(u.Name, 40)
	return u, nil
}

// settingsFrom turns the host's preferences into a table's settings,
// applying the documented defaults to anything missing or malformed.
func settingsFrom(prefs map[string]any, meta games.Meta) Settings {
	st := Settings{
		TurnSeconds: meta.TurnSeconds,
		AgentSpeed:  oneOf(prefString(prefs, "agent_speed", ""), "natural", "fast", "natural", "slow"),
		TableTalk:   oneOf(prefString(prefs, "table_talk", ""), "all", "all", "quiet", "off"),
		Difficulty:  oneOf(prefString(prefs, "agent_difficulty", ""), "regular", "casual", "regular", "shark"),
		Language:    normLang(prefString(prefs, "language", "en")),
		FillEmpty:   prefBool(prefs, "fill_empty_seats", true),
	}
	if st.TurnSeconds <= 0 {
		st.TurnSeconds = 30
	}
	if v, ok := prefInt(prefs, "turn_seconds"); ok && validTurnSeconds(v) {
		st.TurnSeconds = v
	}
	if fav, ok := prefs["favorite_agents"].([]any); ok {
		for _, f := range fav {
			if id, ok := f.(string); ok {
				st.Favorites = append(st.Favorites, id)
			}
		}
	}
	return st
}

func validTurnSeconds(v int) bool { return v == 0 || (v >= 5 && v <= 600) }

func prefString(p map[string]any, k, def string) string {
	if v, ok := p[k].(string); ok && v != "" {
		return v
	}
	return def
}

func prefBool(p map[string]any, k string, def bool) bool {
	switch v := p[k].(type) {
	case bool:
		return v
	case string:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// prefInt accepts numbers and numeric strings: the settings page stores the
// turn clock as "30".
func prefInt(p map[string]any, k string) (int, bool) {
	switch v := p[k].(type) {
	case float64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	return 0, false
}

func oneOf(v, def string, allowed ...string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return def
}

func normLang(l string) string {
	if strings.HasPrefix(strings.ToLower(l), "zh") {
		return "zh"
	}
	return "en"
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ── Randomness ──────────────────────────────────────────────────────────────

// Invite codes avoid characters people misread: no I, L, O, 0 or 1.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newCode() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	out := make([]byte, 6)
	for i, x := range b {
		// 256 % 31 bias is under 1%: irrelevant for an invite code.
		out[i] = codeAlphabet[int(x)%len(codeAlphabet)]
	}
	return string(out)
}

// newSeed is the game's only randomness. crypto/rand, so nobody can predict
// a deck from the table's creation time.
func newSeed() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.BigEndian.Uint64(b[:]) & (1<<63 - 1))
}
