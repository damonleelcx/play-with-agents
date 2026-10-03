// Package games is the contract every playable game implements, whether it is
// written in Go (Texas Hold'em) or authored on the platform as a script.
//
// A game is a pure state machine. State is an opaque JSON document the game
// owns; the platform persists it after every move and never interprets it.
// Every function is deterministic: randomness lives inside the state (a seed
// and a counter), so replaying the same moves from the same setup yields the
// same state. That is what lets a table resume after a crash and lets the
// playtester re-run a failing game exactly.
package games

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Seat is a position at the table, 0-based. Spectator (-1) sees only public
// information.
type Seat = int

const Spectator Seat = -1

// State is the game's own serialisation. Never mutate one in place: Apply
// returns a new State.
type State = json.RawMessage

// Config is what a table is created with.
type Config struct {
	Seats   int            `json:"seats"`
	Options map[string]any `json:"options,omitempty"`
}

// Move is what a player (human, agent or the timeout) submits.
type Move struct {
	Type string         `json:"type"`
	Args map[string]any `json:"args,omitempty"`
}

// MoveSpec describes one legal move for the seat to act. Fixed moves carry
// their Args; parametric moves carry a Range for one integer argument (the
// client picks a value inside it). UI hints let the generic board renderer
// map clicks to moves without knowing the game:
//
//	{"cell":[r,c]}               click this cell
//	{"from":[r,c],"to":[r,c]}    pick up a piece, drop it on a cell
//	{"zone":"hand","index":2}    click a card in a zone
//
// A move with no UI hint is rendered as a button with its Label.
type MoveSpec struct {
	Type  string         `json:"type"`
	Label string         `json:"label"`
	Args  map[string]any `json:"args,omitempty"`
	Range *Range         `json:"range,omitempty"`
	UI    map[string]any `json:"ui,omitempty"`
}

type Range struct {
	Arg  string `json:"arg"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
	Step int    `json:"step,omitempty"`
}

// Event is something that happened during Apply, for animation, the table
// log and the agents' table talk. Only, when set, limits who may see it (a
// player's own hole cards); otherwise it is public.
type Event struct {
	Type string         `json:"type"`
	Seat Seat           `json:"seat"`
	Text string         `json:"text,omitempty"`
	Data map[string]any `json:"data,omitempty"`
	Only []Seat         `json:"only,omitempty"`
}

// VisibleTo reports whether seat may see the event.
func (e Event) VisibleTo(seat Seat) bool {
	if len(e.Only) == 0 {
		return true
	}
	for _, s := range e.Only {
		if s == seat {
			return true
		}
	}
	return false
}

// View is what one seat may see. Kind selects the client renderer: "holdem"
// has a bespoke table; "board" is the generic renderer described in
// docs/00-architecture.md. Data must never contain information hidden from
// that seat.
type View struct {
	Kind   string `json:"kind"`
	Data   any    `json:"data"`
	Status string `json:"status,omitempty"`
}

// Outcome is set once the game is over. Rank is 1 for the winner(s); ties
// share a rank. Score is game-specific (chips, points).
type Outcome struct {
	Rank    []int     `json:"rank"`
	Score   []float64 `json:"score"`
	Summary string    `json:"summary"`
}

// Meta describes a game to the lobby, the studio and the agents.
type Meta struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Summary     string         `json:"summary"`
	MinSeats    int            `json:"min_seats"`
	MaxSeats    int            `json:"max_seats"`
	HiddenInfo  bool           `json:"hidden_info"`
	Options     map[string]any `json:"options,omitempty"` // defaults
	RulesMD     string         `json:"rules_md,omitempty"`
	TurnSeconds int            `json:"turn_seconds"` // default human turn clock
}

// Game is the contract. Implementations must be safe for concurrent use: all
// methods are pure functions of their arguments.
type Game interface {
	Meta() Meta
	// Setup deals the opening position. seed is the only source of randomness.
	Setup(cfg Config, seed int64) (State, error)
	// ToMove lists the seats that must act now; empty once the game is over.
	ToMove(st State) ([]Seat, error)
	// Legal lists the moves seat may make now; empty if it is not its turn.
	Legal(st State, seat Seat) ([]MoveSpec, error)
	// Apply validates and plays a move. It returns ErrIllegal (wrapped) for a
	// move that is not legal, never a panic.
	Apply(st State, seat Seat, m Move) (State, []Event, error)
	View(st State, seat Seat) (View, error)
	// Outcome is nil while the game is in progress.
	Outcome(st State) (*Outcome, error)
	// DefaultMove is played for a seat whose clock runs out (check or fold in
	// poker; the first legal move otherwise).
	DefaultMove(st State, seat Seat) (Move, error)
}

var ErrIllegal = errors.New("illegal move")

func Illegal(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrIllegal, fmt.Sprintf(format, a...))
}

// ── registry of built-in games ──────────────────────────────────────────────

var (
	mu       sync.RWMutex
	registry = map[string]Game{}
)

func Register(g Game) {
	mu.Lock()
	defer mu.Unlock()
	registry[g.Meta().ID] = g
}

func Builtin(id string) (Game, bool) {
	mu.RLock()
	defer mu.RUnlock()
	g, ok := registry[id]
	return g, ok
}

func Builtins() []Meta {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Meta, 0, len(registry))
	for _, g := range registry {
		out = append(out, g.Meta())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
