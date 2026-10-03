package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// Sink receives the streamed turn: one or more Meta events (intent, mood,
// cards — later ones refine earlier ones) and the reply text in deltas.
type Sink interface {
	Meta(v map[string]any)
	Delta(s string)
}

type User struct {
	ID, Email, Name string
	Lang            string // the language the player chose in settings
}

// Intents the router chooses from.
const (
	IntentChat       = "chat"
	IntentPlay       = "play"
	IntentBuild      = "build_game"
	IntentRevise     = "revise_game"
	IntentRules      = "rules"
	IntentControl    = "control"
	IntentPreference = "preference"
)

var intents = []string{IntentChat, IntentPlay, IntentBuild, IntentRevise, IntentRules, IntentControl, IntentPreference}

// Card is an item the app renders under Aoi's message (meta.cards).
type Card struct {
	Kind    string `json:"kind"` // table | mission | game
	TableID string `json:"table_id,omitempty"`
	GoalID  string `json:"goal_id,omitempty"`
	GameID  string `json:"game_id,omitempty"`
}

// Route is the router's reading of one message.
type Route struct {
	Intent     string         `json:"intent"`
	Confidence float64        `json:"confidence"`
	Clarify    string         `json:"clarify"`
	Mood       string         `json:"mood"`
	Title      string         `json:"title"`
	GameID     string         `json:"game_id"`
	AgentIDs   strList        `json:"agent_ids"`
	Seats      flexInt        `json:"seats"`
	Options    map[string]any `json:"options"`
	Prompt     string         `json:"prompt"`
	BaseGameID string         `json:"base_game_id"`
	GoalID     string         `json:"goal_id"`
	Action     string         `json:"action"`
	Preference struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	} `json:"preference"`
}

// outcome is what acting on a route produced, for the reply and its meta.
type outcome struct {
	note   string // what happened, for the reply prompt ("FOR THIS REPLY")
	mood   persona.Mood
	cards  []Card
	goalID string
	lang   string // set when the player just changed their language
}

