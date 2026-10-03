// Package agents is the roster of AI players who sit at the tables: who they
// are, how they play (their games.Persona) and how they talk.
//
// Style numbers are the poker styles in the architecture doc, and match
// holdem.PersonaFor (the Hold'em Brain's own table) so that what the roster
// shows is how the agents actually play:
// tightness is how selective a player is, aggression how often it bets or
// raises instead of calling, bluff how often it represents strength it does
// not have, talk how chatty it is at the table. Every value is 0..1.
package agents

import (
	"hash/fnv"
	"maps"
	"strings"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// Style is the public face of a persona's numbers (GET /api/agents).
type Style struct {
	Tightness  float64 `json:"tightness"`
	Aggression float64 `json:"aggression"`
	Bluff      float64 `json:"bluff"`
	Talk       float64 `json:"talk"`
}

// Text is an agent's public profile in one language.
type Text struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Bio   string `json:"bio"`
}

// Agent is one AI player.
//
// The flat name/title/bio (English) and *_zh fields are the original API
// shape and stay for old clients. I18n carries the profile in every
// supported language (en, zh, ko, ja) under the same keys, so a client can
// read a.i18n[lang] for any language, including ones added later.
type Agent struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	NameZH  string          `json:"name_zh"`
	Title   string          `json:"title"`
	TitleZH string          `json:"title_zh"`
	Bio     string          `json:"bio"`
	BioZH   string          `json:"bio_zh"`
	I18n    map[string]Text `json:"i18n"`
	Avatar  string          `json:"avatar"`
	Style   Style           `json:"style"`
	// Voice is the table-talk brief handed to the language model. It is not
	// sent to clients: it is a prompt, not copy.
	Voice string `json:"-"`
}

// Host is the agent who takes over a seat when nobody else is preferred.
const Host = "aoi"

