// Package script runs game modules written in JavaScript (see "Script module
// contract" in docs/00-architecture.md) as a games.Game.
//
// # Sandbox
//
// Modules run in goja runtimes with no I/O, no require, no clock (Date is
// removed) and no ambient randomness (Math.random is removed). Every call has
// a time budget enforced with Runtime.Interrupt; an interrupted runtime is
// discarded. Values cross the boundary as JSON in both directions, so a
// module only ever sees fresh copies and cannot mutate state across calls.
// Every output is validated (see validate.go) and states larger than
// Options.MaxStateBytes are rejected.
//
// goja checks for interrupts between bytecode instructions, so one native
// builtin call runs to completion whatever the budget. The prelude
// (prelude.go) therefore removes RegExp and caps every builtin that could
// iterate or allocate unboundedly in one call, and JSON.stringify is a Go
// implementation (json.go) that enforces an output budget and polls the
// deadline. Known residual: the + operator on strings is one instruction
// per concatenation, so doubling a string in a loop is bounded only by the
// time budget (about 1 GB of garbage within 250ms). goja has no heap limit
// hook; run the server with GOMEMLIMIT and a container memory limit.
//
// After the top level runs, lockdown (lockdown.go) freezes the builtins and
// everything the module bound, so a call cannot leave state behind for the
// next call on a pooled runtime. A call bound to a context (WithContext) is
// interrupted when the context ends.
//
// # Randomness
//
// The runtime owns the random stream. The persisted state is an envelope
//
//	{"s": <module state>, "rng": "<uint64>", "n": <moves applied>, "seats": N, "o": {options}}
//
// ctx.random() draws from rng (splitmix64), and the advanced stream is stored
// back after setup and apply, so replaying the same moves from the same seed
// yields byte-identical states.
//
// # Concurrency
//
// A *Game is safe for concurrent use. The program is compiled once; calls
// run on a pool of runtimes, each used by one goroutine at a time.
package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"time"

	"github.com/dop251/goja"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
)

// Options bounds a module's resource use. Zero values take the defaults.
type Options struct {
	CallBudget    time.Duration // per call; default 250ms
	RolloutBudget time.Duration // per whole random playout (ai.Rollouter); default 8×CallBudget
	MaxStateBytes int           // serialised module state; default 256 KiB
	MaxViewBytes  int           // serialised view; default 64 KiB
	MaxLegal      int           // legal moves per call; default 512
	MaxEvents     int           // events per apply; default 64
	PoolSize      int           // runtimes; default GOMAXPROCS (max 16)
	MaxCallStack  int           // JS call depth; default 2048
	ConsoleLines  int           // console.log lines kept; default 200
}

func (o Options) withDefaults() Options {
	if o.CallBudget <= 0 {
		o.CallBudget = 250 * time.Millisecond
	}
	if o.RolloutBudget <= 0 {
		o.RolloutBudget = 8 * o.CallBudget
	}
	if o.MaxStateBytes <= 0 {
		o.MaxStateBytes = 256 << 10
	}
	if o.MaxViewBytes <= 0 {
		o.MaxViewBytes = 64 << 10
	}
	if o.MaxLegal <= 0 {
		o.MaxLegal = 512
	}
	if o.MaxEvents <= 0 {
		o.MaxEvents = 64
	}
	if o.PoolSize <= 0 {
		o.PoolSize = min(runtime.GOMAXPROCS(0), 16)
	}
	if o.MaxCallStack <= 0 {
		o.MaxCallStack = 2048
	}
	if o.ConsoleLines <= 0 {
		o.ConsoleLines = 200
	}
	return o
}

// Game is a loaded module. It implements games.Game and the optional ai
// interfaces (Heuristic and Determinizer report whether the module defines
// them; Reseeder and Rollouter are always available).
type Game struct {
	meta games.Meta
	prog *goja.Program
	opts Options

	hasDefault, hasHeuristic, hasDeterminize bool

	info    *moduleInfo
	mutable []string // module-level values that cannot be frozen (Check reports them)
	// ctx, set on the copies WithContext returns, interrupts their calls.
	ctx context.Context

	idle    chan *vm      // ready runtimes
	slots   chan struct{} // one token per live runtime, bounds the pool
	console *consoleBuf
	memo    *memo
}

