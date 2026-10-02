package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/db"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

// These tests run against a real Postgres, because the properties under test
// — SKIP LOCKED, fencing, transactional checkpoints — only exist there. They
// use the local dev container (scripts/dev.sh starts it) unless
// PLAY_TEST_DATABASE_URL points elsewhere:
//
//	docker run -d --name play-pg -e POSTGRES_USER=play -e POSTGRES_HOST_AUTH_METHOD=trust \
//	  -e POSTGRES_DB=play -p 127.0.0.1:55860:5432 postgres:17
//	go test ./internal/engine/
//
// When neither is reachable the tests skip rather than fail.
const defaultTestDSN = "postgres://play@127.0.0.1:55860/play?sslmode=disable"

func testDSN(t *testing.T) string {
	t.Helper()
	url := os.Getenv("PLAY_TEST_DATABASE_URL")
	if url == "" {
		url = defaultTestDSN
	}
	// db.Open waits ~30s for a database that is starting; a test run with no
	// database at all should skip in a second, so probe first.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Skipf("no test database at %s (%v); start play-pg or set PLAY_TEST_DATABASE_URL", url, err)
	}
	_ = conn.Close(ctx)
	return url
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Only the engine's own tables are cleared: Claim takes ANY ready task, so
	// they must start empty. Accounts are per test (unique emails, deleted on
	// cleanup) so that other packages' tests sharing this database keep theirs.
	_, err = pool.Exec(ctx, `TRUNCATE goals, tasks, task_deps, checkpoints, tool_calls, approvals, events,
		episode_summaries, llm_calls, outbox CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ── Test-only tools and verifiers ──────────────────────────────────────────
//
// The production registry holds only generic tools, so the engine's
// properties are exercised through these. Registered once per test binary.
func init() {
	obj := `{"type":"object"}`
	tools.Register(&tools.Tool{Name: "test_count", Description: "Pure computation.",
		Input:  `{"type":"object","required":["n"],"properties":{"n":{"type":"integer"}}}`,
		Output: obj, Effect: tools.Read, Gate: tools.G0,
		Run: func(_ context.Context, _ *tools.Env, a map[string]any) (map[string]any, error) {
			return map[string]any{"n": a["n"]}, nil
		}})
	tools.Register(&tools.Tool{Name: "test_note", Description: "Writes our own database.",
		Input: obj, Output: obj, Effect: tools.Write, Gate: tools.G0,
		Run: func(context.Context, *tools.Env, map[string]any) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		}})
	// An external side effect gated on the owner — and refused outright when
	// the arguments say it would involve real money, whatever else they say.
	tools.Register(&tools.Tool{Name: "test_send", Description: "Sends a message to someone outside.",
		Input:  `{"type":"object","required":["to","body"],"properties":{"to":{"type":"string"},"body":{"type":"string"},"real_money":{"type":"boolean"}}}`,
		Output: `{"type":"object","required":["message_id"]}`, Effect: tools.External, Gate: tools.G1,
		GateFor: func(a map[string]any) tools.Gate {
			if a["real_money"] == true {
				return tools.G3
			}
			return tools.G1
		},
		IdemKey: func(env *tools.Env, a map[string]any) string { return tools.Key(env, "test_send", a["to"], a["body"]) },
		Preview: func(a map[string]any) string { return "To: " + tools.Str(a, "to") },
		Run: func(ctx context.Context, env *tools.Env, a map[string]any) (map[string]any, error) {
			id, err := env.Mailer.Send(ctx, mail.Message{To: tools.Str(a, "to"), Subject: "test", Text: tools.Str(a, "body"),
				MessageID: strings.ReplaceAll(tools.Key(env, "test_send", a["to"], a["body"]), ":", "-")})
			if err != nil {
				return nil, &tools.Transient{Err: err}
			}
			return map[string]any{"message_id": id}, nil
		}})
	tools.Register(&tools.Tool{Name: "test_publish", Description: "Publishes the result.",
		Input: obj, Output: obj, Effect: tools.Write, Gate: tools.G1, Notify: "build_ready",
		Run: func(context.Context, *tools.Env, map[string]any) (map[string]any, error) {
			return map[string]any{"published": true}, nil
		}})
	RegisterVerifier(Verifier{Name: "test_guard", Guard: true, Check: func(ctx context.Context, s *Store, t *Task) []string {
		v, _ := lookupVerifier("called:test_note")
		return v.Check(ctx, s, t)
	}})
}

// fakeModel is a scripted OpenAI-compatible endpoint. The script sees the
// request and returns the assistant message; calls are counted.
type fakeModel struct {
	srv    *httptest.Server
	calls  atomic.Int64
	mu     sync.Mutex
	script func(req fakeReq) llm.Message
}

type fakeReq struct {
	Model    string        `json:"model"`
	Messages []llm.Message `json:"messages"`
	Tools    []llm.ToolDef `json:"tools"`
	JSON     bool
}

func newFake(t *testing.T, script func(fakeReq) llm.Message) *fakeModel {
	f := &fakeModel{script: script}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		var raw map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		var req fakeReq
		_ = json.Unmarshal(raw["model"], &req.Model)
		_ = json.Unmarshal(raw["messages"], &req.Messages)
		_ = json.Unmarshal(raw["tools"], &req.Tools)
		_, req.JSON = raw["response_format"]
		f.mu.Lock()
		msg := f.script(req)
		f.mu.Unlock()
		msg.Role = "assistant"
		for i := range msg.ToolCalls {
			msg.ToolCalls[i].Type = "function"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": req.Model,
			"choices": []any{map[string]any{"message": msg}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 20}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func call(id, name string, args any) llm.ToolCall {
	var c llm.ToolCall
	c.ID, c.Type = id, "function"
	c.Function.Name = name
	b, _ := json.Marshal(args)
	c.Function.Arguments = string(b)
	return c
}

// toolResults counts tool messages in a request.
func toolResults(req fakeReq) int {
	n := 0
	for _, m := range req.Messages {
		if m.Role == "tool" {
			n++
		}
	}
	return n
}

// countingMailer records sends; it is the "external system" in the
// idempotency tests.
type countingMailer struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *countingMailer) Enabled() bool { return true }
func (m *countingMailer) Send(_ context.Context, msg mail.Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return "<" + msg.MessageID + "@test>", nil
}
func (m *countingMailer) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.sent) }

type rig struct {
	pool    *pgxpool.Pool
	store   *Store
	model   *Model
	fake    *fakeModel
	mailer  *countingMailer
	userID  string
	email   string
	convID  string
	planner *Planner
}

var userSeq atomic.Int64

// newUser creates a verified account unique to this test run and deletes it
// (and, by cascade, everything it owns) when the test ends.
func newUser(t *testing.T, pool *pgxpool.Pool, prefs map[string]any) (id, email string) {
	t.Helper()
	ctx := context.Background()
	email = fmt.Sprintf("engine-%d-%d@example.com", time.Now().UnixNano(), userSeq.Add(1))
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name, email_verified_at) VALUES ($1,'x','Casey', now()) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if prefs != nil {
		raw, _ := json.Marshal(prefs)
		if _, err := pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2)`, id, raw); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
	return id, email
}

