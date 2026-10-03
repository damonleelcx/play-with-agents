// Package playtest is the Playtester: it plays hundreds of simulated games
// of any games.Game and reports crashes, contract violations, balance and
// whether skill matters. It uses no LLM and is deterministic given its seed,
// so every error it reports can be replayed exactly.
package playtest

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
	"github.com/damonleelcx/play-with-agents/internal/games/script"
)

// Mix weights the matchups played. Game i's matchup follows a fixed
// round-robin pattern built from the weights, so a run is reproducible.
type Mix struct {
	RandomVsRandom int // every seat random: balance and first-player stats
	AIVsRandom     int // one AI seat, the rest random: does skill matter?
	AIVsAI         int // every seat AI: exercises the AI-facing functions
}

// Matchup is the kind of one simulated game.
type Matchup string

const (
	RandomVsRandom Matchup = "random-vs-random"
	AIVsRandom     Matchup = "ai-vs-random"
	AIVsAI         Matchup = "ai-vs-ai"
)

// Options configures Run. Zero values take the defaults noted.
type Options struct {
	Games       int   // default 200
	Seats       []int // seat counts to test; default min and max (and a middle one when the range is wide)
	Mix         Mix   // default {6, 3, 1}
	MaxMoves    int   // per game; reaching it aborts the game; default 1000
	Seed        int64 // base seed; default 1
	Parallelism int   // default GOMAXPROCS
	// AIIterations is the AI search budget per decision. It is fixed (no
	// deadline) so results do not depend on machine speed. Default 32.
	AIIterations int
	// AI overrides the AI brain (default ai.New with AIIterations).
	AI games.Brain
	// LeakEvery runs the hidden-information leak check every n moves when
	// the game has a determinizer. Default 4.
	LeakEvery int
	// FullCheckEvery checks every seat's view and legal list every n moves
	// (the acting seat is checked every move). Default 5.
	FullCheckEvery int
	// MaxErrors bounds the errors kept in the report (all are counted).
	// Default 50.
	MaxErrors int
}

func (o Options) withDefaults(m games.Meta) Options {
	if o.Games <= 0 {
		o.Games = 200
	}
	if len(o.Seats) == 0 {
		o.Seats = []int{m.MinSeats}
		if m.MaxSeats > m.MinSeats+1 {
			o.Seats = append(o.Seats, (m.MinSeats+m.MaxSeats)/2)
		}
		if m.MaxSeats > m.MinSeats {
			o.Seats = append(o.Seats, m.MaxSeats)
		}
	}
	if o.Mix == (Mix{}) {
		o.Mix = Mix{RandomVsRandom: 6, AIVsRandom: 3, AIVsAI: 1}
	}
	if o.MaxMoves <= 0 {
		o.MaxMoves = 1000
	}
	if o.Seed == 0 {
		o.Seed = 1
	}
	if o.Parallelism <= 0 {
		o.Parallelism = runtime.GOMAXPROCS(0)
	}
	if o.AIIterations <= 0 {
		o.AIIterations = 32
	}
	if o.AI == nil {
		n := o.AIIterations
		o.AI = ai.New(ai.Options{Iterations: [3]int{n, n, n}})
	}
	if o.LeakEvery <= 0 {
		o.LeakEvery = 4
	}
	if o.FullCheckEvery <= 0 {
		o.FullCheckEvery = 5
	}
	if o.MaxErrors <= 0 {
		o.MaxErrors = 50
	}
	return o
}

func (m Mix) pattern() []Matchup {
	var p []Matchup
	for range max(m.RandomVsRandom, 0) {
		p = append(p, RandomVsRandom)
	}
	for range max(m.AIVsRandom, 0) {
		p = append(p, AIVsRandom)
	}
	for range max(m.AIVsAI, 0) {
		p = append(p, AIVsAI)
	}
	if len(p) == 0 {
		p = []Matchup{RandomVsRandom}
	}
	return p
}

// Persona used for AI seats: the strongest setting, so "AI beats random"
// measures the game, not the persona.
var aiPersona = games.Persona{ID: "playtest", Skill: 2}

// Run plays opt.Games games and returns the report. It respects ctx: games
// not started when ctx ends are not counted.
func Run(ctx context.Context, g games.Game, opt Options) Report {
	meta := g.Meta()
	opt = opt.withDefaults(meta)
	start := time.Now()

	pattern := opt.Mix.pattern()
	results := make([]gameResult, opt.Games)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(opt.Parallelism, opt.Games) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				seats := opt.Seats[i%len(opt.Seats)]
				mu := pattern[(i/len(opt.Seats))%len(pattern)]
				res := playOne(ctx, g, opt, i, seats, mu)
				// The budget is wall-clock, so a busy host can push a call
				// over it. Replaying the same seed separates that from a real
				// runaway loop, which times out on every attempt.
				for retry := 0; retry < 2 && onlyTimeouts(res.errs); retry++ {
					again := playOne(ctx, g, opt, i, seats, mu)
					if len(again.errs) == 0 {
						again.flakyTimeout = true
						res = again
					} else if !onlyTimeouts(again.errs) {
						res = again // a different failure is the real one
					}
				}
				results[i] = res
			}
		}()
	}
