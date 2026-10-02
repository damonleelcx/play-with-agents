// Package holdem is no-limit Texas Hold'em for 2 to 9 seats: the game engine
// (a games.Game), a fast seven-card evaluator, the AI player (Brain) and the
// coaching helper the host agent uses for tips.
//
// The engine is a pure state machine over a JSON document. All randomness
// comes from the table seed: each hand's deck is a function of (seed,
// hand number), so a table replays exactly from its setup and moves.
package holdem

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// ID is the registry id of the built-in game.
const ID = "holdem"

func init() { games.Register(New()) }

// Game implements games.Game. It holds no state, so one value is shared by
// every table and is safe for concurrent use.
type Game struct{}

// New returns the Hold'em game.
func New() *Game { return &Game{} }

var _ games.Game = (*Game)(nil)

const rulesMD = `# No-limit Texas Hold'em

Each player gets two private cards. Five community cards are dealt in three
steps (the flop, the turn and the river) with a betting round before each and
one after the river. Make the best five-card hand from your two cards and the
board, or make everyone else fold.

- Blinds: the two players left of the button post the small and big blind.
  Heads-up, the button posts the small blind and acts first before the flop.
- On your turn: **fold**, **check** (when there is no bet), **call**, **bet /
  raise** (at least the size of the last bet or raise), or go **all-in**.
- A short all-in that is less than a full raise does not reopen the betting
  for players who already acted: they may only call or fold.
- Players who run out of chips are out. Blinds double every few hands.
- The game ends when one player has every chip, or after the hand limit.

Chips are play money only.`

func (g *Game) Meta() games.Meta {
	d := defaultOptions()
	return games.Meta{
		ID:         ID,
		Name:       "Texas Hold'em",
		Summary:    "No-limit Texas Hold'em: two hole cards, five on the board, best hand or the last one standing wins.",
		MinSeats:   2,
		MaxSeats:   9,
		HiddenInfo: true,
		Options: map[string]any{
			"starting_stack":      d.StartingStack,
			"small_blind":         d.SmallBlind,
			"big_blind":           d.BigBlind,
			"blinds_double_every": d.BlindsDoubleEvery,
			"max_hands":           d.MaxHands,
		},
		RulesMD:     rulesMD,
		TurnSeconds: 30,
	}
}

// parseOptions overlays the table's options on the defaults. Unknown keys
// are ignored (the platform may pass table-level settings through), but a
// known key with a non-integer or out-of-range value is an error.
func parseOptions(in map[string]any) (options, error) {
	o := defaultOptions()
	fields := []struct {
		key string
		dst *int
		min int
	}{
		{"starting_stack", &o.StartingStack, 1},
		{"small_blind", &o.SmallBlind, 1},
		{"big_blind", &o.BigBlind, 1},
		{"blinds_double_every", &o.BlindsDoubleEvery, 0},
		{"max_hands", &o.MaxHands, 0},
	}
	for _, f := range fields {
		v, ok := in[f.key]
		if !ok || v == nil {
			continue
		}
		n, ok := toInt(v)
		if !ok || n < f.min || n > 1_000_000_000 {
			return o, fmt.Errorf("holdem: option %s must be an integer >= %d", f.key, f.min)
		}
		*f.dst = n
	}
	if o.BigBlind < o.SmallBlind {
		return o, fmt.Errorf("holdem: big_blind (%d) must be at least small_blind (%d)", o.BigBlind, o.SmallBlind)
	}
	return o, nil
}

// toInt accepts the integer forms a move or option can arrive in: Go ints
// from in-process callers, float64 or json.Number from decoded JSON, and
// rejects fractions, NaN, strings and everything else.
func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int8:
		return int(x), true
	case int16:
		return int(x), true
	case int32:
		return int(x), true
	case int64:
		if x < math.MinInt32 || x > math.MaxInt32 {
			return 0, false
		}
		return int(x), true
	case uint8:
		return int(x), true
	case uint16:
		return int(x), true
	case uint32:
		return int(x), true
	case float32:
		return toInt(float64(x))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) || math.Abs(x) > math.MaxInt32 {
			return 0, false
		}
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return toInt(n)
	}
	return 0, false
}

