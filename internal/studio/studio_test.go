package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/art"
	"github.com/damonleelcx/play-with-agents/internal/db"
	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/games/script"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/skills"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

// These tests drive real builds through the real engine against Postgres
// (a database of this package's own), with a scripted model standing in for
// the Designer, Engineer, Critic and Coordinator. What is under test is the
// studio's machinery: the playbook's shape, what the tools store, the gates,
// the publish approval and the bounded revision loop.

const defaultTestDSN = "postgres://play@127.0.0.1:55860/play?sslmode=disable"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("PLAY_TEST_DATABASE_URL")
	if base == "" {
		base = defaultTestDSN
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url, err := db.EnsureDatabase(ctx, base, "play_studio_test")
	if err != nil {
		t.Skipf("no test database at %s (%v); start play-pg or set PLAY_TEST_DATABASE_URL", base, err)
	}
	pool, err := db.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	// Claim takes any ready task: every test starts with an empty queue.
	if _, err := pool.Exec(context.Background(), `TRUNCATE goals, tasks, task_deps, checkpoints, tool_calls, approvals, events,
		episode_summaries, llm_calls, outbox CASCADE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

var seq atomic.Int64

func newPlayer(t *testing.T, pool *pgxpool.Pool, prefs map[string]any) (uid, conv string) {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("studio-%d-%d@example.com", time.Now().UnixNano(), seq.Add(1))
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name, email_verified_at) VALUES ($1,'x','Casey', now()) RETURNING id`, email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if prefs != nil {
		raw, _ := json.Marshal(prefs)
		if _, err := pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2)`, uid, raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO conversations (user_id) VALUES ($1) RETURNING id`, uid).Scan(&conv); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM games WHERE owner_id=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid)
	})
	return uid, conv
}

// ── the scripted model ─────────────────────────────────────────────────────

type script_ struct {
	mu            sync.Mutex
	criticVerdict string // pass | revise
	source        string // what the Engineer saves
	calls         map[string]int
}

var rePublish = regexp.MustCompile(`Call publish_game with (\{[^}]*\})`)

func (s *script_) reply(msgs []llm.Message) llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	system := ""
	if len(msgs) > 0 && msgs[0].Role == "system" {
		system = msgs[0].Content
	}
	last := msgs[len(msgs)-1]
	done := last.Role == "tool"
	role := "other"
	for _, r := range []string{"Designer", "Engineer", "Critic", "Coordinator"} {
		if strings.HasPrefix(system, "You are the "+r) {
			role = r
		}
	}
	s.calls[role]++
	switch role {
	case "Designer":
		if done {
			return llm.Message{Content: "Tic-tac-toe on a 3x3 grid, two players."}
		}
		return toolCall("save_rules", map[string]any{"name": "Tic-tac-toe", "summary": "Three in a row wins.",
			"rules_md":  strings.Repeat("Players alternate placing marks on a 3x3 grid; three in a row wins; a full grid is a draw. ", 4),
			"min_seats": 2, "max_seats": 2, "hidden_info": false})
	case "Engineer":
		if done && strings.Contains(last.Content, `"ok":true`) {
			return llm.Message{Content: "Built and checked."}
		}
		return toolCall("save_module", map[string]any{"source": s.source})
	case "Critic":
		if done {
			return llm.Message{Content: "**Verdict: " + s.criticVerdict + "**"}
		}
		return toolCall("submit_review", map[string]any{"verdict": s.criticVerdict, "summary": "Checked rules against the code.",
			"findings": []map[string]any{{"severity": "high", "area": "rules", "detail": "Diagonals are not counted.", "fix": "Add both diagonals to LINES."}}})
	case "Coordinator":
		// Like a real model: once the decision is in, it writes its summary.
		for _, m := range msgs {
			if m.Role == "tool" && (strings.Contains(m.Content, `"published"`) || strings.Contains(m.Content, "DECLINED")) {
				return llm.Message{Content: "Asked and answered."}
			}
		}
		var args map[string]any
		for _, m := range msgs {
			if mm := rePublish.FindStringSubmatch(m.Content); mm != nil {
				_ = json.Unmarshal([]byte(mm[1]), &args)
			}
		}
		return toolCall("publish_game", args)
	}
	if strings.Contains(system, "You are Aoi") {
		return llm.Message{Content: "Your game is on the shelf — let's play!"}
	}
	return llm.Message{Content: `{"assessment":"nothing to change","ops":[],"goal_status":"continue","complete":false,"unmet":["test"]}`}
}