var (
	_ games.Game       = (*Game)(nil)
	_ ai.Heuristic     = (*Game)(nil)
	_ ai.Determinizer  = (*Game)(nil)
	_ ai.Reseeder      = (*Game)(nil)
	_ ai.Rollouter     = (*Game)(nil)
	_ ai.ContextBinder = (*Game)(nil)
)

// Load compiles a module and validates its shape (the game object, meta and
// required functions). The error message is written for the module's author.
func Load(id string, src string, opts Options) (*Game, error) {
	opts = opts.withDefaults()
	prog, err := goja.Compile(id+".js", src, false)
	if err != nil {
		return nil, fmt.Errorf("script: syntax error: %v", err)
	}
	info, err := analyze(id+".js", src)
	if err != nil {
		return nil, err
	}
	g := &Game{
		prog:    prog,
		info:    info,
		opts:    opts,
		idle:    make(chan *vm, opts.PoolSize),
		slots:   make(chan struct{}, opts.PoolSize),
		console: newConsole(opts.ConsoleLines),
		memo:    newMemo(),
	}
	v, err := newVM(prog, info, opts, g.console.add)
	if err != nil {
		return nil, loadErr(err)
	}
	g.mutable = v.mutable
	g.slots <- struct{}{}
	defer g.release(v)

	res, err := v.call("describe", opts.CallBudget)
	if err != nil {
		return nil, loadErr(err)
	}
	if err := g.readDescription(id, res.String()); err != nil {
		return nil, err
	}
	return g, nil
}

func loadErr(err error) error {
	var me *ModuleError
	if errors.As(err, &me) {
		return fmt.Errorf("script: running the module failed: %s", me.Message)
	}
	return err
}

type description struct {
	Type string            `json:"type"`
	Meta json.RawMessage   `json:"meta"`
	Fns  map[string]string `json:"fns"`
}

func (g *Game) readDescription(id, raw string) error {
	var d description
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return fmt.Errorf("script: internal: describe: %w", err)
	}
	if d.Type != "object" {
		return fmt.Errorf("script: `game` must be an object, got %s", d.Type)
	}
	for _, fn := range []string{"setup", "toMove", "legal", "apply", "view", "outcome"} {
		if d.Fns[fn] != "function" {
			return fmt.Errorf("script: game.%s must be a function (got %s)", fn, d.Fns[fn])
		}
	}
	for _, fn := range []string{"defaultMove", "heuristic", "determinize"} {
		if t := d.Fns[fn]; t != "function" && t != "undefined" {
			return fmt.Errorf("script: optional game.%s must be a function or absent (got %s)", fn, t)
		}
	}
	g.hasDefault = d.Fns["defaultMove"] == "function"
	g.hasHeuristic = d.Fns["heuristic"] == "function"
	g.hasDeterminize = d.Fns["determinize"] == "function"

	m, err := parseMeta(d.Meta)
	if err != nil {
		return err
	}
	m.ID = id
	g.meta = m
	return nil
}

// ── games.Game ──────────────────────────────────────────────────────────────

func (g *Game) Meta() games.Meta {
	m := g.meta
	m.Options = maps.Clone(g.meta.Options)
	return m
}

// Logs returns the most recent console output of the module, oldest first.
func (g *Game) Logs() []string { return g.console.snapshot() }

func (g *Game) HasHeuristic() bool    { return g.hasHeuristic }
func (g *Game) HasDeterminizer() bool { return g.hasDeterminize }

func (g *Game) Setup(cfg games.Config, seed int64) (games.State, error) {
	if cfg.Seats < g.meta.MinSeats || cfg.Seats > g.meta.MaxSeats {
		return nil, fmt.Errorf("script %s: %d seats; this game takes %d to %d", g.meta.ID, cfg.Seats, g.meta.MinSeats, g.meta.MaxSeats)
	}
	opts := maps.Clone(g.meta.Options)
	if opts == nil && len(cfg.Options) > 0 {
		opts = map[string]any{}
	}
	maps.Copy(opts, cfg.Options)
	var optsJSON json.RawMessage
	if len(opts) > 0 {
		b, err := json.Marshal(opts)
		if err != nil {
			return nil, fmt.Errorf("script %s: options: %w", g.meta.ID, err)
		}
		optsJSON = b
	}

	var out games.State
	err := g.with(func(v *vm) error {
		v.rng = seedState(uint64(seed))
		res, err := v.call("setup", g.opts.CallBudget, cfg.Seats, string(optsJSON))
		if err != nil {
			return err
		}
		s, err := g.stateOut("setup", res)
		if err != nil {
			return err
		}
		out, err = envelope{S: s, RNG: v.rng, Seats: cfg.Seats, Opts: optsJSON}.encode()
		return err
	})
	return out, err
}

