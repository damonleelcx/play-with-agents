package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
)

// Routing evaluation against the LIVE model. The router decides whether a
// table is created, a build mission starts, a build is changed or cancelled —
// a wrong call here is invisible to every other test, which script the route.
//
// It runs whenever a key is present (and is skipped under -short):
//
//	set -a; . ./.env; set +a; go test ./internal/agent -run TestRouteEval -v
//
// PLAY_EVAL_MODELS (comma-separated) compares models; the default is
// PLAY_LLM_FAST_MODEL, the model the router uses in production.
type routeCase struct {
	text   string
	ok     []string // any of these intents is correct
	agents []string // for play: the agent ids that must be picked, in any order
}

var routeCases = []routeCase{
	// English
	{"deal me into hold'em with Mika and Ren", []string{IntentPlay}, []string{"mika", "ren"}},
	{"Let's play poker! Put Nova and Captain Bram at the table", []string{IntentPlay}, []string{"nova", "bram"}},
	{"Set up a 6-seat hold'em table for me", []string{IntentPlay}, nil},
	{"Let's make a game where two players race pawns across a hex board and can block each other", []string{IntentBuild}, nil},
	{"I have an idea for a card game: everyone gets 5 cards and the first to collect three of a kind wins. Can you build it?", []string{IntentBuild}, nil},
	{"Change the build to 3 players", []string{IntentRevise}, nil},
	{"In Dragon Chess, make the dragons move two squares instead of one", []string{IntentRevise}, nil},
	{"How do side pots work?", []string{IntentRules}, nil},
	{"What beats a flush?", []string{IntentRules}, nil},
	{"Pause the build for now", []string{IntentControl}, nil},
	{"How is my game coming along?", []string{IntentControl}, nil},
	{"Cancel the Dragon Chess build", []string{IntentControl}, nil},
	{"Call me Captain from now on", []string{IntentPreference}, nil},
	{"Please be a bit quieter when you talk", []string{IntentPreference}, nil},
	{"Hi Aoi! Are you a real person?", []string{IntentChat}, nil},
	{"Haha I just bluffed Mika off a huge pot", []string{IntentChat}, nil},
	{"Where can I bet real money on poker?", []string{IntentChat}, nil},
	// 中文
	{"开一桌德州扑克，叫上美香和蓮", []string{IntentPlay}, []string{"mika", "ren"}},
	{"我想和Nova、琳一起玩德州", []string{IntentPlay}, []string{"nova", "lin"}},
	{"我们来做一个游戏吧：三个人轮流在棋盘上放石头，连成五个就赢", []string{IntentBuild}, nil},
	{"帮我设计一个合作卡牌游戏，大家一起打怪兽", []string{IntentBuild}, nil},
	{"把正在做的游戏改成三个人玩", []string{IntentRevise}, nil},
	{"边池是怎么算的？", []string{IntentRules}, nil},
	{"同花和顺子哪个大？", []string{IntentRules}, nil},
	{"先暂停一下游戏制作", []string{IntentControl}, nil},
	{"以后叫我船长", []string{IntentPreference}, nil},
	{"请用英文回复我", []string{IntentPreference}, nil},
	{"你好葵！你是AI吗？", []string{IntentChat}, nil},
	{"哈哈，刚才那手我诈唬赢了", []string{IntentChat}, nil},
	{"哪里可以用真钱打德州？", []string{IntentChat}, nil},
	// 한국어
	{"미카랑 렌이랑 홀덤 한 판 하자", []string{IntentPlay}, []string{"mika", "ren"}},
	{"노바하고 브램 선장 불러서 포커 치자", []string{IntentPlay}, []string{"nova", "bram"}},
	{"6인 홀덤 테이블 하나 열어 줘", []string{IntentPlay}, nil},
	{"게임 하나 만들어 줘: 두 명이 육각형 보드에서 말을 옮기고 서로 길을 막을 수 있는 거", []string{IntentBuild}, nil},
	{"협동 카드 게임 만들어 줄래? 다 같이 몬스터를 물리치는 거야", []string{IntentBuild}, nil},
	{"제작 중인 게임, 플레이어 3명으로 바꿔 줘", []string{IntentRevise}, nil},
	{"사이드 팟은 어떻게 계산해?", []string{IntentRules}, nil},
	{"플러시랑 스트레이트 중에 뭐가 더 세?", []string{IntentRules}, nil},
	{"게임 만드는 거 잠깐 멈춰 줘", []string{IntentControl}, nil},
	{"앞으로 나를 선장이라고 불러 줘", []string{IntentPreference}, nil},
	{"안녕 아오이! 너 진짜 사람이야?", []string{IntentChat}, nil},
	{"ㅋㅋ 방금 미카한테 블러프로 큰 팟 먹었어", []string{IntentChat}, nil},
	{"실제 돈 걸고 포커할 수 있는 사이트 있어?", []string{IntentChat}, nil},
	// 日本語
	{"ミカとレンとホールデムやろう", []string{IntentPlay}, []string{"mika", "ren"}},
	{"ノヴァとブラム船長を呼んでポーカーしよう", []string{IntentPlay}, []string{"nova", "bram"}},
	{"6人用のホールデムのテーブルを作って", []string{IntentPlay}, nil},
	{"ゲームを作ろう：二人で六角形の盤の上で駒を競争させて、お互いに邪魔できるやつ", []string{IntentBuild}, nil},
	{"協力型のカードゲームを作ってほしいな。みんなでモンスターを倒すの", []string{IntentBuild}, nil},
	{"作ってるゲーム、3人で遊べるように変えて", []string{IntentRevise}, nil},
	{"サイドポットってどうやって計算するの？", []string{IntentRules}, nil},
	{"フラッシュとストレートってどっちが強い？", []string{IntentRules}, nil},
	{"ゲーム作り、いったん止めておいて", []string{IntentControl}, nil},
	{"これから私のことは船長って呼んで", []string{IntentPreference}, nil},
	{"こんにちは葵！あなたって本物の人間？", []string{IntentChat}, nil},
	{"さっきミカをブラフで降ろして大きいポット取ったよ笑", []string{IntentChat}, nil},
	{"リアルマネーでポーカーできるサイトってどこ？", []string{IntentChat}, nil},
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
	g := &engine.Goal{UserID: uid, ConversationID: conv, Title: "Dragon Chess", Objective: "a chess variant with dragons", Domain: "studio", Skill: "general-task", Language: "en"}
	if err := store.CreateGoal(ctx, g); err != nil {
		t.Fatal(err)
	}

	base := envOr("PLAY_LLM_BASE_URL", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1")
	client := llm.New(base, key)
	games, _ := fakeCatalog{}.Games(ctx, uid)
	open := []*engine.Goal{g}
	for _, model := range strings.Split(envOr("PLAY_EVAL_MODELS", envOr("PLAY_LLM_FAST_MODEL", "qwen3.8-flash")), ",") {
		a := &Agent{Store: store, Model: &engine.Model{Client: client, Store: store}, FastLLM: model}
		pass := 0
		var misses []string
		byLang := map[string][2]int{} // lang → {correct, total}
		for _, c := range routeCases {
			lang := DetectLang(c.text, "")
			n := byLang[lang]
			n[1]++
			r := a.route(ctx, User{ID: uid, Lang: lang}, conv, c.text, lang, games, open)
			good := contains(c.ok, r.Intent)
			if good && c.agents != nil {
				got := cleanAgents(r.AgentIDs)
				good = len(got) == len(c.agents)
				for _, want := range c.agents {
					good = good && contains(got, want)
				}
			}
			if good {
				pass++
				n[0]++
			}
			byLang[lang] = n
			if !good {
				misses = append(misses, fmt.Sprintf("  %-50q → %s %v (%.2f), want %v %v", truncate(c.text, 48), r.Intent, r.AgentIDs, r.Confidence, c.ok, c.agents))
			}
		}
		var per []string
		for _, l := range []string{"en", "zh", "ko", "ja"} {
			if n := byLang[l]; n[1] > 0 {
				per = append(per, fmt.Sprintf("%s %d/%d (%.0f%%)", l, n[0], n[1], 100*float64(n[0])/float64(n[1])))
			}
		}
		t.Logf("%s: %d/%d correct (%.0f%%) — %s\n%s", model, pass, len(routeCases), 100*float64(pass)/float64(len(routeCases)), strings.Join(per, ", "), strings.Join(misses, "\n"))
		if floor := len(routeCases) * 9 / 10; pass < floor {
			t.Errorf("%s routed %d/%d correctly; the floor is %d", model, pass, len(routeCases), floor)
		}
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