// Turn handles one player message end to end. Idempotent on clientMsgID: a
// retried POST returns the reply that was already produced.
func (a *Agent) Turn(ctx context.Context, u User, convID, text, clientMsgID string, sink Sink) (int64, error) {
	lang := DetectLang(text, u.Lang)
	userMsgID, dup, err := a.insertUserMessage(ctx, convID, text, clientMsgID)
	if err != nil {
		return 0, err
	}
	if dup {
		var id int64
		var content string
		var meta []byte
		err := a.Store.Pool.QueryRow(ctx, `SELECT id, content, meta FROM messages WHERE conversation_id=$1 AND role='assistant' AND id > $2 ORDER BY id LIMIT 1`,
			convID, userMsgID).Scan(&id, &content, &meta)
		if err == nil {
			m := map[string]any{}
			_ = json.Unmarshal(meta, &m)
			m["duplicate"] = true
			sink.Meta(m)
			sink.Delta(content)
			return id, nil
		}
	}
	_, _ = a.Store.Pool.Exec(ctx, `UPDATE conversations SET updated_at=now() WHERE id=$1`, convID)

	prefs := a.prefs(ctx, u.ID)
	p := persona.PrefsFrom(prefs)
	games := a.games(ctx, u.ID)
	open := a.openMissions(ctx, u.ID)

	route := a.route(ctx, u, convID, text, lang, games, open)
	out := a.act(ctx, u, convID, text, lang, route, games, open)
	if out.lang != "" {
		lang = out.lang
	}
	if route.Intent == IntentPreference {
		// "Call me Captain" should already be honoured in this very reply.
		p = persona.PrefsFrom(a.prefs(ctx, u.ID))
	}

	meta := map[string]any{"intent": route.Intent, "confidence": route.Confidence, "mood": out.mood}
	if len(out.cards) > 0 {
		meta["cards"] = out.cards
	}
	if out.goalID != "" {
		meta["goal_id"] = out.goalID
	}
	sink.Meta(meta)

	sys := persona.ChatSystem(lang, p, u.Name, time.Now(), a.chatContext(ctx, u, p, open, out.note))
	msgs := []llm.Message{{Role: "system", Content: sys}}
	msgs = append(msgs, a.history(ctx, convID, userMsgID, 14)...)
	msgs = append(msgs, llm.Message{Role: "user", Content: text})
	var reply strings.Builder
	_, err = a.Model.Stream(ctx, engine.CallMeta{Purpose: "chat", UserID: u.ID}, llm.Request{Model: a.LLM, Messages: msgs, Temperature: 0.7},
		func(d string) { reply.WriteString(d); sink.Delta(d) })
	if err != nil {
		var budget *engine.ErrBudget
		fallback := map[string]string{
			"en": "Ah — I can't reach my thinking right now. Your message is saved, and anything already started keeps going in the background. Try me again in a minute?",
			"zh": "啊，我这边暂时连不上思考服务。你的消息已经保存，已经开始的事情会在后台继续。过一分钟再叫我好吗？",
			"ko": "앗, 지금은 생각하는 쪽에 연결이 안 되네요. 메시지는 저장됐고, 이미 시작한 일은 뒤에서 계속 진행돼요. 1분 뒤에 다시 불러 줄래요?",
			"ja": "あっ、今ちょっと考える回路につながらないみたい。メッセージは保存したし、始まってることは裏でちゃんと進んでるよ。1分くらいしたら、また呼んでくれる？"}[lang]
		if errors.As(err, &budget) {
			fallback = map[string]string{
				"en": "We've hit today's limit for your account (" + budget.Reason + "). Your message is saved — the tables still work, I just can't chat until it resets.",
				"zh": "你的账户今天的用量到上限了（" + budget.Reason + "）。消息已保存——牌桌照常可以玩，只是我要等重置后才能聊天。",
				"ko": "오늘 계정 사용 한도에 도달했어요 (" + budget.Reason + "). 메시지는 저장됐어요 — 테이블은 그대로 플레이할 수 있고, 저는 초기화된 뒤에 다시 이야기할 수 있어요.",
				"ja": "今日のアカウントの利用上限に達しちゃった（" + budget.Reason + "）。メッセージは保存してあるよ。テーブルはそのまま遊べるけど、私とのおしゃべりはリセットまでお休みね。"}[lang]
		}
		slog.Error("chat stream", "err", err)
		if reply.Len() > 0 {
			fallback = "\n\n" + fallback
		}
		sink.Delta(fallback)
		reply.WriteString(fallback)
		meta["error"], meta["mood"] = true, persona.Sad
		sink.Meta(map[string]any{"error": true, "mood": persona.Sad})
	}
	metaRaw, _ := json.Marshal(meta)
	var replyID int64
	if err := a.Store.Pool.QueryRow(context.WithoutCancel(ctx), `INSERT INTO messages (conversation_id, role, content, meta) VALUES ($1,'assistant',$2,$3) RETURNING id`,
		convID, reply.String(), metaRaw).Scan(&replyID); err != nil {
		return 0, err
	}
	go a.afterTurn(context.WithoutCancel(ctx), u, p, convID, text, route)
	return replyID, nil
}

