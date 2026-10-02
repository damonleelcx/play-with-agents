package holdem

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// tableWith deals hand 1 at a table whose seats start with the given stacks.
// Seat 0 holds the button, so with three or more seats seat 1 posts the
// small blind and seat 2 the big blind.
func tableWith(t testing.TB, stacks []int, sb, bb int) *state {
	t.Helper()
	s := &state{
		V: stateVersion, Seed: 42,
		Opts:       options{StartingStack: stacks[0], SmallBlind: sb, BigBlind: bb},
		SmallBlind: sb, BigBlind: bb, MinRaise: bb,
		Players: make([]player, len(stacks)),
	}
	for i, st := range stacks {
		s.Players[i] = player{Stack: st, Status: statusActive, StartStack: st}
	}
	s.startHand()
	s.ev = nil
	return s
}

func encodeT(t testing.TB, s *state) games.State {
	t.Helper()
	b, err := s.encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeT(t testing.TB, st games.State) *state {
	t.Helper()
	s, err := decodeState(st)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// play applies a move through the public Game API and returns the new state
// and its events, failing the test on any error.
func play(t testing.TB, s *state, seat int, typ string, to ...int) (*state, []games.Event) {
	t.Helper()
	m := games.Move{Type: typ}
	if len(to) > 0 {
		m.Args = map[string]any{"to": to[0]}
	}
	out, ev, err := New().Apply(encodeT(t, s), seat, m)
	if err != nil {
		t.Fatalf("seat %d %s %v: %v", seat, typ, to, err)
	}
	return decodeT(t, out), ev
}

// mustReject asserts that the move is refused as illegal.
func mustReject(t testing.TB, s *state, seat int, m games.Move) {
	t.Helper()
	out, ev, err := New().Apply(encodeT(t, s), seat, m)
	if err == nil {
		t.Fatalf("seat %d %+v was accepted", seat, m)
	}
	if !errors.Is(err, games.ErrIllegal) {
		t.Fatalf("seat %d %+v: error %v is not ErrIllegal", seat, m, err)
	}
	if out != nil || ev != nil {
		t.Fatalf("rejected move returned a state or events")
	}
}

func legalTypes(t testing.TB, s *state, seat int) map[string]games.MoveSpec {
	t.Helper()
	specs, err := New().Legal(encodeT(t, s), seat)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]games.MoveSpec{}
	for _, sp := range specs {
		out[sp.Type] = sp
	}
	return out
}

func viewOf(t testing.TB, st games.State, seat int) ViewData {
	t.Helper()
	v, err := New().View(st, seat)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != "holdem" {
		t.Fatalf("view kind %q", v.Kind)
	}
	return v.Data.(ViewData)
}

func eventsOfType(ev []games.Event, typ string) []games.Event {
	var out []games.Event
	for _, e := range ev {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func chips(s *state) int {
	t := 0
	for _, p := range s.Players {
		t += p.Stack + p.TotalBet
	}
	return t
}

func jsonRound(t testing.TB, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
