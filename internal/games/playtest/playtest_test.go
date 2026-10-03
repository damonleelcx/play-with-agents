package playtest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
	"github.com/damonleelcx/play-with-agents/internal/games/script"
)

func load(t *testing.T, id, src string, opts script.Options) *script.Game {
	t.Helper()
	g, err := script.Load(id, src, opts)
	if err != nil {
		t.Fatalf("Load %s: %v", id, err)
	}
	return g
}

func example(t *testing.T, id string) *script.Game {
	t.Helper()
	e, ok := script.ExampleByID(id)
	if !ok {
		t.Fatalf("no example %s", id)
	}
	return load(t, id, e.Source, script.Options{})
}

// TestExamplesPass is the acceptance gate for the bundled examples: 200
// games each with the default matchup mix, and a clean verdict.
func TestExamplesPass(t *testing.T) {
	for _, e := range script.Examples() {
		t.Run(e.ID, func(t *testing.T) {
			t.Parallel()
			// This test proves the examples are valid games; timeouts have
			// their own sandbox tests. A shared desktop can stall a thread
			// for most of a second (antivirus, other load), so the budget is
			// the 1s live tables use rather than the playtester's 250ms.
			sopts := script.Options{CallBudget: time.Second}
			if raceEnabled {
				// Budgets are wall-clock; the race detector slows the
				// interpreter by an order of magnitude.
				sopts.CallBudget = 5 * time.Second
			}
			g := load(t, e.ID, e.Source, sopts)
			// A smaller search than the default keeps this test quick; the
			// AI still beats random players by a wide margin.
			opt := Options{Games: 200, AIIterations: 16}
			if raceEnabled {
				// Same 200 games and checks; the race detector slows the
				// interpreter ~10x, so the AI searches less.
				opt.AIIterations = 4
				opt.Mix = Mix{RandomVsRandom: 8, AIVsRandom: 1, AIVsAI: 1}
			}
			r := Run(context.Background(), g, opt)
			pass, reasons := r.Verdict()
			t.Logf("\n%s", r.Markdown())
			if !pass || r.Run != 200 || r.Completed != 200 {
				t.Fatalf("verdict %v: %v", pass, reasons)
			}
			// Under -race the 4-iteration AI is too weak to measure skill, so
			// warnings (which include "AI does not beat random") are only
			// enforced in the normal run.
			for _, why := range reasons {
				// A timeout that did not reproduce is the test machine's
				// load (go test runs packages in parallel), not the example.
				if r.FlakyTimeouts > 0 && strings.Contains(why, "not on replay") {
					continue
				}
				if !raceEnabled {
					t.Errorf("unexpected verdict note: %s", why)
				}
			}
			if !raceEnabled && r.AIVsRandom.WinShare < r.AIVsRandom.Baseline+0.2 {
				t.Errorf("AI only won %.0f%% against random", 100*r.AIVsRandom.WinShare)
			}
			if g.HasDeterminizer() && r.LeakChecks == 0 {
				t.Error("no leak checks ran for a hidden-information game")
			}
		})
	}
}

func TestRunIsDeterministic(t *testing.T) {
	g := example(t, "lantern_market")
	opt := Options{Games: 24, Seed: 42, AIIterations: 8, Parallelism: 4}
	norm := func(r Report) string {
		r.Latency, r.LatencyByCall, r.Elapsed = Latency{}, nil, 0
		b, _ := json.Marshal(r)
		return string(b)
	}
	a := norm(Run(context.Background(), g, opt))
	opt.Parallelism = 1
	if b := norm(Run(context.Background(), g, opt)); a != b {
		t.Fatalf("same seed, different reports:\n%s\n%s", a, b)
	}
	opt.Seed = 43
	if c := norm(Run(context.Background(), g, opt)); a == c {
		t.Fatal("different seeds gave identical reports")
	}
}

