package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/engine"
)

// One test (or more) per intent handler, with the router scripted and the
// capabilities faked: what Aoi does with each route, which cards and mood
// the reply carries, and what the reply prompt is told.

func fullRig(t *testing.T, route map[string]any, prefs map[string]any) (*rig, *fakeTables, *catalogLog) {
	r := newRig(t, route, prefs)
	tables, log := &fakeTables{}, &catalogLog{}
	r.agent.Tables, r.agent.Coach, r.agent.Stats = tables, tables, tables
	r.agent.Catalog, r.agent.Studio = fakeCatalog{log: log}, &fakeStudio{}
	r.agent.PublicOrigin = "https://play.example"
	return r, tables, log
}

func (r *rig) sys() string { return r.fake.systemPrompt() }

func wantCard(t *testing.T, meta map[string]any, kind, id string) {
	t.Helper()
	for _, c := range cards(meta) {
		if c["kind"] == kind && (c["table_id"] == id || c["goal_id"] == id || c["game_id"] == id) {
			return
		}
	}
	t.Fatalf("no %s card %s in %v", kind, id, meta["cards"])
}

var lobby = TableInfo{ID: "tbl-lobby", Code: "LBY234", Name: "Sam's lobby", GameName: "Texas Hold'em", Status: "lobby", Seats: 4, OpenSeats: 2, IsHost: true, Seated: true}
var playing = TableInfo{ID: "tbl-play", Code: "PLY234", Name: "Sam's game", GameName: "Texas Hold'em", Status: "playing", Seats: 3, IsHost: true, Seated: true}
var finished = TableInfo{ID: "tbl-done", Code: "DNE234", Name: "Last night", GameName: "Texas Hold'em", Status: "finished", Seats: 3, IsHost: true, Seated: true}

// ── Play ──

func TestGameNightSkillSeatsAgentsKeepsSeatsForFriendsAndHandsOutTheLink(t *testing.T) {
	r, tables, _ := fullRig(t, map[string]any{"intent": "play", "confidence": 0.9, "game_id": "holdem", "agent_ids": []string{"mika"}, "friends": 2,
		"options": map[string]any{"big_blind": 20, "difficulty": "shark", "turn_seconds": 60}}, nil)
	meta := r.turn(t, "set up a game night with Mika and two friends, shark bots, 60s clock")
	req := tables.got[0]
	if req.Seats != 4 || strings.Join(req.AgentIDs, ",") != "mika" || req.Settings.Difficulty != "shark" || req.Settings.TurnSeconds == nil || *req.Settings.TurnSeconds != 60 {
		t.Fatalf("request %+v", req)
	}
	if _, leaked := req.Options["difficulty"]; leaked || req.Options["big_blind"] == nil {
		t.Fatalf("table settings must not reach the game options: %v", req.Options)
	}
	wantCard(t, meta, "table", "tbl-1")
	if sys := r.sys(); !strings.Contains(sys, "K7M2QX") || !strings.Contains(sys, "https://play.example/join/K7M2QX") || !strings.Contains(sys, "shark difficulty") {
		t.Fatal("the reply was not given the invite code, link and settings")
	}
}

func TestInviteGivesTheCodeAndLinkOfTheirTable(t *testing.T) {
	r, tables, _ := fullRig(t, map[string]any{"intent": "invite", "confidence": 0.9}, nil)
	tables.tables = []TableInfo{playing, lobby}
	meta := r.turn(t, "invite my friends")
	wantCard(t, meta, "table", "tbl-lobby")
	if !strings.Contains(r.sys(), "https://play.example/join/LBY234") {
		t.Fatal("no invite link")
	}
	tables.tables = nil
	meta = r.turn(t, "invite my friends")
	if meta["cards"] != nil || !strings.Contains(r.sys(), "no open table") {
		t.Fatalf("invite without a table: %v", meta)
	}
}