func (a *Agent) insertUserMessage(ctx context.Context, convID, text, clientMsgID string) (int64, bool, error) {
	var id int64
	var cm any
	if clientMsgID != "" {
		cm = clientMsgID
	}
	err := a.Store.Pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, role, content, client_msg_id) VALUES ($1,'user',$2,$3)
		ON CONFLICT (conversation_id, client_msg_id) WHERE client_msg_id IS NOT NULL DO NOTHING RETURNING id`, convID, text, cm).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = a.Store.Pool.QueryRow(ctx, `SELECT id FROM messages WHERE conversation_id=$1 AND client_msg_id=$2`, convID, clientMsgID).Scan(&id)
		return id, true, err
	}
	return id, false, err
}

func (a *Agent) prefs(ctx context.Context, userID string) map[string]any {
	c, err := a.Store.Client(ctx, userID)
	if err != nil || c == nil {
		return map[string]any{}
	}
	return c.Prefs
}

// games is what the player can play: the catalog, or the built-ins when no
// catalog is wired. A catalog error degrades to the built-ins too — the
// router still knows hold'em.
func (a *Agent) games(ctx context.Context, userID string) []GameInfo {
	if a.Catalog == nil {
		return builtinGames
	}
	gs, err := a.Catalog.Games(ctx, userID)
	if err != nil {
		slog.Warn("catalog", "err", err)
		return builtinGames
	}
	return gs
}

// openMissions are the player's goals that can still change.
func (a *Agent) openMissions(ctx context.Context, userID string) []*engine.Goal {
	gs, _ := a.Store.Goals(ctx, userID, 10)
	var out []*engine.Goal
	for _, g := range gs {
		if !terminal(g.Status) {
			out = append(out, g)
		}
	}
	return out
}

func terminal(status string) bool {
	return status == "completed" || status == "cancelled" || status == "failed"
}

// ── Routing ────────────────────────────────────────────────────────────────

func (a *Agent) route(ctx context.Context, u User, convID, text, lang string, games []GameInfo, open []*engine.Goal) Route {
	var roster strings.Builder
	for _, r := range Roster {
		fmt.Fprintf(&roster, "- %s: %s / %s / %s / %s (%s)\n", r.ID, r.Name, r.NameZH, r.NameKO, r.NameJA, r.Style)
	}
	var catalog strings.Builder
	for i, g := range games {
		if i >= 40 {
			break
		}
		mine := ""
		if g.Mine {
			mine = " [theirs]"
		}
		fmt.Fprintf(&catalog, "- id=%s %q %d-%d seats%s: %s\n", g.ID, g.Name, g.MinSeats, g.MaxSeats, mine, truncate(g.Summary, 100))
	}
	var missions strings.Builder
	for _, g := range open {
		fmt.Fprintf(&missions, "- goal_id=%s status=%s title=%q\n", g.ID, g.Status, g.Title)
	}
	if missions.Len() == 0 {
		missions.WriteString("(none)\n")
	}
	prompt := fmt.Sprintf(`You route messages for Aoi, the AI host of "Play with Agents": play-money Texas Hold'em and custom board games with friends and AI agents. Players can also describe a new game and the studio agents build it.

Classify the player's LATEST MESSAGE into exactly one intent:
- play: they want to sit down and play now, start a table, be dealt in ("deal me in", "let's play hold'em with Mika", "开一桌", "홀덤 한 판 하자", "ポーカーやろう"). Fill game_id, agent_ids, seats, options.
- build_game: they describe a NEW game they want made, or ask for one to be made ("let's make a game where…", "can you build a chess variant…"). Fill prompt with the whole idea in their words, and base_game_id only if they want to start from an existing game.
- revise_game: they want to change a game that is being built or that they own ("change the build to 3 players", "make the board bigger", "把刚才那个游戏改成…", "만들던 게임 3인용으로 바꿔 줘", "さっきのゲームを3人用にして"). Fill goal_id (the open mission it refers to) or game_id (their game), and prompt with the change.
- rules: a question about how a game is played, its terms or strategy ("how do side pots work?", "what beats a flush?"). Fill game_id when it is about a game in the catalog.
- control: pause, resume, cancel or check on a running build ("stop the build", "how is my game coming along?"). action = pause|resume|cancel|status; goal_id.
- preference: they ask to change a setting or how Aoi treats them ("call me Captain", "be quieter", "speak Chinese", "日本語で話して", "한국어로 해 줘", "turn the sound off"). preference.key/value from: %s.
- chat: anything else — greetings, banter, questions about Aoi, talk about a hand they played, questions about real-money gambling (Aoi steers those back to play money).

