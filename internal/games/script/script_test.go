package script

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
)

// baseSrc is a tiny valid game. Tests override single functions with
//
//	module(`legal(s, seat) { ... },`)
//
// which keeps each test focused on the one behaviour it breaks.
const baseSrc = `
const base = {
  meta: { name: "Counter", summary: "Count to five.", minSeats: 2, maxSeats: 2, hiddenInfo: false, turnSeconds: 10 },
  setup(ctx) { return { n: 0, turn: 0 }; },
  toMove(s) { return s.n >= 5 ? [] : [s.turn]; },
  legal(s, seat) {
    if (s.n >= 5 || seat !== s.turn) return [];
    return [
      { type: "inc", label: "+1" },
      { type: "bet", label: "Bet", range: { arg: "x", min: 1, max: 10, step: 3 } },
      { type: "pick", label: "Pick A", args: { which: "a" } },
    ];
  },
  apply(s, seat, m) {
    return { state: { n: s.n + 1, turn: 1 - s.turn, last: m },
             events: [{ type: m.type, seat, text: "{s:" + seat + "} plays " + m.type }] };
  },
  view(s, seat) { return { title: "Counter", counters: [{ label: "n", value: s.n }], message: "{s:" + s.turn + "} to move" }; },
  outcome(s) { return s.n >= 5 ? { rank: [1, 2], score: [1, 0], summary: "{s:0} wins" } : null; },
};
`

func module(overrides string) string {
	return baseSrc + "\nconst game = Object.assign({}, base, {" + overrides + "});\n"
}

func mustLoad(t *testing.T, src string, opts Options) *Game {
	t.Helper()
	g, err := Load("test", src, opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return g
}

func mustSetup(t *testing.T, g *Game, seats int, seed int64) games.State {
	t.Helper()
	st, err := g.Setup(games.Config{Seats: seats}, seed)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return st
}

func moduleState(t *testing.T, st games.State) map[string]any {
	t.Helper()
	var env struct {
		S map[string]any `json:"s"`
	}
	if err := json.Unmarshal(st, &env); err != nil {
		t.Fatal(err)
	}
	return env.S
}

// ── sandbox ─────────────────────────────────────────────────────────────────

func TestSandboxRemovesAmbientAuthority(t *testing.T) {
	g := mustLoad(t, module(`setup(ctx) { return {
	    random: typeof Math.random, date: typeof Date, require: typeof require,
	    timeout: typeof setTimeout, process: typeof process, fetch: typeof fetch,
	    console: typeof console.log, frozen: Object.isFrozen(game) && Object.isFrozen(game.meta),
	    ctx: typeof ctx.random + typeof ctx.randomInt + typeof ctx.shuffle, n: 0, turn: 0 }; },`), Options{})
	s := moduleState(t, mustSetup(t, g, 2, 1))
	for _, k := range []string{"random", "date", "require", "timeout", "process", "fetch"} {
		if s[k] != "undefined" {
			t.Errorf("typeof %s = %v, want undefined", k, s[k])
		}
	}
	if s["console"] != "function" || s["ctx"] != "functionfunctionfunction" {
		t.Errorf("console/ctx helpers missing: %v %v", s["console"], s["ctx"])
	}
	if s["frozen"] != true {
		t.Error("game object is not frozen")
	}
}

func TestMathRandomGivesHint(t *testing.T) {
	g := mustLoad(t, module(`setup(ctx) { return { n: Math.random(), turn: 0 }; },`), Options{})
	_, err := g.Setup(games.Config{Seats: 2}, 1)
	if !errors.Is(err, ErrModule) || !strings.Contains(err.Error(), "use ctx.random()") {
		t.Fatalf("err = %v, want ErrModule with a ctx.random hint", err)
	}
}

func TestInfiniteLoopIsInterrupted(t *testing.T) {
	const budget = 50 * time.Millisecond
	g := mustLoad(t, module(`legal(s, seat) { if (seat === 1) { while (true) {} } return base.legal(s, seat); },`),
		Options{CallBudget: budget, PoolSize: 1})
	st := mustSetup(t, g, 2, 1)

	start := time.Now()
	_, err := g.Legal(st, 1)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, ErrModule) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed > budget+500*time.Millisecond {
		t.Errorf("interrupt took %v for a %v budget", elapsed, budget)
	}
	// The interrupted runtime is discarded; the game keeps working.
	if specs, err := g.Legal(st, 0); err != nil || len(specs) != 3 {
		t.Fatalf("after timeout: %v %v", specs, err)
	}
}