func TestJoinTableByCode(t *testing.T) {
	r, tables, _ := fullRig(t, map[string]any{"intent": "join_table", "confidence": 0.9, "code": ""}, nil)
	tables.tables = []TableInfo{{ID: "tbl-friend", Code: "QW3ERT", Name: "Kim's table", Status: "lobby", Seats: 4, OpenSeats: 1}}
	meta := r.turn(t, "join QW3ERT please")
	if c := tables.called(); len(c) != 1 || c[0] != "join:QW3ERT" {
		t.Fatalf("calls %v", c)
	}
	wantCard(t, meta, "table", "tbl-friend")
	r.turn(t, "join my friend's table")
	if c := tables.called(); len(c) != 1 || !strings.Contains(r.sys(), "did not give a valid invite code") {
		t.Fatalf("joined without a code: %v", c)
	}
	r.fake.setRoute(map[string]any{"intent": "join_table", "confidence": 0.9, "code": "zzzzzz"})
	r.turn(t, "join zzzzzz")
	if !strings.Contains(r.sys(), "No live table has the invite code ZZZZZZ") {
		t.Fatal("unknown code not explained")
	}
}

func TestListResumeRematchAndBack(t *testing.T) {
	r, tables, _ := fullRig(t, map[string]any{"intent": "list_tables", "confidence": 0.9}, nil)
	away := playing
	away.Away, away.Paused = true, true
	tables.tables = []TableInfo{away, lobby, finished}
	meta := r.turn(t, "show my tables")
	if c := cards(meta); len(c) != 2 || !strings.Contains(r.sys(), "Sam's lobby") || strings.Contains(r.sys(), "Last night") {
		t.Fatalf("open tables only: %v", meta["cards"])
	}
	r.fake.setRoute(map[string]any{"intent": "resume_table", "confidence": 0.9})
	meta = r.turn(t, "resume my last game")
	wantCard(t, meta, "table", "tbl-play")
	r.fake.setRoute(map[string]any{"intent": "im_back", "confidence": 0.9})
	r.turn(t, "I'm back")
	// The router names the open table; the finished one is what "again" means.
	r.fake.setRoute(map[string]any{"intent": "rematch", "confidence": 0.9, "table_id": "tbl-play"})
	meta = r.turn(t, "rematch!")
	wantCard(t, meta, "table", "tbl-rematch")
	if c := tables.called(); strings.Join(c, ",") != "back:tbl-play,back:tbl-play,rematch:tbl-done" {
		t.Fatalf("calls %v", c)
	}
}

func TestLeavingATableNeedsAnExplicitYes(t *testing.T) {
	r, tables, _ := fullRig(t, map[string]any{"intent": "leave_table", "confidence": 0.9}, nil)
	tables.tables = []TableInfo{playing}
	meta := r.turn(t, "I'm leaving the table")
	if meta["pending"] == nil || len(tables.called()) != 0 || !strings.Contains(r.sys(), "say yes to confirm") {
		t.Fatalf("left without asking: %v %v", meta, tables.called())
	}
	meta = r.turn(t, "no, stay")
	if len(tables.called()) != 0 || meta["intent"] != "confirm" || meta["action"] != "no" {
		t.Fatalf("a no left the table: %v", tables.called())
	}
	r.turn(t, "I'm leaving the table")
	r.turn(t, "yes")
	if c := tables.called(); len(c) != 1 || c[0] != "leave:tbl-play" {
		t.Fatalf("calls %v", c)
	}
}

// ── Help at the table ──

