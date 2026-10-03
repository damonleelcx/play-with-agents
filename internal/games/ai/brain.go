package ai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// Random plays a uniformly random legal move. It is the baseline opponent of
// the playtester and the floor every other brain must beat.
type Random struct{}

func (Random) Choose(ctx context.Context, g games.Game, st games.State, seat games.Seat, _ games.Persona, rng *rand.Rand) (games.Move, error) {
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	specs, err := g.Legal(st, seat)
	if err != nil {
		return games.Move{}, err
	}
	if len(specs) == 0 {
		return games.Move{}, fmt.Errorf("ai: seat %d has no legal moves", seat)
	}
	return RandomMove(specs, rng), nil
}

// Options tunes Brain. Zero values take the defaults noted on each field.
type Options struct {
	// Iterations is the search budget per decision, indexed by Persona.Skill
	// (0 casual, 1 regular, 2 shark). The context deadline also stops search,
	// whichever comes first. Default {150, 600, 2400}.
	Iterations [3]int
	// MaxRolloutPlies bounds a random playout when the game has no heuristic.
	// Default 300.
	MaxRolloutPlies int
	// CutoffPlies is the playout length when the game has a heuristic: the
	// position is evaluated instead of played out. Default 6.
	CutoffPlies int
	// Exploration is the UCT constant for rewards in [0, 1]. Default 0.7.
	Exploration float64
	// RangeSamples is how many values of a ranged move are searched. Default 5.
	RangeSamples int
	// Temperature of the final softmax over move values, by skill. A zero
	// temperature plays the most-searched move. Default {0.12, 0.04, 0}.
	Temperature [3]float64
}

func (o Options) withDefaults() Options {
	def := [3]int{150, 600, 2400}
	for i := range o.Iterations {
		if o.Iterations[i] <= 0 {
			o.Iterations[i] = def[i]
		}
	}
	if o.MaxRolloutPlies <= 0 {
		o.MaxRolloutPlies = 300
	}
	if o.CutoffPlies <= 0 {
		o.CutoffPlies = 6
	}
	if o.Exploration <= 0 {
		o.Exploration = 0.7
	}
	if o.RangeSamples <= 0 {
		o.RangeSamples = 5
	}
	if o.Temperature == ([3]float64{}) {
		o.Temperature = [3]float64{0.12, 0.04, 0}
	}
	return o
}

// Brain is a generic games.Brain.
//
//   - Perfect information: open-loop UCT (Monte Carlo tree search) with random
//     playouts, cut short and scored by the game's heuristic when it has one.
//   - Hidden information with a Determinizer: the same search, but every
//     iteration starts from a fresh determinization from the deciding seat's
//     point of view, with availability-counted UCB (single-observer ISMCTS).
//   - Hidden information without one: a uniformly random legal move. Any
//     evaluation (heuristic or playout) would run on the true state and so
//     see the opponents' hidden information. Check rejects such modules.
//
// The tree is "open loop": nodes store move statistics, not states, and every
// iteration re-applies the moves from a (re)sampled root. That one design
// covers chance (dice drawn from a reseeded stream differ per iteration),
// hidden information and simultaneous moves without special node types.
//
// Brain is safe for concurrent use; each Choose call owns its search tree.
type Brain struct{ opt Options }

// New returns a Brain. The zero Options give sensible defaults.
func New(opt Options) *Brain { return &Brain{opt: opt.withDefaults()} }

var _ games.Brain = (*Brain)(nil)
var _ games.Brain = Random{}

// errSearch marks a failure inside one search iteration (a module error in a
// simulated line). The search tolerates a few before giving up.
var errSearch = errors.New("ai: search iteration failed")

func skillIndex(p games.Persona) int { return min(max(p.Skill, 0), 2) }

func (b *Brain) Choose(ctx context.Context, g games.Game, st games.State, seat games.Seat, p games.Persona, rng *rand.Rand) (games.Move, error) {
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	specs, err := g.Legal(st, seat)
	if err != nil {
		return games.Move{}, err
	}
	if len(specs) == 0 {
		return games.Move{}, fmt.Errorf("ai: seat %d has no legal moves", seat)
	}
	cands := ExpandMoves(specs, b.opt.RangeSamples)
	if len(cands) == 1 || ctx.Err() != nil {
		return b.fallback(g, st, seat, cands), nil
	}

	if g.Meta().HiddenInfo && DeterminizerOf(g) == nil {
		// Without a determinizer every evaluation (heuristic or playout)
		// would run on the true state, hidden cards included: play a
		// uniformly random legal move instead of peeking.
		return cands[rng.Intn(len(cands))], nil
	}

	// Search on a context-bound view when the game offers one, so an
	// abandoned decision stops mid-call. The fallback below runs on g:
	// the bound view fails every call once ctx is done.
	sg := g
	if cb, ok := g.(ContextBinder); ok {
		sg = cb.WithContext(ctx)
	}
	mv, ok := b.search(ctx, sg, DeterminizerOf(sg), HeuristicOf(sg), st, seat, p, rng, cands)
	if ok {
		for _, c := range cands {
			if SameMove(c, mv) {
				return mv, nil
			}
		}
	}
	return b.fallback(g, st, seat, cands), nil
}