Notes:
- "Deal me in" or "play" with no game named means holdem.
- agent_ids only from ROSTER ids; map names in any language (Mika/美香/미카/ミカ → mika). seats = total seats including the player, 0 if not stated.
- options: only what they state, as numbers (hold'em: starting_stack, small_blind, big_blind, blinds_double_every, max_hands).
- If they say "the build", "my game" or "it" and there is one OPEN MISSION, use its goal_id.
- mood is the face Aoi shows with her reply: neutral | smile | wink | surprised | angry | sad ("angry" only for playful mock outrage at a tease; "sad" for a disappointment).
- confidence 0..1. If below 0.5, write clarify: one short question in %s.

ROSTER:
%sCATALOG:
%sOPEN MISSIONS:
%sRECENT CONVERSATION:
%s

LATEST MESSAGE: %q

Return JSON only: {"intent":"","confidence":0,"clarify":"","mood":"","title":"3-6 word conversation title in %s","game_id":"","agent_ids":[],"seats":0,"options":{},"prompt":"","base_game_id":"","goal_id":"","action":"","preference":{"key":"","value":""}}`,
		chatPrefKeys(), persona.LangName(lang), roster.String(), catalog.String(), missions.String(),
		a.Store.RecentConversation(ctx, convID, 6), text, persona.LangName(lang))

	resp, err := a.Model.Chat(ctx, engine.CallMeta{Purpose: "route", UserID: u.ID}, llm.Request{
		Model: a.FastLLM, JSON: true, Temperature: 0, Messages: []llm.Message{{Role: "user", Content: prompt}}})
	var r Route
	if err == nil {
		err = json.Unmarshal([]byte(llm.ExtractJSON(resp.Message.Content)), &r)
	}
	if err != nil {
		slog.Warn("route failed; answering conversationally", "err", err)
		return Route{Intent: IntentChat, Confidence: 1}
	}
	r.Intent = strings.ToLower(strings.TrimSpace(r.Intent))
	if !contains(intents, r.Intent) {
		r.Intent = IntentChat
	}
	slog.Info("route", "intent", r.Intent, "confidence", r.Confidence, "game", r.GameID, "agents", r.AgentIDs, "goal_id", r.GoalID)
	return r
}

// ── Acting on the route ────────────────────────────────────────────────────

func (a *Agent) act(ctx context.Context, u User, convID, text, lang string, r Route, games []GameInfo, open []*engine.Goal) outcome {
	// An unsure router does not get to start things: ask instead.
	if r.Confidence < 0.5 && strings.TrimSpace(r.Clarify) != "" && r.Intent != IntentChat && r.Intent != IntentRules {
		return outcome{mood: persona.Neutral, note: "You are not sure what the player wants. Ask exactly this, in your own words: " + r.Clarify}
	}
	switch r.Intent {
	case IntentPlay:
		return a.play(ctx, u, lang, r, games)
	case IntentBuild:
		return a.build(ctx, u, convID, text, lang, r, games)
	case IntentRevise:
		return a.revise(ctx, u, convID, text, lang, r, games, open)
	case IntentRules:
		return a.rules(ctx, u, r, games)
	case IntentControl:
		return a.control(ctx, u, r, open)
	case IntentPreference:
		return a.preference(ctx, u, r)
	}
	mood, ok := persona.ParseMood(r.Mood)
	if !ok {
		mood = persona.Smile
	}
	return outcome{mood: mood}
}

func (a *Agent) play(ctx context.Context, u User, lang string, r Route, games []GameInfo) outcome {
	if a.Tables == nil {
		return outcome{mood: persona.Sad, note: "The player wants to play, but the tables are not open yet on this server. Say so honestly and briefly, and offer to explain a game or talk strategy meanwhile."}
	}
	want := r.GameID
	if strings.TrimSpace(want) == "" {
		want = "holdem"
	}
	g, ok := findGame(games, want)
	if !ok {
		return outcome{mood: persona.Surprised, note: fmt.Sprintf("The player asked to play %q, which is not a game they can play here. Say you couldn't find it and name a few they can play: %s. Offer to build it in the studio if it is a new idea.", want, gameNames(games, 6))}
	}
	agents := cleanAgents(r.AgentIDs)
	seats := int(r.Seats)
	if seats < 1+len(agents) {
		seats = 1 + len(agents)
	}
	if g.MinSeats > 0 && seats < g.MinSeats {
		seats = g.MinSeats
	}
	if g.MaxSeats > 0 && seats > g.MaxSeats {
		return outcome{mood: persona.Surprised, note: fmt.Sprintf("%s seats at most %d players, and the player asked for %d (themselves plus %d agents). Nothing was created. Say so and ask who should sit out.", g.Name, g.MaxSeats, seats, len(agents))}
	}
	tableID, err := a.Tables.CreateTable(ctx, u.ID, TableRequest{GameID: g.ID, AgentIDs: agents, Seats: seats, Options: r.Options, Lang: lang})
	if err != nil {
		if errors.Is(err, ErrRejected) {
			return outcome{mood: persona.Sad, note: "You tried to set up the table, but it was refused: " + err.Error() + ". Nothing was created. Explain in one line and suggest a fix."}
		}
		slog.Error("create table", "user", u.ID, "game", g.ID, "err", err)
		return outcome{mood: persona.Sad, note: "You tried to set up the table, but something went wrong on the server. Nothing was created. Apologise briefly and suggest trying again in a moment."}
	}
	names := make([]string, len(agents))
	for i, id := range agents {
		names[i] = agentName(id, lang)
	}
	who := "just the player so far"
	if len(names) > 0 {
		who = "the player, " + strings.Join(names, ", ")
	}
	openSeats := seats - 1 - len(agents)
	return outcome{mood: persona.Wink, cards: []Card{{Kind: "table", TableID: tableID}},
		note: fmt.Sprintf("You just created a %s table with %d seats: %s; %d seat(s) open for friends or more agents. The table card is shown under your message. Say it is ready, add one line of banter about the opponents (their styles), and tell them to tap the card to sit down. Don't list the rules unless asked.",
			g.Name, seats, who, openSeats)}
}

func (a *Agent) build(ctx context.Context, u User, convID, text, lang string, r Route, games []GameInfo) outcome {
	if a.Studio == nil {
		return outcome{mood: persona.Sad, note: "The player described a game to build, but the game studio is not open yet on this server. Say so honestly, say you love the idea (one specific thing about it), and offer to talk the rules through so they are ready when it opens."}
	}
	prompt := strings.TrimSpace(r.Prompt)
	if prompt == "" {
		prompt = text
	}
	base := ""
	if r.BaseGameID != "" {
		if g, ok := findGame(games, r.BaseGameID); ok {
			base = g.ID
		}
	}
	goalID, gameID, err := a.Studio.StartBuild(ctx, u.ID, convID, prompt, base, lang)
	if err != nil {
		return a.capabilityError("start the build", u, err)
	}
	slog.Info("build started", "user", u.ID, "goal", goalID, "game", gameID)
	return outcome{mood: persona.Smile, goalID: goalID, cards: []Card{{Kind: "mission", GoalID: goalID}},
		note: "You just started a build mission for the player's game idea; its card is shown under your message. The studio agents are on it: the rules first, then the game itself, then hundreds of simulated games, then a review. The player approves before it is published, and can play the draft as soon as it is built. React to the idea with real curiosity (one specific detail you like). If one important thing is unclear (player count, how you win), ask exactly one question — their answer will reach the build."}
}

func (a *Agent) revise(ctx context.Context, u User, convID, text, lang string, r Route, games []GameInfo, open []*engine.Goal) outcome {
	change := strings.TrimSpace(r.Prompt)
	if change == "" {
		change = text
	}
	// 1. A build still running: the change goes into its plan.
	var target *engine.Goal
	for _, g := range open {
		if g.ID == r.GoalID {
			target = g
		}
	}
	if target == nil && r.GoalID == "" && r.GameID == "" && len(open) == 1 {
		target = open[0]
	}
	if target != nil {
		if target.Status == "paused" || target.Status == "needs_attention" {
			_ = a.Store.SetGoalStatus(ctx, target.ID, "active", "the owner sent a change")
		}
		if _, err := a.Store.EnqueuePlan(ctx, target.ID, fmt.Sprintf("change-%d", time.Now().UnixNano()), "change", "the player said: "+truncate(change, 1500)); err != nil {
			slog.Error("enqueue change", "goal", target.ID, "err", err)
			return outcome{mood: persona.Sad, note: "You could not pass the change to the build because of a server error. Apologise and ask them to try again."}
		}
		return outcome{mood: persona.Smile, goalID: target.ID, cards: []Card{{Kind: "mission", GoalID: target.ID}},
			note: fmt.Sprintf("You passed the player's change (%q) to the running build %q; the plan will adapt. Confirm what will change in one line.", truncate(change, 200), target.Title)}
	}
	// 2. A game they own that is no longer building: a new build based on it.
	if r.GameID != "" {
		g, ok := findGame(games, r.GameID)
		if !ok || !g.Mine {
			return outcome{mood: persona.Neutral, note: "The player wants to change a game you could not find among their own games. Ask which of their games they mean."}
		}
		if a.Studio == nil {
			return outcome{mood: persona.Sad, note: "The player wants to change their game, but the game studio is not open yet on this server. Say so honestly."}
		}
		goalID, _, err := a.Studio.StartBuild(ctx, u.ID, convID, change, g.ID, lang)
		if err != nil {
			return a.capabilityError("start the revision", u, err)
		}
		return outcome{mood: persona.Smile, goalID: goalID, cards: []Card{{Kind: "mission", GoalID: goalID}},
			note: fmt.Sprintf("You started a new build that revises their game %q with this change: %q. Its card is shown under your message. The current version stays playable until they approve the new one.", g.Name, truncate(change, 200))}
	}
	return outcome{mood: persona.Neutral, note: "The player wants to change a game, but it is not clear which one. Ask which game (or build) they mean."}
}

func (a *Agent) rules(ctx context.Context, u User, r Route, games []GameInfo) outcome {
	out := outcome{mood: persona.Neutral, note: "The player asked about rules or strategy. Answer clearly: the idea in one line, then the details, then a tiny example."}
	if r.GameID == "" {
		return out
	}
	g, ok := findGame(games, r.GameID)
	if !ok {
		return out
	}
	out.cards = []Card{{Kind: "game", GameID: g.ID}}
	if a.Catalog != nil {
		if text, err := a.Catalog.Rules(ctx, u.ID, g.ID); err == nil && strings.TrimSpace(text) != "" {
			out.note += fmt.Sprintf("\nThe question is about %s. Base your answer on its rules as written here (they are authoritative for this platform; if they do not cover the question, say so and say how it is usually played):\n<<<RULES\n%s\nRULES>>>", g.Name, truncate(text, 6000))
			return out
		}
	}
	out.note += "\nThe question is about " + g.Name + "."
	return out
}

func (a *Agent) control(ctx context.Context, u User, r Route, open []*engine.Goal) outcome {
	goalID := r.GoalID
	if goalID == "" && len(open) == 1 {
		goalID = open[0].ID
	}
	g, err := a.Store.GoalForUser(ctx, goalID, u.ID)
	if goalID == "" || err != nil {
		var titles []string
		for _, x := range open {
			titles = append(titles, x.Title)
		}
		if len(titles) == 0 {
			return outcome{mood: persona.Neutral, note: "The player referred to a build, but they have no builds running. Say so, and offer to start one."}
		}
		return outcome{mood: persona.Neutral, note: "The player referred to a build you could not identify. Ask which one they mean: " + strings.Join(titles, "; ")}
	}
	card := []Card{{Kind: "mission", GoalID: g.ID}}
	switch r.Action {
	case "pause":
		if g.Status == "active" || g.Status == "planning" {
			_ = a.Store.SetGoalStatus(ctx, g.ID, "paused", "the owner asked to pause")
		}
		return outcome{mood: persona.Neutral, goalID: g.ID, cards: card, note: "You paused the build " + g.Title + ". Nothing runs until they resume it."}
	case "resume":
		if g.Status == "paused" || g.Status == "needs_attention" {
			_ = a.Store.SetGoalStatus(ctx, g.ID, "active", "the owner asked to resume")
			_, _ = a.Store.EnqueuePlan(ctx, g.ID, fmt.Sprintf("resume-%d", time.Now().Unix()/60), "review", "the owner resumed the build; check the plan still fits")
		}
		return outcome{mood: persona.Smile, goalID: g.ID, cards: card, note: "You resumed the build " + g.Title + " from where it stopped."}
	case "cancel":
		if !terminal(g.Status) {
			_ = a.Store.SetGoalStatus(ctx, g.ID, "cancelled", "the owner cancelled")
		}
		return outcome{mood: persona.Sad, goalID: g.ID, cards: card, note: "You cancelled the build " + g.Title + ". Unfinished work stopped and any pending approval was withdrawn. A draft that was already playable stays in their games."}
	default:
		return outcome{mood: persona.Neutral, goalID: g.ID, cards: card, note: "The player asked how the build " + g.Title + " is going. Use the MISSIONS section: what is done, what is running, and what waits on them (an approval, an answer)."}
	}
}

func (a *Agent) preference(ctx context.Context, u User, r Route) outcome {
	key := strings.TrimSpace(r.Preference.Key)
	raw := strings.TrimSpace(fmt.Sprint(r.Preference.Value))
	if r.Preference.Value == nil {
		raw = ""
	}
	v, err := coercePref(key, raw)
	if err != nil {
		return outcome{mood: persona.Neutral, note: "The player asked to change a setting, but it could not be applied (" + err.Error() + "). Tell them what you can change, or point them to Settings."}
	}
	patch, _ := json.Marshal(map[string]any{key: v})
	if _, err := a.Store.Pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET data = user_preferences.data || EXCLUDED.data, updated_at=now()`, u.ID, patch); err != nil {
		slog.Error("set preference", "err", err)
		return outcome{mood: persona.Sad, note: "Saving the setting failed on the server. Apologise and point them to Settings."}
	}
	out := outcome{mood: persona.Smile, note: fmt.Sprintf("You changed the player's setting %s to %v (it is saved; they can change it in Settings). Confirm briefly — and from this reply on, behave accordingly.", key, v)}
	if key == "language" {
		out.lang = persona.Normalize(fmt.Sprint(v))
	}
	return out
}