func TestCoachAsksForTheCallersOwnSeatOnly(t *testing.T) {
	r, tables, _ := fullRig(t, map[string]any{"intent": "coach", "confidence": 0.9}, nil)
	tables.tables = []TableInfo{lobby, playing}
	tables.advice = CoachAdvice{TableID: "tbl-play", GameName: "Texas Hold'em", YourTurn: true, Legal: []string{"Fold", "Call 40"},
		YourView: `{"players":[{"cards":["Ah","Kd"]},{"cards":[]}]}`,
		Holdem:   &CoachTip{HandName: "high card", Equity: 0.41, PotOdds: 0.25, Opponents: 2, ToCall: 40, Suggestion: "Calling is profitable."}}
	meta := r.turn(t, "what should I do?")
	if c := tables.called(); len(c) != 1 || c[0] != "advice:"+r.uid+":tbl-play" {
		t.Fatalf("advice must be asked for the caller at the table they play: %v", c)
	}
	sys := r.sys()
	if !strings.Contains(sys, "equity about 41%") || !strings.Contains(sys, "Calling is profitable") || !strings.Contains(sys, "THEIR OWN seat only") ||
		!strings.Contains(sys, "Never claim to know or guess other players' hidden cards") {
		t.Fatal("coaching brief incomplete")
	}
	wantCard(t, meta, "table", "tbl-play")

	tables.tables = []TableInfo{lobby}
	r.turn(t, "what should I do?")
	if len(tables.called()) != 1 || !strings.Contains(r.sys(), "not playing at a table") {
		t.Fatal("coached without a game in progress")
	}
}

// ── Agents ──

func TestAgentsInfoAndFavourites(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "agents_info", "confidence": 0.9, "agent_ids": []string{"mika"}}, nil)
	r.turn(t, "tell me about Mika")
	if sys := r.sys(); !strings.Contains(sys, "all-in button") || strings.Contains(sys, "Ren folds most hands") {
		t.Fatal("Mika's profile (only) should reach the reply")
	}
	r.fake.setRoute(map[string]any{"intent": "favorite_agents", "confidence": 0.9, "action": "add", "agent_ids": []string{"nova", "lin"}})
	r.turn(t, "make Nova and Lin favourites")
	r.fake.setRoute(map[string]any{"intent": "favorite_agents", "confidence": 0.9, "action": "remove", "agent_ids": []string{"lin"}})
	r.turn(t, "remove Lin")
	var raw []byte
	_ = r.pool.QueryRow(context.Background(), `SELECT data->'favorite_agents' FROM user_preferences WHERE user_id=$1`, r.uid).Scan(&raw)
	if string(raw) != `["nova"]` {
		t.Fatalf("favourites %s", raw)
	}
}

// ── Games ──

func TestRecommendListAndGameInfo(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "recommend_game", "confidence": 0.9, "game_id": "star-race", "seats": 4}, nil)
	meta := r.turn(t, "what should four of us play?")
	wantCard(t, meta, "game", "star-race")
	r.fake.setRoute(map[string]any{"intent": "recommend_game", "confidence": 0.9, "game_id": "dragon-chess", "seats": 4})
	if meta = r.turn(t, "what should four of us play?"); meta["cards"] != nil {
		t.Fatal("recommended a 2-player game for 4")
	}
	r.fake.setRoute(map[string]any{"intent": "list_games", "confidence": 0.9, "action": "mine"})
	meta = r.turn(t, "show my games")
	if c := cards(meta); len(c) != 2 || strings.Contains(r.sys(), "Star Race") {
		t.Fatalf("my games: %v", meta["cards"])
	}
	r.fake.setRoute(map[string]any{"intent": "game_info", "confidence": 0.9, "game_id": "star-race"})
	meta = r.turn(t, "tell me about Star Race")
	wantCard(t, meta, "game", "star-race")
	if !strings.Contains(r.sys(), "by Kim") {
		t.Fatal("game details missing")
	}
}