func (g *Game) ToMove(st games.State) ([]games.Seat, error) {
	env, err := decodeEnvelope(st)
	if err != nil {
		return nil, err
	}
	k := g.memo.key(env.S, 't', 0)
	if e := g.memo.get(k, env.S); e != nil {
		return slices.Clone(e.toMove), nil
	}
	var out []games.Seat
	err = g.with(func(v *vm) (err error) {
		out, err = g.toMove(v, env)
		return err
	})
	return slices.Clone(out), err
}

// toMove returns a memoised result: callers must not modify it.
func (g *Game) toMove(v *vm, env envelope) ([]games.Seat, error) {
	k := g.memo.key(env.S, 't', 0)
	if e := g.memo.get(k, env.S); e != nil {
		return e.toMove, nil
	}
	res, err := v.call("toMove", g.opts.CallBudget, string(env.S))
	if err != nil {
		return nil, err
	}
	raw, ok := jsonOut(res)
	if !ok {
		return nil, outErr("toMove", "", "returned undefined; return an array of seats, [] when the game is over")
	}
	tm, err := parseToMove(raw, env.Seats)
	if err != nil {
		return nil, err
	}
	g.memo.put(k, env.S, &memoEntry{toMove: tm})
	return tm, nil
}

func (g *Game) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	env, err := decodeEnvelope(st)
	if err != nil {
		return nil, err
	}
	if seat == games.Spectator {
		return []games.MoveSpec{}, nil
	}
	if seat < 0 || seat >= env.Seats {
		return nil, fmt.Errorf("script %s: seat %d out of range 0..%d", g.meta.ID, seat, env.Seats-1)
	}
	k := g.memo.key(env.S, 'l', seat)
	if e := g.memo.get(k, env.S); e != nil {
		return cloneSpecs(e.legal), nil
	}
	var out []games.MoveSpec
	err = g.with(func(v *vm) (err error) {
		out, err = g.legal(v, env, seat)
		return err
	})
	return cloneSpecs(out), err
}

// legal returns a memoised result: callers must not modify it.
func (g *Game) legal(v *vm, env envelope, seat games.Seat) ([]games.MoveSpec, error) {
	k := g.memo.key(env.S, 'l', seat)
	if e := g.memo.get(k, env.S); e != nil {
		return e.legal, nil
	}
	res, err := v.call("legal", g.opts.CallBudget, string(env.S), seat)
	if err != nil {
		return nil, err
	}
	raw, ok := jsonOut(res)
	if !ok {
		return nil, outErr("legal", "", "returned undefined; return an array of moves, [] when seat cannot move")
	}
	specs, err := parseLegal(raw, g.opts.MaxLegal)
	if err != nil {
		return nil, err
	}
	g.memo.put(k, env.S, &memoEntry{legal: specs})
	return specs, nil
}

// Apply checks the move against legal(state, seat) before the module sees
// it, so a throw inside apply is always a module bug (ErrModule), and an
// out-of-turn or unlisted move is always games.ErrIllegal.
func (g *Game) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	env, err := decodeEnvelope(st)
	if err != nil {
		return nil, nil, err
	}
	if seat < 0 || seat >= env.Seats {
		return nil, nil, games.Illegal("seat %d is not at this table", seat)
	}
	var (
		out    games.State
		events []games.Event
	)
	err = g.with(func(v *vm) error {
		tm, err := g.toMove(v, env)
		if err != nil {
			return err
		}
		if !containsSeat(tm, seat) {
			return games.Illegal("it is not seat %d's turn", seat)
		}
		specs, err := g.legal(v, env, seat)
		if err != nil {
			return err
		}
		canon, ok := matchMove(specs, m)
		if !ok {
			return games.Illegal("%s is not a legal move for seat %d", describeMove(m), seat)
		}
		mj, err := json.Marshal(canon)
		if err != nil {
			return games.Illegal("move args are not JSON: %v", err)
		}

		v.rng = env.RNG
		res, err := v.call("apply", g.opts.CallBudget, string(env.S), seat, string(mj), env.Seats, string(env.Opts))
		if err != nil {
			return err
		}
		pair := res.ToObject(v.rt)
		s, err := g.stateOut("apply", pair.Get("0"))
		if err != nil {
			return err
		}
		evRaw, _ := jsonOut(pair.Get("1"))
		if events, err = parseEvents(evRaw, seat, env.Seats, g.opts.MaxEvents); err != nil {
			return err
		}
		next := envelope{S: s, RNG: v.rng, N: env.N + 1, Seats: env.Seats, Opts: env.Opts}
		out, err = next.encode()
		return err
	})
	return out, events, err
}

