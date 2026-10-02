package script

import (
	"math"
	"math/big"
	"reflect"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// jsonEncoder is JSON.stringify for the sandbox, written in Go so that it can
// enforce an output budget while it writes and poll the call's deadline.
// goja's own JSON.stringify is one native call that cannot be interrupted:
// JSON.stringify(new Array(6e7)) ran for 6 s and allocated 2.7 GB under a
// 250 ms budget, and a value that shares a subtree (o = {a: o, b: o}, 40
// times) prints 2^40 copies. A JavaScript pre-walk would have bounded that
// but costs about 20× the stringify itself, and getters or toJSON could
// return something else the second time.
//
// The output is byte-for-byte what JSON.stringify produces (toJSON, replacer
// functions and property lists, gap, boxed primitives, lone surrogates as
// \uXXXX, cycles and BigInt as TypeError); script_test.go compares the two
// on a corpus of values. It also backs the runtime's own serialisation of
// every value a module returns, so states persist exactly as before.
type jsonEncoder struct {
	v        *vm
	buf      []byte
	max      int
	stack    []*goja.Object // objects being serialised, for cycle detection
	replacer goja.Callable
	propList []string // nil: every enumerable own key
	gap      string
	indent   string
	steps    int
}

const (
	jsonMaxBytes = 4 << 20 // output of one JSON.stringify (states are at most 256 KiB)
	jsonMaxArray = 1 << 18 // elements of one array, like the other array builtins
	jsonMaxDepth = 1000    // nesting (a replacer or toJSON can nest forever)
)

var (
	typeBool   = reflect.TypeOf(true)
	typeInt64  = reflect.TypeOf(int64(0))
	typeFloat  = reflect.TypeOf(float64(0))
	typeBigInt = reflect.TypeOf((*big.Int)(nil))
)

// stringify implements JSON.stringify(value, replacer, space). It panics
// with a JavaScript error, as a builtin would.
func (v *vm) stringify(call goja.FunctionCall) goja.Value {
	e := &jsonEncoder{v: v, max: jsonMaxBytes}
	if r, ok := call.Argument(1).(*goja.Object); ok {
		if fn, ok := goja.AssertFunction(r); ok {
			e.replacer = fn
		} else if r.ClassName() == "Array" {
			e.propList = e.propertyList(r)
		}
	}
	switch sp := e.primitive(call.Argument(2)).(type) {
	case goja.String:
		s := sp.String()
		if sp.Length() > 10 {
			s = sp.Substring(0, 10).String()
		}
		e.gap = s
	default:
		if t := sp.ExportType(); t == typeInt64 || t == typeFloat {
			n := min(10, max(0, int(sp.ToInteger())))
			for range n {
				e.gap += " "
			}
		}
	}
	holder := v.rt.NewObject()
	value := call.Argument(0)
	if e.replacer != nil {
		_ = holder.Set("", value)
	}
	if !e.str("", holder, value) {
		return goja.Undefined()
	}
	return v.rt.ToValue(string(e.buf))
}

// propertyList builds the allow-list from a replacer array.
func (e *jsonEncoder) propertyList(arr *goja.Object) []string {
	n := arr.Get("length").ToInteger()
	if n > jsonMaxArray {
		e.tooLong("replacer length", n)
	}
	seen := map[string]bool{}
	list := []string{}
	for i := int64(0); i < n; i++ {
		x := arr.Get(strconv.FormatInt(i, 10))
		var item string
		switch {
		case isString(x):
			item = x.String()
		case isNumber(x):
			item = x.String()
		default:
			o, ok := x.(*goja.Object)
			if !ok || (o.ClassName() != "String" && o.ClassName() != "Number") {
				continue
			}
			item = o.String()
		}
		if !seen[item] {
			seen[item] = true
			list = append(list, item)
		}
	}
	return list
}

// primitive unwraps Number and String objects, as the spec does for space.
func (e *jsonEncoder) primitive(x goja.Value) goja.Value {
	if o, ok := x.(*goja.Object); ok {
		switch o.ClassName() {
		case "Number":
			return o.ToNumber()
		case "String":
			return o.ToString()
		}
	}
	return x
}

func isString(x goja.Value) bool { _, ok := x.(goja.String); return ok }

func isNumber(x goja.Value) bool {
	if x == nil {
		return false
	}
	t := x.ExportType()
	return t == typeInt64 || t == typeFloat
}

func (e *jsonEncoder) call(fn goja.Callable, this goja.Value, args ...goja.Value) goja.Value {
	r, err := fn(this, args...)
	if err != nil {
		panic(err) // a JavaScript exception, or an interrupt: let it propagate
	}
	return r
}

// tick polls the call's deadline every few hundred values, so a large
// output is interrupted like JavaScript code would be.
func (e *jsonEncoder) tick() {
	if e.steps++; e.steps&255 == 0 && e.v.expired.Load() {
		panic(errInterruptedNative)
	}
}

func (e *jsonEncoder) tooLong(what string, n int64) {
	panic(e.v.rangeError("JSON.stringify: " + what + " " + strconv.FormatInt(n, 10) + " is over the sandbox limit"))
}

func (e *jsonEncoder) grow() {
	if len(e.buf) > e.max {
		e.tooLong("output length", int64(len(e.buf)))
	}
}

// str is SerializeJSONProperty: it writes holder[key] and reports whether
// anything was written (false for undefined, functions and symbols).
func (e *jsonEncoder) str(key string, holder *goja.Object, value goja.Value) bool {
	e.tick()
	if value == nil {
		value = goja.Undefined()
	}
	if o, ok := value.(*goja.Object); ok {
		if fn, ok := goja.AssertFunction(o.Get("toJSON")); ok {
			value = e.call(fn, o, e.v.rt.ToValue(key))
		}
	}
	if e.replacer != nil {
		value = e.call(e.replacer, holder, e.v.rt.ToValue(key), value)
	}
	if o, ok := value.(*goja.Object); ok {
		switch o.ClassName() {
		case "Number":
			value = o.ToNumber()
		case "String":
			value = o.ToString()
		case "Boolean":
			if b, ok := o.Export().(bool); ok {
				value = e.v.rt.ToValue(b)
			}
		case "BigInt":
			panic(e.v.rt.NewTypeError("Do not know how to serialize a BigInt"))
		}
	}
	switch x := value.(type) {
	case nil:
		return false
	case goja.String:
		e.quote(x)
		return true
	case *goja.Symbol:
		return false
	case *goja.Object:
		if x.ClassName() == "Function" {
			return false
		}
		for _, s := range e.stack {
			if s == x {
				panic(e.v.rt.NewTypeError("Converting circular structure to JSON"))
			}
		}
		if len(e.stack) >= jsonMaxDepth {
			panic(e.v.rangeError("JSON.stringify: nesting deeper than " + strconv.Itoa(jsonMaxDepth) + " levels"))
		}
		e.stack = append(e.stack, x)
		if x.ClassName() == "Array" {
			e.array(x)
		} else {
			e.object(x)
		}
		e.stack = e.stack[:len(e.stack)-1]
		return true
	}
	if goja.IsUndefined(value) {
		return false
	}
	if goja.IsNull(value) {
		e.buf = append(e.buf, "null"...)
		return true
	}
	switch value.ExportType() {
	case typeBool:
		if value.ToBoolean() {
			e.buf = append(e.buf, "true"...)
		} else {
			e.buf = append(e.buf, "false"...)
		}
	case typeInt64:
		e.buf = strconv.AppendInt(e.buf, value.ToInteger(), 10)
	case typeFloat:
		if f := value.ToFloat(); math.IsNaN(f) || math.IsInf(f, 0) {
			e.buf = append(e.buf, "null"...)
		} else {
			e.buf = append(e.buf, value.String()...)
		}
	case typeBigInt:
		panic(e.v.rt.NewTypeError("Do not know how to serialize a BigInt"))
	default:
		return false
	}
	e.grow()
	return true
}

func (e *jsonEncoder) array(a *goja.Object) {
	n := a.Get("length").ToInteger()
	if n > jsonMaxArray {
		e.tooLong("array length", n)
	}
	if n <= 0 {
		e.buf = append(e.buf, "[]"...)
		return
	}
	stepback := e.indent
	e.indent += e.gap
	e.buf = append(e.buf, '[')
	for i := int64(0); i < n; i++ {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.newline()
		k := strconv.FormatInt(i, 10)
		if !e.str(k, a, a.Get(k)) {
			e.buf = append(e.buf, "null"...)
		}
		e.grow()
	}
	e.indent = stepback
	e.newline()
	e.buf = append(e.buf, ']')
}

func (e *jsonEncoder) object(o *goja.Object) {
	keys := e.propList
	if keys == nil {
		keys = o.Keys()
	}
	stepback := e.indent
	e.indent += e.gap
	e.buf = append(e.buf, '{')
	mark := len(e.buf)
	empty := true
	for _, k := range keys {
		off := len(e.buf)
		if !empty {
			e.buf = append(e.buf, ',')
		}
		e.newline()
		e.quoteGo(k)
		e.buf = append(e.buf, ':')
		if e.gap != "" {
			e.buf = append(e.buf, ' ')
		}
		if e.str(k, o, o.Get(k)) {
			empty = false
		} else {
			e.buf = e.buf[:off]
		}
	}
	e.indent = stepback
	if empty {
		e.buf = e.buf[:mark]
	} else {
		e.newline()
	}
	e.buf = append(e.buf, '}')
	e.grow()
}

func (e *jsonEncoder) newline() {
	if e.gap != "" {
		e.buf = append(e.buf, '\n')
		e.buf = append(e.buf, e.indent...)
	}
}

const hexDigits = "0123456789abcdef"

// quote writes a JavaScript string. ASCII strings (the common case) are
// read as Go strings; others are read as UTF-16 code units so that lone
// surrogates are escaped exactly as JSON.stringify escapes them.
func (e *jsonEncoder) quote(s goja.String) {
	if g := s.String(); isASCII(g) {
		e.quoteGo(g)
		return
	}
	e.buf = append(e.buf, '"')
	n := s.Length()
	for i := 0; i < n; i++ {
		c := rune(s.CharAt(i))
		if utf16.IsSurrogate(c) {
			if c < 0xDC00 && i+1 < n {
				if d := rune(s.CharAt(i + 1)); d >= 0xDC00 && d <= 0xDFFF {
					e.buf = utf8.AppendRune(e.buf, utf16.DecodeRune(c, d))
					i++
					continue
				}
			}
			e.buf = append(e.buf, '\\', 'u', hexDigits[c>>12], hexDigits[(c>>8)&0xF], hexDigits[(c>>4)&0xF], hexDigits[c&0xF])
			continue
		}
		e.char(c)
	}
	e.buf = append(e.buf, '"')
	e.grow()
}

func (e *jsonEncoder) quoteGo(s string) {
	e.buf = append(e.buf, '"')
	for _, c := range s {
		e.char(c)
	}
	e.buf = append(e.buf, '"')
}

func (e *jsonEncoder) char(c rune) {
	switch c {
	case '"', '\\':
		e.buf = append(e.buf, '\\', byte(c))
	case '\b':
		e.buf = append(e.buf, '\\', 'b')
	case '\t':
		e.buf = append(e.buf, '\\', 't')
	case '\n':
		e.buf = append(e.buf, '\\', 'n')
	case '\f':
		e.buf = append(e.buf, '\\', 'f')
	case '\r':
		e.buf = append(e.buf, '\\', 'r')
	default:
		if c < 0x20 {
			e.buf = append(e.buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
		} else {
			e.buf = utf8.AppendRune(e.buf, c)
		}
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// rangeError builds a JavaScript RangeError to panic with from a builtin.
func (v *vm) rangeError(msg string) *goja.Object {
	if c, ok := goja.AssertConstructor(v.rt.Get("RangeError")); ok {
		if o, err := c(nil, v.rt.ToValue(msg)); err == nil {
			return o
		}
	}
	return v.rt.NewTypeError(msg)
}