func TestVisibilityPublicNeedsConfirmationPrivateDoesNot(t *testing.T) {
	r, _, log := fullRig(t, map[string]any{"intent": "game_visibility", "confidence": 0.9, "game_id": "dragon-chess", "visibility": "unlisted"}, nil)
	r.turn(t, "share Dragon Chess by link")
	if c := log.all(); len(c) != 1 || c[0] != "visibility:dragon-chess:unlisted" || !strings.Contains(r.sys(), "https://play.example/app/games/dragon-chess") {
		t.Fatalf("calls %v", c)
	}
	r.fake.setRoute(map[string]any{"intent": "game_visibility", "confidence": 0.9, "game_id": "dragon-chess", "visibility": "public"})
	meta := r.turn(t, "make Dragon Chess public")
	if len(log.all()) != 1 || meta["pending"] == nil {
		t.Fatal("made public without asking")
	}
	meta = r.turn(t, "Yes!")
	if c := log.all(); len(c) != 2 || c[1] != "visibility:dragon-chess:public" {
		t.Fatalf("calls %v", c)
	}
	wantCard(t, meta, "game", "dragon-chess")
	// Someone else's game: refused before anything is asked.
	r.fake.setRoute(map[string]any{"intent": "game_visibility", "confidence": 0.9, "game_id": "star-race", "visibility": "private"})
	meta = r.turn(t, "make Star Race private")
	if len(log.all()) != 2 || meta["pending"] != nil || !strings.Contains(r.sys(), "not one of their own games") {
		t.Fatal("changed someone else's game")
	}
}

func TestDeleteDraftNeedsYesAndPublishedGamesAreKept(t *testing.T) {
	r, _, log := fullRig(t, map[string]any{"intent": "delete_game", "confidence": 0.9, "game_id": "dragon-chess"}, nil)
	meta := r.turn(t, "delete my Dragon Chess draft")
	p, _ := meta["pending"].(map[string]any)
	if p["action"] != "delete_game" || len(log.all()) != 0 || !strings.Contains(r.sys(), `delete the draft "Dragon Chess"`) {
		t.Fatalf("pending %v calls %v", p, log.all())
	}
	r.turn(t, "好的，删掉吧")
	if c := log.all(); len(c) != 1 || c[0] != "delete:dragon-chess" {
		t.Fatalf("calls %v", c)
	}
	// A second yes finds nothing pending.
	r.fake.setRoute(map[string]any{"intent": "chat", "confidence": 0.9})
	r.turn(t, "yes")
	if len(log.all()) != 1 {
		t.Fatal("a stale confirmation ran twice")
	}
	r.fake.setRoute(map[string]any{"intent": "delete_game", "confidence": 0.9, "game_id": "moon-tiles"})
	meta = r.turn(t, "delete Moon Tiles")
	if meta["pending"] != nil || !strings.Contains(r.sys(), "published games are not deleted") {
		t.Fatal("offered to delete a published game")
	}
}