feed:
	for i := range opt.Games {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	return buildReport(meta, ai.DeterminizerOf(g) != nil, opt, results, time.Since(start))
}

// gameSeed derives a per-game seed so each game is independent of the
// others and of scheduling.
func gameSeed(base int64, i int) int64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d/%d", base, i)
	return int64(h.Sum64() & (1<<63 - 1))
}

type gameResult struct {
	played    bool
	seats     int
	matchup   Matchup
	aiSeat    games.Seat // AIVsRandom only; -1 otherwise
	first     games.Seat // first seat to move
	moves     int
	completed bool
	aborted   bool
	outcome   *games.Outcome
	errs      []Error
	leaks     int
	latency   map[string][]time.Duration
	// flakyTimeout is set when the first attempt timed out and a replay of
	// the same seed did not: load, not the module.
	flakyTimeout bool
}

func onlyTimeouts(errs []Error) bool {
	if len(errs) == 0 {
		return false
	}
	for _, e := range errs {
		if e.Class != ClassTimeout {
			return false
		}
	}
	return true
}

// playOne plays game i. Every random choice comes from rng seeded by the
// game's seed, so the same (Seed, i) replays the same game.
func playOne(ctx context.Context, g games.Game, opt Options, i, seats int, mu Matchup) (res gameResult) {
	if ctx.Err() != nil {
		return res
	}
	seed := gameSeed(opt.Seed, i)
	rng := rand.New(rand.NewSource(seed))
	tg := &timed{g: g, lat: map[string][]time.Duration{}}
	tw := wrap(tg)
	res = gameResult{played: true, seats: seats, matchup: mu, aiSeat: -1, first: -1, latency: tg.lat}

	brains := make([]games.Brain, seats)
	for s := range brains {
		brains[s] = ai.Random{}
	}
	switch mu {
	case AIVsRandom:
		res.aiSeat = rng.Intn(seats)
		brains[res.aiSeat] = opt.AI
	case AIVsAI:
		for s := range brains {
			brains[s] = opt.AI
		}
	}

	var history []SeatMove
	fail := func(class ErrorClass, seat games.Seat, m *games.Move, err error, msg string) gameResult {
		if msg == "" && err != nil {
			msg = err.Error()
		}
		res.errs = append(res.errs, Error{
			Game: i, Seed: seed, Seats: seats, Matchup: mu, MoveIndex: len(history),
			Seat: seat, Move: m, Class: class, Message: msg, History: slices.Clone(history),
		})
		return res
	}

	st, err := tg.Setup(games.Config{Seats: seats}, seed)
	if err != nil {
		return fail(classify(err), -1, nil, err, "")
	}

	for move := 0; ; move++ {
		if ctx.Err() != nil {
			res.played = false // interrupted: neither completed nor aborted
			return res
		}
		tm, err := tg.ToMove(st)
		if err != nil {
			return fail(classify(err), -1, nil, err, "")
		}
		o, err := tg.Outcome(st)
		if err != nil {
			return fail(classify(err), -1, nil, err, "")
		}
		if len(tm) == 0 {
			if o == nil {
				return fail(ClassInvalid, -1, nil, nil, "toMove returned no seats but outcome is nil")
			}
			if len(o.Rank) != seats {
				return fail(ClassInvalid, -1, nil, nil, fmt.Sprintf("outcome has %d ranks for %d seats", len(o.Rank), seats))
			}
			res.completed, res.outcome, res.moves = true, o, move
			return res
		}
		if o != nil {
			return fail(ClassInvalid, -1, nil, nil, fmt.Sprintf("outcome is set but toMove still lists %v", tm))
		}
		if move >= opt.MaxMoves {
			res.aborted, res.moves = true, move
			return res
		}
		if res.first < 0 {
			res.first = tm[0]
		}

		// Contract checks on this position.
		full := move%opt.FullCheckEvery == 0
		for s := range seats {
			if !full && !slices.Contains(tm, s) {
				continue
			}
			specs, err := tg.Legal(st, s)
			if err != nil {
				return fail(classify(err), s, nil, err, "")
			}
			mover := slices.Contains(tm, s)
			if mover && len(specs) == 0 {
				return fail(ClassMismatch, s, nil, nil, fmt.Sprintf("seat %d is in toMove %v but has no legal moves", s, tm))
			}
			if !mover && len(specs) > 0 {
				return fail(ClassMismatch, s, nil, nil, fmt.Sprintf("seat %d is not in toMove %v but legal returned %d moves", s, tm, len(specs)))
			}
		}
		viewers := []games.Seat{tm[0]}
		if full {
			viewers = append(viewers, games.Spectator)
			for s := range seats {
				if s != tm[0] {
					viewers = append(viewers, s)
				}
			}
		}
		for _, s := range viewers {
			if _, err := tg.View(st, s); err != nil {
				return fail(classify(err), s, nil, err, "")
			}
		}
		if d := ai.DeterminizerOf(g); d != nil && move%opt.LeakEvery == 0 {
			for s := range seats {
				det, err := d.Determinize(st, s, rng.Uint64())
				if err != nil {
					return fail(classify(err), s, nil, err, "determinize: "+err.Error())
				}
				res.leaks++
				msg, err := script.LeakDiff(g, st, det, s)
				if err != nil {
					return fail(classify(err), s, nil, err, "")
				}
				if msg != "" {
					return fail(ClassLeak, s, nil, nil, msg)
				}
			}
		}

		// Simultaneous moves: any listed seat may act; pick one at random.
		seat := tm[rng.Intn(len(tm))]
		brainRNG := rand.New(rand.NewSource(rng.Int63()))
		m, err := brains[seat].Choose(ctx, tw, st, seat, aiPersona, brainRNG)
		if err != nil {
			if ctx.Err() != nil {
				res.played = false
				return res
			}
			return fail(classify(err), seat, nil, err, "brain: "+err.Error())
		}
		next, _, err := tg.Apply(st, seat, m)
		if err != nil {
			class := classify(err)
			if errors.Is(err, games.ErrIllegal) {
				class = ClassMismatch
			}
			return fail(class, seat, &m, err, "")
		}
		history = append(history, SeatMove{Seat: seat, Move: m})
		st = next
	}
}