// buggySrc throws in apply on its fourth move when the bet is high, so only
// some games fail, at varying points.
const buggySrc = `
const game = {
  meta: { name: "Buggy", summary: "Crashes sometimes.", minSeats: 2, maxSeats: 3, turnSeconds: 10 },
  setup(ctx) { return { n: 0, turn: 0, seats: ctx.seats }; },
  toMove(s) { return s.n >= 8 ? [] : [s.turn]; },
  legal(s, seat) {
    if (s.n >= 8 || seat !== s.turn) return [];
    return [{ type: "bet", label: "Bet", range: { arg: "x", min: 1, max: 9 } }, { type: "check", label: "Check" }];
  },
  apply(s, seat, m) {
    if (s.n === 3 && m.type === "bet" && m.args.x > 6) throw new Error("bet overflow at " + m.args.x);
    return { n: s.n + 1, turn: (s.turn + 1) % s.seats, seats: s.seats };
  },
  view(s) { return { message: "{s:" + s.turn + "} to act" }; },
  outcome(s) { return s.n >= 8 ? { rank: Array(s.seats).fill(1), score: Array(s.seats).fill(0) } : null; },
};`

func TestErrorsAreReplayable(t *testing.T) {
	g := load(t, "buggy", buggySrc, script.Options{})
	r := Run(context.Background(), g, Options{Games: 60, Mix: Mix{RandomVsRandom: 1}})
	if pass, _ := r.Verdict(); pass || r.ErrorCount == 0 || r.Errored != r.ErrorCount {
		t.Fatalf("buggy module passed: %+v", r)
	}
	if r.Completed+r.Errored != r.Run || r.Completed == 0 {
		t.Fatalf("counts: run %d completed %d errored %d", r.Run, r.Completed, r.Errored)
	}
	for _, e := range r.Errors {
		if e.Class != ClassModule || e.MoveIndex != 3 || e.Move == nil || !strings.Contains(e.Message, "bet overflow") {
			t.Fatalf("error = %+v", e)
		}
		// Replay: setup, the history, then the failing move.
		st, err := g.Setup(games.Config{Seats: e.Seats}, e.Seed)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range e.History {
			if st, _, err = g.Apply(st, h.Seat, h.Move); err != nil {
				t.Fatalf("replaying history: %v", err)
			}
		}
		_, _, err = g.Apply(st, e.Seat, *e.Move)
		if !errors.Is(err, script.ErrModule) || err.Error() != e.Message {
			t.Fatalf("replay gave %v, report said %s", err, e.Message)
		}
	}
	if md := r.Markdown(); !strings.Contains(md, "FAIL") || !strings.Contains(md, "module error") {
		t.Fatalf("markdown:\n%s", md)
	}
}

const leakySrc = `
const game = {
  meta: { name: "Leaky", summary: "Shows the secret.", minSeats: 2, maxSeats: 2, hiddenInfo: true, turnSeconds: 10 },
  setup(ctx) { return { secret: [ctx.randomInt(100), ctx.randomInt(100)], turn: 0, n: 0 }; },
  toMove(s) { return s.n >= 6 ? [] : [s.turn]; },
  legal(s, seat) { return s.n >= 6 || seat !== s.turn ? [] : [{ type: "a", label: "A" }, { type: "b", label: "B" }]; },
  apply(s, seat) { return { secret: s.secret, turn: 1 - seat, n: s.n + 1 }; },
  view(s, seat) { return { counters: [{ label: "Their secret", value: s.secret[1 - Math.max(seat, 0)] }] }; },
  outcome(s) { return s.n >= 6 ? { rank: [1, 2], score: [1, 0] } : null; },
  determinize(s, seat, ctx) { s.secret[1 - seat] = ctx.randomInt(100); return s; },
};`

func TestHiddenInfoLeakIsCaught(t *testing.T) {
	g := load(t, "leaky", leakySrc, script.Options{})
	r := Run(context.Background(), g, Options{Games: 10})
	if r.ByClass[string(ClassLeak)] != 10 {
		t.Fatalf("leaks = %v", r.ByClass)
	}
	if pass, _ := r.Verdict(); pass {
		t.Fatal("leaky game passed")
	}
}

