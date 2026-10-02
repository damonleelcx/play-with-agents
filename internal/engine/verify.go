package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/damonleelcx/play-with-agents/internal/tools"
)

// Verifier is a deterministic check the worker runs when the model says a
// task is done. It reads the database (the tool_calls ledger, the rows a tool
// wrote), never the model's account of what it did. It returns the problems
// it found, each phrased as an instruction the model can act on; none means
// the task is done.
type Verifier struct {
	Name string
	// Guard marks a safety property rather than a quality check: a tailored
	// plan that drops a guard from the playbook is replaced by the playbook,
	// and a replan may not skip or cancel a task that carries one. The
	// studio's "playtest passed" check is a guard; "rules saved" is not.
	Guard bool
	// Final marks a verdict rather than a mistake: when it reports a
	// problem the task fails at once, without asking the model to correct
	// it (a critic asked to "fix" its own rejection would just flip it).
	// The replanner decides what follows.
	Final bool
	Check func(ctx context.Context, s *Store, t *Task) []string
}

var (
	verifiersMu sync.RWMutex
	verifiers   = map[string]*Verifier{}
)

// RegisterVerifier adds a named check tasks can declare in TaskSpec.Verify.
// Duplicate names are a programming error.
func RegisterVerifier(v Verifier) {
	verifiersMu.Lock()
	defer verifiersMu.Unlock()
	if v.Name == "" || v.Check == nil || strings.HasPrefix(v.Name, calledPrefix) {
		panic("invalid verifier " + v.Name)
	}
	if _, dup := verifiers[v.Name]; dup {
		panic("verifier registered twice: " + v.Name)
	}
	verifiers[v.Name] = &v
}

// calledPrefix is the one built-in verifier family: "called:<tool>" passes
// when this task has a succeeded call of that tool in the ledger. It covers
// the common "you must actually save it" check without a bespoke verifier.
const calledPrefix = "called:"

// lookupVerifier resolves a name to its verifier, including the built-in
// "called:<tool>" family (valid only for a registered tool).
func lookupVerifier(name string) (*Verifier, bool) {
	if tool, ok := strings.CutPrefix(name, calledPrefix); ok {
		if _, known := tools.Get(tool); !known {
			return nil, false
		}
		return &Verifier{Name: name, Check: func(ctx context.Context, s *Store, t *Task) []string {
			var n int
			_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM tool_calls WHERE task_id=$1 AND tool=$2 AND status='succeeded'`, t.ID, tool).Scan(&n)
			if n == 0 {
				return []string{fmt.Sprintf("%s was never called successfully in this task — call it now with this task's result", tool)}
			}
			return nil
		}}, true
	}
	verifiersMu.RLock()
	defer verifiersMu.RUnlock()
	v, ok := verifiers[name]
	return v, ok
}

// isGuard reports whether a verifier name is a registered guard.
func isGuard(name string) bool {
	v, ok := lookupVerifier(name)
	return ok && v.Guard
}

// guardNames lists the registered guards, for prompts.
func guardNames() []string {
	verifiersMu.RLock()
	defer verifiersMu.RUnlock()
	var out []string
	for n, v := range verifiers {
		if v.Guard {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
