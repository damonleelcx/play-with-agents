package script

import (
	"context"
	"math/rand"
	"testing"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
)

// BenchmarkExampleCalls measures one random game's worth of the calls a
// table makes per move (toMove, legal, apply, view) for each example.
func BenchmarkExampleCalls(b *testing.B) {
	for _, e := range Examples() {
		b.Run(e.ID, func(b *testing.B) {
			g, err := Load(e.ID, e.Source, Options{PoolSize: 1})
			if err != nil {
				b.Fatal(err)
			}
			n := g.Meta().MinSeats
			rng := rand.New(rand.NewSource(1))
			st, _ := g.Setup(games.Config{Seats: n}, 1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tm, err := g.ToMove(st)
				if err != nil {
					b.Fatal(err)
				}
				if len(tm) == 0 {
					st, _ = g.Setup(games.Config{Seats: n}, int64(i))
					continue
				}
				specs, _ := g.Legal(st, tm[0])
				next, _, err := g.Apply(st, tm[0], ai.RandomMove(specs, rng))
				if err != nil {
					b.Fatal(err)
				}
				if _, err := g.View(next, tm[0]); err != nil {
					b.Fatal(err)
				}
				st = next
			}
		})
	}
}

// BenchmarkBrain measures one AI decision (fixed iterations) per example.
func BenchmarkBrain(b *testing.B) {
	brain := ai.New(ai.Options{Iterations: [3]int{200, 200, 200}})
	for _, e := range Examples() {
		b.Run(e.ID, func(b *testing.B) {
			g, err := Load(e.ID, e.Source, Options{PoolSize: 1})
			if err != nil {
				b.Fatal(err)
			}
			st, _ := g.Setup(games.Config{Seats: g.Meta().MinSeats}, 1)
			tm, _ := g.ToMove(st)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := brain.Choose(context.Background(), g, st, tm[0], games.Persona{Skill: 2}, rand.New(rand.NewSource(int64(i)))); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
