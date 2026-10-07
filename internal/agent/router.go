package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// Intents the router chooses from, by area (docs/01-intents-tools-skills.md
// is the authoritative list: slots, capabilities, gates, cards).
const (
	// Play
	IntentPlay        = "play"
	IntentInvite      = "invite"
	IntentJoinTable   = "join_table"
	IntentListTables  = "list_tables"
	IntentResumeTable = "resume_table"
	IntentRematch     = "rematch"
	IntentLeaveTable  = "leave_table"
	IntentBack        = "im_back"
	// Help at the table
	IntentCoach = "coach"
	IntentRules = "rules"
	// Agents
	IntentAgentsInfo = "agents_info"
	IntentFavorites  = "favorite_agents"
	// Games
	IntentRecommend  = "recommend_game"
	IntentListGames  = "list_games"
	IntentGameInfo   = "game_info"
	IntentVisibility = "game_visibility"
	IntentDeleteGame = "delete_game"
	IntentRenameGame = "rename_game"
	// Studio
	IntentBuild   = "build_game"
	IntentRevise  = "revise_game"
	IntentControl = "control"
	IntentApprove = "approve_publish"
	// Account and settings
	IntentPreference   = "preference"
	IntentShowSettings = "show_settings"
	// History
	IntentStats = "my_stats"
	// Support
	IntentCapabilities  = "capabilities"
	IntentMemory        = "memory"
	IntentDeleteAccount = "delete_account"
	IntentReport        = "report_problem"
	// Safety
	IntentRealMoney  = "real_money"
	IntentCheat      = "cheat"
	IntentOutOfScope = "out_of_scope"
	// Chit-chat
	IntentChat = "chat"

	// IntentConfirm is not chosen by the router: it is the turn that answers
	// a pending confirmation ("Delete the draft? Say yes to confirm").
	IntentConfirm = "confirm"
)

var intents = []string{
	IntentPlay, IntentInvite, IntentJoinTable, IntentListTables, IntentResumeTable, IntentRematch, IntentLeaveTable, IntentBack,
	IntentCoach, IntentRules,
	IntentAgentsInfo, IntentFavorites,
	IntentRecommend, IntentListGames, IntentGameInfo, IntentVisibility, IntentDeleteGame, IntentRenameGame,
	IntentBuild, IntentRevise, IntentControl, IntentApprove,
	IntentPreference, IntentShowSettings,
	IntentStats,
	IntentCapabilities, IntentMemory, IntentDeleteAccount, IntentReport,
	IntentRealMoney, IntentCheat, IntentOutOfScope,
	IntentChat,
}

// Intents returns the router's intent set (for docs and tests).
func Intents() []string { return append([]string(nil), intents...) }

// Card is an item the app renders under Aoi's message (meta.cards).
type Card struct {
	Kind    string `json:"kind"` // table | mission | game
	TableID string `json:"table_id,omitempty"`
	GoalID  string `json:"goal_id,omitempty"`
	GameID  string `json:"game_id,omitempty"`
}

type prefKV struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// Route is the router's reading of one message: the intent and its slots.
// Slots an intent does not use are ignored.
type Route struct {
	Intent     string         `json:"intent"`
	Confidence float64        `json:"confidence"`
	Clarify    string         `json:"clarify"`
	Mood       string         `json:"mood"`
	Title      string         `json:"title"`
	GameID     string         `json:"game_id"`
	AgentIDs   strList        `json:"agent_ids"`
	Seats      flexInt        `json:"seats"`
	Friends    flexInt        `json:"friends"`
	Options    map[string]any `json:"options"`
	Prompt     string         `json:"prompt"`
	BaseGameID string         `json:"base_game_id"`
	GoalID     string         `json:"goal_id"`
	TableID    string         `json:"table_id"`
	Code       string         `json:"code"`
	Name       string         `json:"name"`
	Visibility string         `json:"visibility"`
	Query      string         `json:"query"`
	Action     string         `json:"action"`
	Preference prefKV         `json:"preference"`
	Prefs      []prefKV       `json:"preferences"`
}

