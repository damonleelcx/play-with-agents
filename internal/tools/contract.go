// Package tools holds every action an agent can take, each behind a strict
// contract: typed input, typed output, a gate, a timeout, a retry policy, an
// idempotency rule for side effects, and a verifier that checks the effect
// actually happened. See docs/01-intents-tools-skills.md §2.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/damonleelcx/play-with-agents/internal/mail"
)

type Effect string

const (
	Read     Effect = "R" // no side effects
	Write    Effect = "W" // writes our own database only
	External Effect = "X" // changes something outside this system
)

type Gate string

// Gate classes. There is deliberately nothing between "the owner says yes"
// and "never": an action riskier than a player's own approval covers is not
// something an agent on a games platform should be doing at all.
const (
	G0 Gate = "G0" // automatic: runs without asking
	G1 Gate = "G1" // the goal's owner approves this exact call (e.g. publish a game)
	G3 Gate = "G3" // never automated: refused, reported to the model, never queued
)

// Env is what a tool may touch. Tools get no other handle on the world.
type Env struct {
	Pool           *pgxpool.Pool
	Mailer         mail.Mailer
	UserID         string
	UserEmail      string
	UserName       string
	GoalID         string
	TaskID         string
	ConversationID string
	Lang           string
	// Scope distinguishes side effects that happen outside any task (a tool
	// invoked from a chat turn): it is the message id there.
	Scope string
}

type Tool struct {
	Name        string
	Description string
	Input       string // JSON Schema
	Output      string // JSON Schema
	Effect      Effect
	Gate        Gate
	// GateFor refines the gate from the arguments, e.g. a call that would
	// reach outside the platform is G3 whatever the static gate says.
	GateFor func(args map[string]any) Gate
	// Notify is the outbox notification kind the owner receives when a G1
	// call parks for approval. Empty means "approval_requested"; the studio's
	// publish step uses "build_ready".
	Notify     string
	Timeout    time.Duration
	MaxRetries int
	// IdemKey must be set for External tools. It is derived from the task and
	// the arguments, so a retry of the same step collides and a different step
	// does not.
	IdemKey func(env *Env, args map[string]any) string
	Run     func(ctx context.Context, env *Env, args map[string]any) (map[string]any, error)
	Verify  func(ctx context.Context, env *Env, args, out map[string]any) error
	Preview func(args map[string]any) string

	inSchema, outSchema *jsonschema.Schema
}

var (
	registry = map[string]*Tool{}
	regMu    sync.RWMutex
)

// Register adds a tool to the registry. It panics on a malformed schema, an
// external tool without an idempotency key, or a duplicate name: all three
// are programming errors that must stop the process at start-up, not surface
// at the first call.
func Register(t *Tool) {
	c := jsonschema.NewCompiler()
	must := func(name, src string) *jsonschema.Schema {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(src))
		if err != nil {
			panic(fmt.Sprintf("tool %s: %s schema: %v", t.Name, name, err))
		}
		url := "mem://" + t.Name + "/" + name
		if err := c.AddResource(url, doc); err != nil {
			panic(err)
		}
		s, err := c.Compile(url)
		if err != nil {
			panic(fmt.Sprintf("tool %s: %s schema: %v", t.Name, name, err))
		}
		return s
	}
	t.inSchema = must("in", t.Input)
	t.outSchema = must("out", t.Output)
	if t.Effect == External && t.IdemKey == nil {
		panic("external tool without an idempotency key: " + t.Name)
	}
	if t.Timeout == 0 {
		t.Timeout = 30 * time.Second
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[t.Name]; dup {
		panic("tool registered twice: " + t.Name)
	}
	registry[t.Name] = t
}

func Get(name string) (*Tool, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	t, ok := registry[name]
	return t, ok
}

func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}

// Schema returns the input schema, for the model's tool definition.
func (t *Tool) Schema() json.RawMessage { return json.RawMessage(t.Input) }