func TestAnythingButYesDropsThePendingConfirmation(t *testing.T) {
	r, _, log := fullRig(t, map[string]any{"intent": "delete_game", "confidence": 0.9, "game_id": "dragon-chess"}, nil)
	r.turn(t, "delete my Dragon Chess draft")
	r.fake.setRoute(map[string]any{"intent": "list_games", "confidence": 0.9, "action": "mine"})
	meta := r.turn(t, "hmm, which games do I have again?")
	if meta["intent"] != "list_games" {
		t.Fatalf("a question was taken as an answer: %v", meta)
	}
	r.fake.setRoute(map[string]any{"intent": "chat", "confidence": 0.9})
	r.turn(t, "yes")
	if len(log.all()) != 0 {
		t.Fatal("deleted on a yes that did not answer the question")
	}
	// An expired confirmation does not run either.
	r.fake.setRoute(map[string]any{"intent": "delete_game", "confidence": 0.9, "game_id": "dragon-chess"})
	r.turn(t, "delete my Dragon Chess draft")
	_, _ = r.pool.Exec(context.Background(), `UPDATE messages SET meta = jsonb_set(meta, '{pending,at}', to_jsonb($2::text))
		WHERE id = (SELECT max(id) FROM messages WHERE conversation_id=$1 AND role='assistant')`, r.conv, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	r.turn(t, "yes")
	if len(log.all()) != 0 {
		t.Fatal("an expired confirmation ran")
	}
}

func TestRenameGame(t *testing.T) {
	r, _, log := fullRig(t, map[string]any{"intent": "rename_game", "confidence": 0.9, "game_id": "dragon-chess", "name": "Wyrm Wars"}, nil)
	meta := r.turn(t, "rename Dragon Chess to Wyrm Wars")
	if c := log.all(); len(c) != 1 || c[0] != "rename:dragon-chess:Wyrm Wars" {
		t.Fatalf("calls %v", c)
	}
	wantCard(t, meta, "game", "dragon-chess")
}

// ── Studio ──

func newGoal(t *testing.T, r *rig, uid, title string) *engine.Goal {
	t.Helper()
	g := &engine.Goal{UserID: uid, ConversationID: r.conv, Title: title, Objective: "x", Domain: "studio", Skill: "general-task", Language: "en"}
	if uid != r.uid {
		g.ConversationID = ""
	}
	if err := r.agent.Store.CreateGoal(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestControlPauseResumeAndWhy(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "control", "confidence": 0.9, "action": "pause"}, nil)
	g := newGoal(t, r, r.uid, "Dragon Chess")
	ctx := context.Background()
	_ = r.agent.Store.SetGoalStatus(ctx, g.ID, "active", "test")
	meta := r.turn(t, "pause the build")
	if st, _ := r.agent.Store.Goal(ctx, g.ID); st.Status != "paused" {
		t.Fatalf("status %s", st.Status)
	}
	wantCard(t, meta, "mission", g.ID)
	r.fake.setRoute(map[string]any{"intent": "control", "confidence": 0.9, "action": "resume"})
	r.turn(t, "resume it")
	if st, _ := r.agent.Store.Goal(ctx, g.ID); st.Status != "active" {
		t.Fatalf("status %s", st.Status)
	}
	_, _ = r.pool.Exec(ctx, `UPDATE tasks SET status='failed', error='playtest: 12 of 200 games never ended' WHERE goal_id=$1`, g.ID)
	_ = r.agent.Store.SetGoalStatus(ctx, g.ID, "needs_attention", "four revision rounds did not pass the playtest")
	r.fake.setRoute(map[string]any{"intent": "control", "confidence": 0.9, "action": "why"})
	r.turn(t, "why did it stop?")
	if sys := r.sys(); !strings.Contains(sys, "never ended") || !strings.Contains(sys, "four revision rounds") {
		t.Fatal("the reasons did not reach the reply")
	}
}

func approvalFor(t *testing.T, r *rig, g *engine.Goal) string {
	t.Helper()
	var id string
	err := r.pool.QueryRow(context.Background(), `INSERT INTO approvals (goal_id, task_id, user_id, tool, args, call_id, preview)
		SELECT $1, id, $2, 'publish_game', '{}', 'c1', 'Publish Dragon Chess v3 to the shelf' FROM tasks WHERE goal_id=$1 LIMIT 1 RETURNING id`, g.ID, g.UserID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func approvalStatus(r *rig, id string) string {
	var s string
	_ = r.pool.QueryRow(context.Background(), `SELECT status FROM approvals WHERE id=$1`, id).Scan(&s)
	return s
}

func TestApprovePublishFromChatGoesThroughDecideAfterAYes(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "approve_publish", "confidence": 0.9, "action": "approve"}, nil)
	// Someone else's approval is invisible to this player.
	other, _ := newPlayer(t, r.pool, nil)
	theirs := approvalFor(t, r, newGoal(t, r, other, "Not yours"))
	meta := r.turn(t, "approve it")
	if meta["pending"] != nil || !strings.Contains(r.sys(), "nothing is waiting for their approval") {
		t.Fatalf("offered someone else's approval: %v", meta)
	}
	g := newGoal(t, r, r.uid, "Dragon Chess")
	mine := approvalFor(t, r, g)
	meta = r.turn(t, "approve it")
	if approvalStatus(r, mine) != "pending" || meta["pending"] == nil || !strings.Contains(r.sys(), "Publish Dragon Chess v3") {
		t.Fatal("approved without confirmation")
	}
	meta = r.turn(t, "yes")
	if approvalStatus(r, mine) != "approved" || approvalStatus(r, theirs) != "pending" {
		t.Fatalf("mine %s theirs %s", approvalStatus(r, mine), approvalStatus(r, theirs))
	}
	var by string
	_ = r.pool.QueryRow(context.Background(), `SELECT decided_by FROM approvals WHERE id=$1`, mine).Scan(&by)
	var task string
	_ = r.pool.QueryRow(context.Background(), `SELECT data->>'decision' FROM events WHERE goal_id=$1 AND type='approval.decided'`, g.ID).Scan(&task)
	if by != r.uid || task != "approved" {
		t.Fatalf("decided_by %s event %s: not the engine's Decide path", by, task)
	}
	wantCard(t, meta, "mission", g.ID)

	// Declining needs no confirmation.
	again := approvalFor(t, r, g)
	r.fake.setRoute(map[string]any{"intent": "approve_publish", "confidence": 0.9, "action": "decline"})
	r.turn(t, "not yet")
	if approvalStatus(r, again) != "rejected" {
		t.Fatalf("decline: %s", approvalStatus(r, again))
	}
}

// ── Account and settings ──

func TestSeveralPreferencesDisplayNameAndReset(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "preference", "confidence": 0.9, "preferences": []map[string]any{
		{"key": "sound", "value": "off"}, {"key": "theme", "value": "light"}, {"key": "felt", "value": "purple"}, {"key": "display_name", "value": "Captain Sam"}}}, map[string]any{"aoi_talk": "quiet"})
	r.turn(t, "sound off, light theme, purple felt, and call me Captain Sam on my profile")
	var data []byte
	_ = r.pool.QueryRow(context.Background(), `SELECT data FROM user_preferences WHERE user_id=$1`, r.uid).Scan(&data)
	var p map[string]any
	_ = json.Unmarshal(data, &p)
	var name string
	_ = r.pool.QueryRow(context.Background(), `SELECT name FROM users WHERE id=$1`, r.uid).Scan(&name)
	if p["sound"] != false || p["theme"] != "light" || p["felt"] != nil || name != "Captain Sam" {
		t.Fatalf("prefs %v name %q", p, name)
	}
	if !strings.Contains(r.sys(), "felt must be one of") {
		t.Fatal("the invalid value was not reported")
	}
	r.fake.setRoute(map[string]any{"intent": "preference", "confidence": 0.9, "preference": map[string]any{"key": "aoi_talk", "value": "default"}})
	r.turn(t, "reset how much you talk")
	_ = r.pool.QueryRow(context.Background(), `SELECT data FROM user_preferences WHERE user_id=$1`, r.uid).Scan(&data)
	if strings.Contains(string(data), "aoi_talk") {
		t.Fatalf("not reset: %s", data)
	}
	r.fake.setRoute(map[string]any{"intent": "show_settings", "confidence": 0.9})
	r.turn(t, "what are my settings?")
	if sys := r.sys(); !strings.Contains(sys, "theme=light") || !strings.Contains(sys, "turn_seconds=30") {
		t.Fatal("settings not shown")
	}
}

