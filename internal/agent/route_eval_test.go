package agent

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
)

// Routing evaluation against the LIVE model. The router decides whether a
// table is created, a build mission starts, a game goes public, a build is
// cancelled — a wrong call here is invisible to every other test, which
// script the route.
//
// It runs whenever a key is present (and is skipped under -short):
//
//	set -a; . ./.env; set +a; go test ./internal/agent -run TestRouteEval -v
//
// PLAY_EVAL_MODELS (comma-separated) compares models; the default is
// PLAY_LLM_FAST_MODEL, the model the router uses in production. A case is
// correct when the intent is one of ok, the seated agents match (play) and
// the action matches (when the case names one). The floor is 95%.
type routeCase struct {
	text   string
	ok     []string // any of these intents is correct
	agents []string // for play: the agent ids that must be picked, in any order
	action string   // when set, the route's action must be this
}

func rc(text string, intent string) routeCase { return routeCase{text: text, ok: []string{intent}} }
func rca(text, intent, action string) routeCase {
	return routeCase{text: text, ok: []string{intent}, action: action}
}

var routeCases = []routeCase{
	// ── Play ──
	{"deal me into hold'em with Mika and Ren", []string{IntentPlay}, []string{"mika", "ren"}, ""},
	{"Let's play poker! Put Nova and Captain Bram at the table", []string{IntentPlay}, []string{"nova", "bram"}, ""},
	rc("Set up a 6-seat hold'em table for me", IntentPlay),
	{"Set up a game night with Mika and two of my friends", []string{IntentPlay}, []string{"mika"}, ""},
	rc("Start a Star Race table with shark-level bots and a 60 second clock", IntentPlay),
	{"开一桌德州扑克，叫上美香和蓮", []string{IntentPlay}, []string{"mika", "ren"}, ""},
	{"我想和Nova、琳一起玩德州", []string{IntentPlay}, []string{"nova", "lin"}, ""},
	rc("开一桌德州，留两个位置给我朋友，盲注10/20", IntentPlay),
	{"미카랑 렌이랑 홀덤 한 판 하자", []string{IntentPlay}, []string{"mika", "ren"}, ""},
	{"노바하고 브램 선장 불러서 포커 치자", []string{IntentPlay}, []string{"nova", "bram"}, ""},
	rc("6인 홀덤 테이블 하나 열어 줘", IntentPlay),
	{"ミカとレンとホールデムやろう", []string{IntentPlay}, []string{"mika", "ren"}, ""},
	{"ノヴァとブラム船長を呼んでポーカーしよう", []string{IntentPlay}, []string{"nova", "bram"}, ""},
	rc("6人用のホールデムのテーブルを作って", IntentPlay),

	rc("Invite my friends to my table", IntentInvite),
	rc("What's the invite code for my table?", IntentInvite),
	rc("把邀请链接发给我，我要叫朋友来", IntentInvite),
	rc("这桌的房间码是多少？", IntentInvite),
	rc("친구 초대 링크 줘", IntentInvite),
	rc("友達を招待したいからリンクちょうだい", IntentInvite),

	rc("Join table K7M2QX", IntentJoinTable),
	rc("My friend gave me the code HJK234, put me in", IntentJoinTable),
	rc("加入房间 QW3ERT", IntentJoinTable),
	rc("用这个码进桌：ABC234", IntentJoinTable),
	rc("코드 QW3ERT로 들어갈래", IntentJoinTable),
	rc("コードHJK234のテーブルに参加したい", IntentJoinTable),

	rca("Show me my tables", IntentListTables, ""),
	rc("Do I have any games still going?", IntentListTables),
	rc("我现在有哪些牌桌？", IntentListTables),
	rc("我还有没打完的桌子吗？", IntentListTables),
	rc("내 테이블 목록 보여 줘", IntentListTables),

	rc("Resume my last game", IntentResumeTable),
	rc("Take me back to my hold'em table", IntentResumeTable),
	rc("继续上一局", IntentResumeTable),
	rc("带我回到刚才那桌", IntentResumeTable),
	rc("아까 하던 게임 이어서 할래", IntentResumeTable),
	rc("さっきのゲームの続きをしたい", IntentResumeTable),

	rc("Rematch!", IntentRematch),
	rc("That was close, run it back with the same table", IntentRematch),
	rc("再来一局！", IntentRematch),
	rc("同样的人再开一局", IntentRematch),
	rc("한 판 더 하자!", IntentRematch),
	rc("もう一回やろう！", IntentRematch),

	rc("I'm leaving the table", IntentLeaveTable),
	rc("Get me out of this game, I have to go", IntentLeaveTable),
	rc("我不玩了，退出牌桌", IntentLeaveTable),
	rc("帮我离开这张桌子", IntentLeaveTable),
	rc("테이블에서 나갈게", IntentLeaveTable),
	rc("このテーブル抜けるね", IntentLeaveTable),

	rc("I'm back", IntentBack),
	rc("Sorry, had to step away, back now", IntentBack),
	rc("我回来了", IntentBack),
	rc("刚才离开了一下，现在回来了", IntentBack),
	rc("나 돌아왔어", IntentBack),
	rc("ただいま、戻ったよ", IntentBack),

	// ── Help at the table ──
	rc("What should I do here?", IntentCoach),
	rc("Should I call this bet?", IntentCoach),
	rc("这手牌我该跟还是弃？", IntentCoach),
	rc("现在该怎么打？", IntentCoach),
	rc("지금 콜해야 돼?", IntentCoach),
	rc("今どうすればいい？レイズすべき？", IntentCoach),

	rc("How do side pots work?", IntentRules),
	rc("What beats a flush?", IntentRules),
	rc("How do you win at Star Race?", IntentRules),
	rc("边池是怎么算的？", IntentRules),
	rc("同花和顺子哪个大？", IntentRules),
	rc("사이드 팟은 어떻게 계산해?", IntentRules),
	rc("플러시랑 스트레이트 중에 뭐가 더 세?", IntentRules),
	rc("サイドポットってどうやって計算するの？", IntentRules),
	rc("フラッシュとストレートってどっちが強い？", IntentRules),

	// ── Agents ──
	rc("Who are the agents I can play with?", IntentAgentsInfo),
	rc("Tell me about Mika", IntentAgentsInfo),
	rc("美香是什么风格的玩家？", IntentAgentsInfo),
	rc("这里有哪些AI对手？", IntentAgentsInfo),
	rc("노바는 어떤 캐릭터야?", IntentAgentsInfo),
	rc("レンってどんな人？", IntentAgentsInfo),

	rca("Make Nova one of my favourite agents", IntentFavorites, "add"),
	rca("Remove Bram from my favourites", IntentFavorites, "remove"),
	rca("把琳加入我的收藏", IntentFavorites, "add"),
	rca("我收藏了哪些AI？", IntentFavorites, "show"),
	rca("미카를 즐겨찾기에 추가해 줘", IntentFavorites, "add"),

	// ── Games ──
	rc("What should we play tonight? We're four people", IntentRecommend),
	rc("Recommend me a game", IntentRecommend),
	rc("推荐一个适合三个人玩的游戏", IntentRecommend),
	rc("有什么好玩的游戏推荐吗？", IntentRecommend),
	rc("뭐 하고 놀지 추천해 줘", IntentRecommend),
	rc("何かおすすめのゲームある？", IntentRecommend),

	rca("What games can I play here?", IntentListGames, "all"),
	rca("Show me my games", IntentListGames, "mine"),
	rca("这里都有什么游戏？", IntentListGames, "all"),
	rca("我做过哪些游戏？", IntentListGames, "mine"),
	rca("내가 만든 게임 목록 보여 줘", IntentListGames, "mine"),
	rca("どんなゲームがあるの？", IntentListGames, "all"),

	rc("Tell me about Star Race", IntentGameInfo),
	rc("What is Moon Tiles?", IntentGameInfo),
	rc("介绍一下Star Race这个游戏", IntentGameInfo),
	rc("Moon Tiles是什么游戏？", IntentGameInfo),
	rc("Star Race는 어떤 게임이야?", IntentGameInfo),

	rc("Publish Moon Tiles to the shelf so everyone can play", IntentVisibility),
	rc("Make Dragon Chess private", IntentVisibility),
	rc("I want to share Moon Tiles with just a link, not on the shelf", IntentVisibility),
	rc("把Moon Tiles设为私密", IntentVisibility),
	rc("把我的游戏Moon Tiles公开给所有人", IntentVisibility),
	rc("Moon Tiles를 링크로만 공유하게 해 줘", IntentVisibility),

	rc("Delete my Dragon Chess draft", IntentDeleteGame),
	rc("Get rid of the Dragon Chess game, I don't want it anymore", IntentDeleteGame),
	rc("删掉Dragon Chess那个草稿", IntentDeleteGame),
	rc("把我的龙棋游戏删除", IntentDeleteGame),
	rc("Dragon Chess 초안 삭제해 줘", IntentDeleteGame),

	rc("Rename Dragon Chess to Wyrm Wars", IntentRenameGame),
	rc("Call my Moon Tiles game 'Lunar Base' instead", IntentRenameGame),
	rc("把Dragon Chess改名为飞龙棋", IntentRenameGame),
	rc("Moon Tiles 이름을 달 기지로 바꿔 줘", IntentRenameGame),

	// ── Studio ──
	rc("Let's make a game where two players race pawns across a hex board and can block each other", IntentBuild),
	rc("I have an idea for a card game: everyone gets 5 cards and the first to collect three of a kind wins. Can you build it?", IntentBuild),
	rc("我们来做一个游戏吧：三个人轮流在棋盘上放石头，连成五个就赢", IntentBuild),
	rc("帮我设计一个合作卡牌游戏，大家一起打怪兽", IntentBuild),
	rc("게임 하나 만들어 줘: 두 명이 육각형 보드에서 말을 옮기고 서로 길을 막을 수 있는 거", IntentBuild),
	rc("협동 카드 게임 만들어 줄래? 다 같이 몬스터를 물리치는 거야", IntentBuild),
	rc("ゲームを作ろう：二人で六角形の盤の上で駒を競争させて、お互いに邪魔できるやつ", IntentBuild),
	rc("協力型のカードゲームを作ってほしいな。みんなでモンスターを倒すの", IntentBuild),

	rc("Change the build to 3 players", IntentRevise),
	rc("In Moon Tiles, make the board smaller and the game easier", IntentRevise),
	rc("把正在做的游戏改成三个人玩", IntentRevise),
	rc("把Moon Tiles改简单一点，然后重新发布", IntentRevise),
	rc("제작 중인 게임, 플레이어 3명으로 바꿔 줘", IntentRevise),
	rc("作ってるゲーム、3人で遊べるように変えて", IntentRevise),

	rca("Pause the build for now", IntentControl, "pause"),
	rca("Resume the Dragon Chess build", IntentControl, "resume"),
	rca("Cancel the Dragon Chess build", IntentControl, "cancel"),
	rca("How is my game coming along?", IntentControl, "status"),
	rca("Why did my build fail?", IntentControl, "why"),
	rca("先暂停一下游戏制作", IntentControl, "pause"),
	rca("继续做我的游戏吧", IntentControl, "resume"),
	rca("取消正在做的那个游戏", IntentControl, "cancel"),
	rca("我的游戏做到哪一步了？", IntentControl, "status"),
	rca("游戏制作为什么卡住了？", IntentControl, "why"),
	rca("게임 만드는 거 잠깐 멈춰 줘", IntentControl, "pause"),
	rca("ゲーム作り、いったん止めておいて", IntentControl, "pause"),
	rca("ゲーム作りはどこまで進んだ？", IntentControl, "status"),

	rca("Approve publishing Dragon Chess", IntentApprove, "approve"),
	rca("Not yet, keep it as a draft — don't publish", IntentApprove, "decline"),
	rca("批准发布", IntentApprove, "approve"),
	rca("先别发布，留作草稿", IntentApprove, "decline"),
	rca("공개 승인할게", IntentApprove, "approve"),
	rca("公開を承認します", IntentApprove, "approve"),

	// ── Account and settings ──
	rc("Call me Captain from now on", IntentPreference),
	rc("Please be a bit quieter when you talk", IntentPreference),
	rc("Turn off the sound and switch to the light theme", IntentPreference),
	rc("Make the agents play harder, shark level", IntentPreference),
	rc("Set my turn clock to 60 seconds", IntentPreference),
	rc("Use the emerald felt and the midnight card back", IntentPreference),
	rc("Stop emailing me when it's my turn", IntentPreference),
	rc("以后叫我船长", IntentPreference),
	rc("请用英文回复我", IntentPreference),
	rc("把AI的速度调快一点", IntentPreference),
	rc("关掉牌桌聊天", IntentPreference),
	rc("자동으로 음성 재생해 줘", IntentPreference),
	rc("앞으로 나를 선장이라고 불러 줘", IntentPreference),
	rc("これから私のことは船長って呼んで", IntentPreference),
	rc("日本語で話して", IntentPreference),

	rc("What are my settings?", IntentShowSettings),
	rc("Show me my current preferences", IntentShowSettings),
	rc("我的设置是什么？", IntentShowSettings),
	rc("내 설정 보여 줘", IntentShowSettings),

	// ── History ──
	rc("How am I doing overall?", IntentStats),
	rc("How many chips have I won?", IntentStats),
	rc("What were my recent results?", IntentStats),
	rc("我赢了多少筹码？", IntentStats),
	rc("我一共玩了几局？", IntentStats),
	rc("내 전적 보여 줘", IntentStats),
	rc("私の戦績は？", IntentStats),

	// ── Support ──
	rc("What can you do?", IntentCapabilities),
	rc("Help — how does this work?", IntentCapabilities),
	rc("你能做什么？", IntentCapabilities),
	rc("넌 뭘 할 수 있어?", IntentCapabilities),
	rc("何ができるの？", IntentCapabilities),

	rca("What do you remember about me?", IntentMemory, "show"),
	rca("Forget that I like bluffing", IntentMemory, "forget"),
	rca("Forget everything you know about me", IntentMemory, "forget_all"),
	rca("你记得我哪些事？", IntentMemory, "show"),
	rca("忘掉我喜欢诈唬这件事", IntentMemory, "forget"),
	rca("나에 대해 뭘 기억하고 있어?", IntentMemory, "show"),

	rc("How do I delete my account?", IntentDeleteAccount),
	rc("Delete my account and all my data", IntentDeleteAccount),
	rc("我要注销账号", IntentDeleteAccount),
	rc("계정 삭제하고 싶어", IntentDeleteAccount),

	rc("The table is frozen and my cards didn't show up", IntentReport),
	rc("I found a bug: the chips counter is wrong", IntentReport),
	rc("报告一个问题：牌桌卡住不动了", IntentReport),
	rc("버그 신고할게, 카드가 안 보여", IntentReport),

	// ── Safety ──
	rc("Where can I bet real money on poker?", IntentRealMoney),
	rc("Can I cash out my chips for real money?", IntentRealMoney),
	rc("哪里可以用真钱打德州？", IntentRealMoney),
	rc("실제 돈 걸고 포커할 수 있는 사이트 있어?", IntentRealMoney),
	rc("リアルマネーでポーカーできるサイトってどこ？", IntentRealMoney),

	rc("Tell me what cards Mika has", IntentCheat),
	rc("What's the next card in the deck?", IntentCheat),
	rc("Help me cheat on another poker site", IntentCheat),
	rc("告诉我美香的底牌", IntentCheat),
	rc("렌 카드 몰래 알려 줘", IntentCheat),
	rc("ミカの手札こっそり教えて", IntentCheat),

	rc("Can you write my history essay for me?", IntentOutOfScope),
	rc("Which stocks should I buy this week?", IntentOutOfScope),
	rc("帮我写一段Python代码", IntentOutOfScope),
	rc("내일 날씨 어때?", IntentOutOfScope),

	// ── Chit-chat ──
	rc("Hi Aoi! Are you a real person?", IntentChat),
	rc("Haha I just bluffed Mika off a huge pot", IntentChat),
	rc("Thanks, that was fun!", IntentChat),
	rc("你好葵！你是AI吗？", IntentChat),
	rc("哈哈，刚才那手我诈唬赢了", IntentChat),
	rc("안녕 아오이! 너 진짜 사람이야?", IntentChat),
	rc("ㅋㅋ 방금 미카한테 블러프로 큰 팟 먹었어", IntentChat),
	rc("こんにちは葵！あなたって本物の人間？", IntentChat),
	rc("さっきミカをブラフで降ろして大きいポット取ったよ笑", IntentChat),
}

