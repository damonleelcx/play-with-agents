package script

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// lookupSrc fetches the module's game object. A top-level "const game" is a
// lexical binding, not a property of the global object, so it has to be
// evaluated rather than read with Runtime.Get.
const lookupSrc = `typeof game === "undefined" ? undefined : game`

// bindSrc builds the API the Go side calls. Doing the JSON parse and
// stringify inside one JS function keeps each Go→JS crossing to a single call,
// and every call parses a fresh copy of the state, so a module can never
// mutate the state it was given in a way that outlives the call.
//
// The game object graph is already frozen by lockdown. verify (or null)
// reports a top-level binding a call reassigned; such a call fails, and the
// runtime is discarded (see lockdown.go).
const bindSrc = `(function (game, random, verify) {
  "use strict";
  const parse = JSON.parse, stringify = JSON.stringify, apply = Reflect.apply;
  const FNS = ["setup", "toMove", "legal", "apply", "view", "outcome", "defaultMove", "heuristic", "determinize"];

  const checked = (name, f) => verify === null ? f : function (...args) {
    let ok = false;
    try {
      const r = apply(f, this, args);
      ok = true;
      return r;
    } finally {
      const bad = verify();
      if (bad !== "") {
        throw new Error("` + moduleStateMarker + ` " + bad + " changed during " + name + ": game functions must not keep data " +
          "between calls (replays and other tables would see it). Keep everything in the state; use const for module-level tables" +
          (ok ? "" : " (the call also threw)"));
      }
    }
  };

  const mkctx = (seats, opts, rand) => Object.freeze({
    seats,
    options: opts ? parse(opts) : {},
    random: rand,
    randomInt: (n) => Math.floor(rand() * n),
    shuffle: (a) => {
      for (let i = a.length - 1; i > 0; i--) {
        const j = Math.floor(rand() * (i + 1));
        const t = a[i]; a[i] = a[j]; a[j] = t;
      }
      return a;
    },
  });

  // apply may return the new state or { state, events }.
  const isWrapped = (r) => r !== null && typeof r === "object" && !Array.isArray(r) &&
    Object.prototype.hasOwnProperty.call(r, "state") &&
    Object.keys(r).every((k) => k === "state" || k === "events");
  const stateOf = (r) => isWrapped(r) ? r.state : r;

  const api = {
    describe() {
      if (game === null || typeof game !== "object") return stringify({ type: game === null ? "null" : typeof game });
      const fns = {};
      for (const k of FNS) fns[k] = typeof game[k];
      return stringify({ type: "object", meta: game.meta === undefined ? null : game.meta, fns });
    },
    setup: (seats, opts) => stringify(game.setup(mkctx(seats, opts, random))),
    toMove: (s) => stringify(game.toMove(parse(s))),
    legal: (s, seat) => stringify(game.legal(parse(s), seat)),
    apply(s, seat, m, seats, opts) {
      const r = game.apply(parse(s), seat, parse(m), mkctx(seats, opts, random));
      if (isWrapped(r)) return [stringify(r.state), r.events === undefined ? "[]" : stringify(r.events)];
      return [stringify(r), "[]"];
    },
    view: (s, seat) => stringify(game.view(parse(s), seat)),
    outcome: (s) => stringify(game.outcome(parse(s))),
    defaultMove: (s, seat) => stringify(game.defaultMove(parse(s), seat)),
    heuristic: (s, seat) => game.heuristic(parse(s), seat),
    determinize: (s, seat, seats, opts) => stringify(game.determinize(parse(s), seat, mkctx(seats, opts, random))),

    // rollout plays uniformly random moves inside JS (no JSON per move) and
    // returns only the end: {o: outcome|null, h: [heuristic per seat]|null, p: plies}.
    // Its own PRNG (mulberry32) is fine: a playout is a throwaway sample.
    rollout(s, seats, opts, maxPlies, seed) {
      let a = seed | 0;
      const rnd = () => {
        a = (a + 0x6D2B79F5) | 0;
        let t = Math.imul(a ^ (a >>> 15), 1 | a);
        t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
      };
      const ctx = mkctx(seats, opts, rnd);
      let st = parse(s);
      for (let ply = 0; ; ply++) {
        const tm = game.toMove(st);
        if (!Array.isArray(tm)) throw new TypeError("toMove must return an array of seats");
        if (tm.length === 0) {
          const o = game.outcome(st);
          return stringify({ o: o === undefined ? null : o, h: null, p: ply, t: true });
        }
        if (ply >= maxPlies) {
          let h = null;
          if (typeof game.heuristic === "function") {
            h = [];
            for (let i = 0; i < seats; i++) h.push(game.heuristic(st, i));
          }
          return stringify({ o: null, h, p: ply, t: false });
        }
        const seat = tm[0];
        const legal = game.legal(st, seat);
        if (!Array.isArray(legal) || legal.length === 0) {
          throw new TypeError("legal(state, " + seat + ") returned no moves although toMove(state) includes seat " + seat);
        }
        const spec = legal[Math.floor(rnd() * legal.length)];
        const args = Object.assign({}, spec.args);
        if (spec.range) {
          const step = spec.range.step || 1;
          const n = Math.floor((spec.range.max - spec.range.min) / step);
          args[spec.range.arg] = spec.range.min + step * Math.floor(rnd() * (n + 1));
        }
        st = stateOf(game.apply(st, seat, { type: spec.type, args }, ctx));
        if (st === undefined) throw new TypeError("apply returned undefined: return the new state");
      }
    },
  };
  for (const k of Object.keys(api)) api[k] = checked(k, api[k]);
  return api;
})`