// fallback is the game's default move when it is legal, else the first
// candidate: Choose must always return a legal move.
func (b *Brain) fallback(g games.Game, st games.State, seat games.Seat, cands []games.Move) games.Move {
	if m, err := g.DefaultMove(st, seat); err == nil {
		for _, c := range cands {
			if SameMove(c, m) {
				return m
			}
		}
	}
	return cands[0]
}

// ── search ──────────────────────────────────────────────────────────────────

type edge struct {
	move   games.Move
	actor  games.Seat
	visits int
	value  float64 // sum of rewards for actor
	avail  int     // iterations in which this move was available (ISMCTS)
	child  *node
}

// node holds the statistics of the moves tried from one position. Edges are
// always visited in the order of the current legal list, never in map
// order, so the search is deterministic.
type node struct {
	edges map[string]*edge
}

func (n *node) edge(k string) *edge {
	if n.edges == nil {
		return nil
	}
	return n.edges[k]
}

func (n *node) add(k string, m games.Move, actor games.Seat) *edge {
	if n.edges == nil {
		n.edges = map[string]*edge{}
	}
	e := &edge{move: m, actor: actor}
	n.edges[k] = e
	return e
}

// values is a lazily evaluated reward vector: heuristic evaluation costs one
// call per seat, and backpropagation usually needs only two or three seats.
type values func(seat games.Seat) float64

func constValues(v float64) values { return func(games.Seat) float64 { return v } }

func sliceValues(vs []float64, scale func(float64) float64) values {
	return func(s games.Seat) float64 {
		if s < 0 || s >= len(vs) {
			return 0.5
		}
		return scale(vs[s])
	}
}

func identity(v float64) float64 { return v }

type searcher struct {
	b    *Brain
	g    games.Game
	det  Determinizer
	h    Heuristic
	rs   Reseeder
	ro   Rollouter
	seat games.Seat
	rng  *rand.Rand
	root *node
	path []*edge
}

func (b *Brain) search(ctx context.Context, g games.Game, det Determinizer, h Heuristic, st games.State, seat games.Seat, p games.Persona, rng *rand.Rand, cands []games.Move) (games.Move, bool) {
	s := &searcher{b: b, g: g, det: det, h: h, seat: seat, rng: rng, root: &node{}}
	s.rs, _ = g.(Reseeder)
	s.ro, _ = g.(Rollouter)

	iters := b.opt.Iterations[skillIndex(p)]
	failures := 0
	for i := 0; i < iters && ctx.Err() == nil; i++ {
		if err := s.iterate(st); err != nil {
			// A broken simulated line should not cost the seat its turn;
			// a module that fails repeatedly gets the fallback move.
			if failures++; failures > 3 {
				break
			}
		}
	}

	stats := make([]choice, 0, len(cands))
	for _, c := range cands {
		e := s.root.edge(MoveKey(c))
		if e == nil || e.visits == 0 {
			continue
		}
		stats = append(stats, choice{move: c, q: e.value / float64(e.visits), visits: e.visits})
	}
	if len(stats) == 0 {
		return games.Move{}, false
	}
	return b.pick(stats, p, rng), true
}

// iterate runs one select–expand–rollout–backpropagate pass.
func (s *searcher) iterate(st games.State) error {
	var err error
	switch {
	case s.det != nil:
		st, err = s.det.Determinize(st, s.seat, s.rng.Uint64())
	case s.rs != nil:
		st, err = s.rs.Reseed(st, s.rng.Uint64())
	}
	if err != nil {
		return err
	}

	s.path = s.path[:0]
	n := s.root
	var vals values
	for {
		tm, err := s.g.ToMove(st)
		if err != nil {
			return err
		}
		if len(tm) == 0 {
			if vals, err = s.terminal(st); err != nil {
				return err
			}
			break
		}
		// Simultaneous moves are serialised. At the root the deciding seat
		// moves first, so the search never conditions on the other seats'
		// choices for this same step.
		actor := tm[0]
		if n == s.root {
			actor = s.seat
		}
		specs, err := s.g.Legal(st, actor)
		if err != nil {
			return err
		}
		if len(specs) == 0 {
			return fmt.Errorf("%w: seat %d is to move but has no legal moves", errSearch, actor)
		}
		moves := ExpandMoves(specs, s.b.opt.RangeSamples)

		var untried []int
		keys := make([]string, len(moves))
		for i, m := range moves {
			keys[i] = MoveKey(m)
			if e := n.edge(keys[i]); e != nil {
				e.avail++
			} else {
				untried = append(untried, i)
			}
		}

		if len(untried) > 0 {
			i := untried[s.rng.Intn(len(untried))]
			e := n.add(keys[i], moves[i], actor)
			e.avail++
			if st, _, err = s.g.Apply(st, actor, moves[i]); err != nil {
				return err
			}
			s.path = append(s.path, e)
			if vals, err = s.rollout(st); err != nil {
				return err
			}
			break
		}

		e := s.selectUCB(n, keys)
		if st, _, err = s.g.Apply(st, actor, e.move); err != nil {
			return err
		}
		s.path = append(s.path, e)
		if e.child == nil {
			e.child = &node{}
		}
		n = e.child
	}

	for _, e := range s.path {
		e.visits++
		e.value += vals(e.actor)
	}
	return nil
}

