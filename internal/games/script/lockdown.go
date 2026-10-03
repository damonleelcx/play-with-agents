package script

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/token"
)

// Module-level state
//
// A module's top level runs once per pooled runtime, and every call may land
// on any runtime. Anything a call leaves behind (a counter in a top-level
// let, a property added to a top-level object or to a builtin) would make
// later calls depend on which runtime served them, breaking replay, and
// could carry information from one table to another. So after the top level
// has run, lockdown (in the prelude):
//
//   - deep-freezes the intrinsics (Object.prototype, Array.prototype, ...)
//     and makes the global object non-extensible;
//   - deep-freezes every value the top level bound (top-level let, const,
//     class, var and function bindings and any global properties it added);
//     module-level Map/Set/WeakMap/WeakSet become read-only;
//   - snapshots every reassignable binding (let, class, var, function) and
//     checks after each call that it still holds the same value. A call that
//     reassigned one fails with a module error naming the variable, and the
//     runtime is discarded.
//
// Values that cannot be frozen (typed arrays, ArrayBuffer, DataView) are
// reported by Check as errors. State captured in a closure (an IIFE that
// returns the game object) is invisible to this analysis; Check's replay on
// fresh runtimes still detects it when it changes results.

// moduleStateMarker starts the message of a call that changed module-level
// state; the runtime that ran it is discarded.
const moduleStateMarker = "module-level variable"

// moduleInfo is what Load learns from the module's syntax tree.
type moduleInfo struct {
	lexNames []string // top-level let/const/class bindings, in source order
	lexKinds []string // "let", "const" or "class"
	getLex   *goja.Program
}

// analyze parses src (which already compiled) and finds the top-level
// lexical bindings. It rejects regular expression literals.
func analyze(name, src string) (*moduleInfo, error) {
	prog, err := goja.Parse(name, src, parser.WithDisableSourceMaps)
	if err != nil {
		return nil, fmt.Errorf("script: syntax error: %v", err)
	}
	if lit := findRegExp(prog); lit != nil {
		pos := file.Position{}
		if prog.File != nil {
			pos = prog.File.Position(int(lit.Idx) - prog.File.Base())
		}
		return nil, fmt.Errorf("script: regular expressions are not available in game modules (%s at line %d): "+
			"a regular expression can take exponential time. Use string methods instead: indexOf, includes, startsWith, endsWith, split or replaceAll with a string",
			truncate(lit.Literal, 40), pos.Line)
	}

	info := &moduleInfo{}
	for _, st := range prog.Body {
		switch d := st.(type) {
		case *ast.LexicalDeclaration:
			kind := "let"
			if d.Token == token.CONST {
				kind = "const"
			}
			for _, b := range d.List {
				for _, n := range bindingNames(b.Target) {
					info.lexNames = append(info.lexNames, n)
					info.lexKinds = append(info.lexKinds, kind)
				}
			}
		case *ast.ClassDeclaration:
			if d.Class != nil && d.Class.Name != nil {
				info.lexNames = append(info.lexNames, string(d.Class.Name.Name))
				info.lexKinds = append(info.lexKinds, "class")
			}
		}
	}
	// getLex returns the bindings' current values. It runs in the global
	// scope after the module, where the top-level lexical names resolve.
	getLexSrc := "(function () { return [" + strings.Join(info.lexNames, ", ") + "]; })"
	if info.getLex, err = goja.Compile("lockdown.js", getLexSrc, true); err != nil {
		return nil, fmt.Errorf("script: internal: lockdown: %w", err)
	}
	return info, nil
}

// bindingNames lists the identifiers a declaration target binds.
func bindingNames(t any) []string {
	switch t := t.(type) {
	case *ast.Identifier:
		return []string{string(t.Name)}
	case *ast.AssignExpression: // a default value inside a pattern
		return bindingNames(t.Left)
	case *ast.SpreadElement:
		return bindingNames(t.Expression)
	case *ast.ArrayPattern:
		var out []string
		for _, e := range t.Elements {
			if e != nil {
				out = append(out, bindingNames(e)...)
			}
		}
		if t.Rest != nil {
			out = append(out, bindingNames(t.Rest)...)
		}
		return out
	case *ast.ObjectPattern:
		var out []string
		for _, p := range t.Properties {
			switch p := p.(type) {
			case *ast.PropertyShort:
				out = append(out, string(p.Name.Name))
			case *ast.PropertyKeyed:
				out = append(out, bindingNames(p.Value)...)
			}
		}
		if t.Rest != nil {
			out = append(out, bindingNames(t.Rest)...)
		}
		return out
	}
	return nil
}

var (
	regExpType = reflect.TypeOf((*ast.RegExpLiteral)(nil))
	fileType   = reflect.TypeOf((*file.File)(nil))
)

// findRegExp returns the first regular expression literal in the tree.
func findRegExp(prog *ast.Program) *ast.RegExpLiteral {
	seen := map[uintptr]bool{}
	var found *ast.RegExpLiteral
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found != nil {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || v.Type() == fileType {
				return
			}
			if v.Type() == regExpType {
				found = v.Interface().(*ast.RegExpLiteral)
				return
			}
			if p := v.Pointer(); seen[p] {
				return
			} else {
				seen[p] = true
			}
			walk(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(prog))
	return found
}