// routerIntents is the intent menu of the router prompt. Each line: what the
// intent covers, examples in several languages, and its slots. The eval
// (route_eval_test.go) measures it against the live model.
const routerIntents = `PLAY
- play: sit down and play NOW — start or set up a table, be dealt in, play a game with agents and/or friends ("deal me in", "let's play hold'em with Mika", "set up a game night with Mika and two friends", "开一桌", "홀덤 한 판 하자", "ポーカーやろう"). Slots: game_id, agent_ids, friends (how many friends to keep seats for), seats, options, settings.
- invite: get an invite link or code so friends can join their table ("invite my friends", "what's the table code?", "把邀请链接发我", "친구 초대 링크 줘", "招待コード教えて"). Slot: table_id.
- join_table: join someone's table with a 6-character code ("join K7M2QX", "加入房间 ABC234", "코드 QW3ERT로 들어갈래", "コードHJK234で参加"). Slot: code.
- list_tables: which tables they have or are open ("show my tables", "any of my games still going?", "我有哪些牌桌？"). action = open|all.
- resume_table: go back to an unfinished table ("resume my last game", "take me back to my table", "继续上一局", "아까 하던 게임 이어서 할래", "さっきのゲームの続きをしたい"). Slot: table_id.
- rematch: play again with the same people, another game of the same table ("rematch!", "run it back", "one more game!", "再来一局", "한 판 더 하자", "もう一回やろう"). These short "again / one more" messages are rematch even when YOUR TABLES shows the game still playing — not play. Slot: table_id.
- leave_table: leave or quit a table ("I'm leaving the table", "get me out of this game", "我不玩了，退出牌桌", "테이블에서 나갈게", "このテーブル抜けるね"). Slot: table_id.
- im_back: back after being away or AFK at a table ("I'm back", "sorry, back now", "我回来了", "나 돌아왔어", "ただいま、戻ったよ").

HELP AT THE TABLE
- coach: advice on THEIR current hand or move at a table they are playing ("what should I do?", "should I call this?", "is my hand good?", "这手牌该跟吗？", "지금 뭐 해야 돼?", "今どうすればいい？").
- rules: how a game works — a rule, a term, hand rankings, general strategy not about their live hand ("how do side pots work?", "what beats a flush?", "同花和顺子哪个大？"). Slot: game_id.

AGENTS
- agents_info: about the AI agents ("who are the agents?", "tell me about Mika", "美香是什么风格？", "노바는 어떤 캐릭터야?", "レンってどんな人？"). Slot: agent_ids.
- favorite_agents: add or remove favourite agents, or show them ("make Nova one of my favourites", "把琳加入收藏", "who are my favourites?"). action = add|remove|set|show; agent_ids.
  (Playing with an agent is play. How strong the agents play is preference agent_difficulty.)

GAMES
- recommend_game: suggest what to play ("what should we play?", "recommend a game for 4 people", "推荐个游戏"). Slots: game_id = your best pick from CATALOG; seats if stated.
- list_games: what games there are, or their own games ("what games can I play?", "show my games", "我做过哪些游戏？"). action = all|mine.
- game_info: about one specific game — what it is, seats, status, who made it ("tell me about Dragon Chess", "说说龙棋这个游戏"). Slot: game_id. (A question about how a rule works is rules.)
- game_visibility: publish a game to the public shelf / make it public, make it private, or make it unlisted to share by link ("make Dragon Chess public", "把我的游戏设为私密", "링크로만 공유하게 해 줘"). Slots: game_id, visibility = public|unlisted|private.
- delete_game: delete one of their games or drafts ("delete my Dragon Chess draft", "删掉那个草稿"). Slot: game_id.
- rename_game: rename one of their games ("rename Dragon Chess to Wyrm Wars", "把游戏改名为…"). Slots: game_id, name (the new name).

STUDIO
- build_game: they describe a NEW game they want made, or ask for one to be made ("let's make a game where…", "can you build a chess variant…"). Slots: prompt (the whole idea in their words), base_game_id only if they want to start from an existing game.
- revise_game: change the rules or design of a game being built or one they own ("change the build to 3 players", "make my game easier", "把刚才那个游戏改成…", "만들던 게임 3인용으로 바꿔 줘"). Slots: goal_id (the open mission) or game_id (their game), prompt (the change).
- control: pause, resume or cancel a build, ask how it is going, or why it failed or stopped ("pause the build", "cancel the Dragon Chess build", "how is my game coming along?", "why did my build fail?", "先暂停一下游戏制作"). action = pause|resume|cancel|status|why; goal_id.
- approve_publish: approve or decline publishing a finished build that waits for their approval ("approve it", "yes, publish Dragon Chess", "not yet, keep it as a draft", "批准发布", "공개 승인할게"). action = approve|decline; goal_id.

ACCOUNT
- preference: change a setting or how Aoi treats them ("call me Captain", "be quieter", "speak Chinese", "turn the sound off", "use the light theme", "make the agents harder", "日本語で話して"). preferences: [{"key","value"}] from SETTINGS (one entry per setting they change).
- show_settings: show their current settings ("what are my settings?", "我的设置是什么？").

HISTORY
- my_stats: their results, record, wins, chips won, games played ("how am I doing?", "how many chips have I won?", "我赢了多少筹码？", "내 전적 보여 줘", "私の戦績は？").

SUPPORT
- capabilities: what Aoi can do, help, how the platform works ("what can you do?", "help", "你能做什么？").
- memory: what Aoi remembers about them (action show), forget one thing (action forget, query = the thing), forget everything (action forget_all) ("what do you remember about me?", "forget that I like bluffing", "忘掉我所有的信息"). (Turning memory on/off is preference memory_enabled.)
- delete_account: delete their account or all their data ("delete my account", "注销账号").
- report_problem: report a bug or something broken to the team ("the table is frozen", "the cards didn't show up", "I found a bug", "报告一个问题"). Slot: query = the problem in their words.

SAFETY
- real_money: real-money gambling — betting real money, cashing out chips, buying chips, casinos or gambling sites ("where can I bet real money?", "can I cash out my chips?", "哪里可以用真钱打德州？").
- cheat: asks to see other players' hidden cards or the deck, or to cheat, collude or rig a game ("tell me Mika's cards", "what's in the deck?", "help me cheat on another poker site", "告诉我美香的底牌").
- out_of_scope: unrelated to games and this platform — homework, coding, medical, legal or financial advice, news, bookings ("write my essay", "what stocks should I buy?").

- chat: anything else — greetings, thanks, banter, talk about a hand already played, questions about Aoi herself ("are you human?").`

