package script

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// costOf runs f and reports its wall time and the bytes allocated
// meanwhile (process-wide; these tests do not run in parallel).
func costOf(f func()) (time.Duration, uint64) {
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	start := time.Now()
	f()
	d := time.Since(start)
	runtime.ReadMemStats(&m1)
	return d, m1.TotalAlloc - m0.TotalAlloc
}

// TestNativeBombsFailFast: a single native builtin call cannot be
// interrupted, so each of these used to run for seconds and allocate
// gigabytes under a 250ms budget (the first three are the reviewer's probes:
// 7.2s; 1.5s and 2.0GB; 6s and 2.7GB). Each must now fail within twice the
// budget and allocate little.
func TestNativeBombsFailFast(t *testing.T) {
	budget := 250 * time.Millisecond
	if raceEnabled {
		// The race detector slows the interpreter about tenfold; the bombs
		// still fail fast, but building their inputs takes longer.
		budget = 3 * time.Second
	}
	const maxAlloc = 64 << 20
	bombs := []struct{ name, code, want string }{
		// Regex literals are rejected at Load; a pattern built at run time
		// (Function, eval) reaches a RegExp whose methods all throw.
		{"catastrophic regex", `Function("return /^(a+)+b(?=c)/")().test("a".repeat(27))`, "RegExp is not available"},
		{"regex replace growth", `"a".repeat(1 << 20).replace(Function("return /a/g")(), "a".repeat(300))`, "RegExp is not available"},
		{"stringify huge sparse array", `JSON.stringify(new Array(6e7))`, "RangeError"},
		{"string match builds a regex", `"aaaa".match("(a+)+b")`, "RegExp is not available"},
		{"replaceAll growth", `"a".repeat(1 << 20).replaceAll("a", "a".repeat(300))`, "RangeError"},
		{"replacement pattern growth", "\"a\".repeat(1 << 19).replaceAll(\"a\", \"$'\")", "RangeError"},
		{"includes over holes", `new Array(6e7).includes(1)`, "RangeError"},
		{"map over holes", `new Array(6e7).map((x) => x)`, "RangeError"},
		{"spread of holes", `const a = new Array(6e7); [...a].length`, "RangeError"},
		{"apply with a huge array-like", `Math.max.apply(null, { length: 6e7 })`, "RangeError"},
		{"typed arrays are unavailable", `new Float64Array(1e8)`, "Float64Array is not defined"},
		{"join of shared big strings", `const b = "x".repeat(1 << 20), a = []; for (let i = 0; i < 2000; i++) a.push(b); a.join("")`, "RangeError"},
		{"stringify of shared big strings", `const b = "x".repeat(1 << 20), a = []; for (let i = 0; i < 2000; i++) a.push(b); JSON.stringify(a)`, "RangeError"},
		{"stringify of a shared subtree", `let o = "x".repeat(1000); for (let i = 0; i < 40; i++) o = { a: o, b: o }; JSON.stringify(o)`, "RangeError"},
		{"flat of shared arrays", `const b = new Array(1 << 16).fill(0), a = new Array(1 << 16).fill(b); a.flat()`, "RangeError"},
		{"split into characters", `"x".repeat(1 << 20).split("").length`, "RangeError"},
		{"split on a separator", `",".repeat(1 << 20).split(",").length`, "RangeError"},
		{"spread of a long string", `[..."x".repeat(1 << 20)].length`, "RangeError"},
	}
	for _, b := range bombs {
		t.Run(b.name, func(t *testing.T) {
			g := mustLoad(t, module(`legal(s, seat) { `+b.code+`; return []; },`), Options{CallBudget: budget, PoolSize: 1})
			st := mustSetup(t, g, 2, 1)
			var err error
			d, alloc := costOf(func() { _, err = g.Legal(st, 0) })
			if !errors.Is(err, ErrModule) || !strings.Contains(err.Error(), b.want) {
				t.Fatalf("err = %v, want a module error containing %q", err, b.want)
			}
			if d > 2*budget {
				t.Errorf("took %v, want < %v", d, 2*budget)
			}
			if alloc > maxAlloc {
				t.Errorf("allocated %d MB, want < %d MB", alloc>>20, maxAlloc>>20)
			}
		})
	}
}

func TestRegexLiteralsAreRejectedAtLoad(t *testing.T) {
	_, err := Load("rx", module(`view(s) { return { message: /^(a+)+b(?=c)/.test("aaa") ? "y" : "n" }; },`), Options{})
	if err == nil || !strings.Contains(err.Error(), "regular expressions are not available") || !strings.Contains(err.Error(), "line ") {
		t.Fatalf("err = %v", err)
	}
	// Division is not a regex.
	mustLoad(t, module(`view(s) { const x = s.n / 2 / 1; return { message: String(x) }; },`), Options{})
}