func toolCall(name string, args any) llm.Message {
	var c llm.ToolCall
	c.ID, c.Type = fmt.Sprintf("call-%s-%d", name, seq.Add(1)), "function"
	c.Function.Name = name
	b, _ := json.Marshal(args)
	c.Function.Arguments = string(b)
	return llm.Message{ToolCalls: []llm.ToolCall{c}}
}

func newModel(t *testing.T, sc *script_) *engine.Model {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string        `json:"model"`
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		msg := sc.reply(req.Messages)
		msg.Role = "assistant"
		_ = json.NewEncoder(w).Encode(map[string]any{"model": req.Model,
			"choices": []any{map[string]any{"message": msg}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 20}})
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL, "test-key")
	c.Retries = 0
	return &engine.Model{Client: c}
}

type rig struct {
	pool   *pgxpool.Pool
	store  *engine.Store
	svc    *Service
	worker *engine.Worker
	sched  *engine.Scheduler
	sc     *script_
	uid    string
	conv   string
}

func newRig(t *testing.T, verdict string) *rig {
	pool := testPool(t)
	st := &engine.Store{Pool: pool}
	sc := &script_{criticVerdict: verdict, source: tictactoe(t), calls: map[string]int{}}
	m := newModel(t, sc)
	m.Store = st
	planner := &engine.Planner{Store: st, Model: m, LLM: "m"}
	r := &rig{pool: pool, store: st, svc: &Service{Pool: pool, Store: st}, sc: sc,
		worker: &engine.Worker{ID: "studio-test", Store: st, Model: m, Planner: planner, LLM: "m", Mailer: mail.Log{}, Lease: 30 * time.Second},
		sched:  &engine.Scheduler{Store: st, Model: m, FastLLM: "m", Mailer: mail.Log{}}}
	r.uid, r.conv = newPlayer(t, pool, map[string]any{"playtest_games": 50, "studio_visibility": "unlisted"})
	return r
}

func tictactoe(t *testing.T) string {
	e, ok := script.ExampleByID("tictactoe")
	if !ok {
		t.Fatal("no tictactoe example")
	}
	return e.Source
}

// drive runs the queue (and the scheduler's pickup of finished goals) until
// done() or the step bound; it never waits on wall-clock retries.
func (r *rig) drive(t *testing.T, done func() bool) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 80; i++ {
		if done() {
			return
		}
		task, err := r.store.Claim(ctx, r.worker.ID, r.worker.Lease)
		if err != nil {
			t.Fatal(err)
		}
		if task == nil {
			r.sched.Tick(ctx)
			if task, err = r.store.Claim(ctx, r.worker.ID, r.worker.Lease); err != nil {
				t.Fatal(err)
			}
			if task == nil {
				if done() {
					return
				}
				t.Fatalf("queue is empty and the condition never held:\n%s", r.dump(t))
			}
		}
		r.worker.Execute(ctx, task)
	}
	t.Fatalf("condition not reached in 80 steps:\n%s", r.dump(t))
}