func TestTopLevelLoopFailsLoad(t *testing.T) {
	start := time.Now()
	_, err := Load("loop", "while (true) {}\n"+module(""), Options{CallBudget: 20 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "time budget") {
		t.Fatalf("err = %v, want a budget error", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Load took %v", time.Since(start))
	}
}

func TestUnboundedRecursionIsModuleError(t *testing.T) {
	g := mustLoad(t, `function f(n) { return f(n + 1) + 1; }`+module(`legal(s, seat) { return f(0); },`), Options{})
	_, err := g.Legal(mustSetup(t, g, 2, 1), 0)
	if !errors.Is(err, ErrModule) || !strings.Contains(err.Error(), "call stack") {
		t.Fatalf("err = %v, want a stack overflow ErrModule", err)
	}
}

func TestHugeAllocationIsCapped(t *testing.T) {
	g := mustLoad(t, module(`legal(s, seat) { const a = new Array(1e9).fill(0); return []; },`), Options{})
	_, err := g.Legal(mustSetup(t, g, 2, 1), 0)
	if !errors.Is(err, ErrModule) || !strings.Contains(err.Error(), "RangeError") {
		t.Fatalf("err = %v, want RangeError", err)
	}
}

func TestOversizedStateIsRejected(t *testing.T) {
	g := mustLoad(t, module(`apply(s, seat, m) { return { n: s.n + 1, turn: 1 - seat, pad: "x".repeat(300000) }; },`), Options{})
	_, _, err := g.Apply(mustSetup(t, g, 2, 1), 0, games.Move{Type: "inc"})
	if !errors.Is(err, ErrInvalidOutput) || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("err = %v, want the state size limit", err)
	}
	// A smaller limit is configurable.
	g = mustLoad(t, module(""), Options{MaxStateBytes: 10})
	if _, err := g.Setup(games.Config{Seats: 2}, 1); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("err = %v with a 10-byte limit", err)
	}
}

func TestThrowInApplyIsModuleError(t *testing.T) {
	g := mustLoad(t, module(`apply(s, seat, m) { if (m.type === "pick") throw new Error("boom"); return base.apply(s, seat, m); },`), Options{})
	st := mustSetup(t, g, 2, 1)
	_, _, err := g.Apply(st, 0, games.Move{Type: "pick", Args: map[string]any{"which": "a"}})
	if !errors.Is(err, ErrModule) || errors.Is(err, games.ErrIllegal) {
		t.Fatalf("err = %v, want ErrModule and not ErrIllegal", err)
	}
	var me *ModuleError
	if !errors.As(err, &me) || me.Func != "apply" || !strings.Contains(me.Message, "boom") || !strings.Contains(me.Message, "test.js:") {
		t.Fatalf("ModuleError = %+v, want func apply, the message and a position", me)
	}
}