func (g *Game) View(st games.State, seat games.Seat) (games.View, error) {
	env, err := decodeEnvelope(st)
	if err != nil {
		return games.View{}, err
	}
	if seat < games.Spectator || seat >= env.Seats {
		return games.View{}, fmt.Errorf("script %s: seat %d out of range", g.meta.ID, seat)
	}
	var out games.View
	err = g.with(func(v *vm) error {
		res, err := v.call("view", g.opts.CallBudget, string(env.S), seat)
		if err != nil {
			return err
		}
		raw, ok := jsonOut(res)
		if !ok {
			return outErr("view", "", "returned undefined; return a board view object")
		}
		msg, err := validateView(raw, env.Seats, g.opts.MaxViewBytes)
		if err != nil {
			return err
		}
		out = games.View{Kind: "board", Data: json.RawMessage(raw), Status: msg}
		return nil
	})
	return out, err
}

func (g *Game) Outcome(st games.State) (*games.Outcome, error) {
	env, err := decodeEnvelope(st)
	if err != nil {
		return nil, err
	}
	var out *games.Outcome
	err = g.with(func(v *vm) error {
		res, err := v.call("outcome", g.opts.CallBudget, string(env.S))
		if err != nil {
			return err
		}
		raw, ok := jsonOut(res)
		if !ok || raw == "null" { // `return;` is a natural way to say "not over"
			return nil
		}
		out, err = parseOutcome(raw, env.Seats)
		return err
	})
	return out, err
}

// DefaultMove asks the module's defaultMove when it has one (it must return
// a legal move), else plays the first legal move, at the minimum of a range.
func (g *Game) DefaultMove(st games.State, seat games.Seat) (games.Move, error) {
	env, err := decodeEnvelope(st)
	if err != nil {
		return games.Move{}, err
	}
	if seat < 0 || seat >= env.Seats {
		return games.Move{}, games.Illegal("seat %d is not at this table", seat)
	}
	var out games.Move
	err = g.with(func(v *vm) error {
		specs, err := g.legal(v, env, seat)
		if err != nil {
			return err
		}
		if len(specs) == 0 {
			return games.Illegal("seat %d has no legal moves", seat)
		}
		if g.hasDefault {
			res, err := v.call("defaultMove", g.opts.CallBudget, string(env.S), seat)
			if err != nil {
				return err
			}
			if raw, ok := jsonOut(res); ok && raw != "null" {
				m, err := parseMove(raw)
				if err != nil {
					return err
				}
				canon, ok := matchMove(specs, m)
				if !ok {
					return outErr("defaultMove", "", "returned %s, which is not in legal(state, %d)", describeMove(m), seat)
				}
				out = canon
				return nil
			}
		}
		out = ai.ExpandMoves(specs[:1], 2)[0]
		return nil
	})
	return out, err
}

// ── optional ai interfaces ──────────────────────────────────────────────────

// Heuristic returns the module's evaluation clamped to [-1, 1]. Without a
// heuristic it returns 0 (no opinion).
func (g *Game) Heuristic(st games.State, seat games.Seat) (float64, error) {
	if !g.hasHeuristic {
		return 0, nil
	}
	env, err := decodeEnvelope(st)
	if err != nil {
		return 0, err
	}
	var h float64
	err = g.with(func(v *vm) error {
		res, err := v.call("heuristic", g.opts.CallBudget, string(env.S), seat)
		if err != nil {
			return err
		}
		h, err = numberOut("heuristic", res)
		return err
	})
	return max(-1, min(1, h)), err
}

