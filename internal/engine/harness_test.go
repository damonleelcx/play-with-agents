package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/db"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
)

// These tests run against a real Postgres, because the properties under test
// — SKIP LOCKED, fencing, transactional checkpoints — only exist there.
//
//	docker run -d --name act-pg -e POSTGRES_USER=act -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=act -p 127.0.0.1:55850:5432 postgres:17
//	ACT_TEST_DATABASE_URL=postgres://act@127.0.0.1:55850/act?sslmode=disable go test ./internal/engine/
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("ACT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ACT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `TRUNCATE users, goals, tasks, task_deps, checkpoints, tool_calls, approvals, events, episode_summaries,
		documents, knowledge_chunks, llm_calls, reminders, conversations, messages, memories, user_preferences, sessions, email_tokens, professional_licenses, outbox CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
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

func lastUser(req fakeReq) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content
		}
	}
	return ""
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
	convID  string
	planner *Planner
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
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name, email_verified_at) VALUES ('c@example.com','x','Casey', now()) RETURNING id`).Scan(&r.userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO conversations (user_id) VALUES ($1) RETURNING id`, r.userID).Scan(&r.convID); err != nil {
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
	g := &Goal{UserID: r.userID, ConversationID: r.convID, Title: "Test matter", Objective: "test", Domain: "legal", Skill: "general-task",
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