func TestIllegalMoves(t *testing.T) {
	g := mustLoad(t, module(""), Options{})
	st := mustSetup(t, g, 2, 1)
	cases := []struct {
		name string
		seat games.Seat
		m    games.Move
	}{
		{"unknown type", 0, games.Move{Type: "fly"}},
		{"out of turn", 1, games.Move{Type: "inc"}},
		{"not a seat", 7, games.Move{Type: "inc"}},
		{"extra arg", 0, games.Move{Type: "inc", Args: map[string]any{"x": 1}}},
		{"wrong fixed arg", 0, games.Move{Type: "pick", Args: map[string]any{"which": "b"}}},
		{"range off step", 0, games.Move{Type: "bet", Args: map[string]any{"x": 2}}},
		{"range too big", 0, games.Move{Type: "bet", Args: map[string]any{"x": 13}}},
		{"range not integer", 0, games.Move{Type: "bet", Args: map[string]any{"x": 4.5}}},
		{"range missing", 0, games.Move{Type: "bet"}},
	}
	for _, c := range cases {
		if _, _, err := g.Apply(st, c.seat, c.m); !errors.Is(err, games.ErrIllegal) {
			t.Errorf("%s: err = %v, want ErrIllegal", c.name, err)
		}
	}
	// Valid range values arrive as Go ints or JSON float64s alike, and the
	// module receives an integer.
	for _, x := range []any{4, 7.0, int64(10)} {
		next, _, err := g.Apply(st, 0, games.Move{Type: "bet", Args: map[string]any{"x": x}})
		if err != nil {
			t.Fatalf("bet %v: %v", x, err)
		}
		last := moduleState(t, next)["last"].(map[string]any)
		if args := last["args"].(map[string]any); fmt.Sprint(args["x"]) != fmt.Sprint(x) {
			t.Errorf("module saw %v for %v", args["x"], x)
		}
	}
}

func TestMalformedOutputsAreReported(t *testing.T) {
	cases := []struct {
		name, override, want string
		call                 func(g *Game, st games.State) error
	}{
		{"board dims", `view(s) { return { board: { rows: 2, cols: 2, cells: [[null, null], [null]] } }; },`, "cells[1] has 1 cells, but board.cols is 2", callView},
		{"unknown view key", `view(s) { return { title: "x", colour: "red" }; },`, "unknown key(s) colour", callView},
		{"bad piece colour", `view(s) { return { board: { rows: 1, cols: 1, cells: [[{ piece: { shape: "disc", color: 3 } }]] } }; },`, "piece.color must be a string", callView},
		{"bad shape", `view(s) { return { board: { rows: 1, cols: 1, cells: [[{ piece: { shape: "star" } }]] } }; },`, "shape must be one of", callView},
		{"hidden face", `view(s) { return { zones: [{ id: "h", cards: [{ face: "7♥", hidden: true }] }] }; },`, "leaks hidden information", callView},
		{"zone owner", `view(s) { return { zones: [{ id: "h", owner: 5, cards: [] }] }; },`, "owner must be a seat number", callView},
		{"bad placeholder", `view(s) { return { message: "{s:7} wins" }; },`, "{s:7}", callView},
		{"view undefined", `view(s) { },`, "returned undefined", callView},
		{"legal not array", `legal(s, seat) { return { type: "inc" }; },`, "must be an array", callLegal},
		{"legal no label", `legal(s, seat) { return [{ type: "inc" }]; },`, "label must be a non-empty string", callLegal},
		{"legal bad range", `legal(s, seat) { return [{ type: "b", label: "B", range: { arg: "x", min: 5, max: 1 } }]; },`, "min <= max", callLegal},
		{"legal duplicate", `legal(s, seat) { return [{ type: "a", label: "A" }, { type: "a", label: "A again" }]; },`, "duplicates", callLegal},
		{"toMove seat range", `toMove(s) { return [2]; },`, "seat number 0..1", callToMove},
		{"outcome rank length", `outcome(s) { return { rank: [1], score: [1, 0] }; },`, "one entry per seat", callOutcome},
		{"outcome no winner", `outcome(s) { return { rank: [2, 2], score: [0, 0] }; },`, "no seat ranked 1", callOutcome},
		{"event seat", `apply(s, seat, m) { return { state: s, events: [{ type: "x", seat: 9 }] }; },`, "events[0].seat", callApply},
		{"apply undefined", `apply(s, seat, m) { s.n++; },`, "returned undefined", callApply},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := mustLoad(t, module(c.override), Options{})
			err := c.call(g, mustSetup(t, g, 2, 1))
			if !errors.Is(err, ErrInvalidOutput) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want ErrInvalidOutput containing %q", err, c.want)
			}
		})
	}
}