func classify(err error) ErrorClass {
	switch {
	case errors.Is(err, script.ErrTimeout):
		return ClassTimeout
	case errors.Is(err, script.ErrInvalidOutput):
		return ClassInvalid
	case errors.Is(err, games.ErrIllegal):
		return ClassMismatch
	default:
		return ClassModule
	}
}

// ── latency instrumentation ─────────────────────────────────────────────────

// timed wraps a game to record the latency of each call. It forwards the
// optional ai interfaces so the AI plays exactly as it would unwrapped. One
// timed wrapper serves one game on one goroutine, so it needs no lock.
type timed struct {
	g   games.Game
	lat map[string][]time.Duration
}

func (t *timed) rec(name string, start time.Time) {
	t.lat[name] = append(t.lat[name], time.Since(start))
}

func (t *timed) Meta() games.Meta { return t.g.Meta() }

func (t *timed) Setup(cfg games.Config, seed int64) (games.State, error) {
	defer t.rec("setup", time.Now())
	return t.g.Setup(cfg, seed)
}

func (t *timed) ToMove(st games.State) ([]games.Seat, error) {
	defer t.rec("toMove", time.Now())
	return t.g.ToMove(st)
}

func (t *timed) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	defer t.rec("legal", time.Now())
	return t.g.Legal(st, seat)
}

func (t *timed) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	defer t.rec("apply", time.Now())
	return t.g.Apply(st, seat, m)
}

func (t *timed) View(st games.State, seat games.Seat) (games.View, error) {
	defer t.rec("view", time.Now())
	return t.g.View(st, seat)
}

func (t *timed) Outcome(st games.State) (*games.Outcome, error) {
	defer t.rec("outcome", time.Now())
	return t.g.Outcome(st)
}

func (t *timed) DefaultMove(st games.State, seat games.Seat) (games.Move, error) {
	defer t.rec("defaultMove", time.Now())
	return t.g.DefaultMove(st, seat)
}

func (t *timed) HasHeuristic() bool { return ai.HeuristicOf(t.g) != nil }

func (t *timed) Heuristic(st games.State, seat games.Seat) (float64, error) {
	defer t.rec("heuristic", time.Now())
	return ai.HeuristicOf(t.g).Heuristic(st, seat)
}

func (t *timed) HasDeterminizer() bool { return ai.DeterminizerOf(t.g) != nil }

func (t *timed) Determinize(st games.State, seat games.Seat, seed uint64) (games.State, error) {
	defer t.rec("determinize", time.Now())
	return ai.DeterminizerOf(t.g).Determinize(st, seat, seed)
}

// The wrapper must expose Reseeder and Rollouter only when the wrapped game
// has them, otherwise the AI would take a different code path than it does
// at a real table. Embedding the forwarders in anonymous structs gives each
// combination its own method set.
type reseedFwd struct{ r ai.Reseeder }

func (f reseedFwd) Reseed(st games.State, seed uint64) (games.State, error) {
	return f.r.Reseed(st, seed)
}

// rolloutFwd is not timed: one rollout is a whole playout, not a call.
type rolloutFwd struct{ r ai.Rollouter }

func (f rolloutFwd) Rollout(st games.State, maxPlies int, seed uint64) (ai.RolloutResult, error) {
	return f.r.Rollout(st, maxPlies, seed)
}

func wrap(t *timed) games.Game {
	rs, hasRS := t.g.(ai.Reseeder)
	ro, hasRO := t.g.(ai.Rollouter)
	switch {
	case hasRS && hasRO:
		return struct {
			*timed
			reseedFwd
			rolloutFwd
		}{t, reseedFwd{rs}, rolloutFwd{ro}}
	case hasRS:
		return struct {
			*timed
			reseedFwd
		}{t, reseedFwd{rs}}
	case hasRO:
		return struct {
			*timed
			rolloutFwd
		}{t, rolloutFwd{ro}}
	}
	return t
}