var (
	preludeProg = goja.MustCompile("prelude.js", preludeSrc, true)
	lookupProg  = goja.MustCompile("lookup.js", lookupSrc, false)
	bindProg    = goja.MustCompile("runtime.js", bindSrc, true)
)

var apiNames = []string{
	"describe", "setup", "toMove", "legal", "apply", "view", "outcome",
	"defaultMove", "heuristic", "determinize", "rollout",
}

// vm is one pooled JavaScript runtime with the module loaded. A vm is used
// by one goroutine at a time.
type vm struct {
	rt  *goja.Runtime
	api map[string]goja.Callable
	// rng is the stream ctx.random() draws from during the current call.
	// The caller loads it before a call and reads it back afterwards.
	rng uint64

	mu     sync.Mutex
	busy   bool          // a call is running: only then may the budget timer interrupt
	budget time.Duration // of the running call, for the error message
	timer  *time.Timer   // reused across calls; allocating one per call shows up in profiles
	broken bool          // interrupted or panicked: do not return to the pool

	// ctx, when set by Game.WithContext for the current call, interrupts
	// the call as soon as it is done.
	ctx context.Context

	// mutable lists module-level values lockdown could not freeze.
	mutable []string

	// expired is set with the interrupt so that long Go builtins (the JSON
	// encoder) can stop too; why is the interrupt value (under mu).
	expired atomic.Bool
	why     error
}

// errInterruptedNative is what a Go builtin panics with when it sees that
// the call was interrupted; run turns it into the interrupt's error.
var errInterruptedNative = errors.New("script: interrupted inside a builtin")

type errBudget struct{ d time.Duration }

func (e errBudget) Error() string { return fmt.Sprintf("exceeded the %v time budget", e.d) }

// errCancelled is the interrupt value when the caller's context ends.
type errCancelled struct{ err error }

func (e errCancelled) Error() string { return "cancelled by the caller: " + e.err.Error() }