func callView(g *Game, st games.State) error    { _, err := g.View(st, 0); return err }
func callLegal(g *Game, st games.State) error   { _, err := g.Legal(st, 0); return err }
func callToMove(g *Game, st games.State) error  { _, err := g.ToMove(st); return err }
func callOutcome(g *Game, st games.State) error { _, err := g.Outcome(st); return err }
func callApply(g *Game, st games.State) error {
	_, _, err := g.Apply(st, 0, games.Move{Type: "inc"})
	return err
}

func TestViewKindAndStatus(t *testing.T) {
	g := mustLoad(t, module(""), Options{})
	v, err := g.View(mustSetup(t, g, 2, 1), games.Spectator)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != "board" || v.Status != "{s:0} to move" {
		t.Fatalf("view = %+v", v)
	}
	if _, err := g.View(mustSetup(t, g, 2, 1), 2); err == nil {
		t.Fatal("seat 2 of 2 accepted")
	}
}

func TestInputsCannotBeMutated(t *testing.T) {
	// Every function gets a fresh copy: mutations inside legal/view must not
	// leak into the stored state or into later calls.
	g := mustLoad(t, module(`
	  legal(s, seat) { s.n = 99; s.turn = 1; return base.legal({ n: 0, turn: seat }, seat); },
	  view(s, seat) { s.n += 10; return base.view(s, seat); },`), Options{PoolSize: 1})
	st := mustSetup(t, g, 2, 1)
	before := bytes.Clone(st)
	for range 3 {
		if _, err := g.Legal(st, 0); err != nil {
			t.Fatal(err)
		}
		v, err := g.View(st, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(v.Data.(json.RawMessage)), `"value":10`) {
			t.Fatalf("view saw a mutated state: %s", v.Data)
		}
	}
	if !bytes.Equal(st, before) {
		t.Fatal("state bytes changed")
	}
	if tm, _ := g.ToMove(st); len(tm) != 1 || tm[0] != 0 {
		t.Fatalf("toMove = %v after mutations", tm)
	}
	// Moves passed to Apply are copies too.
	m := games.Move{Type: "pick", Args: map[string]any{"which": "a"}}
	g2 := mustLoad(t, module(`apply(s, seat, m) { m.args.which = "zzz"; return base.apply(s, seat, m); },`), Options{})
	if _, _, err := g2.Apply(mustSetup(t, g2, 2, 1), 0, m); err != nil || m.Args["which"] != "a" {
		t.Fatalf("caller's move changed: %v %v", m, err)
	}
}

func TestLegalResultsAreNotShared(t *testing.T) {
	g := mustLoad(t, module(""), Options{})
	st := mustSetup(t, g, 2, 1)
	a, _ := g.Legal(st, 0)
	a[2].Args["which"] = "mutated"
	a[1].Range.Max = 99
	b, _ := g.Legal(st, 0)
	if b[2].Args["which"] != "a" || b[1].Range.Max != 10 {
		t.Fatalf("memoised legal moves were mutated through a caller: %+v", b)
	}
}

func TestConsoleIsCaptured(t *testing.T) {
	g := mustLoad(t, module(`setup(ctx) { console.log("hello", { seats: ctx.seats }); console.warn("careful"); return base.setup(ctx); },`),
		Options{ConsoleLines: 2})
	mustSetup(t, g, 2, 1)
	mustSetup(t, g, 2, 1)
	logs := g.Logs()
	if len(logs) != 2 || logs[0] != `hello {"seats":2}` || logs[1] != "warn: careful" {
		t.Fatalf("logs = %q", logs)
	}
}