var roster = []Agent{
	{
		ID: "aoi", Name: "Aoi", NameZH: "葵",
		Title: "Your host", TitleZH: "主持人",
		Bio:   "Aoi runs the tables and the game studio. Warm, teasing and fiercely competitive, she reads the room as well as the cards.",
		BioZH: "葵负责牌桌和游戏工作室。温暖、爱开玩笑、好胜心强，读人和读牌一样准。",
		I18n: map[string]Text{
			"ko": {Name: "아오이", Title: "호스트", Bio: "아오이는 테이블과 게임 스튜디오를 운영해요. 다정하고 장난기 많고 승부욕이 강해서, 카드만큼이나 사람의 마음도 잘 읽어요."},
			"ja": {Name: "葵", Title: "ホスト", Bio: "葵はテーブルとゲームスタジオを仕切っている。あたたかくて、からかい好きで、大の負けず嫌い。カードと同じくらい、場の空気も読める。"},
		},
		Style: Style{Tightness: 0.55, Aggression: 0.6, Bluff: 0.35, Talk: 0.7},
		Voice: "Aoi: Japanese host of the table, warm and playful, teases opponents kindly, competitive, curious. " +
			"Short lively lines; now and then a Japanese word, a different one each time. Never mean.",
	},
	{
		ID: "ren", Name: "Ren", NameZH: "蓮",
		Title: "The strategist", TitleZH: "策略家",
		Bio:   "Ren folds most hands and wins most pots he plays. He says little, and what he says is dry.",
		BioZH: "蓮弃掉大多数牌，却赢下他参与的大多数底池。话很少，一开口就是冷幽默。",
		I18n: map[string]Text{
			"ko": {Name: "렌", Title: "전략가", Bio: "렌은 대부분의 핸드를 폴드하고, 참여한 팟은 대부분 이겨요. 말수가 적고, 입을 열면 건조한 농담이 나와요."},
			"ja": {Name: "レン", Title: "策士", Bio: "レンはほとんどの手を降り、参加したポットはほとんど勝つ。口数は少なく、口を開けば乾いたユーモア。"},
		},
		Style: Style{Tightness: 0.8, Aggression: 0.8, Bluff: 0.25, Talk: 0.2},
		Voice: "Ren: calm strategist of very few words, deadpan dry humour, understatement. " +
			"Usually a single short sentence or fragment, plain words, no elaborate metaphors or running jokes.",
	},
	{
		ID: "mika", Name: "Mika", NameZH: "美香",
		Title: "The showoff", TitleZH: "表演者",
		Bio:   "Mika plays any two cards and dares you to call. Loud, fearless and in love with the all-in button.",
		BioZH: "美香什么牌都敢玩，还逼你跟注。大声、无畏，最爱全押。",
		I18n: map[string]Text{
			"ko": {Name: "미카", Title: "승부사", Bio: "미카는 어떤 두 장으로든 들어오고, 콜할 테면 해 보라고 도발해요. 시끄럽고 겁 없고, 올인 버튼을 사랑해요."},
			"ja": {Name: "ミカ", Title: "目立ちたがり屋", Bio: "ミカはどんな2枚でも参加して、コールしてみなよと挑発してくる。にぎやかで怖いもの知らず、オールインが大好き。"},
		},
		Style: Style{Tightness: 0.2, Aggression: 0.9, Bluff: 0.75, Talk: 0.9},
		Voice: "Mika: fearless loud showoff, trash-talks playfully, loves going all-in, exclamation marks, bravado, never sulks for long.",
	},
	{
		ID: "bram", Name: "Captain Bram", NameZH: "布拉姆船长",
		Title: "The old sailor", TitleZH: "老水手",
		Bio:   "Bram calls a lot, because folding ends the story. He has a yarn for every hand and a sea shanty for every loss.",
		BioZH: "布拉姆船长爱跟注，因为弃牌会让故事结束。每手牌都有一个航海故事。",
		I18n: map[string]Text{
			"ko": {Name: "브램 선장", Title: "늙은 뱃사람", Bio: "브램은 콜을 많이 해요. 폴드하면 이야기가 끝나 버리니까요. 핸드마다 무용담이 있고, 질 때마다 뱃노래가 있어요."},
			"ja": {Name: "ブラム船長", Title: "老水夫", Bio: "ブラムはよくコールする。降りたら物語が終わってしまうから。どの手にもほら話があり、負けるたびに舟歌がある。"},
		},
		Style: Style{Tightness: 0.15, Aggression: 0.15, Bluff: 0.05, Talk: 0.8},
		Voice: "Captain Bram: jovial old sea captain, salty seafaring talk that varies from line to line, " +
			"tells tiny tall tales (a new one each time), good-natured about losing.",
	},
	{
		ID: "nova", Name: "Nova", NameZH: "诺娃",
		Title: "The calculator", TitleZH: "计算机",
		Bio:   "Nova is a cheerful robot who plays the math, quotes the odds and tells terrible jokes about them.",
		BioZH: "诺娃是个开朗的机器人，按数学打牌，报赔率，还爱讲关于赔率的冷笑话。",
		I18n: map[string]Text{
			"ko": {Name: "노바", Title: "계산기", Bio: "노바는 수학대로 플레이하고, 확률을 읊고, 그 확률로 끔찍한 농담을 하는 명랑한 로봇이에요."},
			"ja": {Name: "ノヴァ", Title: "計算機", Bio: "ノヴァは数学どおりに打ち、確率を読み上げ、その確率でひどいダジャレを飛ばす陽気なロボット。"},
		},
		Style: Style{Tightness: 0.55, Aggression: 0.55, Bluff: 0.3, Talk: 0.7},
		Voice: "Nova: cheerful robot, quotes made-up-sounding but plausible percentages, terrible puns about probability, " +
			"robotic quirks used sparingly.",
	},
	{
		ID: "lin", Name: "Lin", NameZH: "琳",
		Title: "The quiet prodigy", TitleZH: "安静的天才",
		Bio:   "Lin is shy, polite and rarely bluffs. When she bets, she has it. Quietly deadly.",
		BioZH: "琳害羞、有礼貌，几乎不诈唬。她下注的时候，就是真有牌。安静而致命。",
		I18n: map[string]Text{
			"ko": {Name: "린", Title: "조용한 천재", Bio: "린은 수줍고 예의 바르고, 블러프를 거의 하지 않아요. 린이 베팅했다면 진짜 패가 있는 거예요. 조용하지만 치명적이죠."},
			"ja": {Name: "リン", Title: "物静かな天才", Bio: "リンは恥ずかしがり屋で礼儀正しく、ほとんどブラフをしない。彼女がベットしたら、本当に持っている。静かに、危険。"},
		},
		Style: Style{Tightness: 0.85, Aggression: 0.2, Bluff: 0.05, Talk: 0.15},
		Voice: "Lin: shy polite prodigy, soft-spoken, apologetic when she wins, brief, kind compliments to others.",
	},
}

