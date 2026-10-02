package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// The turn tests run the real Turn against Postgres with a scripted model:
// the router's JSON is fixed per test, so what is under test is what Aoi DOES
// with a route — which capability she calls, with what, and which cards and
// mood the reply carries. TestRouteEval (route_eval_test.go) measures the
// router itself against the live model.

const defaultTestDSN = "postgres://play@127.0.0.1:55860/play?sslmode=disable"

// testDSN is a database of this package's own on the test server
// (PLAY_TEST_DATABASE_URL, or the local play-pg), so the engine tests'
// clean-up running in parallel cannot touch these rows. Skips when there is
// no server.
func testDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("PLAY_TEST_DATABASE_URL")
	if base == "" {
		base = defaultTestDSN
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url, err := db.EnsureDatabase(ctx, base, "play_agent_test")
	if err != nil {
		t.Skipf("no test database at %s (%v); start play-pg or set PLAY_TEST_DATABASE_URL", base, err)
	}
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
	t.Cleanup(pool.Close)
	return pool
}

var seq atomic.Int64

// newPlayer creates an account and a conversation, removed after the test.
func newPlayer(t *testing.T, pool *pgxpool.Pool, prefs map[string]any) (uid, conv string) {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("agent-%d-%d@example.com", time.Now().UnixNano(), seq.Add(1))
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name, email_verified_at) VALUES ($1,'x','Sam',now()) RETURNING id`, email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	if prefs != nil {
		raw, _ := json.Marshal(prefs)
		_, _ = pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1,$2)`, uid, raw)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO conversations (user_id) VALUES ($1) RETURNING id`, uid).Scan(&conv); err != nil {
		t.Fatal(err)
	}
	return uid, conv
}

// fakeLLM answers the router with a fixed route, memory extraction with
// nothing, and streams a fixed reply. It records the reply's system prompt.
type fakeLLM struct {
	srv    *httptest.Server
	route  map[string]any
	mu     sync.Mutex
	system string
}

func newFakeLLM(t *testing.T, route map[string]any) *fakeLLM {
	f := &fakeLLM{route: route}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool          `json:"stream"`
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			f.mu.Lock()
			f.system = req.Messages[0].Content
			f.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			for _, part := range []string{"よし! ", "Let's play."} {
				b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": part}}}})
				fmt.Fprintf(w, "data: %s\n\n", b)
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		content := `{"facts":[]}`
		if strings.Contains(req.Messages[0].Content, "You route messages") {
			f.mu.Lock()
			b, _ := json.Marshal(f.route)
			f.mu.Unlock()
			content = string(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": content}}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLLM) systemPrompt() string { f.mu.Lock(); defer f.mu.Unlock(); return f.system }

func (f *fakeLLM) setRoute(r map[string]any) { f.mu.Lock(); defer f.mu.Unlock(); f.route = r }

type fakeTables struct {
	got []TableRequest
	err error
}

func (f *fakeTables) CreateTable(_ context.Context, _ string, req TableRequest) (string, error) {
	f.got = append(f.got, req)
	return "tbl-1", f.err
}

type fakeStudio struct {
	prompt, base string
	err          error
}

func (f *fakeStudio) StartBuild(_ context.Context, _, _, prompt, base, _ string) (string, string, error) {
	f.prompt, f.base = prompt, base
	return "11111111-1111-1111-1111-111111111111", "my-game", f.err
}

type fakeCatalog struct{}

func (fakeCatalog) Games(context.Context, string) ([]GameInfo, error) {
	return []GameInfo{builtinGames[0], {ID: "dragon-chess", Name: "Dragon Chess", MinSeats: 2, MaxSeats: 2, Mine: true}}, nil
}
func (fakeCatalog) Rules(_ context.Context, _, id string) (string, error) {
	if id == "holdem" {
		return "Side pots: when a player is all-in, later bets go to a side pot they cannot win.", nil
	}
	return "", errors.New("no rules")
}

type sink struct{ metas []map[string]any }

func (s *sink) Meta(v map[string]any) { s.metas = append(s.metas, v) }
func (s *sink) Delta(string)          {}

type rig struct {
	pool  *pgxpool.Pool
	agent *Agent
	fake  *fakeLLM
	uid   string
	conv  string
}

func newRig(t *testing.T, route map[string]any, prefs map[string]any) *rig {
	pool := testPool(t)
	f := newFakeLLM(t, route)
	c := llm.New(f.srv.URL, "k")
	c.Retries = 0
	store := &engine.Store{Pool: pool}
	r := &rig{pool: pool, fake: f, agent: &Agent{Store: store, Model: &engine.Model{Client: c, Store: store}, LLM: "m", FastLLM: "f"}}
	r.uid, r.conv = newPlayer(t, pool, prefs)
	return r
}

// turn runs one message and returns the saved reply's meta.
func (r *rig) turn(t *testing.T, text string) map[string]any {
	t.Helper()
	s := &sink{}
	id, err := r.agent.Turn(context.Background(), User{ID: r.uid, Name: "Sam", Lang: "en"}, r.conv, text, "", s)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := r.pool.QueryRow(context.Background(), `SELECT meta FROM messages WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{}
	_ = json.Unmarshal(raw, &meta)
	if len(s.metas) == 0 || fmt.Sprint(s.metas[0]["mood"]) != meta["mood"] {
		t.Fatalf("streamed meta %v does not match saved %v", s.metas, meta)
	}
	return meta
}