// TestModuleLevelStateIsLocked: runtimes are pooled, so anything a call
// leaves behind would make later calls depend on which runtime served them.
func TestModuleLevelStateIsLocked(t *testing.T) {
	view := func(t *testing.T, src string) []string {
		t.Helper()
		g := mustLoad(t, src, Options{PoolSize: 1})
		st := mustSetup(t, g, 2, 1)
		var out []string
		for range 3 {
			v, err := g.View(st, 0)
			if err != nil {
				out = append(out, "error: "+err.Error())
				continue
			}
			out = append(out, v.Status)
		}
		return out
	}
	same := func(t *testing.T, got []string, want string) {
		t.Helper()
		for _, s := range got {
			if s != want {
				t.Fatalf("views = %q, want %q every time", got, want)
			}
		}
	}

	t.Run("let reassigned", func(t *testing.T) {
		got := view(t, "let n = 0;\n"+module(`view(s) { n++; return { message: String(n) }; },`))
		for _, s := range got {
			if !strings.Contains(s, "module-level variable n changed during view") {
				t.Fatalf("views = %q, want a module-level state error every time", got)
			}
		}
	})
	t.Run("var reassigned", func(t *testing.T) {
		got := view(t, "var n = 0;\n"+module(`view(s) { n++; return { message: String(n) }; },`))
		if !strings.Contains(got[0], "module-level variable n changed") {
			t.Fatalf("views = %q", got)
		}
	})
	t.Run("function reassigned", func(t *testing.T) {
		// Function declarations are read-only: the assignment is ignored.
		same(t, view(t, "function f() { return 1; }\n"+module(`view(s) { f = () => 2; return { message: String(f()) }; },`)), "1")
	})
	t.Run("globalThis property", func(t *testing.T) {
		same(t, view(t, module(`view(s) { globalThis.count = (globalThis.count || 0) + 1; return { message: String(globalThis.count) }; },`)), "undefined")
	})
	t.Run("builtin prototype", func(t *testing.T) {
		same(t, view(t, module(`view(s) { Array.prototype.count = ([].count || 0) + 1; return { message: String([].count) }; },`)), "undefined")
	})
	t.Run("top-level object", func(t *testing.T) {
		same(t, view(t, "const memo = { n: 0 };\n"+module(`view(s) { memo.n++; return { message: String(memo.n) }; },`)), "0")
	})
	t.Run("top-level Map", func(t *testing.T) {
		got := view(t, "const memo = new Map();\n"+module(`view(s) { memo.set(1, 2); return { message: "x" }; },`))
		if !strings.Contains(got[0], "module-level Map memo is read-only") {
			t.Fatalf("views = %q", got)
		}
	})
	t.Run("const tables and helpers still work", func(t *testing.T) {
		same(t, view(t, "const DIRS = [[0, 1], [1, 0]];\nfunction sum(a) { return a.reduce((x, y) => x + y, 0); }\nclass P { constructor(x) { this.x = x; } }\n"+
			module(`view(s) { const e = new Error("x"); e.name = "Mine"; const o = {}; o.toString = () => "ok";
			  return { message: String(sum(DIRS.map(([a, b]) => a + b))) + new P(1).x + e.name + o }; },`)), "21Mineok")
	})
}

func TestCheckReportsModuleState(t *testing.T) {
	r := Check("state", "let calls = 0;\n"+module(`apply(s, seat, m) { calls++; return base.apply(s, seat, m); },`))
	if r.OK() || !strings.Contains(r.String(), "module-level variable calls changed during apply") {
		t.Fatalf("Check did not report module-level state:\n%s", r)
	}
}

// TestJSONMatchesNative: the sandbox's JSON.stringify (json.go) must print
// exactly what goja's does, or persisted states would change.
func TestJSONMatchesNative(t *testing.T) {
	rt := goja.New()
	v := &vm{rt: rt}
	if err := rt.Set("enc", v.stringify); err != nil {
		t.Fatal(err)
	}
	res, err := rt.RunString(`
	  class Pt { constructor() { this.x = 1; this.y = [2, "3"]; } }
	  const withGetter = { get g() { return [1, 2]; }, h: 3 };
	  const holes = [1, , 3]; holes[6] = 7;
	  const sym = Symbol("s");
	  const cases = [
	    [undefined], [null], [true], [false], [0], [-0], [1.5], [1e21], [1e-7], [-123456789012], [NaN], [Infinity],
	    ["plain"], ["quote \" backslash \\ slash /"], ["\b\f\n\r\t\u0001\u001f\u007f"], ["üñî €"], ["😀"],
	    ["lone \ud800 high"], ["lone \udc00 low"], ["  "],
	    [{}], [[]], [{ a: 1, b: [true, null, "x"], c: { d: {} } }], [{ 2: "b", 1: "a", z: 0, y: 1 }],
	    [{ u: undefined, f() {}, s: sym, [sym]: 1, n: null }], [[undefined, function () {}, sym]], [holes],
	    [{ toJSON(k) { return "key:" + k; } }], [[{ toJSON(k) { return k + "!"; } }]], [new Pt()], [withGetter],
	    [new Number(3)], [new String("s")], [new Boolean(false)], [Object(1.5)],
	    [{ a: [1, { b: 2 }], c: "x" }, null, 2], [{ a: [1, { b: 2 }], c: "x" }, null, "--"], [{ a: [], b: {} }, null, 4],
	    [[1, [2, [3]]], null, "\t"], [{ a: 1 }, null, 20], [{ a: 1 }, null, "abcdefghijkl"], [{ a: 1 }, null, new Number(2)],
	    [{ a: 1, b: 2, c: { a: 3, d: 4 } }, ["a", "c", 1, "a"]],
	    [{ a: 1, b: "x", c: [1, 2] }, function (k, v) { return typeof v === "number" ? v * 10 : v; }],
	    [{ a: 1, b: 2 }, function (k, v) { return k === "b" ? undefined : v; }],
	    [5, function (k, v) { return k === "" ? { wrapped: v, key: k } : v; }],
	  ];
	  const out = [];
	  for (const c of cases) {
	    let a, b;
	    try { a = JSON.stringify(c[0], c[1], c[2]); } catch (e) { a = "throws " + e.name; }
	    try { b = enc(c[0], c[1], c[2]); } catch (e) { b = "throws " + e.name; }
	    out.push([a === undefined ? "<undefined>" : a, b === undefined ? "<undefined>" : b]);
	  }
	  const cyc = {}; cyc.self = cyc;
	  for (const bad of [cyc, { big: 10n }]) {
	    let a, b;
	    try { JSON.stringify(bad); a = "no throw"; } catch (e) { a = "throws " + e.name; }
	    try { enc(bad); b = "no throw"; } catch (e) { b = "throws " + e.name; }
	    out.push([a, b]);
	  }
	  out;`)
	if err != nil {
		t.Fatal(err)
	}
	var pairs [][2]string
	if err := rt.ExportTo(res, &pairs); err != nil {
		t.Fatal(err)
	}
	for i, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("case %d: native %q, sandbox %q", i, p[0], p[1])
		}
	}
	if len(pairs) < 40 {
		t.Fatalf("only %d cases ran", len(pairs))
	}
}