// capabilityError turns a Tables/Studio failure into the reply note.
func (a *Agent) capabilityError(what string, u User, err error) outcome {
	if errors.Is(err, ErrRejected) {
		return outcome{mood: persona.Sad, note: "You tried to " + what + ", but it was refused: " + err.Error() + ". Nothing was started. Explain in one line."}
	}
	slog.Error(what, "user", u.ID, "err", err)
	return outcome{mood: persona.Sad, note: "You tried to " + what + ", but something went wrong on the server. Nothing was started. Apologise briefly and suggest trying again in a moment."}
}

// findGame matches a catalog entry by id, then by name (case-insensitive),
// then by a name that contains the query ("hold'em" → "Texas Hold'em").
func findGame(games []GameInfo, q string) (GameInfo, bool) {
	q = strings.TrimSpace(q)
	lq := strings.ToLower(q)
	for _, g := range games {
		if g.ID == q {
			return g, true
		}
	}
	for _, g := range games {
		if strings.ToLower(g.Name) == lq {
			return g, true
		}
	}
	if len([]rune(lq)) >= 3 {
		for _, g := range games {
			if strings.Contains(strings.ToLower(g.Name), lq) {
				return g, true
			}
		}
	}
	return GameInfo{}, false
}

func gameNames(games []GameInfo, n int) string {
	var out []string
	for i, g := range games {
		if i >= n {
			break
		}
		out = append(out, g.Name)
	}
	return strings.Join(out, ", ")
}