func (r *rig) goal(t *testing.T, id string) *engine.Goal {
	g, err := r.store.Goal(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (r *rig) dump(t *testing.T) string {
	var b strings.Builder
	rows, _ := r.pool.Query(context.Background(), `SELECT g.status, t.key, t.kind, t.status, t.error FROM tasks t JOIN goals g ON g.id=t.goal_id ORDER BY t.created_at, t.key`)
	for rows.Next() {
		var gs, k, kind, st, e string
		_ = rows.Scan(&gs, &k, &kind, &st, &e)
		fmt.Fprintf(&b, "goal=%s %s (%s) %s %s\n", gs, k, kind, st, truncate(e, 200))
	}
	rows.Close()
	rows, _ = r.pool.Query(context.Background(), `SELECT tool, status, error FROM tool_calls WHERE status <> 'succeeded'`)
	for rows.Next() {
		var tool, st, e string
		_ = rows.Scan(&tool, &st, &e)
		fmt.Fprintf(&b, "tool call %s %s %s\n", tool, st, truncate(e, 300))
	}
	rows.Close()
	rows, _ = r.pool.Query(context.Background(), `SELECT goal_id::text, tool, status, args::text FROM approvals`)
	for rows.Next() {
		var g, tool, st, args string
		_ = rows.Scan(&g, &tool, &st, &args)
		fmt.Fprintf(&b, "approval goal=%s %s %s %s\n", g, tool, st, args)
	}
	rows.Close()
	return b.String()
}

func (r *rig) pendingApproval(goalID string) string {
	var id string
	_ = r.pool.QueryRow(context.Background(), `SELECT id::text FROM approvals WHERE goal_id=$1 AND status='pending'`, goalID).Scan(&id)
	return id
}

func (r *rig) gameState(t *testing.T, id string) (status string, cur int) {
	if err := r.pool.QueryRow(context.Background(), `SELECT status, current_version FROM games WHERE id=$1`, id).Scan(&status, &cur); err != nil {
		t.Fatal(err)
	}
	return
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestPlaybookShape(t *testing.T) {
	sk, ok := skills.Get(Skill)
	if !ok || !sk.Fixed {
		t.Fatalf("build_game is not registered as a fixed playbook")
	}
	want := []struct{ key, role, tool, dep string }{
		{"design-rules", RoleDesigner, "", ""}, {"write-module", RoleEngineer, "", "design-rules"},
		{"illustrate-cover", RoleArtist, ToolIllustrate, "design-rules"},
		{"playtest", RolePlaytester, ToolPlaytest, "write-module"},
		{"critic-review", RoleCritic, "", "playtest"}, {"publish", RoleCoordinator, "", "critic-review"},
	}
	if len(sk.Steps) != len(want) {
		t.Fatalf("%d steps", len(sk.Steps))
	}
	for i, w := range want {
		s := sk.Steps[i]
		if s.Key != w.key || s.Role != w.role || s.Tool != w.tool {
			t.Errorf("step %d = %s/%s/%s, want %v", i, s.Key, s.Role, s.Tool, w)
		}
		if (w.dep == "" && len(s.Deps) != 0) || (w.dep != "" && (len(s.Deps) != 1 || s.Deps[0] != w.dep)) {
			t.Errorf("step %s deps %v", s.Key, s.Deps)
		}
	}
	// The Artist never gates publishing: nothing depends on it, it needs no
	// approval and it declares no check that could fail it.
	byKey := map[string]skills.Step{}
	for _, s := range sk.Steps {
		byKey[s.Key] = s
	}
	var upstream func(k string, seen map[string]bool)
	upstream = func(k string, seen map[string]bool) {
		for _, d := range byKey[k].Deps {
			seen[d] = true
			upstream(d, seen)
		}
	}
	for _, s := range sk.Steps {
		seen := map[string]bool{}
		upstream(s.Key, seen)
		if seen["illustrate-cover"] {
			t.Errorf("%s waits for the cover", s.Key)
		}
	}
	if a := byKey["illustrate-cover"]; len(a.Verify) != 0 {
		t.Errorf("illustrate-cover verifies %v", a.Verify)
	}
	if it, ok := tools.Get(ToolIllustrate); !ok || it.Gate != tools.G0 {
		t.Fatal("illustrate_cover must run unattended (G0)")
	}
	// Every gate is declared on its step.
	gates := map[string]string{"write-module": VerifyModuleChecks, "playtest": VerifyPlaytestPassed,
		"critic-review": VerifyCriticAccepted, "publish": VerifyPublishDecided}
	for _, s := range sk.Steps {
		if v, ok := gates[s.Key]; ok && !strings.Contains(strings.Join(s.Verify, ","), v) {
			t.Errorf("%s does not verify %s", s.Key, v)
		}
	}
	if pt, ok := tools.Get(ToolPublish); !ok || pt.Gate != tools.G1 || pt.Notify != "build_ready" {
		t.Fatal("publish_game must be G1 with build_ready")
	}
	if pt, _ := tools.Get(ToolPlaytest); pt.Gate != tools.G0 {
		t.Fatal("playtest must run unattended")
	}

	// Instantiated: the plan task writes exactly these rows, no model call.
	r := newRig(t, "pass")
	goalID, gameID, err := r.svc.StartBuild(context.Background(), r.uid, r.conv, "tic-tac-toe please", "", "en")
	if err != nil {
		t.Fatal(err)
	}
	g := r.goal(t, goalID)
	if g.GameID != gameID || g.Skill != Skill || g.Domain != Domain || g.Limits.MaxReplans != 4 || len(g.Criteria) != 5 {
		t.Fatalf("goal = %+v", g)
	}
	var status, vis, link string
	_ = r.pool.QueryRow(context.Background(), `SELECT status, visibility, coalesce(goal_id::text,'') FROM games WHERE id=$1`, gameID).Scan(&status, &vis, &link)
	if status != "building" || vis != "unlisted" || link != goalID {
		t.Fatalf("game row: %s %s %s", status, vis, link)
	}
	task, _ := r.store.Claim(context.Background(), "w", time.Minute)
	r.worker.Execute(context.Background(), task)
	ts, _ := r.store.Tasks(context.Background(), goalID)
	kinds := map[string]string{}
	for _, x := range ts {
		kinds[x.Key] = x.Kind + "/" + x.Spec.Role + "/" + x.Status
	}
	if kinds["playtest"] != "tool/playtester/blocked" || kinds["design-rules"] != "llm/designer/ready" || kinds["critic-review"] != "llm/critic/blocked" ||
		kinds["illustrate-cover"] != "tool/artist/blocked" {
		t.Fatalf("tasks: %v", kinds)
	}
	if n := r.sc.calls["other"]; n != 0 {
		t.Fatalf("a fixed playbook made %d planner calls", n)
	}
}

func TestToolsStoreVersionsAndReports(t *testing.T) {
	r := newRig(t, "pass")
	ctx := context.Background()
	goalID, gameID, err := r.svc.StartBuild(ctx, r.uid, r.conv, "tic-tac-toe", "", "en")
	if err != nil {
		t.Fatal(err)
	}
	env := &tools.Env{Pool: r.pool, UserID: r.uid, GoalID: goalID, TaskID: "", Lang: "en"}
	invoke := func(name string, args any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(args)
		res, err := tools.Invoke(ctx, env, name, raw, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res.Output
	}
	invoke(ToolSaveRules, map[string]any{"name": "Tic-tac-toe", "summary": "Three in a row.", "rules_md": strings.Repeat("x ", 120),
		"min_seats": 2, "max_seats": 3, "hidden_info": false})
	if g := r.goal(t, goalID); g.Title != "Tic-tac-toe" {
		t.Errorf("goal title %q", g.Title)
	}

	// A broken module: stored, not ok, the issues come back as text.
	out := invoke(ToolSaveModule, map[string]any{"source": "const game = { meta: { name: 'x' } }; // missing everything else"})
	if out["ok"] != false || num(out["version"]) != 1 || !strings.Contains(out["report"].(string), "[error]") {
		t.Fatalf("broken module: %v", out)
	}
	// The real module disagrees with the rules' seat range: an error.
	out = invoke(ToolSaveModule, map[string]any{"source": tictactoe(t)})
	if out["ok"] != false || !strings.Contains(out["report"].(string), "the rules document says 2 to 3 players") {
		t.Fatalf("seat mismatch not reported: %v", out["report"])
	}
	// Rules fixed; the same draft version is replaced in place.
	invoke(ToolSaveRules, map[string]any{"name": "Tic-tac-toe", "summary": "Three in a row.", "rules_md": strings.Repeat("x ", 120),
		"min_seats": 2, "max_seats": 2, "hidden_info": false})
	out = invoke(ToolSaveModule, map[string]any{"source": tictactoe(t)})
	if out["ok"] != true || num(out["version"]) != 1 {
		t.Fatalf("good module: %v", out)
	}

	// The playtest stores its report on that version and makes it a draft.
	pt := invoke(ToolPlaytest, map[string]any{"games": 40})
	if pt["pass"] != true || num(pt["version"]) != 1 {
		t.Fatalf("playtest: %v", pt)
	}
	var md string
	var pass bool
	_ = r.pool.QueryRow(ctx, `SELECT report->>'markdown', (report->'verdict'->>'pass')::boolean FROM game_versions WHERE game_id=$1 AND version=1`, gameID).Scan(&md, &pass)
	if !pass || !strings.Contains(md, "Playtest") {
		t.Fatalf("report not stored: %v %q", pass, md)
	}
	if st, _ := r.gameState(t, gameID); st != "draft" {
		t.Fatalf("status after a passing playtest: %s", st)
	}
	// A save after the playtest is a new version: tested versions are kept.
	if out = invoke(ToolSaveModule, map[string]any{"source": tictactoe(t)}); num(out["version"]) != 2 {
		t.Fatalf("save after playtest: %v", out)
	}

	// publish_game refuses a version that did not pass every gate, even approved.
	raw, _ := json.Marshal(map[string]any{"game_id": gameID, "version": 1})
	if _, err := tools.Invoke(ctx, env, ToolPublish, raw, true); err == nil || !strings.Contains(err.Error(), "critic") {
		t.Fatalf("unreviewed version published: %v", err)
	}
}

func TestBuildToPublishApproval(t *testing.T) {
	r := newRig(t, "pass")
	ctx := context.Background()
	goalID, gameID, err := r.svc.StartBuild(ctx, r.uid, r.conv, "tic-tac-toe", "", "en")
	if err != nil {
		t.Fatal(err)
	}
	r.drive(t, func() bool { return r.pendingApproval(goalID) != "" })

	// Waiting for the owner: a playable draft, nothing published.
	if st, cur := r.gameState(t, gameID); st != "draft" || cur != 0 {
		t.Fatalf("before approval: %s v%d", st, cur)
	}
	var args []byte
	_ = r.pool.QueryRow(ctx, `SELECT args FROM approvals WHERE goal_id=$1`, goalID).Scan(&args)
	if !strings.Contains(string(args), gameID) {
		t.Fatalf("approval args %s lack the game id", args)
	}
	var outbox string
	_ = r.pool.QueryRow(ctx, `SELECT kind FROM outbox WHERE user_id=$1`, r.uid).Scan(&outbox)
	if outbox != "build_ready" {
		t.Fatalf("owner notification kind %q", outbox)
	}

	if err := r.store.Decide(ctx, r.pendingApproval(goalID), r.uid, true, ""); err != nil {
		t.Fatal(err)
	}
	r.drive(t, func() bool { return r.goal(t, goalID).Status == "completed" })
	if st, cur := r.gameState(t, gameID); st != "published" || cur != 1 {
		t.Fatalf("after approval: %s v%d", st, cur)
	}
	// The Artist ran beside the build (no image key here: procedural).
	if c, err := art.Info(ctx, r.pool, gameID); err != nil || c == nil || c.Source != "procedural" {
		t.Fatalf("cover after the build: %+v %v", c, err)
	}
	// Aoi posts the game card in the originating conversation.
	var meta []byte
	var content string
	if err := r.pool.QueryRow(ctx, `SELECT content, meta FROM messages WHERE conversation_id=$1 AND meta->>'kind'='complete'`, r.conv).Scan(&content, &meta); err != nil {
		t.Fatalf("no completion message: %v", err)
	}
	if !strings.Contains(string(meta), `"kind": "game"`) && !strings.Contains(string(meta), `"kind":"game"`) || !strings.Contains(string(meta), gameID) || content == "" {
		t.Fatalf("completion message %q %s", content, meta)
	}
}

func TestDeclinedPublishStaysDraft(t *testing.T) {
	r := newRig(t, "pass")
	ctx := context.Background()
	goalID, gameID, err := r.svc.StartBuild(ctx, r.uid, r.conv, "tic-tac-toe", "", "zh")
	if err != nil {
		t.Fatal(err)
	}
	r.drive(t, func() bool { return r.pendingApproval(goalID) != "" })
	if err := r.store.Decide(ctx, r.pendingApproval(goalID), r.uid, false, "not yet"); err != nil {
		t.Fatal(err)
	}
	r.drive(t, func() bool { return r.goal(t, goalID).Status == "completed" })
	if st, cur := r.gameState(t, gameID); st != "draft" || cur != 0 {
		t.Fatalf("declined: %s v%d", st, cur)
	}
	var n int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE goal_id=$1`, goalID).Scan(&n)
	if n != 1 {
		t.Fatalf("%d approvals: a decline must not be asked again", n)
	}
}

func TestCriticReviseLoopIsBounded(t *testing.T) {
	r := newRig(t, "revise")
	ctx := context.Background()
	goalID, gameID, err := r.svc.StartBuild(ctx, r.uid, r.conv, "tic-tac-toe", "", "en")
	if err != nil {
		t.Fatal(err)
	}
	g := r.goal(t, goalID)
	g.Limits.MaxReplans = 2
	if err := r.store.SetLimits(ctx, goalID, g.Limits); err != nil {
		t.Fatal(err)
	}
	r.drive(t, func() bool { return r.goal(t, goalID).Status == "needs_attention" })

	ts, _ := r.store.Tasks(ctx, goalID)
	byKey := map[string]*engine.Task{}
	for _, x := range ts {
		byKey[x.Key] = x
	}
	for _, k := range []string{"revise-2", "playtest-2", "critic-2", "revise-3", "critic-3"} {
		if byKey[k] == nil {
			t.Fatalf("missing %s:\n%s", k, r.dump(t))
		}
	}
	if byKey["revise-4"] != nil {
		t.Fatal("a revision round beyond the replan limit")
	}
	// Earlier rounds are superseded, not left failed; nothing was published.
	if s := byKey["critic-review"].Status; s != "skipped" {
		t.Errorf("first critic task is %s", s)
	}
	if s := byKey["publish"].Status; s != "cancelled" {
		t.Errorf("first publish task is %s", s)
	}
	if r.pendingApproval(goalID) != "" {
		t.Fatal("asked to publish a rejected module")
	}
	if g := r.goal(t, goalID); g.Replans != 2 || !strings.Contains(g.AttentionReason, "revision rounds") {
		t.Fatalf("goal: replans=%d reason=%q", g.Replans, g.AttentionReason)
	}
	// The revise step carries the critic's findings.
	if !strings.Contains(byKey["revise-2"].Spec.Instructions, "Diagonals are not counted") {
		t.Errorf("findings not passed on: %q", byKey["revise-2"].Spec.Instructions)
	}
	var msg string
	_ = r.pool.QueryRow(ctx, `SELECT content FROM messages WHERE conversation_id=$1 AND meta->>'kind'='attention'`, r.conv).Scan(&msg)
	if !strings.Contains(msg, "I need your call") {
		t.Fatalf("Aoi did not ask for a decision: %q", msg)
	}
	// The review is stored where the UI shows it.
	var review string
	_ = r.pool.QueryRow(ctx, `SELECT report->'review'->>'verdict' FROM game_versions WHERE game_id=$1 ORDER BY version DESC LIMIT 1`, gameID).Scan(&review)
	if review != "revise" {
		t.Fatalf("stored review verdict %q", review)
	}

	// The owner's change grants another round.
	if _, err := r.store.EnqueuePlan(ctx, goalID, "change-test", "change", "the player said: keep it simple"); err != nil {
		t.Fatal(err)
	}
	_ = r.store.SetGoalStatus(ctx, goalID, "active", "the owner sent a change")
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker.Execute(ctx, task)
	if g := r.goal(t, goalID); g.Limits.MaxReplans != 3 {
		t.Fatalf("limit after the owner's change: %d", g.Limits.MaxReplans)
	}
}

func TestRevisionOfOwnedGame(t *testing.T) {
	r := newRig(t, "pass")
	ctx := context.Background()
	goalID, gameID, err := r.svc.StartBuild(ctx, r.uid, r.conv, "tic-tac-toe", "", "en")
	if err != nil {
		t.Fatal(err)
	}
	// A second build of the same game while one runs is refused.
	if _, _, err := r.svc.StartBuild(ctx, r.uid, r.conv, "bigger board", gameID, "en"); err == nil {
		t.Fatal("two builds of one game at once")
	}
	_ = r.store.SetGoalStatus(ctx, goalID, "cancelled", "test")
	// Someone else's game cannot be revised.
	other, _ := newPlayer(t, r.pool, nil)
	if _, _, err := r.svc.StartBuild(ctx, other, "", "steal it", gameID, "en"); err == nil {
		t.Fatal("revised someone else's game")
	}
	g2, same, err := r.svc.StartBuild(ctx, r.uid, r.conv, "bigger board", gameID, "en")
	if err != nil || same != gameID {
		t.Fatalf("revision: %v %s", err, same)
	}
	if g := r.goal(t, g2); g.GameID != gameID || !strings.HasSuffix(g.Title, "(revision)") {
		t.Fatalf("revision goal %+v", g)
	}
}

func TestSeedCommunityIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := SeedCommunity(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var n, versions int
	_ = pool.QueryRow(ctx, `SELECT count(*), (SELECT count(*) FROM game_versions WHERE game_id IN ('tictactoe','connect-four','reversi','lantern-market','story-tiles','comet-run'))
		FROM games WHERE owner_id IS NULL AND kind='script' AND status='published' AND visibility='public'`).Scan(&n, &versions)
	if n != len(script.Examples()) || versions != n {
		t.Fatalf("%d seeded games, %d versions", n, versions)
	}
}

// num reads a number from a tool output (int in process, float64 via JSON).
func num(v any) int {
	var x int
	_, _ = fmt.Sscan(fmt.Sprint(v), &x)
	return x
}

func TestNames(t *testing.T) {
	for _, p := range []string{"a 2-player game on a 5x5 grid where players take turns", "我们来做一个游戏吧", "", "!!!"} {
		name := provisionalName(p)
		id := newGameID(name)
		if !reGameID.MatchString(id) || name == "" || len([]rune(name)) > 40 {
			t.Errorf("%q → %q %q", p, name, id)
		}
	}
	if got := seatCounts(2, 8); len(got) != 5 || got[0] != 2 || got[4] != 8 {
		t.Errorf("seatCounts(2,8) = %v", got)
	}
	if got := seatCounts(2, 4); len(got) != 3 {
		t.Errorf("seatCounts(2,4) = %v", got)
	}
}