// EffectiveGate is the gate for these exact arguments.
func (t *Tool) EffectiveGate(args map[string]any) Gate {
	if t.GateFor != nil {
		return t.GateFor(args)
	}
	return t.Gate
}

// ── Errors the worker branches on ──────────────────────────────────────────

// NeedsApproval stops the step; the worker persists it as an approval row and
// parks the task until the owner decides.
type NeedsApproval struct {
	Gate    Gate
	Preview string
	Notify  string // outbox kind for the owner; see Tool.Notify
}

func (e *NeedsApproval) Error() string { return "approval required (" + string(e.Gate) + ")" }

// ErrBlocked is a G3 action. It is reported to the model as a refusal so it
// can explain what the player can do instead; it is never queued for approval.
var ErrBlocked = errors.New("this action is never automated on this platform")

// ErrAmbiguous means a previous attempt of this exact side effect started and
// never recorded an outcome. Re-running could duplicate it, so the task stops
// and a person looks.
var ErrAmbiguous = errors.New("a previous attempt of this side effect has no recorded outcome; not retrying automatically")

type InvalidInput struct{ Err error }

func (e *InvalidInput) Error() string { return "invalid arguments: " + e.Err.Error() }

// Result is what the worker records and hands back to the model.
type Result struct {
	Output    map[string]any
	Duplicate bool // the side effect had already happened; this is its recorded output
	Latency   time.Duration
}

// Invoke runs a tool through its full contract. approved is true only when the
// worker is replaying a call the owner approved — with the approved arguments.
func Invoke(ctx context.Context, env *Env, name string, rawArgs json.RawMessage, approved bool) (*Result, error) {
	t, ok := Get(name)
	if !ok {
		return nil, &InvalidInput{fmt.Errorf("unknown tool %q", name)}
	}
	var args map[string]any
	if len(rawArgs) == 0 {
		rawArgs = []byte("{}")
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, &InvalidInput{err}
	}
	if args == nil {
		args = map[string]any{}
	}
	var inst any
	_ = json.Unmarshal(rawArgs, &inst)
	if err := t.inSchema.Validate(inst); err != nil {
		return nil, &InvalidInput{err}
	}

	switch gate := t.EffectiveGate(args); gate {
	case G0:
	case G1:
		if !approved {
			preview := ""
			if t.Preview != nil {
				preview = t.Preview(args)
			}
			kind := t.Notify
			if kind == "" {
				kind = "approval_requested"
			}
			return nil, &NeedsApproval{Gate: gate, Preview: preview, Notify: kind}
		}
	default:
		// G3, and any gate this code does not know: fail closed.
		record(ctx, env, t, args, nil, "failed", "", ErrBlocked, 0)
		return nil, ErrBlocked
	}

	key := ""
	if t.Effect == External {
		key = t.IdemKey(env, args)
		prev, status, err := lookupKey(ctx, env.Pool, key)
		if err != nil {
			return nil, err
		}
		switch status {
		case "succeeded":
			return &Result{Output: prev, Duplicate: true}, nil
		case "started":
			return nil, ErrAmbiguous
		}
		// failed / verify_failed / none: a new attempt. The unique index on the
		// key means the old row must be moved out of the way first.
		if status != "" {
			if _, err := env.Pool.Exec(ctx, `UPDATE tool_calls SET idempotency_key = idempotency_key || ':superseded:' || id
				WHERE idempotency_key = $1`, key); err != nil {
				return nil, err
			}
		}
	}

	callID, err := begin(ctx, env, t, args, key)
	if err != nil {
		if key != "" && isUnique(err) {
			// Another worker started the same side effect between our lookup
			// and our insert. It owns it.
			return nil, ErrAmbiguous
		}
		return nil, err
	}

	start := time.Now()
	var out map[string]any
	var runErr error
	attempts := 1 + t.MaxRetries
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				runErr = ctx.Err()
			case <-time.After(time.Duration(1<<i) * 500 * time.Millisecond):
			}
			if ctx.Err() != nil {
				break
			}
		}
		cctx, cancel := context.WithTimeout(ctx, t.Timeout)
		out, runErr = t.Run(cctx, env, args)
		cancel()
		if runErr == nil || !retryable(runErr) {
			break
		}
	}
	lat := time.Since(start)
	if runErr != nil {
		finish(ctx, env.Pool, callID, nil, "failed", runErr, lat)
		return nil, runErr
	}
	if out == nil {
		out = map[string]any{}
	}
	outJSON, _ := json.Marshal(out)
	var outInst any
	_ = json.Unmarshal(outJSON, &outInst)
	if err := t.outSchema.Validate(outInst); err != nil {
		err = fmt.Errorf("tool %s returned output that breaks its own contract: %w", t.Name, err)
		finish(ctx, env.Pool, callID, out, "failed", err, lat)
		return nil, err
	}
	if t.Verify != nil {
		if err := t.Verify(ctx, env, args, out); err != nil {
			err = fmt.Errorf("verification failed: %w", err)
			finish(ctx, env.Pool, callID, out, "verify_failed", err, lat)
			return nil, err
		}
	}
	finish(ctx, env.Pool, callID, out, "succeeded", nil, lat)
	return &Result{Output: out, Latency: lat}, nil
}