func (s *searcher) selectUCB(n *node, available []string) *edge {
	var best *edge
	bestScore := math.Inf(-1)
	for _, k := range available {
		e := n.edges[k]
		if e.visits == 0 {
			return e
		}
		score := e.value/float64(e.visits) +
			s.b.opt.Exploration*math.Sqrt(math.Log(float64(max(e.avail, 1)))/float64(e.visits))
		if score > bestScore {
			best, bestScore = e, score
		}
	}
	return best
}

func (s *searcher) terminal(st games.State) (values, error) {
	o, err := s.g.Outcome(st)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, fmt.Errorf("%w: no seat to move but no outcome", errSearch)
	}
	return sliceValues(OutcomeValues(o), identity), nil
}

func (s *searcher) rollout(st games.State) (values, error) {
	limit := s.b.opt.MaxRolloutPlies
	if s.h != nil {
		limit = s.b.opt.CutoffPlies
	}
	if s.ro != nil {
		r, err := s.ro.Rollout(st, limit, s.rng.Uint64())
		if err != nil {
			return nil, err
		}
		switch {
		case r.Outcome != nil:
			return sliceValues(OutcomeValues(r.Outcome), identity), nil
		case r.Heuristic != nil:
			return sliceValues(r.Heuristic, heuristicValue), nil
		default:
			return constValues(0.5), nil
		}
	}

	for ply := 0; ply < limit; ply++ {
		tm, err := s.g.ToMove(st)
		if err != nil {
			return nil, err
		}
		if len(tm) == 0 {
			return s.terminal(st)
		}
		actor := tm[0]
		specs, err := s.g.Legal(st, actor)
		if err != nil {
			return nil, err
		}
		if len(specs) == 0 {
			return nil, fmt.Errorf("%w: seat %d is to move but has no legal moves", errSearch, actor)
		}
		if st, _, err = s.g.Apply(st, actor, RandomMove(specs, s.rng)); err != nil {
			return nil, err
		}
	}
	if tm, err := s.g.ToMove(st); err == nil && len(tm) == 0 {
		return s.terminal(st)
	}
	if s.h == nil {
		return constValues(0.5), nil
	}
	return s.lazyHeuristic(st), nil
}

func (s *searcher) lazyHeuristic(st games.State) values {
	memo := map[games.Seat]float64{}
	return func(seat games.Seat) float64 {
		if v, ok := memo[seat]; ok {
			return v
		}
		v := 0.5
		if h, err := s.h.Heuristic(st, seat); err == nil {
			v = heuristicValue(h)
		}
		memo[seat] = v
		return v
	}
}

// ── persona ─────────────────────────────────────────────────────────────────

type choice struct {
	move   games.Move
	q      float64
	visits int
}

// pick applies the persona: a shark plays the best move (most visits, then
// best value); casual and regular players sample a softmax over move values
// at a high or medium temperature, so they play plausibly but make mistakes.
func (b *Brain) pick(cs []choice, p games.Persona, rng *rand.Rand) games.Move {
	t := b.opt.Temperature[skillIndex(p)]
	if t <= 0 {
		best := cs[0]
		for _, c := range cs[1:] {
			if c.visits > best.visits || (c.visits == best.visits && c.q > best.q) {
				best = c
			}
		}
		return best.move
	}
	qmax := math.Inf(-1)
	for _, c := range cs {
		qmax = math.Max(qmax, c.q)
	}
	w := make([]float64, len(cs))
	var sum float64
	for i, c := range cs {
		w[i] = math.Exp((c.q - qmax) / t)
		sum += w[i]
	}
	r := rng.Float64() * sum
	for i, c := range cs {
		if r -= w[i]; r <= 0 {
			return c.move
		}
	}
	return cs[len(cs)-1].move
}
