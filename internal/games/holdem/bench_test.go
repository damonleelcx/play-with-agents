package holdem

import (
	"math/rand"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

type applyCase struct {
	st   games.State
	seat int
	m    games.Move
}

// applyCases records real positions from random 6-handed play, including
// the moves that finish a hand and deal the next one.
func applyCases(tb testing.TB, n int) []applyCase {
	tb.Helper()
	g := New()
	rng := rand.New(rand.NewSource(4))
	var out []applyCase
	var st games.State
	for len(out) < n {
		if st == nil {
			st, _ = g.Setup(games.Config{Seats: 6}, rng.Int63())
		}
		toMove, _ := g.ToMove(st)
		if len(toMove) == 0 {
			st = nil
			continue
		}
		specs, _ := g.Legal(st, toMove[0])
		m := randomMove(rng, specs)
		out = append(out, applyCase{st, toMove[0], m})
		next, _, err := g.Apply(st, toMove[0], m)
		if err != nil {
			tb.Fatal(err)
		}
		st = next
	}
	return out
}

func BenchmarkApply(b *testing.B) {
	cases := applyCases(b, 1024)
	g := New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := cases[i&1023]
		if _, _, err := g.Apply(c.st, c.seat, c.m); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBrainShark(b *testing.B) {
	s := tableWith(b, []int{1000, 1000, 1000, 1000}, 10, 20)
	s, _ = play(b, s, 3, "call")
	s, _ = play(b, s, 0, "call")
	s, _ = play(b, s, 1, "call")
	s, _ = play(b, s, 2, "check") // a four-way flop
	st := encodeT(b, s)
	g := New()
	p := PersonaFor("ren", 2)
	rng := rand.New(rand.NewSource(1))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := (Brain{}).Choose(b.Context(), g, st, s.ToAct, p, rng); err != nil {
			b.Fatal(err)
		}
	}
}

// TestApplyIsFast guards the latency budget: a table move, including the
// JSON round trip and dealing the next hand, must take well under 1ms.
func TestApplyIsFast(t *testing.T) {
	if raceEnabled {
		t.Skip("timing is meaningless under the race detector")
	}
	cases := applyCases(t, 2000)
	g := New()
	start := time.Now()
	for _, c := range cases {
		if _, _, err := g.Apply(c.st, c.seat, c.m); err != nil {
			t.Fatal(err)
		}
	}
	avg := time.Since(start) / time.Duration(len(cases))
	t.Logf("average Apply: %v", avg)
	if avg > time.Millisecond {
		t.Fatalf("average Apply took %v, budget 1ms", avg)
	}
}