// newVM creates a runtime, applies the sandbox, runs the module and locks
// down what its top level created.
func newVM(prog *goja.Program, info *moduleInfo, opts Options, sink func(level, line string)) (*vm, error) {
	v := &vm{rt: goja.New(), api: map[string]goja.Callable{}}
	v.rt.SetMaxCallStackSize(opts.MaxCallStack)
	v.timer = time.AfterFunc(time.Hour, v.onBudget)
	v.timer.Stop()

	pre, err := v.rt.RunProgram(preludeProg)
	if err != nil {
		return nil, fmt.Errorf("script: prelude: %w", err)
	}
	preFn, _ := goja.AssertFunction(pre)
	sinkFn := func(c goja.FunctionCall) goja.Value {
		sink(c.Argument(0).String(), c.Argument(1).String())
		return goja.Undefined()
	}
	lockVal, err := preFn(goja.Undefined(), v.rt.GlobalObject(), v.rt.ToValue(sinkFn), v.rt.ToValue(v.stringify))
	if err != nil {
		return nil, fmt.Errorf("script: prelude: %w", err)
	}
	lockdown, _ := goja.AssertFunction(lockVal)

	// The module's top level runs under a (generous) budget too: an infinite
	// loop there must not hang Load.
	if _, err := v.run("<top level>", 4*opts.CallBudget, func() (goja.Value, error) {
		return v.rt.RunProgram(prog)
	}); err != nil {
		return nil, err
	}

	gameVal, err := v.rt.RunProgram(lookupProg)
	if err != nil {
		return nil, fmt.Errorf("script: %w", err)
	}
	if gameVal == nil || goja.IsUndefined(gameVal) {
		return nil, errors.New("script: the module must define a global `game` object, e.g. `const game = { meta: {...}, setup(ctx) {...}, ... }`")
	}

	getLex, err := v.rt.RunProgram(info.getLex)
	if err != nil {
		return nil, fmt.Errorf("script: lockdown: %w", err)
	}
	locked, err := v.run("<top level>", 4*opts.CallBudget, func() (goja.Value, error) {
		return lockdown(goja.Undefined(), v.rt.ToValue(info.lexNames), v.rt.ToValue(info.lexKinds), getLex)
	})
	if err != nil {
		return nil, err
	}
	lockObj := locked.ToObject(v.rt)
	verify := lockObj.Get("verify")
	if err := v.rt.ExportTo(lockObj.Get("mutable"), &v.mutable); err != nil {
		return nil, fmt.Errorf("script: internal: lockdown: %w", err)
	}

	bind, err := v.rt.RunProgram(bindProg)
	if err != nil {
		return nil, fmt.Errorf("script: bind: %w", err)
	}
	bindFn, _ := goja.AssertFunction(bind)
	random := func(goja.FunctionCall) goja.Value {
		return v.rt.ToValue(unitFloat(splitmix64(&v.rng)))
	}
	apiVal, err := bindFn(goja.Undefined(), gameVal, v.rt.ToValue(random), verify)
	if err != nil {
		return nil, fmt.Errorf("script: bind: %w", err)
	}
	apiObj := apiVal.ToObject(v.rt)
	for _, name := range apiNames {
		fn, ok := goja.AssertFunction(apiObj.Get(name))
		if !ok {
			return nil, fmt.Errorf("script: internal: api.%s missing", name)
		}
		v.api[name] = fn
	}
	return v, nil
}

// call invokes one API function under the time budget.
func (v *vm) call(fn string, budget time.Duration, args ...any) (goja.Value, error) {
	vals := make([]goja.Value, len(args))
	for i, a := range args {
		vals[i] = v.rt.ToValue(a)
	}
	callable := v.api[fn]
	return v.run(fn, budget, func() (goja.Value, error) {
		return callable(goja.Undefined(), vals...)
	})
}

// run executes f with an interrupt timer and converts every failure,
// including Go panics inside goja, into a *ModuleError.
func (v *vm) run(fn string, budget time.Duration, f func() (goja.Value, error)) (val goja.Value, err error) {
	if v.ctx != nil {
		if cerr := v.ctx.Err(); cerr != nil {
			return nil, &ModuleError{Func: fn, Message: errCancelled{cerr}.Error(), cause: cerr}
		}
	}
	v.mu.Lock()
	v.busy, v.budget, v.why = true, budget, nil
	v.expired.Store(false)
	v.mu.Unlock()
	v.timer.Reset(budget)
	var stopCtx func() bool
	if ctx := v.ctx; ctx != nil {
		stopCtx = context.AfterFunc(ctx, func() { v.onCancel(ctx) })
	}
	defer func() {
		if stopCtx != nil && !stopCtx() {
			// The context ended during the call; onCancel may still be about
			// to interrupt, which would poison the next call: retire it.
			v.broken = true
		}
		if !v.timer.Stop() {
			// The timer fired (or is firing) for this call. Its callback may
			// still be about to run, and it could then interrupt the next
			// call on this runtime, so retire the runtime instead.
			v.broken = true
		}
		v.mu.Lock()
		v.busy = false
		v.mu.Unlock()
		v.rt.ClearInterrupt()
		if r := recover(); r != nil {
			v.broken = true
			val, err = nil, &ModuleError{Func: fn, Message: fmt.Sprintf("runtime panic: %v", r)}
			if r == errInterruptedNative {
				v.mu.Lock()
				why := v.why
				v.mu.Unlock()
				if c, ok := why.(errCancelled); ok {
					err = &ModuleError{Func: fn, Message: c.Error(), cause: c.err}
				} else if why != nil {
					err = &ModuleError{Func: fn, Message: why.Error() + " (inside a builtin)", Timeout: true}
				}
			}
		}
	}()
	val, err = f()
	if err != nil {
		return nil, v.convert(fn, err)
	}
	return val, nil
}