func newRig(t *testing.T, script func(fakeReq) llm.Message) *rig {
	pool := testPool(t)
	f := newFake(t, script)
	c := llm.New(f.srv.URL, "test-key")
	c.Retries = 0
	st := &Store{Pool: pool}
	r := &rig{pool: pool, store: st, fake: f, mailer: &countingMailer{},
		model: &Model{Client: c, Store: st}}
	r.planner = &Planner{Store: st, Model: r.model, LLM: "m"}
	r.userID, r.email = newUser(t, pool, nil)
	if err := pool.QueryRow(context.Background(), `INSERT INTO conversations (user_id) VALUES ($1) RETURNING id`, r.userID).Scan(&r.convID); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) worker(id string) *Worker {
	return &Worker{ID: id, Store: r.store, Model: r.model, Planner: r.planner, LLM: "m", Mailer: r.mailer, Lease: 3 * time.Second}
}

// goalWith creates an ACTIVE goal with the given tasks already planned.
func (r *rig) goalWith(t *testing.T, tasks []PlannedTask, lim Limits) *Goal {
	t.Helper()
	ctx := context.Background()
	g := &Goal{UserID: r.userID, ConversationID: r.convID, Title: "Test mission", Objective: "test", Domain: "general", Skill: "general-task",
		Criteria: []string{"done"}, Limits: lim, Language: "en"}
	if err := r.store.CreateGoal(ctx, g); err != nil {
		t.Fatal(err)
	}
	// Replace the planning task with the given plan, as Initial would.
	_, _ = r.pool.Exec(ctx, `DELETE FROM tasks WHERE goal_id=$1`, g.ID)
	depth, err := validate(tasks, map[string]int{}, g.Limits.WithDefaults())
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := r.pool.Begin(ctx)
	if err := insertTasks(ctx, tx, g.ID, 1, tasks, depth); err != nil {
		t.Fatal(err)
	}
	_, _ = tx.Exec(ctx, `UPDATE goals SET status='active', plan_version=1 WHERE id=$1`, g.ID)
	_ = promoteTx(ctx, tx, g.ID)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	g, _ = r.store.Goal(ctx, g.ID)
	return g
}

func (r *rig) task(t *testing.T, goalID, key string) *Task {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `SELECT id FROM tasks WHERE goal_id=$1 AND key=$2`, goalID, key).Scan(&id); err != nil {
		t.Fatalf("task %s: %v", key, err)
	}
	x, err := r.store.Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
