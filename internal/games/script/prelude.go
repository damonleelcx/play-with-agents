package script

// preludeSrc runs in every runtime before the module. It removes the ambient
// sources of nondeterminism, installs console, and caps the builtins that
// could otherwise burn unbounded time or memory in one native call.
//
// Why caps and not just the time budget: goja only checks for interrupts
// between bytecode instructions, so a single native builtin call cannot be
// stopped once started. One call such as JSON.stringify(new Array(6e7)) or a
// catastrophically backtracking regular expression would run for seconds and
// allocate gigabytes regardless of the budget. So:
//
//   - RegExp is unavailable. goja runs patterns that need backtracking
//     (lookaround, backreferences) and every exec from a non-zero lastIndex
//     on regexp2, which backtracks without a time limit, so no subset can be
//     guaranteed linear. Regex literals are rejected at Load (see
//     lockdown.go); every RegExp method (and so String.prototype.match,
//     matchAll, search, and replace/replaceAll/split with a regex) throws.
//   - Every builtin that iterates an array natively checks its length first
//     (holes cost time too: new Array(6e7).includes(1) took 6s), and every
//     builtin whose output can be much larger than its input (repeat,
//     padStart/End, replace/replaceAll, concat, join, flat, apply, spread,
//     typed array constructors) checks the size it would produce first. A
//     breach throws a RangeError the author can read.
//   - JSON.stringify is the Go encoder in json.go (bounded, interruptible,
//     byte-identical output).
//
// The prelude returns the lockdown function, which Go calls once the module's
// top level has run (see lockdown.go). It is never reachable from the module.
const preludeSrc = `(function (g, sink, encode) {
  "use strict";
  // No clock and no ambient randomness: every game must replay exactly.
  // ctx.random() is the only source of randomness.
  delete g.Date;
  delete g.Math.random;

  const STR = 1 << 20;      // characters one string builtin may produce
  const LEN = 1 << 18;      // elements one array builtin may iterate or produce

  const R = Reflect, apply = R.apply, construct = R.construct, ownKeys = R.ownKeys;
  const getOwn = Object.getOwnPropertyDescriptor, defineProperty = Object.defineProperty;
  const getProto = Object.getPrototypeOf, freeze = Object.freeze, is = Object.is;
  const isArray = Array.isArray, objectKeys = Object.keys, ToString = String;
  const tagOf = Object.prototype.toString;
  const SetC = Set, MapC = Map, RangeErrorC = RangeError, TypeErrorC = TypeError;
  const symIterator = Symbol.iterator, symReplace = Symbol.replace;
  const strIndexOf = String.prototype.indexOf;

  const limit = (what, n, max) => {
    if (n > max) throw new RangeErrorC(what + " " + n + " is over the sandbox limit of " + max);
  };
  const len = (o) => {
    if (o === null || o === undefined) return 0;
    const n = +o.length;
    return n > 0 ? n : 0;
  };
  const define = (obj, key, value) =>
    defineProperty(obj, key, { value, writable: true, enumerable: false, configurable: true });
  const nameOf = (key) => typeof key === "symbol" ? "[" + key.description + "]" : key;
  // guard replaces obj[key] with a wrapper that runs check(this, args)
  // first. check may replace entries of args (to wrap a callback).
  const guard = (obj, key, check) => {
    const orig = obj[key];
    if (typeof orig !== "function") return;
    const name = nameOf(key);
    const w = { [name](...args) { check(this, args); return apply(orig, this, args); } }[name];
    define(obj, key, w);
  };

  // ── RegExp ──────────────────────────────────────────────────────────────
  const noRegExp = function () {
    throw new TypeErrorC("RegExp is not available in game modules (a regular expression can take exponential time). " +
      "Use string methods: indexOf, includes, startsWith, endsWith, split or replaceAll with a string");
  };
  const RXP = g.RegExp.prototype;
  for (const k of ["exec", "test", "compile", Symbol.match, Symbol.matchAll, Symbol.replace, Symbol.search, Symbol.split]) {
    define(RXP, k, noRegExp);
  }
  delete g.RegExp;

  // ── arrays ──────────────────────────────────────────────────────────────
  const AP = Array.prototype;
  const thisLen = (name) => (self) => limit("Array.prototype." + name + ": length", len(self), LEN);
  // The hot methods (iteration and destructuring call [Symbol.iterator] on
  // every array) get lean wrappers with a fixed arity: for each of them
  // passing undefined is the same as omitting the argument.
  const NOARGS = freeze([]);
  const lean = (key, arity) => {
    const orig = AP[key], name = nameOf(key), what = "Array.prototype." + name + ": length";
    const fns = [
      { [name]() { const n = this === null || this === undefined ? 0 : this.length; if (n > LEN) limit(what, n, LEN); return apply(orig, this, NOARGS); } },
      { [name](a) { const n = this === null || this === undefined ? 0 : this.length; if (n > LEN) limit(what, n, LEN); return apply(orig, this, [a]); } },
      { [name](a, b) { const n = this === null || this === undefined ? 0 : this.length; if (n > LEN) limit(what, n, LEN); return apply(orig, this, [a, b]); } },
      { [name](a, b, c) { const n = this === null || this === undefined ? 0 : this.length; if (n > LEN) limit(what, n, LEN); return apply(orig, this, [a, b, c]); } },
    ];
    if (typeof orig === "function") define(AP, key, fns[arity][name]);
  };
  for (const k of ["keys", "values", "entries", symIterator, "reverse", "shift", "toReversed"]) lean(k, 0);
  for (const k of ["sort", "toSorted"]) lean(k, 1);
  for (const k of ["every", "filter", "find", "findIndex", "findLast", "findLastIndex", "forEach",
      "includes", "indexOf", "map", "some", "slice", "with"]) lean(k, 2);
  for (const k of ["fill", "copyWithin"]) lean(k, 3);
  // Arity matters for these (reduce without an initial value, splice
  // without a delete count, lastIndexOf without fromIndex).
  for (const k of ["lastIndexOf", "reduce", "reduceRight", "splice", "toSpliced", "toLocaleString", "unshift"]) {
    guard(AP, k, thisLen(nameOf(k)));
  }
  guard(AP, "concat", (self, a) => {
    let n = isArray(self) ? len(self) : 1;
    for (let i = 0; i < a.length; i++) n += isArray(a[i]) ? len(a[i]) : 1;
    limit("Array.prototype.concat: result length", n, LEN);
  });
  // flat(depth) can multiply: count the elements it would produce.
  const flatCount = (arr, depth, acc) => {
    const n = len(arr);
    limit("Array.prototype.flat: length", n, LEN);
    for (let i = 0; i < n; i++) {
      const x = arr[i];
      if (depth > 0 && isArray(x)) acc = depth === 1 ? acc + len(x) : flatCount(x, depth - 1, acc);
      else acc++;
      if (acc > LEN) limit("Array.prototype.flat: result length", acc, LEN);
    }
    return acc;
  };
  guard(AP, "flat", (self, a) => {
    const d = a[0] === undefined ? 1 : Math.min(+a[0] || 0, 64);
    flatCount(self, d, 0);
  });
  guard(AP, "flatMap", (self, a) => {
    limit("Array.prototype.flatMap: length", len(self), LEN);
    const fn = a[0];
    if (typeof fn !== "function") return;
    let total = 0;
    a[0] = function (...x) {
      const r = apply(fn, this, x);
      total += isArray(r) ? len(r) : 1;
      limit("Array.prototype.flatMap: result length", total, LEN);
      return r;
    };
  });
  // join: estimate the output before building it.
  guard(AP, "join", (self, a) => {
    const n = len(self);
    limit("Array.prototype.join: length", n, LEN);
    const sep = a[0] === undefined ? 1 : ToString(a[0]).length;
    let total = n > 0 ? (n - 1) * sep : 0;
    if (total > STR) limit("Array.prototype.join: result length", total, STR);
    for (let i = 0; i < n; i++) {
      const x = self[i];
      if (typeof x === "string") total += x.length;
      else if (typeof x === "number") total += 12;
      else if (x !== null && x !== undefined) total += ToString(x).length;
      if (total > STR) limit("Array.prototype.join: result length", total, STR);
    }
  });
  const arrayFrom = Array.from;
  define(Array, "from", function from(src, ...rest) {
    if (src !== null && src !== undefined) limit("Array.from: length", len(src), LEN);
    return apply(arrayFrom, this, [src, ...rest]);
  });

  // ── functions ───────────────────────────────────────────────────────────
  const FP = Function.prototype, fnApply = FP.apply;
  define(FP, "apply", function apply_(thisArg, argList) {
    if (argList !== null && argList !== undefined) limit("Function.prototype.apply: argument count", len(argList), LEN);
    return apply(fnApply, this, [thisArg, argList]);
  });
  define(R, "apply", function apply_(target, thisArg, argList) {
    limit("Reflect.apply: argument count", len(argList), LEN);
    return apply(target, thisArg, argList);
  });
  define(R, "construct", function construct_(target, argList, ...nt) {
    limit("Reflect.construct: argument count", len(argList), LEN);
    return apply(construct, undefined, [target, argList, ...nt]);
  });

  // ── strings ─────────────────────────────────────────────────────────────
  const SP = String.prototype;
  guard(SP, "repeat", (s, a) => limit("String.prototype.repeat: result length", ToString(s).length * (+a[0] || 0), STR));
  guard(SP, "padStart", (s, a) => limit("String.prototype.padStart: result length", +a[0] || 0, STR));
  guard(SP, "padEnd", (s, a) => limit("String.prototype.padEnd: result length", +a[0] || 0, STR));
  guard(SP, "concat", (s, a) => {
    let n = ToString(s).length;
    for (let i = 0; i < a.length; i++) n += ToString(a[i]).length;
    limit("String.prototype.concat: result length", n, STR);
  });
  // split: at most LEN pieces. The native split runs with a limit of LEN + 1,
  // which bounds its work, and a longer result is an error.
  const strSplit = SP.split;
  // occurrences counts p in str, stopping after k (natively, bounded by k).
  const occurrences = (str, p, k) => apply(strSplit, str, [p, k + 1]).length - 1;
  define(SP, "split", { split(sep, lim) {
    const str = ToString(this === null || this === undefined ? apply(strSplit, this, []) : this);
    limit("String.prototype.split: string length", str.length, STR);
    if (sep === undefined || (sep !== null && typeof sep === "object" && sep[Symbol.split] !== undefined)) {
      return apply(strSplit, this, [sep, lim]);
    }
    const want = lim === undefined ? 4294967295 : lim >>> 0;
    const out = apply(strSplit, str, [sep, Math.min(want, LEN + 1)]);
    if (out.length > LEN) limit("String.prototype.split: pieces", out.length, LEN);
    return out;
  } }.split);
  guard(SP, symIterator, (s) => limit("String iterator: string length", ToString(s).length, LEN));
  // replace / replaceAll with a string pattern: matches × the expanded
  // replacement ("$'" inserts the rest of the string, so it counts as the
  // whole string). A replacer function is wrapped to count what it returns.
  const replaceGuard = (all) => (s, a) => {
    const pat = a[0], rep = a[1];
    if (pat !== null && (typeof pat === "object" || typeof pat === "function") && pat[symReplace] !== undefined) return;
    const str = ToString(s), p = ToString(pat), what = "String.prototype." + (all ? "replaceAll" : "replace") + ": result length";
    if (typeof rep === "function") {
      let total = str.length;
      a[1] = function (...x) {
        const out = ToString(apply(rep, undefined, x));
        total += out.length;
        limit(what, total, STR);
        return out;
      };
      return;
    }
    const r = ToString(rep);
    let dollars = 0;
    for (let i = apply(strIndexOf, r, ["$"]); i >= 0; i = apply(strIndexOf, r, ["$", i + 1])) dollars++;
    const grow = r.length + dollars * str.length - p.length;
    if (grow <= 0) return;
    const most = all ? (p.length === 0 ? str.length + 1 : Math.floor(str.length / p.length)) : 1;
    if (str.length + most * grow <= STR) return;
    // The most matches that still fit, plus one: if there are that many, the
    // result is too long.
    const fits = Math.floor((STR - str.length) / grow);
    const n = occurrences(str, p, fits + 1);
    if (n > fits) limit(what, str.length + n * grow, STR);
  };
  guard(SP, "replace", replaceGuard(false));
  guard(SP, "replaceAll", replaceGuard(true));

  // ── JSON.stringify ──────────────────────────────────────────────────────
  // encode is the Go implementation (json.go): same output, but bounded and
  // interruptible.
  define(JSON, "stringify", encode);

  // ── unavailable globals ─────────────────────────────────────────────────
  // Typed arrays and buffers allocate natively (new Float64Array(1e8) took
  // 762 MB in one call) and cannot be frozen; Proxy runs module code inside
  // builtins; WeakRef and FinalizationRegistry observe the garbage collector,
  // which is nondeterministic. Board games need none of them.
  for (const name of ["ArrayBuffer", "SharedArrayBuffer", "DataView", "Atomics", "Int8Array", "Uint8Array",
      "Uint8ClampedArray", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array", "Float32Array",
      "Float64Array", "BigInt64Array", "BigUint64Array", "Proxy", "WeakRef", "FinalizationRegistry"]) {
    delete g[name];
  }

  const fmt = (a) => {
    if (typeof a === "string") return a;
    try { const j = JSON.stringify(a); return j === undefined ? String(a) : j; } catch (e) { return String(a); }
  };
  const out = (level) => (...a) => sink(level, a.map(fmt).join(" "));
  g.console = { log: out("log"), info: out("info"), warn: out("warn"), error: out("error"), debug: out("debug") };

  // ── lockdown ────────────────────────────────────────────────────────────
  const builtins = ownKeys(g);
  const builtinSet = new SetC(builtins);

  // tame turns a data property of a frozen prototype into an accessor whose
  // setter defines an own property on the receiver, so "obj.toString = f" or
  // "this.name = 'MyError'" keep working once the prototype is frozen.
  const tame = (proto, key) => {
    const d = getOwn(proto, key);
    if (!d || !("value" in d) || !d.configurable) return;
    const value = d.value;
    defineProperty(proto, key, {
      get() { return value; },
      set(v) {
        if (this === proto) throw new TypeErrorC("Cannot assign to read only property '" + ToString(key) + "'");
        defineProperty(this, key, { value: v, writable: true, enumerable: true, configurable: true });
      },
      enumerable: d.enumerable, configurable: false,
    });
  };

  // collectionKind tells a real Map, Set, WeakMap or WeakSet (not merely one
  // with that toStringTag, or the prototype itself) from other objects.
  const kinds = [["Map", MapC.prototype.has], ["Set", SetC.prototype.has], ["WeakMap", WeakMap.prototype.has], ["WeakSet", WeakSet.prototype.has]];
  const collectionKind = (o) => {
    const tag = apply(tagOf, o, []);
    if (tag !== "[object Map]" && tag !== "[object Set]" && tag !== "[object WeakMap]" && tag !== "[object WeakSet]") return "";
    for (const [kind, has] of kinds) {
      try { apply(has, o, [undefined]); return kind; } catch (e) { /* not this kind */ }
    }
    return "";
  };

  const readOnly = (kind, path) => function () {
    throw new TypeErrorC("module-level " + kind + " " + path + " is read-only: game functions must not keep data between calls. Keep everything in the state");
  };

  // lockdown freezes the intrinsics, the global object and everything the
  // module's top level created, so no call can leave data behind for the
  // next call on this runtime. lexNames/lexKinds/lexValues describe the
  // module's top-level let/const/class bindings (Go finds them in the AST);
  // getLex returns their current values. It returns:
  //   verify(): the name of a top-level binding that no longer holds the
  //             value it had after the top level ran, or "" (null when the
  //             module has no reassignable bindings);
  //   mutable:  module-level values that cannot be frozen.
  return function lockdown(lexNames, lexKinds, getLex) {
    const mutable = [];
    const seen = new SetC([g]);
    const deepFreeze = (o, path, module) => {
      if (o === null || (typeof o !== "object" && typeof o !== "function") || seen.has(o)) return;
      seen.add(o);
      const kind = module ? collectionKind(o) : "";
      if (kind !== "") {
        for (const m of ["set", "add", "delete", "clear"]) define(o, m, readOnly(kind, path));
        if (kind === "Map" || kind === "Set") {
          for (const e of o) {
            if (isArray(e)) { deepFreeze(e[0], path + " key", module); deepFreeze(e[1], path + " value", module); }
            else deepFreeze(e, path + " entry", module);
          }
        }
      }
      try { freeze(o); } catch (e) { if (module) mutable.push(path); return; }
      for (const k of ownKeys(o)) {
        const d = getOwn(o, k);
        if (!d) continue;
        const p = path + "." + ToString(nameOf(k));
        if ("value" in d) deepFreeze(d.value, p, module);
        else { deepFreeze(d.get, p, module); deepFreeze(d.set, p, module); }
      }
    };

    // Intrinsics a game module is likely to touch are frozen. Freezing one
    // makes goja materialise every method it has (freezing all of them cost
    // about 1 ms and 1 MB per runtime), so the rarely used ones (Error types,
    // Symbol, BigInt, Promise, WeakMap, iterator prototypes) are left as they
    // are: mutating those inside a call is not something game code does by
    // accident. The global bindings themselves are all made read-only.
    for (const k of ["toString", "valueOf", "constructor", "toLocaleString"]) tame(Object.prototype, k);
    for (const k of ["toString", "constructor"]) tame(FP, k);
    for (const o of [Object, Object.prototype, Array, AP, String, SP, Number, Number.prototype, Boolean.prototype,
        FP, g.Math, JSON, R, MapC, MapC.prototype, SetC, SetC.prototype, g.console]) {
      seen.add(o);
      freeze(o);
    }
    for (const k of builtins) {
      const d = getOwn(g, k);
      if (d && "value" in d && (d.configurable || d.writable)) defineProperty(g, k, { writable: false, configurable: false });
    }

    // The module's own bindings.
    const lexValues = getLex();
    for (let i = 0; i < lexNames.length; i++) deepFreeze(lexValues[i], lexNames[i], true);
    const globals = ownKeys(g).filter((k) => !builtinSet.has(k));
    const globalValues = [], watchGlobals = [];
    for (const k of globals) {
      const d = getOwn(g, k);
      const v = d && "value" in d ? d.value : undefined;
      if (d && "value" in d) {
        // Function declarations become read-only (reassigning one is never
        // intended, and checking them would cost every call); var data is
        // watched, so a call that changes it fails loudly.
        if (typeof v === "function") defineProperty(g, k, { writable: false });
        else watchGlobals.push(globalValues.length);
      }
      globalValues.push(v);
      deepFreeze(v, ToString(nameOf(k)), true);
      if (d && !("value" in d)) { deepFreeze(d.get, ToString(nameOf(k)), true); deepFreeze(d.set, ToString(nameOf(k)), true); }
    }
    Object.preventExtensions(g);

    const watch = [];
    for (let i = 0; i < lexNames.length; i++) if (lexKinds[i] !== "const") watch.push(i);
    let verify = null;
    if (watch.length > 0 || watchGlobals.length > 0) {
      verify = function () {
        if (watch.length > 0) {
          const cur = getLex();
          for (const i of watch) if (!is(cur[i], lexValues[i])) return lexNames[i];
        }
        for (const i of watchGlobals) if (!is(g[globals[i]], globalValues[i])) return ToString(nameOf(globals[i]));
        return "";
      };
    }
    return { verify, mutable };
  };
})`