func TestEndlessGamesAreAborted(t *testing.T) {
	const endless = `
const game = {
  meta: { name: "Endless", summary: "Never ends.", minSeats: 2, maxSeats: 2, turnSeconds: 10 },
  setup(ctx) { return { turn: 0 }; },
  toMove(s) { return [s.turn]; },
  legal(s, seat) { return seat === s.turn ? [{ type: "pass", label: "Pass" }] : []; },
  apply(s, seat) { return { turn: 1 - seat }; },
  view(s) { return {}; },
  outcome(s) { return null; },
};`
	g := load(t, "endless", endless, script.Options{})
	r := Run(context.Background(), g, Options{Games: 8, MaxMoves: 50, Mix: Mix{RandomVsRandom: 1}})
	if r.Aborted != 8 || r.Completed != 0 || r.ErrorCount != 0 {
		t.Fatalf("aborted %d completed %d errors %v", r.Aborted, r.Completed, r.Errors)
	}
	pass, reasons := r.Verdict()
	if pass || !strings.Contains(strings.Join(reasons, "\n"), "completed") {
		t.Fatalf("verdict %v %v", pass, reasons)
	}
}

func TestTimeoutsAreClassified(t *testing.T) {
	src := strings.Replace(buggySrc, "if (s.n >= 8 || seat !== s.turn) return [];", "if (s.n >= 8 || seat !== s.turn) return []; if (s.n === 2) { while (true) {} }", 1)
	g := load(t, "slow", src, script.Options{CallBudget: 10 * time.Millisecond})
	r := Run(context.Background(), g, Options{Games: 4, Mix: Mix{RandomVsRandom: 1}})
	if r.ByClass[string(ClassTimeout)] != 4 {
		t.Fatalf("classes = %v", r.ByClass)
	}
}

// liar is a Go game whose Apply rejects a move its Legal offers.
type liar struct{}

func (liar) Meta() games.Meta { return games.Meta{ID: "liar", Name: "Liar", MinSeats: 2, MaxSeats: 2} }
func (liar) Setup(games.Config, int64) (games.State, error) {
	return games.State(`0`), nil
}
func (liar) ToMove(st games.State) ([]games.Seat, error) {
	if string(st) == "5" {
		return []games.Seat{}, nil
	}
	return []games.Seat{0}, nil
}
func (liar) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	if seat != 0 || string(st) == "5" {
		return nil, nil
	}
	return []games.MoveSpec{{Type: "ok", Label: "OK"}, {Type: "trap", Label: "Trap"}}, nil
}
func (liar) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	if m.Type == "trap" {
		return nil, nil, games.Illegal("trap is not allowed")
	}
	return games.State{st[0] + 1}, nil, nil
}
func (liar) View(games.State, games.Seat) (games.View, error) { return games.View{Kind: "board"}, nil }
func (liar) Outcome(st games.State) (*games.Outcome, error) {
	if string(st) != "5" {
		return nil, nil
	}
	return &games.Outcome{Rank: []int{1, 2}, Score: []float64{1, 0}}, nil
}
func (liar) DefaultMove(games.State, games.Seat) (games.Move, error) {
	return games.Move{Type: "ok"}, nil
}

func TestGoGamesAndMismatches(t *testing.T) {
	// A Go game has none of the optional interfaces; the AI must still play
	// (the timing wrapper must not pretend it can roll out or reseed).
	r := Run(context.Background(), liar{}, Options{Games: 30, AIIterations: 8})
	if r.ByClass[string(ClassMismatch)] == 0 {
		t.Fatalf("no mismatch found: %+v", r.ByClass)
	}
	for _, e := range r.Errors {
		if e.Class != ClassMismatch || e.Move == nil || e.Move.Type != "trap" {
			t.Fatalf("error = %+v", e)
		}
	}
	if _, ok := wrap(&timed{g: liar{}}).(ai.Rollouter); ok {
		t.Fatal("wrapper advertises Rollout for a game without it")
	}
	if _, ok := wrap(&timed{g: example(t, "tictactoe")}).(ai.Rollouter); !ok {
		t.Fatal("wrapper hides Rollout of a script game")
	}
}

func TestContextCancellation(t *testing.T) {
	g := example(t, "reversi")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := Run(ctx, g, Options{Games: 500, Mix: Mix{AIVsAI: 1}})
	if time.Since(start) > 10*time.Second {
		t.Fatalf("Run ignored the context for %v", time.Since(start))
	}
	if r.Run >= 500 || r.ErrorCount != 0 {
		t.Fatalf("run %d errors %d", r.Run, r.ErrorCount)
	}
}