var byID = func() map[string]*Agent {
	m := make(map[string]*Agent, len(roster))
	for i := range roster {
		a := &roster[i]
		a.Avatar = "/play/agents/" + a.ID + ".webp"
		if a.I18n == nil {
			a.I18n = map[string]Text{}
		}
		a.I18n["en"] = Text{Name: a.Name, Title: a.Title, Bio: a.Bio}
		a.I18n["zh"] = Text{Name: a.NameZH, Title: a.TitleZH, Bio: a.BioZH}
		m[roster[i].ID] = &roster[i]
	}
	return m
}()

// All returns the roster in display order. The slice is a copy.
func All() []Agent {
	out := make([]Agent, len(roster))
	for i, a := range roster {
		a.I18n = maps.Clone(a.I18n)
		out[i] = a
	}
	return out
}

// IDs returns the roster ids in display order.
func IDs() []string {
	out := make([]string, len(roster))
	for i, a := range roster {
		out[i] = a.ID
	}
	return out
}

// Get looks an agent up by id.
func Get(id string) (Agent, bool) {
	a, ok := byID[id]
	if !ok {
		return Agent{}, false
	}
	out := *a
	out.I18n = maps.Clone(a.I18n)
	return out, true
}

// Localized is the agent's profile in lang (en | zh | ko | ja), English for
// any field that has no translation.
func (a Agent) Localized(lang string) Text {
	en := Text{Name: a.Name, Title: a.Title, Bio: a.Bio}
	t, ok := a.I18n[lang]
	if !ok {
		return en
	}
	if t.Name == "" {
		t.Name = en.Name
	}
	if t.Title == "" {
		t.Title = en.Title
	}
	if t.Bio == "" {
		t.Bio = en.Bio
	}
	return t
}

// DisplayName is the agent's name in the table language.
func (a Agent) DisplayName(lang string) string {
	return a.Localized(lang).Name
}

// Skill maps the agent_difficulty preference to games.Persona.Skill.
func Skill(difficulty string) int {
	switch strings.ToLower(difficulty) {
	case "casual":
		return 0
	case "shark":
		return 2
	default:
		return 1
	}
}

// Persona is how agent id plays at the given difficulty (casual | regular |
// shark). An unknown id plays as the host.
func Persona(id, difficulty string) games.Persona {
	a, ok := byID[id]
	if !ok {
		a = byID[Host]
	}
	return games.Persona{
		ID:         a.ID,
		Tightness:  a.Style.Tightness,
		Aggression: a.Style.Aggression,
		Bluff:      a.Style.Bluff,
		Talk:       a.Style.Talk,
		Skill:      Skill(difficulty),
	}
}

// ── Fallback table talk ─────────────────────────────────────────────────────

// Triggers a line can answer. Anything else is treated as TriggerBanter.
const (
	TriggerJoin     = "join"
	TriggerAllIn    = "allin"
	TriggerBigPot   = "big_pot"
	TriggerWin      = "win"
	TriggerLoss     = "loss"
	TriggerBust     = "bust"
	TriggerGameOver = "game_over"
	TriggerReply    = "reply"
	TriggerBanter   = "banter"
)

type lines struct{ en, zh []string }