func TestEveryChatSettingCanBeSetFromChat(t *testing.T) {
	for _, s := range PrefSpecs {
		if s.Key == "favorite_agents" {
			continue // its own intent
		}
		if !s.Chat {
			t.Errorf("%s cannot be changed from chat", s.Key)
		}
	}
}

// ── History ──

func TestStatsReachTheReply(t *testing.T) {
	r, tables, _ := fullRig(t, map[string]any{"intent": "my_stats", "confidence": 0.9}, nil)
	tables.stats = PlayerStats{Played: 7, Wins: 3, ChipsWon: 1250, ByGame: map[string]int{"Texas Hold'em": 6, "Star Race": 1},
		Recent: []GameResult{{GameName: "Texas Hold'em", Place: 1, Players: 4, Chips: 900, HasChips: true, FinishedAt: time.Now()}}}
	r.turn(t, "how many chips have I won?")
	if sys := r.sys(); !strings.Contains(sys, "7 played, 3 won") || !strings.Contains(sys, "+1250") || !strings.Contains(sys, "play money") {
		t.Fatal("stats missing")
	}
}

// ── Support ──

func TestMemoryShowForgetAndForgetAll(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "memory", "confidence": 0.9, "action": "show"}, nil)
	ctx := context.Background()
	for _, m := range []string{"loves bluffing", "plays hold'em on Fridays", "favourite opponent is Ren"} {
		_, _ = r.pool.Exec(ctx, `INSERT INTO memories (user_id, content) VALUES ($1,$2)`, r.uid, m)
	}
	r.turn(t, "what do you remember about me?")
	if !strings.Contains(r.sys(), "loves bluffing") {
		t.Fatal("memories not listed")
	}
	r.fake.setRoute(map[string]any{"intent": "memory", "confidence": 0.9, "action": "forget", "query": "bluffing"})
	r.turn(t, "forget that I like bluffing")
	var n int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM memories WHERE user_id=$1`, r.uid).Scan(&n)
	if n != 2 {
		t.Fatalf("%d memories left", n)
	}
	r.fake.setRoute(map[string]any{"intent": "memory", "confidence": 0.9, "action": "forget_all"})
	meta := r.turn(t, "forget everything")
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM memories WHERE user_id=$1`, r.uid).Scan(&n)
	if n != 2 || meta["pending"] == nil {
		t.Fatal("forgot everything without asking")
	}
	r.turn(t, "はい")
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM memories WHERE user_id=$1`, r.uid).Scan(&n)
	if n != 0 {
		t.Fatalf("%d memories left", n)
	}
}

func TestAccountDeletionIsNeverDoneFromChat(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "delete_account", "confidence": 1}, nil)
	meta := r.turn(t, "delete my account")
	r.turn(t, "yes")
	var n int
	_ = r.pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE id=$1`, r.uid).Scan(&n)
	if n != 1 || meta["pending"] != nil || !strings.Contains(r.sys(), "Settings → Account → Delete account") {
		t.Fatal("account deletion must only be explained")
	}
}