// TestWithContextInterruptsCalls: a call bound to a context stops when the
// context ends, well before its own budget, and the game keeps working.
func TestWithContextInterruptsCalls(t *testing.T) {
	g := mustLoad(t, module(`view(s, seat) { if (seat === 1) { while (true) {} } return base.view(s, seat); },`),
		Options{CallBudget: 10 * time.Second, PoolSize: 1})
	st := mustSetup(t, g, 2, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := g.WithContext(ctx).View(st, 1)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("cancelled call took %v", d)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrModule) || errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want a module error wrapping context.DeadlineExceeded", err)
	}
	// Done context: fails at once without a call.
	if _, err := g.WithContext(ctx).View(st, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	// The unbound game and a live context still work.
	if _, err := g.View(st, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.WithContext(context.Background()).View(st, 0); err != nil {
		t.Fatal(err)
	}
	var _ games.Game = g.WithContext(ctx)
}

// TestOrdinaryBuiltinsStillWork: the guards must not change what ordinary
// calls return (arity-sensitive ones especially).
func TestOrdinaryBuiltinsStillWork(t *testing.T) {
	g := mustLoad(t, module(`view(s) {
	  const [a, , b] = [1, 2, 3];
	  const r = [
	    "a,b,c".split(",").length, "abc".split("").length, "a,b,c".split(",", 2).length, "a-b".replace("-", "+"),
	    "ab".replaceAll("b", "$&$&"), "aXbX".replace("X", (m) => m.toLowerCase()), "x".padStart(3, "-"), "ab".repeat(2), "a".concat("b", 1),
	    [3, 1, 2].sort().join(""), [1, [2, [3]]].flat(Infinity).length, [1, 2].flatMap((x) => [x, x]).length,
	    [1, 2, 3].reduce((p, c) => p + c), [1, 2, 3].reduceRight((p, c) => p + c, ""), [1, 2, 3].splice(1).length,
	    [1, 2, 1].lastIndexOf(1), [1, 2, 3].indexOf(3), [1, 2, 3].includes(2), [1, 2, 3].slice(1).length,
	    Math.max(...[1, 2]), Array.from({ length: 3 }, (_, i) => i).join(""), [..."abc"].length,
	    new Map([[1, 2]]).size, [1, 2].concat([3], 4).length, ["a", "b"].join(), Math.max.apply(null, [1, 5]),
	    Reflect.apply(Math.max, null, [1, 7]), [5, 6].entries().next().value.join(":"), a + b,
	    [1, 2, 3].findLast((x) => x < 3), [0, 0, 0].fill(7, 1).join(""), JSON.stringify({ a: [1, { b: "c" }] }),
	    String([1, [2, 3]]), [4, 5].map((x) => x * 2).filter((x) => x > 8).length, [1, 2].every((x) => x > 0),
	  ];
	  return { message: r.join("|") };
	},`), Options{})
	v, err := g.View(mustSetup(t, g, 2, 1), 0)
	if err != nil {
		t.Fatal(err)
	}
	const want = `3|3|2|a+b|abb|axbX|--x|abab|ab1|123|3|4|6|321|2|2|2|true|2|2|012|3|1|4|a,b|5|7|0:5|4|2|077|{"a":[1,{"b":"c"}]}|1,2,3|1|true`
	if v.Status != want {
		t.Fatalf("got  %s\nwant %s", v.Status, want)
	}
}