func TestMetaAndLoadErrors(t *testing.T) {
	g := mustLoad(t, module(""), Options{})
	if m := g.Meta(); m.ID != "test" || m.Name != "Counter" || m.MinSeats != 2 || m.TurnSeconds != 10 {
		t.Fatalf("meta = %+v", m)
	}
	if _, err := g.Setup(games.Config{Seats: 3}, 1); err == nil {
		t.Fatal("3 seats accepted for a 2-seat game")
	}
	bad := map[string]string{
		"syntax":       "const game = {",
		"no game":      "const x = 1;",
		"not object":   "const game = 3;",
		"no apply":     baseSrc + "const game = Object.assign({}, base, { apply: undefined });",
		"seats":        baseSrc + `const game = Object.assign({}, base, { meta: { name: "x", minSeats: 0, maxSeats: 20 } });`,
		"meta typo":    baseSrc + `const game = Object.assign({}, base, { meta: { name: "x", minSeats: 1, maxSeats: 2, hidenInfo: true } });`,
		"throws early": "throw new Error('nope');",
	}
	for name, src := range bad {
		if _, err := Load("bad", src, Options{}); err == nil {
			t.Errorf("%s: loaded", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
}

// ── randomness and determinism ──────────────────────────────────────────────

const diceSrc = `
const game = {
  meta: { name: "Dice", summary: "Roll.", minSeats: 1, maxSeats: 1, turnSeconds: 10, options: { sides: 6 } },
  setup(ctx) { return { rolls: [ctx.randomInt(ctx.options.sides)], sides: ctx.options.sides }; },
  toMove(s) { return s.rolls.length >= 20 ? [] : [0]; },
  legal(s, seat) { return s.rolls.length >= 20 ? [] : [{ type: "roll", label: "Roll" }]; },
  apply(s, seat, m, ctx) { s.rolls.push(1 + ctx.randomInt(ctx.options.sides)); return s; },
  view(s) { return { message: s.rolls.join(",") }; },
  outcome(s) { return s.rolls.length >= 20 ? { rank: [1], score: [s.rolls.reduce((a, b) => a + b, 0)] } : null; },
};`

func TestRandomStreamIsDeterministic(t *testing.T) {
	g := mustLoad(t, diceSrc, Options{})
	play := func(seed int64, opts map[string]any) games.State {
		st, err := g.Setup(games.Config{Seats: 1, Options: opts}, seed)
		if err != nil {
			t.Fatal(err)
		}
		for range 10 {
			if st, _, err = g.Apply(st, 0, games.Move{Type: "roll"}); err != nil {
				t.Fatal(err)
			}
		}
		return st
	}
	a, b := play(7, nil), play(7, nil)
	if !bytes.Equal(a, b) {
		t.Fatal("same seed, different states")
	}
	if bytes.Equal(a, play(8, nil)) {
		t.Fatal("different seeds, same states")
	}
	if s := moduleState(t, play(7, map[string]any{"sides": 100})); s["sides"] != 100.0 {
		t.Fatalf("table options not applied: %v", s["sides"])
	}

	// Reseed changes future rolls but not the module state itself.
	r, err := g.Reseed(a, 12345)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustJSON(moduleState(t, r)), mustJSON(moduleState(t, a))) {
		t.Fatal("Reseed changed the module state")
	}
	next1, _, _ := g.Apply(a, 0, games.Move{Type: "roll"})
	diff := false
	for seed := uint64(1); seed < 10 && !diff; seed++ {
		r, _ := g.Reseed(a, seed)
		next2, _, _ := g.Apply(r, 0, games.Move{Type: "roll"})
		diff = !bytes.Equal(mustJSON(moduleState(t, next1)), mustJSON(moduleState(t, next2)))
	}
	if !diff {
		t.Fatal("reseeding never changed the next roll")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// playRandom plays a random game and returns every state and move.
func playRandom(t *testing.T, g games.Game, seats int, seed int64) ([]games.State, []step) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	st, err := g.Setup(games.Config{Seats: seats}, seed)
	if err != nil {
		t.Fatal(err)
	}
	states := []games.State{st}
	var steps []step
	for range 500 {
		tm, err := g.ToMove(st)
		if err != nil {
			t.Fatal(err)
		}
		if len(tm) == 0 {
			return states, steps
		}
		seat := tm[rng.Intn(len(tm))]
		specs, err := g.Legal(st, seat)
		if err != nil {
			t.Fatal(err)
		}
		m := ai.RandomMove(specs, rng)
		if st, _, err = g.Apply(st, seat, m); err != nil {
			t.Fatalf("apply %v: %v", m, err)
		}
		states = append(states, st)
		steps = append(steps, step{seat, m})
	}
	t.Fatal("game did not end in 500 moves")
	return nil, nil
}

func TestReplayIsDeterministic(t *testing.T) {
	for _, e := range Examples() {
		t.Run(e.ID, func(t *testing.T) {
			g1 := mustLoad(t, e.Source, Options{})
			g2 := mustLoad(t, e.Source, Options{PoolSize: 1})
			m := g1.Meta()
			for seed := int64(1); seed <= 3; seed++ {
				states, steps := playRandom(t, g1, m.MaxSeats, seed)
				st := mustSetup(t, g2, m.MaxSeats, seed)
				for i, s := range steps {
					if !bytes.Equal(st, states[i]) {
						t.Fatalf("seed %d: state %d differs on replay", seed, i)
					}
					var err error
					if st, _, err = g2.Apply(st, s.seat, s.move); err != nil {
						t.Fatal(err)
					}
				}
				if !bytes.Equal(st, states[len(states)-1]) {
					t.Fatalf("seed %d: final state differs on replay", seed)
				}
			}
		})
	}
}

func TestConcurrentUseOfOneGame(t *testing.T) {
	for _, id := range []string{"connect_four", "lantern_market"} {
		t.Run(id, func(t *testing.T) {
			e, _ := ExampleByID(id)
			g := mustLoad(t, e.Source, Options{PoolSize: 4})
			seats := g.Meta().MaxSeats
			const n = 24
			want := make([]games.State, n)
			for i := range n {
				states, _ := playRandom(t, g, seats, int64(i))
				want[i] = states[len(states)-1]
			}
			var wg sync.WaitGroup
			got := make([]games.State, n)
			for i := range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					states, _ := playRandom(t, g, seats, int64(i))
					got[i] = states[len(states)-1]
					// Hammer the read-only functions too.
					last := states[len(states)/2]
					for s := games.Spectator; s < seats; s++ {
						if _, err := g.View(last, s); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			wg.Wait()
			for i := range n {
				if !bytes.Equal(got[i], want[i]) {
					t.Fatalf("game %d: concurrent result differs from sequential", i)
				}
			}
		})
	}
}

// ── optional interfaces ─────────────────────────────────────────────────────

func TestOptionalInterfaces(t *testing.T) {
	load := func(id string) *Game {
		e, ok := ExampleByID(id)
		if !ok {
			t.Fatalf("no example %s", id)
		}
		return mustLoad(t, e.Source, Options{})
	}
	rev, lm, c4 := load("reversi"), load("lantern_market"), load("connect_four")
	if ai.HeuristicOf(rev) == nil || ai.HeuristicOf(c4) != nil || ai.DeterminizerOf(lm) == nil || ai.DeterminizerOf(rev) != nil {
		t.Fatal("capabilities misreported")
	}

	st := mustSetup(t, rev, 2, 1)
	h0, err := rev.Heuristic(st, 0)
	if err != nil || h0 < -1 || h0 > 1 {
		t.Fatalf("heuristic = %v, %v", h0, err)
	}
	r, err := rev.Rollout(st, 0, 1)
	if err != nil || r.Outcome != nil || len(r.Heuristic) != 2 {
		t.Fatalf("cut-off rollout = %+v, %v", r, err)
	}
	r, err = c4.Rollout(mustSetup(t, c4, 2, 1), 1000, 7)
	if err != nil || r.Outcome == nil || r.Plies < 7 {
		t.Fatalf("full rollout = %+v, %v", r, err)
	}

	// Determinize keeps what the seat knows and reshuffles the rest.
	for seats := 2; seats <= 4; seats++ {
		states, _ := playRandom(t, lm, seats, int64(seats))
		for _, st := range states[:len(states)-1] {
			for seat := range seats {
				det, err := lm.Determinize(st, seat, uint64(seat+1))
				if err != nil {
					t.Fatal(err)
				}
				if msg, err := SameInformation(lm, st, det, seat); msg != "" || err != nil {
					t.Fatal(msg, err)
				}
			}
		}
	}
}

func TestDefaultMove(t *testing.T) {
	e, _ := ExampleByID("connect_four")
	c4 := mustLoad(t, e.Source, Options{})
	m, err := c4.DefaultMove(mustSetup(t, c4, 2, 1), 0)
	if err != nil || m.Type != "drop" || fmt.Sprint(m.Args["col"]) != "3" {
		t.Fatalf("default = %v, %v", m, err)
	}
	if _, err := c4.DefaultMove(mustSetup(t, c4, 2, 1), 1); !errors.Is(err, games.ErrIllegal) {
		t.Fatalf("default for the waiting seat: %v", err)
	}
	// Without defaultMove: the first legal move, at the bottom of a range.
	g := mustLoad(t, module(`legal(s, seat) { return seat === s.turn ? [{ type: "bet", label: "Bet", range: { arg: "x", min: 2, max: 9 } }] : []; },`), Options{})
	if m, err := g.DefaultMove(mustSetup(t, g, 2, 1), 0); err != nil || m.Args["x"] != 2 {
		t.Fatalf("default = %v, %v", m, err)
	}
	// A defaultMove that returns an illegal move is reported.
	g = mustLoad(t, module(`defaultMove(s, seat) { return { type: "jump" }; },`), Options{})
	if _, err := g.DefaultMove(mustSetup(t, g, 2, 1), 0); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("illegal defaultMove: %v", err)
	}
}

func TestEvents(t *testing.T) {
	e, _ := ExampleByID("lantern_market")
	g := mustLoad(t, e.Source, Options{})
	st := mustSetup(t, g, 3, 1)
	tm, _ := g.ToMove(st)
	_, events, err := g.Apply(st, tm[0], games.Move{Type: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].VisibleTo((tm[0]+1)%3) != true || events[1].VisibleTo((tm[0]+1)%3) || !events[1].VisibleTo(tm[0]) {
		t.Fatalf("draw events = %+v", events)
	}
	if !strings.Contains(events[0].Text, fmt.Sprintf("{s:%d}", tm[0])) {
		t.Fatalf("event text %q does not use {s:N}", events[0].Text)
	}
}

// ── examples and Check ──────────────────────────────────────────────────────

func TestExamples(t *testing.T) {
	ex := Examples()
	ids := map[string]bool{}
	for _, e := range ex {
		ids[e.ID] = true
		if e.Name == "" || e.Summary == "" {
			t.Errorf("%s: missing name or summary", e.ID)
		}
		if !strings.Contains(e.Source, "{s:") || !strings.Contains(e.Source, "p${") && !strings.Contains(e.Source, `"p0"`) {
			t.Errorf("%s: must use {s:N} text and p0..p7 colours", e.ID)
		}
		r := Check(e.ID, e.Source)
		if len(r.Issues) > 0 || r.Finished != r.Playouts {
			t.Errorf("%s: Check is not clean:\n%s", e.ID, r)
		}
	}
	for _, want := range []string{"tictactoe", "connect_four", "reversi", "lantern_market"} {
		if !ids[want] {
			t.Errorf("missing example %s", want)
		}
	}
}

func TestCheckCatchesBrokenModules(t *testing.T) {
	cases := []struct {
		name, src, want string
		sev             Severity
	}{
		{"syntax error", "const game = { meta: ", "syntax error", SevError},
		{"missing outcome", baseSrc + "const game = Object.assign({}, base, { outcome: 1 });", "game.outcome must be a function", SevError},
		{"too many seats", baseSrc + `const game = Object.assign({}, base, { meta: { name: "x", minSeats: 2, maxSeats: 40 } });`, "maxSeats <= 12", SevError},
		{"legal out of turn", module(`legal(s, seat) { return base.legal(Object.assign({}, s, { turn: seat }), seat); },`), "is not in toMove", SevError},
		{"view dims", module(`view(s) { return { board: { rows: 3, cols: 3, cells: [] } }; },`), "has 0 rows, but board.rows is 3", SevError},
		{"no outcome", module(`outcome(s) { return null; },`), "outcome returned null", SevError},
		{"Math.random", module(`apply(s, seat, m) { return { n: s.n + Math.random(), turn: 1 - seat }; },`), "use ctx.random()", SevError},
		{"module state", "let calls = 0;\n" + module(`setup(ctx) { calls++; return { n: 0, turn: 0, calls }; },`), "not deterministic", SevError},
		{"apply throws", module(`apply(s, seat, m) { if (m.type === "bet") throw new RangeError("bad bet " + m.args.x); return base.apply(s, seat, m); },`), "bad bet", SevError},
		{"hidden face", module(`view(s) { return { zones: [{ id: "deck", cards: [{ face: "A♠", hidden: true }] }] }; },`), "leaks hidden information", SevError},
		{"seat names", module(`view(s) { return { message: "Player 1 to move" }; },`), "{s:N}", SevWarn},
		{"endless", module(`toMove(s) { return [s.turn]; }, outcome(s) { return null; },
		  legal(s, seat) { return seat === s.turn ? [{ type: "inc", label: "+1" }] : []; },`), "did not end", SevWarn},
		{"hidden without determinize", baseSrc + `const game = Object.assign({}, base, { meta: Object.assign({}, base.meta, { hiddenInfo: true }) });`, "no determinize", SevWarn},
		{"leaky view", leakySrc, "should not have", SevError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Check("broken", c.src)
			for _, i := range r.Issues {
				if i.Severity == c.sev && strings.Contains(i.Message, c.want) {
					if c.sev == SevError && r.OK() {
						t.Fatal("OK() with an error issue")
					}
					return
				}
			}
			t.Fatalf("no %s issue containing %q:\n%s", c.sev, c.want, r)
		})
	}
}

// leakySrc is a hidden-information game whose view shows the opponent's
// secret: the leak check must notice that determinizing changes the view.
const leakySrc = `
const game = {
  meta: { name: "Leaky", summary: "Guess the secret.", minSeats: 2, maxSeats: 2, hiddenInfo: true, turnSeconds: 10 },
  setup(ctx) { return { secret: [ctx.randomInt(100), ctx.randomInt(100)], turn: 0, n: 0 }; },
  toMove(s) { return s.n >= 4 ? [] : [s.turn]; },
  legal(s, seat) { return s.n >= 4 || seat !== s.turn ? [] : [{ type: "pass", label: "Pass" }]; },
  apply(s, seat) { return { secret: s.secret, turn: 1 - seat, n: s.n + 1 }; },
  view(s, seat) { return { counters: [{ label: "Their secret", value: s.secret[1 - Math.max(seat, 0)] }] }; },
  outcome(s) { return s.n >= 4 ? { rank: [1, 1], score: [0, 0] } : null; },
  determinize(s, seat, ctx) { s.secret[1 - seat] = ctx.randomInt(100); return s; },
};`