func TestVerdictWarnings(t *testing.T) {
	base := Report{Run: 100, Completed: 100, Options: OptionsIn{MaxMoves: 1000}}

	r := base
	r.BySeats = []SeatStats{{Seats: 2, Games: 50, WinShare: []float64{0.9, 0.1}}}
	r.AIVsRandom = AIStats{Games: 30, WinShare: 0.52, Baseline: 0.5}
	pass, reasons := r.Verdict()
	text := strings.Join(reasons, "\n")
	if !pass || !strings.Contains(text, "seat 0 wins 90%") || !strings.Contains(text, "pure luck") {
		t.Fatalf("%v %v", pass, reasons)
	}

	r = base
	r.BySeats = []SeatStats{{Seats: 2, Games: 50, WinShare: []float64{0.5, 0.5}, Draws: 50}}
	if _, reasons := r.Verdict(); !strings.Contains(strings.Join(reasons, "\n"), "draw") {
		t.Fatalf("no always-draw warning: %v", reasons)
	}

	r = base
	r.Completed, r.Aborted = 90, 10
	if pass, _ := r.Verdict(); pass {
		t.Fatal("90% completion passed")
	}

	r = base
	r.ErrorCount, r.Errored, r.Completed = 1, 1, 99
	r.ByClass = map[string]int{string(ClassInvalid): 1}
	r.Errors = []Error{{Class: ClassInvalid, Message: "view: bad"}}
	if pass, reasons := r.Verdict(); pass || len(reasons) < 2 {
		t.Fatalf("invalid output: %v %v", pass, reasons)
	}

	if pass, _ := (Report{}).Verdict(); pass {
		t.Fatal("an empty report passed")
	}
}

// spectatorLeakSrc: every seat's own view is correct, but the spectator view
// shows every hand. The leak check must cover seat -1 as well.
const spectatorLeakSrc = `
const game = {
  meta: { name: "Peek", summary: "Spectators see everything.", minSeats: 2, maxSeats: 2, hiddenInfo: true, turnSeconds: 10 },
  setup(ctx) { return { hands: [[ctx.randomInt(10)], [ctx.randomInt(10)]], turn: 0, n: 0 }; },
  toMove(s) { return s.n >= 6 ? [] : [s.turn]; },
  legal(s, seat) { return s.n >= 6 || seat !== s.turn ? [] : [{ type: "a", label: "A" }, { type: "b", label: "B" }]; },
  apply(s, seat) { return { hands: s.hands, turn: 1 - seat, n: s.n + 1 }; },
  view(s, seat) {
    return { zones: s.hands.map((h, i) => ({ id: "hand-" + i, owner: i,
      cards: h.map((c) => seat < 0 || seat === i ? { face: String(c) } : { face: "", hidden: true }) })) };
  },
  outcome(s) { return s.n >= 6 ? { rank: [1, 2], score: [1, 0] } : null; },
  determinize(s, seat, ctx) { s.hands[1 - seat] = [ctx.randomInt(10)]; return s; },
};`

func TestSpectatorLeakIsCaught(t *testing.T) {
	g := load(t, "peek", spectatorLeakSrc, script.Options{})
	r := Run(context.Background(), g, Options{Games: 10})
	if r.ByClass[string(ClassLeak)] != 10 {
		t.Fatalf("leaks = %v", r.ByClass)
	}
	if !strings.Contains(r.Errors[0].Message, "spectator view") {
		t.Fatalf("message = %s", r.Errors[0].Message)
	}
	if pass, _ := r.Verdict(); pass {
		t.Fatal("leaky game passed")
	}
}

func TestHiddenInfoWithoutDeterminizerFails(t *testing.T) {
	src := strings.Replace(spectatorLeakSrc, "determinize(s, seat, ctx)", "notDeterminize(s, seat, ctx)", 1)
	g := load(t, "nodet", src, script.Options{})
	r := Run(context.Background(), g, Options{Games: 10})
	pass, reasons := r.Verdict()
	if pass || len(reasons) == 0 || !strings.Contains(reasons[0], "must define determinize") {
		t.Fatalf("verdict %v %v", pass, reasons)
	}
}