func cards(meta map[string]any) []map[string]any {
	var out []map[string]any
	raw, _ := json.Marshal(meta["cards"])
	_ = json.Unmarshal(raw, &out)
	return out
}

func TestPlayCreatesATableAndAttachesItsCard(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "play", "confidence": 0.95, "game_id": "holdem", "agent_ids": []string{"Mika", "ren", "nobody"}}, nil)
	tables := &fakeTables{}
	r.agent.Tables, r.agent.Catalog = tables, fakeCatalog{}
	meta := r.turn(t, "deal me into hold'em with Mika and Ren")
	if len(tables.got) != 1 {
		t.Fatalf("CreateTable calls: %d", len(tables.got))
	}
	req := tables.got[0]
	if req.GameID != "holdem" || strings.Join(req.AgentIDs, ",") != "mika,ren" || req.Seats != 3 {
		t.Fatalf("request %+v", req)
	}
	c := cards(meta)
	if len(c) != 1 || c[0]["kind"] != "table" || c[0]["table_id"] != "tbl-1" || meta["mood"] != "wink" || meta["intent"] != "play" {
		t.Fatalf("meta %v", meta)
	}
}

func TestPlayRefusesMoreSeatsThanTheGameHas(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "play", "confidence": 0.9, "game_id": "dragon-chess", "agent_ids": []string{"mika", "ren"}}, nil)
	tables := &fakeTables{}
	r.agent.Tables, r.agent.Catalog = tables, fakeCatalog{}
	meta := r.turn(t, "dragon chess with mika and ren")
	if len(tables.got) != 0 || meta["cards"] != nil {
		t.Fatalf("a 3-player table was requested for a 2-player game: %v %v", tables.got, meta)
	}
	if !strings.Contains(r.fake.systemPrompt(), "at most 2 players") {
		t.Fatal("the reply was not told why")
	}
}

func TestBuildStartsAMissionAndAttachesItsCard(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "build_game", "confidence": 0.9, "prompt": "a game where dragons capture by jumping"}, nil)
	studio := &fakeStudio{}
	r.agent.Studio, r.agent.Catalog = studio, fakeCatalog{}
	meta := r.turn(t, "Let's make a game where dragons capture by jumping")
	if studio.prompt != "a game where dragons capture by jumping" || studio.base != "" {
		t.Fatalf("studio got %+v", studio)
	}
	c := cards(meta)
	if len(c) != 1 || c[0]["kind"] != "mission" || c[0]["goal_id"] != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("meta %v", meta)
	}
}

// Every capability may be missing; the turn still succeeds and says so.
func TestMissingCapabilitiesReplyGracefully(t *testing.T) {
	for _, intent := range []string{"play", "build_game"} {
		r := newRig(t, map[string]any{"intent": intent, "confidence": 0.9}, nil)
		meta := r.turn(t, "deal me in / build me a game")
		if meta["mood"] != "sad" || meta["cards"] != nil || !strings.Contains(r.fake.systemPrompt(), "not open yet") {
			t.Fatalf("%s: meta %v", intent, meta)
		}
	}
}