// Determinize returns a state consistent with what seat knows, with hidden
// parts resampled by the module from a stream seeded by seed. The returned
// state's own random stream is replaced as well, so searching it cannot
// reveal future chance events either.
func (g *Game) Determinize(st games.State, seat games.Seat, seed uint64) (games.State, error) {
	if !g.hasDeterminize {
		return nil, errors.New("script: module has no determinize function")
	}
	env, err := decodeEnvelope(st)
	if err != nil {
		return nil, err
	}
	var out games.State
	err = g.with(func(v *vm) error {
		v.rng = seedState(seed)
		res, err := v.call("determinize", g.opts.CallBudget, string(env.S), seat, env.Seats, string(env.Opts))
		if err != nil {
			return err
		}
		s, err := g.stateOut("determinize", res)
		if err != nil {
			return err
		}
		env.S, env.RNG = s, v.rng
		out, err = env.encode()
		return err
	})
	return out, err
}

// Reseed replaces the state's random stream. It makes no module call.
func (g *Game) Reseed(st games.State, seed uint64) (games.State, error) {
	env, err := decodeEnvelope(st)
	if err != nil {
		return nil, err
	}
	env.RNG = seedState(seed)
	return env.encode()
}

// Rollout plays a random playout inside the runtime (one call instead of
// several JSON round trips per move).
func (g *Game) Rollout(st games.State, maxPlies int, seed uint64) (ai.RolloutResult, error) {
	env, err := decodeEnvelope(st)
	if err != nil {
		return ai.RolloutResult{}, err
	}
	var out ai.RolloutResult
	err = g.with(func(v *vm) error {
		res, err := v.call("rollout", g.opts.RolloutBudget, string(env.S), env.Seats, string(env.Opts), maxPlies, int64(uint32(seed)))
		if err != nil {
			return err
		}
		raw, _ := jsonOut(res)
		var r struct {
			O json.RawMessage `json:"o"`
			H []*float64      `json:"h"`
			P int             `json:"p"`
			T bool            `json:"t"`
		}
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return outErr("rollout", "", "returned malformed JSON: %v", err)
		}
		out.Plies = r.P
		if len(r.O) > 0 && string(r.O) != "null" {
			if out.Outcome, err = parseOutcome(string(r.O), env.Seats); err != nil {
				return err
			}
		} else if r.T {
			return outErr("outcome", "", "returned null although toMove returned [] (the game is over)")
		}
		if r.H != nil {
			out.Heuristic = make([]float64, len(r.H))
			for i, h := range r.H {
				if h != nil {
					out.Heuristic[i] = max(-1, min(1, *h))
				}
			}
		}
		return nil
	})
	return out, err
}

// ── plumbing ────────────────────────────────────────────────────────────────

// with runs f on a pooled runtime, creating one if the pool is not full.
func (g *Game) with(f func(v *vm) error) error {
	if g.ctx != nil {
		if err := g.ctx.Err(); err != nil {
			return &ModuleError{Func: "call", Message: errCancelled{err}.Error(), cause: err}
		}
	}
	v, err := g.acquire()
	if err != nil {
		return err
	}
	v.ctx = g.ctx
	defer func() {
		v.ctx = nil
		g.release(v)
	}()
	return f(v)
}

// WithContext returns a view of g whose calls fail at once when ctx is done
// and are interrupted when ctx ends mid-call (the runtime is discarded).
// The AI uses it (ai.ContextBinder) so a search abandoned at its deadline
// does not keep a runtime busy for up to a whole call or rollout budget.
func (g *Game) WithContext(ctx context.Context) games.Game {
	c := *g
	c.ctx = ctx
	return &c
}

func (g *Game) acquire() (*vm, error) {
	select {
	case v := <-g.idle:
		return v, nil
	default:
	}
	select {
	case v := <-g.idle:
		return v, nil
	case g.slots <- struct{}{}:
		v, err := newVM(g.prog, g.info, g.opts, g.console.add)
		if err != nil {
			<-g.slots
			return nil, err
		}
		return v, nil
	}
}

func (g *Game) release(v *vm) {
	if v.broken {
		<-g.slots // drop it; the next acquire builds a fresh one
		return
	}
	g.idle <- v
}

// stateOut validates a state returned by the module.
func (g *Game) stateOut(fn string, res goja.Value) (json.RawMessage, error) {
	raw, ok := jsonOut(res)
	if !ok {
		return nil, outErr(fn, "", "returned undefined; return the state (a JSON object)")
	}
	if raw == "" || (raw[0] != '{' && raw[0] != '[') {
		return nil, outErr(fn, "", "must return the state as an object or array, got %s", truncate(raw, 40))
	}
	if len(raw) > g.opts.MaxStateBytes {
		return nil, outErr(fn, "", "returned a %d-byte state, over the %d-byte limit", len(raw), g.opts.MaxStateBytes)
	}
	return json.RawMessage(raw), nil
}