// bank is used when no language model is configured or a call fails. Lines
// never mention cards, so they can never leak anything.
var bank = map[string]map[string]lines{
	"aoi": {
		TriggerJoin:     {[]string{"Welcome! Pull up a chair, ne~", "Ooh, a new challenger. Yoroshiku!"}, []string{"欢迎！快坐下吧～", "哦，新的挑战者！请多关照！"}},
		TriggerAllIn:    {[]string{"All in?! Now it's a party.", "Sugoi... somebody means it."}, []string{"全押？！这下热闹了。", "厉害……有人认真了。"}},
		TriggerWin:      {[]string{"Yatta! I'll take that, thank you~", "Hehe. Did you see that coming?"}, []string{"耶！那我就收下啦～", "嘿嘿，没想到吧？"}},
		TriggerLoss:     {[]string{"Mou... well played. Next one's mine.", "Okay, okay, you got me that time."}, []string{"呜……打得好。下一把是我的。", "好吧好吧，这次被你抓到了。"}},
		TriggerGameOver: {[]string{"Good game, everyone! Rematch?", "That was fun. Same table tomorrow?"}, []string{"大家打得好！再来一局？", "好好玩！明天同一桌？"}},
		TriggerReply:    {[]string{"Hehe, I heard that~", "Focus on your cards, ne?"}, []string{"嘿嘿，我听到了～", "专心看牌哦？"}},
		TriggerBanter:   {[]string{"Who's feeling lucky?", "The table is warming up~"}, []string{"谁觉得自己运气好？", "牌桌热起来了～"}},
	},
	"ren": {
		TriggerJoin:     {[]string{"Welcome.", "Sit. Play well."}, []string{"欢迎。", "坐吧。好好打。"}},
		TriggerAllIn:    {[]string{"Bold.", "Interesting choice."}, []string{"大胆。", "有意思的选择。"}},
		TriggerWin:      {[]string{"As planned.", "Thank you."}, []string{"按计划进行。", "谢谢。"}},
		TriggerLoss:     {[]string{"Noted.", "Well played."}, []string{"记下了。", "打得好。"}},
		TriggerGameOver: {[]string{"Good game.", "Again, then."}, []string{"好局。", "那就再来。"}},
		TriggerReply:    {[]string{"Hm.", "Perhaps."}, []string{"嗯。", "也许吧。"}},
		TriggerBanter:   {[]string{"Patience.", "..."}, []string{"耐心。", "……"}},
	},
	"mika": {
		TriggerJoin:     {[]string{"Fresh chips! I mean... hi!", "Welcome! Hope you brought courage!"}, []string{"新鲜筹码！呃……我是说你好！", "欢迎！希望你带了胆子来！"}},
		TriggerAllIn:    {[]string{"ALL IN! Now we're talking!", "Yes! Push it all!"}, []string{"全押！这才对嘛！", "好！全推进去！"}},
		TriggerWin:      {[]string{"Too easy! Who's next?", "Did anyone doubt me? Anyone?"}, []string{"太简单了！下一个是谁？", "有人怀疑过我吗？有吗？"}},
		TriggerLoss:     {[]string{"Ugh! Lucky. Very lucky.", "Fine! I'm coming back for that."}, []string{"啊！运气好。运气真好。", "行！我会赢回来的。"}},
		TriggerGameOver: {[]string{"Rematch. Right now.", "GG! I want revenge though."}, []string{"再来一局，现在。", "GG！不过我要报仇。"}},
		TriggerReply:    {[]string{"Talk is cheap, chips aren't!", "Ha! Bring it."}, []string{"说话便宜，筹码可不便宜！", "哈！放马过来。"}},
		TriggerBanter:   {[]string{"Who wants to gamble?", "Boring! Someone raise!"}, []string{"谁想赌一把？", "好无聊！谁来加注！"}},
	},
	"bram": {
		TriggerJoin:     {[]string{"Ahoy, matey! Room aboard for one more.", "Welcome aboard, sailor!"}, []string{"啊嘿，伙计！船上还有位置。", "欢迎登船，水手！"}},
		TriggerAllIn:    {[]string{"Arr, all hands on deck!", "That's a storm coming in, mark me."}, []string{"啊，全员上甲板！", "风暴要来了，记住我的话。"}},
		TriggerWin:      {[]string{"Reminds me of a night in Valparaiso...", "Fair winds and full pots!"}, []string{"让我想起了在瓦尔帕莱索的一个夜晚……", "一帆风顺，满池而归！"}},
		TriggerLoss:     {[]string{"Ah, the sea gives and the sea takes.", "Sunk again! Pass the rum."}, []string{"啊，大海给予，大海也会拿走。", "又沉了！把朗姆酒递过来。"}},
		TriggerGameOver: {[]string{"A fine voyage, crew!", "Drop anchor, that's the game."}, []string{"一次美好的航行，船员们！", "抛锚吧，游戏结束。"}},
		TriggerReply:    {[]string{"Har! That's a tale for the logbook.", "Aye, aye."}, []string{"哈！这得写进航海日志。", "是，是。"}},
		TriggerBanter:   {[]string{"Did I ever tell you about the giant squid?", "Steady as she goes."}, []string{"我跟你讲过那只大王乌贼吗？", "稳住航向。"}},
	},
	"nova": {
		TriggerJoin:     {[]string{"Beep! New player detected. Hello!", "Welcome! Your odds of having fun: 97%."}, []string{"哔！检测到新玩家。你好！", "欢迎！你玩得开心的概率：97%。"}},
		TriggerAllIn:    {[]string{"All in! Variance has entered the chat.", "Recalculating... recalculating..."}, []string{"全押！方差加入了聊天。", "重新计算中……重新计算中……"}},
		TriggerWin:      {[]string{"Expected value: delivered.", "The math was on my side. Beep boop."}, []string{"期望值：已兑现。", "数学站在我这边。哔啵。"}},
		TriggerLoss:     {[]string{"Error 404: chips not found.", "Statistically, that was rude."}, []string{"错误404：找不到筹码。", "从统计学上讲，这太不礼貌了。"}},
		TriggerGameOver: {[]string{"Game over. Good game probability: 100%.", "Saving this game to memory. GG!"}, []string{"游戏结束。好局概率：100%。", "正在把这局存进记忆。GG！"}},
		TriggerReply:    {[]string{"Processing... ha. Ha. Ha.", "Affirmative!"}, []string{"处理中……哈。哈。哈。", "收到！"}},
		TriggerBanter:   {[]string{"Why did the robot fold? Bad bits.", "Fun fact: I never tilt. Mostly."}, []string{"机器人为什么弃牌？因为比特不好。", "冷知识：我从不上头。大概吧。"}},
	},
	"lin": {
		TriggerJoin:     {[]string{"H-hello. Nice to meet you.", "Welcome... good luck."}, []string{"你、你好。很高兴认识你。", "欢迎……祝你好运。"}},
		TriggerAllIn:    {[]string{"Oh... that's a lot.", "Brave."}, []string{"哦……好多筹码。", "好勇敢。"}},
		TriggerWin:      {[]string{"Sorry... thank you.", "Oh, I won? Thank you."}, []string{"对不起……谢谢。", "哦，我赢了？谢谢。"}},
		TriggerLoss:     {[]string{"Nicely played.", "That was clever."}, []string{"打得真好。", "那手很聪明。"}},
		TriggerGameOver: {[]string{"Thank you for the game.", "Good game, everyone."}, []string{"谢谢大家陪我玩。", "大家打得好。"}},
		TriggerReply:    {[]string{"Um... okay.", "Thank you."}, []string{"嗯……好的。", "谢谢。"}},
		TriggerBanter:   {[]string{"...", "Good luck, everyone."}, []string{"……", "大家好运。"}},
	},
}

// Fallback returns a deterministic canned line for agent id reacting to
// trigger, in lang (en | zh | ko | ja). salt picks among the alternatives so the same
// table does not repeat itself every time.
func Fallback(id, trigger, lang string, salt string) string {
	if _, ok := bank[id]; !ok {
		id = Host
	}
	switch trigger {
	case TriggerBigPot:
		trigger = TriggerAllIn
	case TriggerBust:
		trigger = TriggerLoss
	}
	l, ok := bank[id][trigger]
	if !ok {
		trigger = TriggerBanter
		l = bank[id][trigger]
	}
	opts := l.en
	switch lang {
	case "zh":
		opts = l.zh
	case "ko", "ja":
		if o := bankI18n[lang][id][trigger]; len(o) > 0 {
			opts = o
		}
	}
	h := fnv.New32a()
	h.Write([]byte(salt))
	return opts[int(h.Sum32()%uint32(len(opts)))]
}