// Transient marks an error a retry may fix (timeouts, 5xx, 429).
type Transient struct{ Err error }

func (e *Transient) Error() string { return e.Err.Error() }
func (e *Transient) Unwrap() error { return e.Err }

func retryable(err error) bool {
	var t *Transient
	return errors.As(err, &t) || errors.Is(err, context.DeadlineExceeded)
}

func lookupKey(ctx context.Context, pool *pgxpool.Pool, key string) (map[string]any, string, error) {
	var out []byte
	var status string
	err := pool.QueryRow(ctx, `SELECT coalesce(output::text,'{}'), status FROM tool_calls WHERE idempotency_key=$1`, key).Scan(&out, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m, status, nil
}

func begin(ctx context.Context, env *Env, t *Tool, args map[string]any, key string) (int64, error) {
	in, _ := json.Marshal(args)
	var id int64
	var k any
	if key != "" {
		k = key
	}
	err := env.Pool.QueryRow(ctx, `INSERT INTO tool_calls (goal_id, task_id, tool, input, status, idempotency_key)
		VALUES (nullif($1,'')::uuid, nullif($2,'')::uuid, $3, $4, 'started', $5) RETURNING id`,
		env.GoalID, env.TaskID, t.Name, in, k).Scan(&id)
	return id, err
}

func finish(ctx context.Context, pool *pgxpool.Pool, id int64, out map[string]any, status string, err error, lat time.Duration) {
	var o any
	if out != nil {
		b, _ := json.Marshal(out)
		o = b
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	// Recorded with a fresh context: a cancelled task must still record what
	// its side effect did, or the next attempt cannot tell.
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, _ = pool.Exec(c, `UPDATE tool_calls SET output=$2, status=$3, error=$4, latency_ms=$5, finished_at=now() WHERE id=$1`,
		id, o, status, msg, int(lat.Milliseconds()))
}

func record(ctx context.Context, env *Env, t *Tool, args, out map[string]any, status, key string, err error, lat time.Duration) {
	id, e := begin(ctx, env, t, args, key)
	if e == nil {
		finish(ctx, env.Pool, id, out, status, err, lat)
	}
}

func isUnique(err error) bool { return err != nil && strings.Contains(err.Error(), "23505") }

// Key builds an idempotency key from the task and a stable subset of args.
func Key(env *Env, tool string, parts ...any) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s", tool, env.TaskID, env.GoalID, env.UserID, env.Scope)
	for _, p := range parts {
		b, _ := json.Marshal(p)
		h.Write([]byte{'|'})
		h.Write(b)
	}
	return tool + ":" + hex.EncodeToString(h.Sum(nil))[:40]
}

// ── small helpers for tool bodies ──────────────────────────────────────────

// Str reads a string argument; a missing or mistyped one is "". Exported for
// tools registered from other packages (the studio).
func Str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