// ── Reply context ──────────────────────────────────────────────────────────

// chatContext is what the reply needs from persisted state.
func (a *Agent) chatContext(ctx context.Context, u User, p persona.Prefs, open []*engine.Goal, note string) string {
	var b strings.Builder
	if p.MemoryEnabled {
		if mem := a.Store.Memories(ctx, u.ID, 20); len(mem) > 0 {
			b.WriteString("WHAT YOU KNOW ABOUT THE PLAYER:\n")
			for _, m := range mem {
				b.WriteString("- " + m + "\n")
			}
		}
	}
	if len(open) > 0 {
		b.WriteString("\nMISSIONS (builds in the studio):\n")
		for _, g := range open {
			fmt.Fprintf(&b, "* %s — %s", g.Title, g.Status)
			if g.AttentionReason != "" {
				fmt.Fprintf(&b, " (needs: %s)", g.AttentionReason)
			}
			b.WriteString("\n")
			ts, _ := a.Store.Tasks(ctx, g.ID)
			for _, t := range ts {
				if t.Kind == "llm" || t.Kind == "wait" {
					fmt.Fprintf(&b, "   - %s: %s\n", t.Title, t.Status)
				}
			}
		}
	}
	var pending int
	_ = a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE user_id=$1 AND status='pending'`, u.ID).Scan(&pending)
	if pending > 0 {
		fmt.Fprintf(&b, "\nWAITING FOR THE PLAYER'S APPROVAL: %d (they approve on the card in the app, so the decision is on record)\n", pending)
	}
	if note != "" {
		b.WriteString("\nFOR THIS REPLY: " + note + "\n")
	}
	return b.String()
}

func (a *Agent) history(ctx context.Context, convID string, beforeID int64, n int) []llm.Message {
	rows, err := a.Store.Pool.Query(ctx, `SELECT role, content FROM (SELECT id, role, content FROM messages
		WHERE conversation_id=$1 AND id < $2 AND role IN ('user','assistant') ORDER BY id DESC LIMIT $3) x ORDER BY id`, convID, beforeID, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []llm.Message
	for rows.Next() {
		var m llm.Message
		if rows.Scan(&m.Role, &m.Content) == nil {
			m.Content = truncate(m.Content, 3000)
			out = append(out, m)
		}
	}
	return out
}

// afterTurn: conversation title and durable-fact extraction, off the hot path.
func (a *Agent) afterTurn(ctx context.Context, u User, p persona.Prefs, convID, text string, r Route) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = text
	}
	_, _ = a.Store.Pool.Exec(ctx, `UPDATE conversations SET title=$2 WHERE id=$1 AND title=''`, convID, truncate(title, 60))
	if !p.MemoryEnabled || len([]rune(text)) < 25 {
		return
	}
	resp, err := a.Model.Chat(ctx, engine.CallMeta{Purpose: "memory", UserID: u.ID}, llm.Request{Model: a.FastLLM, JSON: true, Temperature: 0,
		Messages: []llm.Message{{Role: "user", Content: `Extract durable facts about this player worth remembering across future conversations: games they like or are learning, their skill level, how they like to play (fast, casual, competitive), favourite AI opponents, the nickname they go by. ` +
			`Not details of a single request, and nothing sensitive (health, money, location, contact details). Reply with JSON only: {"facts": ["..."]} (max 3, each under 20 words, in the message's language), or {"facts": []}.` + "\n\nMESSAGE: " + text}}})
	if err != nil {
		return
	}
	var out struct {
		Facts []string `json:"facts"`
	}
	if json.Unmarshal([]byte(llm.ExtractJSON(resp.Message.Content)), &out) != nil {
		return
	}
	for i, f := range out.Facts {
		if i >= 3 || strings.TrimSpace(f) == "" {
			break
		}
		_, _ = a.Store.Pool.Exec(ctx, `INSERT INTO memories (user_id, kind, content, source)
			SELECT $1,'fact',$2,'conversation' WHERE NOT EXISTS (SELECT 1 FROM memories WHERE user_id=$1 AND lower(content)=lower($2))`, u.ID, f)
	}
}

// DetectLang picks the reply language for a message from its script:
// Korean when it has Hangul, Japanese when it has kana (hiragana or
// katakana), Chinese when it has Han characters only, English when it has
// real English text, and the player's chosen language (pref) when the
// message is too short to tell — "ok", "👍", "100".
//
// Han-only text is ambiguous: "了解" or "麻雀最高" is also Japanese written
// in kanji alone. It is Chinese unless the player chose Japanese, in which
// case a kanji-only message stays Japanese. (Kana or Hangul in a message
// always win, whatever the preference.)
func DetectLang(text, pref string) string {
	han, kana, hangul, latin := 0, 0, 0, 0
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r), r == 'ー':
			kana++
		case unicode.Is(unicode.Han, r):
			han++
		case r < 128 && unicode.IsLetter(r):
			latin++
		}
	}
	pref = strings.TrimSpace(pref)
	if pref != "" {
		pref = persona.Normalize(pref)
	}
	cjk := han + kana + hangul
	switch {
	case cjk > 0 && cjk*3 >= latin/4:
		switch {
		case hangul > 0 && hangul >= kana:
			return "ko"
		case kana > 0:
			return "ja"
		case pref == "ja":
			return "ja"
		}
		return "zh"
	case latin >= 4:
		return "en"
	case pref != "":
		return pref
	}
	return "en"
}

// ── lenient JSON shapes for model output ───────────────────────────────────

// strList accepts ["a","b"], "a", "a, b" or null.
type strList []string

func (l *strList) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*l = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		*l = nil
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	*l = out
	return nil
}

// flexInt accepts 3, 3.0, "3" or null.
type flexInt int

func (n *flexInt) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		*n = flexInt(f)
		return nil
	}
	var s string
	_ = json.Unmarshal(b, &s)
	_, _ = fmt.Sscan(s, &f)
	*n = flexInt(f)
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
