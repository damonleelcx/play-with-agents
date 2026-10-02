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
		for _, c := range routeCases {
			lang := DetectLang(c.text, "")
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
			} else {
				misses = append(misses, fmt.Sprintf("  %-50q → %s %v (%.2f), want %v %v", truncate(c.text, 48), r.Intent, r.AgentIDs, r.Confidence, c.ok, c.agents))
			}
		}
		t.Logf("%s: %d/%d correct (%.0f%%)\n%s", model, pass, len(routeCases), 100*float64(pass)/float64(len(routeCases)), strings.Join(misses, "\n"))
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