func intArg(args map[string]any, key string) (int, error) {
	v, ok := args[key]
	if !ok {
		return 0, games.Illegal("missing %q", key)
	}
	n, ok := toInt(v)
	if !ok {
		return 0, games.Illegal("%q must be a whole number of chips", key)
	}
	return n, nil
}

func (g *Game) Setup(cfg games.Config, seed int64) (games.State, error) {
	if cfg.Seats < 2 || cfg.Seats > 9 {
		return nil, fmt.Errorf("holdem: needs 2 to 9 seats, got %d", cfg.Seats)
	}
	o, err := parseOptions(cfg.Options)
	if err != nil {
		return nil, err
	}
	s := &state{
		V: stateVersion, Seed: seed, Opts: o,
		SmallBlind: o.SmallBlind, BigBlind: o.BigBlind, MinRaise: o.BigBlind,
		Players: make([]player, cfg.Seats),
	}
	for i := range s.Players {
		s.Players[i] = player{Stack: o.StartingStack, Status: statusActive, StartStack: o.StartingStack}
	}
	s.startHand()
	return s.encode()
}

func (g *Game) ToMove(st games.State) ([]games.Seat, error) {
	s, err := decodeState(st)
	if err != nil {
		return nil, err
	}
	if s.Street == streetOver {
		return []games.Seat{}, nil
	}
	return []games.Seat{s.ToAct}, nil
}

func (g *Game) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	s, err := decodeState(st)
	if err != nil {
		return nil, err
	}
	return s.legalSpecs(seat), nil
}

func (s *state) legalSpecs(seat int) []games.MoveSpec {
	l, ok := s.legalFor(seat)
	if !ok {
		return []games.MoveSpec{}
	}
	p := &s.Players[seat]
	var out []games.MoveSpec
	if l.canFold {
		out = append(out, games.MoveSpec{Type: "fold", Label: "Fold"})
	}
	if l.canCheck {
		out = append(out, games.MoveSpec{Type: "check", Label: "Check"})
	}
	if l.canCall {
		label := "Call " + strconv.Itoa(l.callAmt)
		if l.callAmt == p.Stack {
			label += " (all-in)"
		}
		out = append(out, games.MoveSpec{Type: "call", Label: label})
	}
	if l.canRaise {
		label := "Raise to"
		if s.CurrentBet == 0 {
			label = "Bet"
		}
		out = append(out, games.MoveSpec{Type: "raise", Label: label,
			Range: &games.Range{Arg: "to", Min: l.raiseMin, Max: l.raiseMax, Step: 1}})
	}
	if l.canAllin {
		out = append(out, games.MoveSpec{Type: "allin", Label: "All-in " + strconv.Itoa(p.Stack)})
	}
	return out
}

func (g *Game) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	s, err := decodeState(st)
	if err != nil {
		return nil, nil, err
	}
	if err := s.act(seat, m); err != nil {
		return nil, nil, err
	}
	out, err := s.encode()
	if err != nil {
		return nil, nil, err
	}
	return out, s.ev, nil
}

func (g *Game) Outcome(st games.State) (*games.Outcome, error) {
	s, err := decodeState(st)
	if err != nil {
		return nil, err
	}
	if s.Street != streetOver {
		return nil, nil
	}
	o := &games.Outcome{Rank: s.ranking(), Score: make([]float64, s.n()), Summary: s.summary()}
	for i := range s.Players {
		o.Score[i] = float64(s.Players[i].Stack)
	}
	return o, nil
}

func (g *Game) DefaultMove(st games.State, seat games.Seat) (games.Move, error) {
	s, err := decodeState(st)
	if err != nil {
		return games.Move{}, err
	}
	l, ok := s.legalFor(seat)
	if !ok {
		return games.Move{}, games.Illegal("seat %d has no move to make", seat)
	}
	if l.canCheck {
		return games.Move{Type: "check"}, nil
	}
	return games.Move{Type: "fold"}, nil
}
