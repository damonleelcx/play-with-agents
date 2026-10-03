package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestEveryExternalToolIsIdempotentAndDescribed(t *testing.T) {
	for _, n := range Names() {
		tl, _ := Get(n)
		if tl.Effect == External && tl.IdemKey == nil {
			t.Errorf("%s has external effects and no idempotency key", n)
		}
		if tl.Description == "" {
			t.Errorf("%s has no description", n)
		}
		switch tl.Gate {
		case G0, G1, G3:
		default:
			t.Errorf("%s has unknown gate %q", n, tl.Gate)
		}
	}
}

func TestRegisterRejectsDuplicatesAndUnkeyedExternalTools(t *testing.T) {
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: no panic", name)
			}
		}()
		f()
	}
	obj := `{"type":"object"}`
	mustPanic("duplicate", func() { Register(&Tool{Name: "notify_user", Input: obj, Output: obj, Effect: Write}) })
	mustPanic("external without key", func() { Register(&Tool{Name: "zz_ext", Input: obj, Output: obj, Effect: External}) })
	mustPanic("bad schema", func() { Register(&Tool{Name: "zz_bad", Input: `{`, Output: obj}) })
}

// Gates are enforced before a tool body runs, so these need no database: a
// G1 call without approval must stop at NeedsApproval, a G3 call (and any
// unknown gate) must never reach Run.
func TestGatesStopBeforeRun(t *testing.T) {
	ran := false
	obj := `{"type":"object"}`
	run := func(context.Context, *Env, map[string]any) (map[string]any, error) { ran = true; return nil, nil }
	Register(&Tool{Name: "zz_g1", Description: "x", Input: obj, Output: obj, Effect: Write, Gate: G1, Notify: "build_ready", Run: run,
		Preview: func(map[string]any) string { return "preview" }})
	Register(&Tool{Name: "zz_unknown_gate", Description: "x", Input: obj, Output: obj, Effect: Write, Gate: "G2", Run: run})

	_, err := Invoke(context.Background(), &Env{}, "zz_g1", json.RawMessage(`{}`), false)
	var need *NeedsApproval
	if !errors.As(err, &need) || need.Gate != G1 || need.Notify != "build_ready" || need.Preview != "preview" {
		t.Fatalf("G1 without approval: %v", err)
	}
	if ran {
		t.Fatal("a G1 tool ran before approval")
	}
	// A gate this code does not know fails closed. record() needs a pool, so
	// only the refusal path before it is exercised: EffectiveGate's value.
	tl, _ := Get("zz_unknown_gate")
	if g := tl.EffectiveGate(nil); g == G0 || g == G1 {
		t.Fatalf("unknown gate treated as %s", g)
	}
	if _, err := Invoke(context.Background(), &Env{}, "nope", nil, false); err == nil {
		t.Fatal("unknown tool accepted")
	}
}

func TestInvalidArgumentsAreRejectedBeforeTheGate(t *testing.T) {
	_, err := Invoke(context.Background(), &Env{}, "memory_save", json.RawMessage(`{"fact":"x","extra":1}`), false)
	var inv *InvalidInput
	if !errors.As(err, &inv) {
		t.Fatalf("got %v", err)
	}
}