func TestCapabilityErrorsAreNotLeaked(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "play", "confidence": 0.9, "game_id": "holdem"}, nil)
	r.agent.Tables = &fakeTables{err: errors.New("pq: deadlock detected on relation tables")}
	r.turn(t, "deal me in")
	if sys := r.fake.systemPrompt(); strings.Contains(sys, "deadlock") || !strings.Contains(sys, "went wrong") {
		t.Fatal("an internal error reached the reply prompt")
	}
	r.agent.Tables = &fakeTables{err: fmt.Errorf("%w: hold'em needs at least 2 players", ErrRejected)}
	r.turn(t, "deal me in")
	if !strings.Contains(r.fake.systemPrompt(), "needs at least 2 players") {
		t.Fatal("a rejection reason did not reach the reply prompt")
	}
}

func TestRulesAnswerFromTheCatalogsRules(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "rules", "confidence": 0.9, "game_id": "holdem"}, nil)
	r.agent.Catalog = fakeCatalog{}
	meta := r.turn(t, "How do side pots work?")
	if !strings.Contains(r.fake.systemPrompt(), "later bets go to a side pot") {
		t.Fatal("the rules text was not given to the reply")
	}
	if c := cards(meta); len(c) != 1 || c[0]["kind"] != "game" || c[0]["game_id"] != "holdem" || meta["mood"] != "neutral" {
		t.Fatalf("meta %v", meta)
	}
}