// jsonOut reads a JSON string produced by JSON.stringify inside the runtime;
// ok is false when the module returned undefined.
func jsonOut(v goja.Value) (string, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return "", false
	}
	return v.String(), true
}

func numberOut(fn string, v goja.Value) (float64, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0, outErr(fn, "", "returned %v; return a number in [-1, 1]", v)
	}
	switch x := v.Export().(type) {
	case int64:
		return float64(x), nil
	case float64:
		if x != x {
			return 0, outErr(fn, "", "returned NaN")
		}
		return x, nil
	}
	return 0, outErr(fn, "", "returned %s; return a number in [-1, 1]", v.String())
}

// envelope is the persisted state: the module's state plus the runtime's
// bookkeeping. RNG is a string in JSON because JavaScript clients cannot
// represent a uint64 exactly.
type envelope struct {
	S     json.RawMessage
	RNG   uint64
	N     int
	Seats int
	Opts  json.RawMessage
}

type envelopeJSON struct {
	S     json.RawMessage `json:"s"`
	RNG   string          `json:"rng"`
	N     int             `json:"n"`
	Seats int             `json:"seats"`
	Opts  json.RawMessage `json:"o,omitempty"`
}

func (e envelope) encode() (games.State, error) {
	b, err := json.Marshal(envelopeJSON{S: e.S, RNG: strconv.FormatUint(e.RNG, 10), N: e.N, Seats: e.Seats, Opts: e.Opts})
	if err != nil {
		return nil, outErr("state", "", "is not valid JSON: %v", err)
	}
	return b, nil
}

func decodeEnvelope(st games.State) (envelope, error) {
	var j envelopeJSON
	if err := json.Unmarshal(st, &j); err != nil {
		return envelope{}, fmt.Errorf("script: malformed state: %w", err)
	}
	rng, err := strconv.ParseUint(j.RNG, 10, 64)
	if err != nil || len(j.S) == 0 || j.Seats < 1 || j.N < 0 {
		return envelope{}, errors.New("script: malformed state envelope")
	}
	return envelope{S: j.S, RNG: rng, N: j.N, Seats: j.Seats, Opts: j.Opts}, nil
}

func containsSeat(s []games.Seat, seat games.Seat) bool {
	for _, x := range s {
		if x == seat {
			return true
		}
	}
	return false
}

// matchMove finds the legal spec m instantiates and returns the canonical
// move (normalised args, the range argument as an integer). A fixed spec
// matches on type and identical args; a ranged spec matches on type, its
// fixed args, and an integer range argument inside [min, max] on the step.
func matchMove(specs []games.MoveSpec, m games.Move) (games.Move, bool) {
	args, err := normArgs(m.Args)
	if err != nil {
		return games.Move{}, false
	}
	for _, sp := range specs {
		if sp.Type != m.Type {
			continue
		}
		fixed, _ := normArgs(sp.Args)
		if sp.Range == nil {
			if argsEqual(fixed, args) {
				return games.Move{Type: m.Type, Args: args}, true
			}
			continue
		}
		r := sp.Range
		x, ok := args[r.Arg].(float64)
		if !ok || x != float64(int(x)) {
			continue
		}
		val := int(x)
		step := max(r.Step, 1)
		if val < r.Min || val > r.Max || (val-r.Min)%step != 0 {
			continue
		}
		rest := maps.Clone(args)
		delete(rest, r.Arg)
		delete(fixed, r.Arg)
		if !argsEqual(fixed, rest) {
			continue
		}
		rest[r.Arg] = val
		return games.Move{Type: m.Type, Args: rest}, true
	}
	return games.Move{}, false
}

// normArgs round-trips args through JSON so Go ints and decoded float64s
// compare equal, exactly as the module will see them.
func normArgs(a map[string]any) (map[string]any, error) {
	if len(a) == 0 {
		return map[string]any{}, nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func argsEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func describeMove(m games.Move) string {
	if len(m.Args) == 0 {
		return strconv.Quote(m.Type)
	}
	b, _ := json.Marshal(m.Args)
	return fmt.Sprintf("%q %s", m.Type, b)
}