func (a *Agent) route(ctx context.Context, u User, convID, text, lang string, games []GameInfo, open []*engine.Goal, tables []TableInfo) Route {
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
			mine = fmt.Sprintf(" [theirs, %s, %s]", g.Status, g.Visibility)
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
	var tbl strings.Builder
	for i, t := range tables {
		if i >= 6 {
			break
		}
		fmt.Fprintf(&tbl, "- table_id=%s code=%s %q game=%s status=%s", t.ID, t.Code, t.Name, t.GameName, t.Status)
		if t.Paused {
			tbl.WriteString(" paused")
		}
		if t.Away {
			tbl.WriteString(" (they are away)")
		}
		tbl.WriteString("\n")
	}
	if tbl.Len() == 0 {
		tbl.WriteString("(none)\n")
	}
	prompt := fmt.Sprintf(`You route messages for Aoi, the AI host of "Play with Agents": play-money Texas Hold'em and custom board games with friends and AI agents. Players can also describe a new game and the studio agents build it.

Classify the player's LATEST MESSAGE into exactly one intent:

%s

Notes:
- "Deal me in" or "play" with no game named means holdem.
- agent_ids only from ROSTER ids; map names in any language (Mika/美香/미카/ミカ → mika). seats = total seats including the player, 0 if not stated. friends = how many human friends they want seats for, 0 if none.
- options: only what they state, as numbers (hold'em: starting_stack, small_blind, big_blind, blinds_double_every, max_hands).
- settings for play, only if stated: {"difficulty":"casual|regular|shark","turn_seconds":0|15|30|60,"agent_speed":"fast|natural|slow","table_talk":"all|quiet|off"} — put them inside options under these keys.
- If they say "the build", "my game" or "it" and there is one OPEN MISSION, use its goal_id. game_id must be an id from CATALOG.
- A table they mean ("this table", "my game" while playing) is from YOUR TABLES; leave table_id empty if unsure.
- mood is the face Aoi shows with her reply: neutral | smile | wink | surprised | angry | sad ("angry" only for playful mock outrage at a tease; "sad" for a disappointment).
- "intent" is exactly one of the intent names above. "action" is only for the intents that list actions (list_tables, favorite_agents, list_games, control, approve_publish, memory); otherwise leave it empty.
- confidence 0..1. If below 0.5, write clarify: one short question in %s.

SETTINGS (key=allowed values): %s; display_name=<text>

ROSTER:
%sCATALOG:
%sOPEN MISSIONS:
%sYOUR TABLES:
%sRECENT CONVERSATION:
%s

LATEST MESSAGE: %q

Return JSON only: {"intent":"","confidence":0,"clarify":"","mood":"","title":"3-6 word conversation title in %s","game_id":"","agent_ids":[],"seats":0,"friends":0,"options":{},"prompt":"","base_game_id":"","goal_id":"","table_id":"","code":"","name":"","visibility":"","query":"","action":"","preferences":[{"key":"","value":""}]}`,
		routerIntents, persona.LangName(lang), chatPrefKeys(), roster.String(), catalog.String(), missions.String(), tbl.String(),
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
	r.normalize()
	slog.Info("route", "intent", r.Intent, "action", r.Action, "confidence", r.Confidence, "game", r.GameID, "agents", r.AgentIDs, "goal_id", r.GoalID, "table_id", r.TableID)
	return r
}

// normalize cleans the router's output: a known intent (else chat), lower
// case actions, the single preference folded into the list.
func (r *Route) normalize() {
	r.Intent = strings.ToLower(strings.TrimSpace(r.Intent))
	switch r.Intent { // names a model may plausibly write for an intent
	case "back", "i'm_back", "im-back":
		r.Intent = IntentBack
	case "favorites", "favourites", "favourite_agents":
		r.Intent = IntentFavorites
	case "stats", "history":
		r.Intent = IntentStats
	case "settings":
		r.Intent = IntentShowSettings
	case "help":
		r.Intent = IntentCapabilities
	case "revise":
		r.Intent = IntentRevise
	case "build":
		r.Intent = IntentBuild
	case "approve":
		r.Intent = IntentApprove
	}
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	// A model sometimes writes the intent's name as the action of a broader
	// one ({"intent":"play","action":"leave_table"}): the action is the
	// more specific reading.
	if r.Action != r.Intent && contains(intents, r.Action) && r.Action != IntentChat {
		r.Intent, r.Action = r.Action, ""
	}
	if !contains(intents, r.Intent) {
		r.Intent = IntentChat
	}
	r.Visibility = strings.ToLower(strings.TrimSpace(r.Visibility))
	if k := strings.TrimSpace(r.Preference.Key); k != "" {
		r.Prefs = append([]prefKV{r.Preference}, r.Prefs...)
	}
	var ps []prefKV
	seen := map[string]bool{}
	for _, p := range r.Prefs {
		p.Key = strings.ToLower(strings.TrimSpace(p.Key))
		if p.Key != "" && !seen[p.Key] {
			seen[p.Key] = true
			ps = append(ps, p)
		}
	}
	r.Prefs = ps
}