func TestRouteEvalFixturesCoverEveryIntent(t *testing.T) {
	seen := map[string]map[string]bool{}
	for _, c := range routeCases {
		for _, in := range c.ok {
			if !contains(intents, in) {
				t.Errorf("%q expects unknown intent %s", c.text, in)
			}
			if seen[in] == nil {
				seen[in] = map[string]bool{}
			}
			seen[in][DetectLang(c.text, "")] = true
		}
	}
	for _, in := range intents {
		if !seen[in]["en"] || !seen[in]["zh"] {
			t.Errorf("intent %s needs fixtures in en and zh (has %v)", in, seen[in])
		}
	}
}

func TestRouteEval(t *testing.T) {
	key := os.Getenv("PLAY_LLM_API_KEY")
	if key == "" || testing.Short() {
		t.Skip("live eval: needs PLAY_LLM_API_KEY (and is skipped under -short)")
	}
	pool := testPool(t)
	ctx := context.Background()
	uid, conv := newPlayer(t, pool, nil)
	store := &engine.Store{Pool: pool}
	// Real accounts have a build in progress, and the router is told about
	// it so it can recognise "the build". That must not pull unrelated
	// requests into it.
	g := &engine.Goal{UserID: uid, ConversationID: conv, Title: "Dragon Chess", Objective: "a chess variant with dragons", Domain: "studio", Skill: "general-task", Language: "en", GameID: "dragon-chess"}
	if err := store.CreateGoal(ctx, g); err != nil {
		t.Fatal(err)
	}
	// …and a hold'em table they are playing at.
	tables := []TableInfo{{ID: "7a6c1d2e-0000-4000-8000-000000000001", Code: "K7M2QX", Name: "Sam's Texas Hold'em", GameID: "holdem", GameName: "Texas Hold'em",
		Status: "playing", Seats: 4, Players: []string{"Sam", "Mika", "Ren", "Nova"}, IsHost: true, Seated: true}}

	base := envOr("PLAY_LLM_BASE_URL", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1")
	client := llm.New(base, key)
	games, _ := fakeCatalog{}.Games(ctx, uid)
	open := []*engine.Goal{g}
	only := os.Getenv("PLAY_EVAL_ONLY") // a substring of the fixtures to run, for iterating on the prompt
	for _, model := range strings.Split(envOr("PLAY_EVAL_MODELS", envOr("PLAY_LLM_FAST_MODEL", "qwen3.8-flash")), ",") {
		a := &Agent{Store: store, Model: &engine.Model{Client: client, Store: store}, FastLLM: model}
		var cases []routeCase
		for _, c := range routeCases {
			if only == "" || strings.Contains(c.text, only) || contains(c.ok, only) {
				cases = append(cases, c)
			}
		}
		routes := make([]Route, len(cases))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8) // bounded concurrency against the provider
		for i, c := range cases {
			wg.Add(1)
			go func(i int, c routeCase) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
				defer cancel()
				routes[i] = a.route(cctx, User{ID: uid, Lang: DetectLang(c.text, "")}, conv, c.text, DetectLang(c.text, ""), games, open, tables)
			}(i, c)
		}
		wg.Wait()

		pass := 0
		var misses []string
		byLang := map[string][2]int{}   // lang → {correct, total}
		byIntent := map[string][2]int{} // expected intent → {correct, total}
		for i, c := range cases {
			r := routes[i]
			lang := DetectLang(c.text, "")
			good := contains(c.ok, r.Intent)
			if good && c.agents != nil {
				got := cleanAgents(r.AgentIDs)
				good = len(got) == len(c.agents)
				for _, want := range c.agents {
					good = good && contains(got, want)
				}
			}
			if good && c.action != "" {
				good = r.Action == c.action
			}
			n, m := byLang[lang], byIntent[c.ok[0]]
			n[1]++
			m[1]++
			if good {
				pass++
				n[0]++
				m[0]++
			} else {
				misses = append(misses, fmt.Sprintf("  %-50q → %s/%s %v (%.2f), want %v/%s %v", truncate(c.text, 48), r.Intent, r.Action, r.AgentIDs, r.Confidence, c.ok, c.action, c.agents))
			}
			byLang[lang], byIntent[c.ok[0]] = n, m
		}
		var per []string
		for _, l := range []string{"en", "zh", "ko", "ja"} {
			if n := byLang[l]; n[1] > 0 {
				per = append(per, fmt.Sprintf("%s %d/%d (%.0f%%)", l, n[0], n[1], 100*float64(n[0])/float64(n[1])))
			}
		}
		var perIntent []string
		names := make([]string, 0, len(byIntent))
		for k := range byIntent {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			n := byIntent[k]
			perIntent = append(perIntent, fmt.Sprintf("  %-16s %2d/%-2d %3.0f%%", k, n[0], n[1], 100*float64(n[0])/float64(n[1])))
		}
		t.Logf("%s: %d/%d correct (%.1f%%) — %s\nper intent:\n%s\nmisses:\n%s", model, pass, len(cases), 100*float64(pass)/float64(len(cases)),
			strings.Join(per, ", "), strings.Join(perIntent, "\n"), strings.Join(misses, "\n"))
		if floor := (len(cases)*95 + 99) / 100; pass < floor {
			t.Errorf("%s routed %d/%d correctly; the floor is %d (95%%)", model, pass, len(cases), floor)
		}
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