// onCancel runs when the context of the current call ends.
func (v *vm) onCancel(ctx context.Context) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.busy {
		v.why = errCancelled{ctx.Err()}
		v.expired.Store(true)
		v.rt.Interrupt(v.why)
	}
}

// onBudget runs on the timer goroutine when a call overruns its budget.
func (v *vm) onBudget() {
	v.mu.Lock()
	defer v.mu.Unlock()
	// The timer can fire just after the call returned; interrupting then
	// would poison the next call on this runtime.
	if v.busy {
		v.why = errBudget{v.budget}
		v.expired.Store(true)
		v.rt.Interrupt(v.why)
	}
}

func (v *vm) convert(fn string, err error) error {
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		// An interrupted runtime may hold half-built objects; never reuse it.
		v.broken = true
		if c, ok := ie.Value().(errCancelled); ok {
			return &ModuleError{Func: fn, Message: c.Error(), cause: c.err}
		}
		return &ModuleError{Func: fn, Message: ie.Error(), Timeout: true}
	}
	var so *goja.StackOverflowError
	if errors.As(err, &so) {
		v.broken = true
		return &ModuleError{Func: fn, Message: "maximum call stack size exceeded (unbounded recursion?)" + firstFrame(so.String())}
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		if strings.Contains(ex.Error(), moduleStateMarker) {
			v.broken = true // the module-level state is no longer what the top level left
		}
		return &ModuleError{Func: fn, Message: ex.Error() + hint(ex.Error()), Stack: truncate(ex.String(), 2000)}
	}
	var ce *goja.CompilerSyntaxError
	if errors.As(err, &ce) {
		return &ModuleError{Func: fn, Message: "syntax error: " + ce.Error()}
	}
	return &ModuleError{Func: fn, Message: err.Error()}
}

// hint explains the sandbox for the errors an author is most likely to hit.
func hint(msg string) string {
	switch {
	case strings.Contains(msg, "no member 'random'"):
		return " (Math.random is not available: use ctx.random(), ctx.randomInt(n) or ctx.shuffle(array) in setup/apply/determinize)"
	case strings.Contains(msg, "Date is not defined"):
		return " (Date is not available: games must not depend on the clock)"
	case strings.Contains(msg, "RegExp is not defined"):
		return " (RegExp is not available: use string methods such as indexOf, includes, split or replaceAll with a string)"
	case strings.Contains(msg, "Array is not defined") && !strings.Contains(msg, " Array is not defined"),
		strings.Contains(msg, "ArrayBuffer is not defined"), strings.Contains(msg, "DataView is not defined"):
		return " (typed arrays and buffers are not available in game modules: use plain arrays)"
	case strings.Contains(msg, "Proxy is not defined"), strings.Contains(msg, "WeakRef is not defined"):
		return " (not available in game modules)"
	}
	return ""
}

func firstFrame(stack string) string {
	for _, line := range strings.Split(stack, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "at ") {
			return " " + t
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── console ────────────────────────────────────────────────────────────────

// consoleBuf keeps the most recent console lines across all runtimes of a
// Game, for debugging modules. It is bounded in lines and line length.
type consoleBuf struct {
	mu    sync.Mutex
	lines []string
	next  int
	full  bool
}

const maxConsoleLine = 500

func newConsole(n int) *consoleBuf { return &consoleBuf{lines: make([]string, n)} }

func (c *consoleBuf) add(level, line string) {
	if len(c.lines) == 0 {
		return
	}
	if level != "log" {
		line = level + ": " + line
	}
	line = truncate(line, maxConsoleLine)
	c.mu.Lock()
	c.lines[c.next] = line
	c.next = (c.next + 1) % len(c.lines)
	if c.next == 0 {
		c.full = true
	}
	c.mu.Unlock()
}

func (c *consoleBuf) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.full {
		return append([]string(nil), c.lines[:c.next]...)
	}
	out := make([]string, 0, len(c.lines))
	out = append(out, c.lines[c.next:]...)
	return append(out, c.lines[:c.next]...)
}
