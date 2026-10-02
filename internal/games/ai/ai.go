// Package ai provides generic computer players (games.Brain) that work for any
// games.Game, whether written in Go or authored as a script module.
//
// The brains only use the games.Game contract plus a few optional interfaces
// declared here. A game opts in to stronger play by implementing them:
//
//	Heuristic     scores a position for a seat (lets the search cut rollouts short)
//	Determinizer  resamples what a seat cannot see (enables hidden-information search)
//	Reseeder      replaces the game's random stream (so search never "knows" future dice)
//	Rollouter     plays a whole random playout in one call (a large speed-up for scripts)
//
// The interfaces live here, not in the script runtime, so the dependency points
// one way: internal/games/script imports ai, never the reverse.
package ai

import (
	"encoding/json"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// Heuristic is implemented by games that can estimate how good a position is
// for a seat. HasHeuristic exists because a script runtime implements the
// method for every module but only some modules define a heuristic.
//
// Heuristic returns a value in [-1, 1] (1 = winning for seat). It must only
// use information seat is allowed to see.
type Heuristic interface {
	HasHeuristic() bool
	Heuristic(st games.State, seat games.Seat) (float64, error)
}

// Determinizer is implemented by hidden-information games that can produce a
// full state consistent with everything seat knows, with the unknown parts
// (opponents' hands, deck order) resampled using seed. Search over many such
// samples is how the AI plays without peeking.
type Determinizer interface {
	HasDeterminizer() bool
	Determinize(st games.State, seat games.Seat, seed uint64) (games.State, error)
}

// Reseeder is implemented by games that keep their random stream inside the
// state. Search reseeds every simulated root, otherwise simulations would
// replay the real future dice rolls and the AI would play with foresight.
type Reseeder interface {
	Reseed(st games.State, seed uint64) (games.State, error)
}

// Rollouter is implemented by games that can play a uniformly random playout
// internally, which avoids a serialisation round trip per simulated move.
// The playout stops when the game ends or after maxPlies moves; in the latter
// case Heuristic holds one value per seat when the game has a heuristic.
type Rollouter interface {
	Rollout(st games.State, maxPlies int, seed uint64) (RolloutResult, error)
}

// RolloutResult is the end of a random playout: either a finished game
// (Outcome set) or a cut-off position (Heuristic set, or neither when the game
// has no heuristic).
type RolloutResult struct {
	Outcome   *games.Outcome
	Heuristic []float64
	Plies     int
}

// HeuristicOf returns g's heuristic, or nil when it has none.
func HeuristicOf(g games.Game) Heuristic {
	if h, ok := g.(Heuristic); ok && h.HasHeuristic() {
		return h
	}
	return nil
}

// DeterminizerOf returns g's determinizer, or nil when it has none.
func DeterminizerOf(g games.Game) Determinizer {
	if d, ok := g.(Determinizer); ok && d.HasDeterminizer() {
		return d
	}
	return nil
}

// ── moves ───────────────────────────────────────────────────────────────────

// ExpandMoves turns move specs into concrete moves. Fixed specs map to one
// move each; a ranged spec is sampled at up to perRange evenly spaced,
// step-aligned values that always include its minimum and maximum (the
// all-in and the min-raise are the values that matter most).
func ExpandMoves(specs []games.MoveSpec, perRange int) []games.Move {
	if perRange < 2 {
		perRange = 2
	}
	out := make([]games.Move, 0, len(specs))
	for _, sp := range specs {
		if sp.Range == nil {
			out = append(out, games.Move{Type: sp.Type, Args: copyArgs(sp.Args)})
			continue
		}
		for _, v := range rangeSamples(*sp.Range, perRange) {
			a := copyArgs(sp.Args)
			if a == nil {
				a = map[string]any{}
			}
			a[sp.Range.Arg] = v
			out = append(out, games.Move{Type: sp.Type, Args: a})
		}
	}
	return out
}

// RandomMove picks a uniformly random spec and, for a ranged spec, a
// uniformly random step-aligned value inside the range.
func RandomMove(specs []games.MoveSpec, rng *rand.Rand) games.Move {
	sp := specs[rng.Intn(len(specs))]
	m := games.Move{Type: sp.Type, Args: copyArgs(sp.Args)}
	if r := sp.Range; r != nil {
		step := max(r.Step, 1)
		n := (r.Max - r.Min) / step
		if m.Args == nil {
			m.Args = map[string]any{}
		}
		m.Args[r.Arg] = r.Min + step*rng.Intn(max(n, 0)+1)
	}
	return m
}

func rangeSamples(r games.Range, k int) []int {
	step := max(r.Step, 1)
	n := (r.Max - r.Min) / step // number of steps above Min
	if n < 0 {
		return nil
	}
	if n+1 <= k {
		vs := make([]int, 0, n+1)
		for i := 0; i <= n; i++ {
			vs = append(vs, r.Min+i*step)
		}
		return vs
	}
	vs := make([]int, 0, k)
	last := -1
	for i := 0; i < k; i++ {
		idx := int(math.Round(float64(i) * float64(n) / float64(k-1)))
		if idx != last {
			vs = append(vs, r.Min+idx*step)
			last = idx
		}
	}
	return vs
}

func copyArgs(a map[string]any) map[string]any {
	if len(a) == 0 {
		return nil
	}
	c := make(map[string]any, len(a))
	for k, v := range a {
		c[k] = v
	}
	return c
}

// MoveKey is a canonical string for a move, equal for equal moves regardless
// of map order or whether a number arrived as int or float64.
func MoveKey(m games.Move) string {
	if len(m.Args) == 0 {
		return m.Type
	}
	keys := make([]string, 0, len(m.Args))
	for k := range m.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(m.Type)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		writeCanon(&b, m.Args[k])
	}
	return b.String()
}

func writeCanon(b *strings.Builder, v any) {
	switch x := v.(type) {
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case float64:
		b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
	case string:
		b.WriteString(strconv.Quote(x))
	default:
		// Nested values are rare in move args; JSON is canonical enough
		// (encoding/json sorts map keys).
		j, _ := json.Marshal(x)
		b.Write(j)
	}
}

// SameMove reports whether two moves are identical.
func SameMove(a, b games.Move) bool { return MoveKey(a) == MoveKey(b) }

// ── outcomes ────────────────────────────────────────────────────────────────

// OutcomeValues maps an outcome to one reward per seat in [0, 1]: the share
// of opponents a seat finished ahead of, with ties counting half. A 2-player
// win is 1, a draw 0.5, a loss 0. A single-seat game scores 1 for rank 1.
func OutcomeValues(o *games.Outcome) []float64 {
	n := len(o.Rank)
	vs := make([]float64, n)
	if n == 1 {
		if o.Rank[0] == 1 {
			vs[0] = 1
		}
		return vs
	}
	for i := range n {
		var better float64
		for j := range n {
			switch {
			case i == j:
			case o.Rank[i] < o.Rank[j]:
				better++
			case o.Rank[i] == o.Rank[j]:
				better += 0.5
			}
		}
		vs[i] = better / float64(n-1)
	}
	return vs
}

// heuristicValue maps a [-1, 1] heuristic to a [0, 1] reward.
func heuristicValue(h float64) float64 {
	if math.IsNaN(h) {
		return 0.5
	}
	return math.Max(0, math.Min(1, (h+1)/2))
}