func TestReportProblemIsRecordedForTheTeam(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "report_problem", "confidence": 0.9, "query": "table frozen"}, nil)
	r.turn(t, "the table is frozen")
	var text string
	_ = r.pool.QueryRow(context.Background(), `SELECT data->>'text' FROM events WHERE user_id=$1 AND type='support.report'`, r.uid).Scan(&text)
	if text != "table frozen" {
		t.Fatalf("report %q", text)
	}
}

func TestCapabilitiesAndSafetyIntentsActOnNothing(t *testing.T) {
	for intent, want := range map[string]string{
		"capabilities": "what you can do", "real_money": "play money only", "cheat": "cannot see other seats' hidden cards", "out_of_scope": "outside what you do",
	} {
		r, tables, log := fullRig(t, map[string]any{"intent": intent, "confidence": 0.9}, nil)
		r.turn(t, "…")
		if !strings.Contains(r.sys(), want) || len(tables.got)+len(tables.called())+len(log.all()) != 0 {
			t.Errorf("%s: brief or side effects wrong", intent)
		}
	}
}

// ── Confirmation answers ──

func TestConfirmAnswer(t *testing.T) {
	cases := map[string]answer{
		"yes": answerYes, "Yes!": answerYes, "yes, delete it": answerYes, "ok": answerYes, "go ahead": answerYes, "sure thing": answerYes,
		"好": answerYes, "好的，删掉吧": answerYes, "确认": answerYes, "네": answerYes, "네, 삭제해 줘": answerYes, "はい": answerYes, "うん、お願い": answerYes,
		"no": answerNo, "nope": answerNo, "cancel": answerNo, "不要": answerNo, "算了": answerNo, "아니요": answerNo, "いいえ": answerNo, "やめて": answerNo,
		"yes but wait": answerOther, "确定不要": answerOther, "what games do I have?": answerOther, "maybe": answerOther, "": answerOther,
		"yesterday was fun": answerOther, "okay so what's next, show me something cool and new to play tonight with friends": answerOther,
	}
	for text, want := range cases {
		if got := confirmAnswer(text); got != want {
			t.Errorf("confirmAnswer(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestEveryIntentHasAHandler(t *testing.T) {
	// act's default branch is chat: every other intent must be dispatched.
	r, _, _ := fullRig(t, map[string]any{"intent": "chat", "confidence": 1}, nil)
	for _, in := range intents {
		if in == IntentChat {
			continue
		}
		out := r.agent.act(context.Background(), &turn{u: User{ID: r.uid, Name: "Sam", Lang: "en"}, convID: r.conv, text: "x", lang: "en",
			r: Route{Intent: in, Confidence: 1}, games: fakeGames, prefs: map[string]any{}})
		if out.note == "" {
			t.Errorf("intent %s has no handler (empty brief)", in)
		}
	}
	if !slices.Contains(intents, IntentCoach) || fmt.Sprint(len(intents)) == "0" {
		t.Fatal("intent set")
	}
}

func TestRouteNormalize(t *testing.T) {
	r := Route{Intent: "play", Action: "leave_table"}
	r.normalize()
	if r.Intent != IntentLeaveTable || r.Action != "" {
		t.Fatalf("%+v", r)
	}
	r = Route{Intent: "control", Action: "Pause", Preference: prefKV{Key: "sound", Value: "off"}, Prefs: []prefKV{{Key: "Theme", Value: "light"}, {Key: "sound", Value: "on"}}}
	r.normalize()
	if r.Intent != IntentControl || r.Action != "pause" || len(r.Prefs) != 2 || r.Prefs[0].Value != "off" || r.Prefs[1].Key != "theme" {
		t.Fatalf("%+v", r)
	}
	r = Route{Intent: "teleport"}
	r.normalize()
	if r.Intent != IntentChat {
		t.Fatalf("%+v", r)
	}
}

func TestReviseFindsTheirGameNamedInTheMessageWhenTheRouterLeftItOut(t *testing.T) {
	r, _, _ := fullRig(t, map[string]any{"intent": "revise_game", "confidence": 0.95, "prompt": "give it a custom layout"}, nil)
	studio := r.agent.Studio.(*fakeStudio)
	meta := r.turn(t, "Revise Moon Tiles: give it a custom layout with the board in the middle")
	if studio.base != "moon-tiles" {
		t.Fatalf("base %q, meta %v", studio.base, meta)
	}
	// A router guess that is not theirs falls back to the name in the message too.
	studio.base = ""
	r.fake.setRoute(map[string]any{"intent": "revise_game", "confidence": 0.95, "game_id": "star-race", "prompt": "bigger board"})
	r.turn(t, "make the board bigger in Dragon Chess")
	if studio.base != "dragon-chess" {
		t.Fatalf("base %q", studio.base)
	}
	// Someone else's game named in the message is not revised.
	studio.base = ""
	r.fake.setRoute(map[string]any{"intent": "revise_game", "confidence": 0.95})
	r.turn(t, "change Star Race to 6 players")
	if studio.base != "" {
		t.Fatalf("revised someone else's game: %q", studio.base)
	}
}