func TestReviseFeedsTheRunningBuildAndControlCancelsIt(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "revise_game", "confidence": 0.9, "prompt": "make it 3 players"}, nil)
	g := &engine.Goal{UserID: r.uid, ConversationID: r.conv, Title: "Dragon Chess", Objective: "x", Domain: "studio", Skill: "general-task", Language: "en"}
	if err := r.agent.Store.CreateGoal(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	meta := r.turn(t, "Change the build to 3 players")
	var n int
	_ = r.pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE goal_id=$1 AND spec->>'mode'='change' AND spec->>'reason' LIKE '%3 players%'`, g.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("change tasks: %d", n)
	}
	if c := cards(meta); len(c) != 1 || c[0]["goal_id"] != g.ID {
		t.Fatalf("meta %v", meta)
	}

	r.fake.setRoute(map[string]any{"intent": "control", "confidence": 0.9, "action": "cancel", "goal_id": g.ID})
	meta = r.turn(t, "cancel the build")
	if st, _ := r.agent.Store.Goal(context.Background(), g.ID); st.Status != "cancelled" || meta["mood"] != "sad" {
		t.Fatalf("status %s meta %v", st.Status, meta)
	}
}

func TestPreferenceFromChatIsValidatedSavedAndHonouredAtOnce(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "preference", "confidence": 0.9, "preference": map[string]any{"key": "call_me", "value": "Captain"}}, nil)
	r.turn(t, "call me Captain")
	var v string
	_ = r.pool.QueryRow(context.Background(), `SELECT data->>'call_me' FROM user_preferences WHERE user_id=$1`, r.uid).Scan(&v)
	if v != "Captain" || !strings.Contains(r.fake.systemPrompt(), "CALL THE PLAYER: Captain") {
		t.Fatalf("call_me=%q", v)
	}
	r.fake.setRoute(map[string]any{"intent": "preference", "confidence": 0.9, "preference": map[string]any{"key": "aoi_tone", "value": "grumpy"}})
	r.turn(t, "be grumpy")
	_ = r.pool.QueryRow(context.Background(), `SELECT coalesce(data->>'aoi_tone','') FROM user_preferences WHERE user_id=$1`, r.uid).Scan(&v)
	if v != "" {
		t.Fatalf("an invalid tone was stored: %q", v)
	}
	r.fake.setRoute(map[string]any{"intent": "preference", "confidence": 0.9, "preference": map[string]any{"key": "language", "value": "zh"}})
	r.turn(t, "please speak Chinese")
	if !strings.Contains(r.fake.systemPrompt(), "REPLY LANGUAGE: Simplified Chinese") {
		t.Fatal("the language change did not apply to this reply")
	}
}

func TestMemoryOffKeepsMemoriesOutOfTheReply(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "chat", "confidence": 1, "mood": "surprised"}, map[string]any{"memory_enabled": false})
	_, _ = r.pool.Exec(context.Background(), `INSERT INTO memories (user_id, content) VALUES ($1, 'loves bluffing')`, r.uid)
	meta := r.turn(t, "hi Aoi")
	if sys := r.fake.systemPrompt(); strings.Contains(sys, "loves bluffing") || !strings.Contains(sys, "turned long-term memory off") {
		t.Fatal("memory setting not honoured")
	}
	if meta["mood"] != "surprised" {
		t.Fatalf("the router's mood for chat was not used: %v", meta["mood"])
	}
}

func TestLowConfidenceAsksInsteadOfActing(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "play", "confidence": 0.3, "clarify": "Which game?"}, nil)
	tables := &fakeTables{}
	r.agent.Tables = tables
	r.turn(t, "let's go")
	if len(tables.got) != 0 || !strings.Contains(r.fake.systemPrompt(), "Which game?") {
		t.Fatal("acted on an unsure route")
	}
}

func TestRetriedMessageReturnsTheSameReply(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "chat", "confidence": 1}, nil)
	ctx := context.Background()
	u := User{ID: r.uid, Lang: "en"}
	id1, err := r.agent.Turn(ctx, u, r.conv, "hello", "c-1", &sink{})
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	id2, err := r.agent.Turn(ctx, u, r.conv, "hello", "c-1", s)
	if err != nil || id1 != id2 || s.metas[0]["duplicate"] != true {
		t.Fatalf("retry produced %d vs %d (%v)", id2, id1, err)
	}
}

// ── pure helpers ───────────────────────────────────────────────────────────

func TestDetectLang(t *testing.T) {
	cases := []struct{ text, pref, want string }{
		{"deal me in", "zh", "en"},
		{"开一桌德州", "en", "zh"},
		{"ok", "zh", "zh"},
		{"👍", "zh", "zh"},
		{"100", "", "en"},
		{"Aoi，来一局 hold'em", "en", "zh"},
	}
	for _, c := range cases {
		if got := DetectLang(c.text, c.pref); got != c.want {
			t.Errorf("DetectLang(%q,%q)=%s want %s", c.text, c.pref, got, c.want)
		}
	}
}

func TestPreferenceValidation(t *testing.T) {
	good := map[string]any{"language": "zh", "aoi_tone": "calm", "turn_seconds": float64(0), "playtest_games": float64(500),
		"call_me": "Cap", "favorite_agents": []any{"mika", "lin"}, "sound": false, "voice_volume": float64(0), "aoi_voice": false}
	for k, v := range good {
		if err := ValidatePref(k, v); err != nil {
			t.Errorf("%s=%v rejected: %v", k, v, err)
		}
	}
	bad := map[string]any{"language": "fr", "aoi_tone": "grumpy", "turn_seconds": float64(45), "playtest_games": "200",
		"call_me": strings.Repeat("x", 41), "favorite_agents": []any{"mika", "mika"}, "sound": "yes", "theme": "dark", "display_name": "x"}
	for k, v := range bad {
		if err := ValidatePref(k, v); err == nil {
			t.Errorf("%s=%v accepted", k, v)
		}
	}
	d := WithDefaults(map[string]any{"aoi_talk": "quiet", "aoi_tone": "removed-option", "legacy": 1})
	if d["aoi_talk"] != "quiet" || d["aoi_tone"] != "playful" || d["legacy"] != nil || d["turn_seconds"] != 30 {
		t.Fatalf("WithDefaults: %v", d)
	}
	if v, err := coercePref("turn_seconds", "60"); err != nil || v != 60 {
		t.Errorf("coerce turn_seconds: %v %v", v, err)
	}
	if v, err := coercePref("voice_volume", "65%"); err != nil || v != 65 {
		t.Errorf("coerce voice_volume: %v %v", v, err)
	}
	if _, err := coercePref("voice_volume", "200"); err == nil {
		t.Error("voice_volume 200 accepted")
	}
	if v, err := coercePref("sound", "off"); err != nil || v != false {
		t.Errorf("coerce sound: %v %v", v, err)
	}
	if _, err := coercePref("favorite_agents", "mika"); err == nil {
		t.Error("a list setting was changeable from chat")
	}
}

func TestEveryMoodHasAFace(t *testing.T) {
	for _, m := range persona.Moods {
		if persona.FaceAsset(m) != "/play/aoi/aoi-face-"+string(m)+".webp" {
			t.Errorf("%s → %s", m, persona.FaceAsset(m))
		}
	}
	if persona.FaceAsset("furious") != "/play/aoi/aoi-face-neutral.webp" {
		t.Error("unknown mood did not fall back to neutral")
	}
}
