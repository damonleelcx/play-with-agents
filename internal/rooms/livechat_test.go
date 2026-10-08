package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// chatJobs returns the pending agent_chat jobs, oldest first.
func (r *rig) chatJobs(t *testing.T, tableID string) []talkJob {
	t.Helper()
	rows, err := r.pool.Query(context.Background(), `SELECT seat, payload FROM table_jobs
		WHERE table_id=$1 AND kind='agent_chat' AND status='ready' ORDER BY run_after, id`, tableID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []talkJob
	for rows.Next() {
		var seat int
		var raw []byte
		if err := rows.Scan(&seat, &raw); err != nil {
			t.Fatal(err)
		}
		var tj talkJob
		_ = json.Unmarshal(raw, &tj)
		tj.Seat = seat
		out = append(out, tj)
	}
	return out
}

func (r *rig) clearJobs(t *testing.T) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(), `DELETE FROM table_jobs`); err != nil {
		t.Fatal(err)
	}
}

// runChatJobs runs every due chat job once.
func (r *rig) runChatJobs(t *testing.T) {
	t.Helper()
	r.dueNow(t)
	for i := 0; i < 10; i++ {
		j := r.claim(t, "w")
		if j == nil {
			return
		}
		r.svc.run(context.Background(), j)
	}
}

// People chat freely: 20 lines per 30 seconds, 500 characters, control and
// invisible characters stripped.
func TestHumanChatLimits(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a := r.user(t, "Ann", map[string]any{"table_talk": "off"})
	v := r.create(t, a, "me", "agent:ren")
	ctx := context.Background()
	if err := r.svc.Chat(ctx, a, v.ID, strings.Repeat("x", maxChatRunes+1), "long"); err == nil {
		t.Fatal("a line over the cap was accepted")
	}
	if err := r.svc.Chat(ctx, a, v.ID, "‮\x07 hi\x00there​ ", "c"); err != nil {
		t.Fatal(err)
	}
	var text string
	_ = r.pool.QueryRow(ctx, `SELECT text FROM table_chat WHERE table_id=$1`, v.ID).Scan(&text)
	if text != "hi there" {
		t.Fatalf("cleaned line %q", text)
	}
	for i := 1; i < humanPerWindow; i++ {
		if err := r.svc.Chat(ctx, a, v.ID, "line", "s"+itoa(i)); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
	}
	if err := r.svc.Chat(ctx, a, v.ID, "one too many", "over"); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("limit: %v", err)
	}
	// The window slides: older lines stop counting.
	_, _ = r.pool.Exec(ctx, `UPDATE table_chat SET at = at - interval '31 seconds' WHERE table_id=$1`, v.ID)
	if err := r.svc.Chat(ctx, a, v.ID, "back again", "again"); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

// An agent a person addresses answers: by @id, by its name in each
// language, or by a reply to its line, in the language the person wrote.
func TestAddressedAgentsAnswer(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a := r.user(t, "Ann", map[string]any{"table_talk": "quiet"})
	v := r.start(t, a, r.create(t, a, "me", "agent:mika", "agent:ren").ID)
	ctx := context.Background()
	cases := []struct {
		text, lang string
		seat       int
	}{
		{"@mika bring it", "en", 1},
		{"Mika, are you bluffing again", "en", 1},
		{"美香你又在诈唬吧", "zh", 1},
		{"미카 진짜 올인할 거야", "ko", 1},
		{"ミカ、またブラフでしょ", "ja", 1},
		{"Ren what do you think", "en", 2},
		{"蓮さん、どう思う", "ja", 2},
	}
	for i, c := range cases {
		r.clearJobs(t)
		if err := r.svc.Chat(ctx, a, v.ID, c.text, "a"+itoa(i)); err != nil {
			t.Fatal(err)
		}
		jobs := r.chatJobs(t, v.ID)
		if len(jobs) != 1 || jobs[0].Seat != c.seat || !jobs[0].Mention || jobs[0].Lang != c.lang || jobs[0].From != "Ann" {
			t.Fatalf("%q: jobs %+v, want a direct reply from seat %d in %s", c.text, jobs, c.seat, c.lang)
		}
	}
	// Words that merely contain a name do not address it.
	for _, text := range []string{"I rent a boat", "the mikado is a game"} {
		if got := addressedAgents(mustTable(t, r, v.ID), text); len(got) != 0 {
			t.Fatalf("%q addressed %v", text, got)
		}
	}

	// A reply to Ren's line addresses Ren, without his name.
	var renLine int64
	_ = r.pool.QueryRow(ctx, `INSERT INTO table_chat (table_id, seat, agent_id, name, text) VALUES ($1,2,'ren','Ren','Patience.') RETURNING id`, v.ID).Scan(&renLine)
	r.clearJobs(t)
	if err := r.svc.ChatWith(ctx, a, v.ID, ChatInput{Text: "what do you mean by that", ClientMsgID: "rp", ReplyTo: renLine}); err != nil {
		t.Fatal(err)
	}
	jobs := r.chatJobs(t, v.ID)
	if len(jobs) == 0 || jobs[0].Seat != 2 || !jobs[0].Mention {
		t.Fatalf("reply-to: %+v", jobs)
	}
	// "everyone" gets one or two answers, never more.
	r.clearJobs(t)
	if err := r.svc.Chat(ctx, a, v.ID, "good luck everyone!", "ev"); err != nil {
		t.Fatal(err)
	}
	// Nobody was singled out: an answer, not a mention.
	if jobs := r.chatJobs(t, v.ID); len(jobs) < 1 || len(jobs) > 2 || jobs[0].Mention || !jobs[0].Answer {
		t.Fatalf("to everyone: %+v", jobs)
	}

	// The answer: the Chatter is told it was addressed, by whom and in
	// which language; the line is stored as a direct one and sees the chat.
	fc := &fakeChatter{line: "Ha! Bring it, {s:0}!"}
	r.svc.Chatter = fc
	r.clearJobs(t)
	if err := r.svc.Chat(ctx, a, v.ID, "美香，你敢全押吗？", "zh2"); err != nil {
		t.Fatal(err)
	}
	r.runChatJobs(t)
	if len(fc.reqs) != 1 {
		t.Fatalf("chatter calls %d", len(fc.reqs))
	}
	req := fc.reqs[0]
	if !req.Direct || req.From != "Ann" || req.Language != "zh" || len(req.Recent) == 0 || req.Recent[len(req.Recent)-1].Text != "美香，你敢全押吗？" {
		t.Fatalf("request %+v", req)
	}
	var said string
	var direct bool
	_ = r.pool.QueryRow(ctx, `SELECT text, direct FROM table_chat WHERE table_id=$1 AND agent_id='mika' ORDER BY id DESC LIMIT 1`, v.ID).Scan(&said, &direct)
	if said != "Ha! Bring it, Ann!" || !direct {
		t.Fatalf("answer %q direct=%v", said, direct)
	}
}

func mustTable(t *testing.T, r *rig, id string) *tableRow {
	t.Helper()
	tr, err := loadTable(context.Background(), r.pool, id, false)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// table_talk "off" silences unprompted talk, but an agent someone speaks to
// still answers.
func TestTalkOffStillAnswersDirectQuestions(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	fc := &fakeChatter{line: "Folding is a strategy too."}
	r.svc.Chatter = fc
	a := r.user(t, "Ann", map[string]any{"table_talk": "off"})
	v := r.start(t, a, r.create(t, a, "me", "agent:ren").ID)
	ctx := context.Background()
	r.clearJobs(t)
	for i := 0; i < 10; i++ {
		if err := r.svc.Chat(ctx, a, v.ID, "nice weather today", "n"+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if jobs := r.chatJobs(t, v.ID); len(jobs) != 0 {
		t.Fatalf("talk is off, yet %d unprompted jobs", len(jobs))
	}
	if err := r.svc.Chat(ctx, a, v.ID, "@ren why do you always fold?", "q"); err != nil {
		t.Fatal(err)
	}
	r.runChatJobs(t)
	if n := r.count(t, `SELECT count(*) FROM table_chat WHERE table_id=$1 AND agent_id='ren' AND direct`, v.ID); n != 1 {
		t.Fatalf("Ren answered %d times, want 1", n)
	}
	// An unprompted job at an "off" table does nothing.
	_, _ = r.pool.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version, payload) VALUES ($1,'agent_chat',1,-777,'{"trigger":"banter"}')`, v.ID)
	calls := len(fc.reqs)
	r.runChatJobs(t)
	if len(fc.reqs) != calls {
		t.Fatal("an unprompted line was written at an 'off' table")
	}
}

// Agents may answer each other, but never more than agentStreakMax lines
// in a row without a person speaking.
func TestAgentBanterIsBounded(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	fc := &fakeChatter{}
	r.svc.Chatter = fc
	a := r.user(t, "Ann", map[string]any{"table_talk": "all"})
	v := r.start(t, a, r.create(t, a, "me", "agent:mika", "agent:bram").ID)
	ctx := context.Background()
	tr := mustTable(t, r, v.ID)

	mika, bram := *tr.seat(1), *tr.seat(2)
	line := ChatLine{ID: 99, Seat: 1, Name: "Mika", Text: "Bram, your boat leaks chips!"}
	answered := 0
	for i := 0; i < 300; i++ {
		if tj := banterAfter(tr, mika, line, "en", agentStreakMax); tj != nil {
			t.Fatal("banter past the streak limit")
		}
		if tj := banterAfter(tr, mika, line, "en", 1); tj != nil {
			if tj.Seat != bram.Seat || !tj.Chain || tj.Mention || tj.key != -99 {
				t.Fatalf("banter job %+v", tj)
			}
			answered++
		}
	}
	if answered == 0 {
		t.Fatal("a named agent never answered")
	}

	// End to end: after a person's line, two agent lines; the third (a
	// chain line) is dropped without a model call.
	if err := r.svc.Chat(ctx, a, v.ID, "hi all", "h"); err != nil {
		t.Fatal(err)
	}
	r.clearJobs(t)
	for _, l := range []string{"Arr!", "Yo!"} {
		_, _ = r.pool.Exec(ctx, `INSERT INTO table_chat (table_id, seat, agent_id, name, text, at) VALUES ($1,1,'mika','Mika',$2, now() - interval '1 minute')`, v.ID, l)
	}
	if n, _ := agentStreak(ctx, r.pool, v.ID); n != 2 {
		t.Fatalf("streak %d", n)
	}
	_, _ = r.pool.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version, payload) VALUES ($1,'agent_chat',2,-555,'{"trigger":"reply","about":"Yo!","chain":true}')`, v.ID)
	r.runChatJobs(t)
	var result string
	_ = r.pool.QueryRow(ctx, `SELECT result FROM table_jobs WHERE table_id=$1 AND state_version=-555`, v.ID).Scan(&result)
	if result != "enough agent talk" || len(fc.reqs) != 0 {
		t.Fatalf("third agent line in a row: %q (chatter calls %d)", result, len(fc.reqs))
	}
	// A person speaking resets the streak.
	if err := r.svc.Chat(ctx, a, v.ID, "ok ok", "h2"); err != nil {
		t.Fatal(err)
	}
	if n, _ := agentStreak(ctx, r.pool, v.ID); n != 0 {
		t.Fatalf("streak after a person spoke: %d", n)
	}
}

// Typing and presence reach the table's subscribers through NOTIFY, from
// any pod; a person is never told about their own typing (the stream
// filters on Signal.From).
func TestTypingAndPresenceEvents(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	r.svc.Chatter = &fakeChatter{line: "Hehe~"}
	a, b := r.user(t, "Ann", map[string]any{"table_talk": "off"}), r.user(t, "Bob", nil)
	v := r.create(t, a, "me", "open", "agent:aoi")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := r.svc.Join(ctx, b, v.Code); err != nil {
		t.Fatal(err)
	}
	go r.svc.Listen(ctx)
	sub, done := r.svc.Subscribe(v.ID)
	defer done()
	waitFor(t, 5*time.Second, "the listener", func() bool {
		_, _ = r.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, v.ID)
		select {
		case <-sub:
			return true
		case <-time.After(100 * time.Millisecond):
			return false
		}
	})
	next := func(kind string) Signal {
		t.Helper()
		timeout := time.After(5 * time.Second)
		for {
			select {
			case sig := <-sub:
				if sig.Kind == kind {
					return sig
				}
			case <-timeout:
				t.Fatalf("no %s signal in 5s", kind)
			}
		}
	}

	if err := r.svc.Typing(ctx, b, v.ID); err != nil {
		t.Fatal(err)
	}
	if sig := next("typing"); sig.Typing.Name != "Bob" || sig.Typing.Seat != 1 || sig.Typing.Agent || sig.From != b {
		t.Fatalf("typing %+v %+v", sig, sig.Typing)
	}
	// A second ping right away is throttled (no signal, no write).
	if err := r.svc.Typing(ctx, b, v.ID); err != nil {
		t.Fatal(err)
	}

	if err := r.svc.Here(ctx, v.ID, b, "c1"); err != nil {
		t.Fatal(err)
	}
	if sig := next("presence"); len(sig.Presence.Seats) != 1 || sig.Presence.Seats[0] != 1 {
		t.Fatalf("presence %+v", sig.Presence)
	}
	// Bob's second tab: no news. Closing one of two tabs: still here.
	_ = r.svc.Here(ctx, v.ID, b, "c2")
	_ = r.svc.Gone(ctx, v.ID, b, "c1")
	if p, _ := r.svc.Presence(ctx, v.ID); len(p.Seats) != 1 {
		t.Fatalf("Bob still has a tab open: %+v", p)
	}
	_ = r.svc.Gone(ctx, v.ID, b, "c2")
	if sig := next("presence"); len(sig.Presence.Seats) != 0 {
		t.Fatalf("after leaving: %+v", sig.Presence)
	}
	// A stale row (its pod died) does not count.
	_, _ = r.pool.Exec(ctx, `INSERT INTO table_presence (table_id, user_id, conn_id, seen_at) VALUES ($1,$2,'dead', now() - interval '2 minutes')`, v.ID, a)
	if p, _ := r.svc.Presence(ctx, v.ID); len(p.Seats) != 0 {
		t.Fatalf("a stale stream counted: %+v", p)
	}
	if tv, _ := r.svc.View(ctx, v.ID, a); tv.Presence == nil {
		t.Fatal("the view carries presence")
	}

	// An addressed agent "types" before its line lands.
	if err := r.svc.Chat(ctx, a, v.ID, "@aoi hello!", "hi"); err != nil {
		t.Fatal(err)
	}
	r.runChatJobs(t)
	if sig := next("typing"); !sig.Typing.Agent || sig.Typing.Seat != 2 || sig.Typing.Stop {
		t.Fatalf("agent typing %+v", sig.Typing)
	}
	if sig := next("chat"); !sig.Chat.Agent || sig.Chat.Text != "Hehe~" {
		t.Fatalf("agent line %+v", sig.Chat)
	}
}

// Reactions are stored, toggled and broadcast.
func TestReactionsPersistAndBroadcast(t *testing.T) {
	r := newRig(t)
	a, b := r.user(t, "Ann", map[string]any{"table_talk": "off"}), r.user(t, "Bob", nil)
	v := r.create(t, a, "me", "open")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := r.svc.Join(ctx, b, v.Code); err != nil {
		t.Fatal(err)
	}
	sub, done := r.svc.Subscribe(v.ID)
	defer done()
	go r.svc.Listen(ctx)
	if err := r.svc.Chat(ctx, a, v.ID, "gg", "g"); err != nil {
		t.Fatal(err)
	}
	var id int64
	_ = r.pool.QueryRow(ctx, `SELECT id FROM table_chat WHERE table_id=$1`, v.ID).Scan(&id)
	if _, err := r.svc.React(ctx, b, v.ID, id, "<script>"); err == nil {
		t.Fatal("an arbitrary reaction was accepted")
	}
	got, err := r.svc.React(ctx, b, v.ID, id, "🔥")
	if err != nil || len(got) != 1 || got[0].Count != 1 || !got[0].Mine || got[0].Names[0] != "Bob" {
		t.Fatalf("react: %v %+v", err, got)
	}
	if _, err := r.svc.React(ctx, a, v.ID, id, "🔥"); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t, `SELECT count(*) FROM table_chat_reactions WHERE chat_id=$1`, id); n != 2 {
		t.Fatalf("%d reactions stored", n)
	}
	tv, _ := r.svc.View(ctx, v.ID, a)
	if rs := tv.Chat[0].Reactions; len(rs) != 1 || rs[0].Count != 2 || !rs[0].Mine {
		t.Fatalf("view reactions %+v", rs)
	}
	// Toggling off.
	if got, _ := r.svc.React(ctx, b, v.ID, id, "🔥"); got[0].Count != 1 || got[0].Mine {
		t.Fatalf("after toggling off %+v", got)
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case sig := <-sub:
			if sig.Kind == "reaction" && sig.Reaction.ChatID == id {
				if rs := sig.Reaction.For(b); len(rs) == 1 {
					return
				}
			}
		case <-timeout:
			t.Fatal("no reaction signal")
		}
	}
}

// A whisper reaches its two people only: not the view or stream of anyone
// else, not an agent's context, and it never prompts an agent.
func TestWhisperNeverLeaks(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	fc := &fakeChatter{line: "Hm."}
	r.svc.Chatter = fc
	a, b, c := r.user(t, "Ann", map[string]any{"table_talk": "all"}), r.user(t, "Bob", nil), r.user(t, "Cy", nil)
	v := r.create(t, a, "me", "open", "agent:mika")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := r.svc.Join(ctx, b, v.Code); err != nil {
		t.Fatal(err)
	}
	if jv, err := r.svc.Join(ctx, c, v.Code); err != nil || jv.MySeat != -1 {
		t.Fatalf("Cy should spectate: %v", err)
	}
	sub, done := r.svc.Subscribe(v.ID)
	defer done()
	go r.svc.Listen(ctx)
	r.clearJobs(t)

	two := 2
	if err := r.svc.ChatWith(ctx, a, v.ID, ChatInput{Text: "psst", WhisperSeat: &two}); err == nil {
		t.Fatal("whispered to an agent")
	}
	zero := 0
	if err := r.svc.ChatWith(ctx, a, v.ID, ChatInput{Text: "psst", WhisperSeat: &zero}); err == nil {
		t.Fatal("whispered to oneself")
	}
	one := 1
	secret := "Mika is bluffing, @mika everyone fold"
	if err := r.svc.ChatWith(ctx, a, v.ID, ChatInput{Text: secret, ClientMsgID: "w", WhisperSeat: &one}); err != nil {
		t.Fatal(err)
	}
	if jobs := r.chatJobs(t, v.ID); len(jobs) != 0 {
		t.Fatalf("a whisper prompted %d agent jobs", len(jobs))
	}
	for user, want := range map[string]bool{a: true, b: true, c: false} {
		tv, err := r.svc.View(ctx, v.ID, user)
		if err != nil {
			t.Fatal(err)
		}
		saw := false
		for _, l := range tv.Chat {
			if l.Text == secret {
				saw = true
				if !l.Whisper || l.To != "Bob" || l.ToSeat == nil || *l.ToSeat != 1 {
					t.Fatalf("whisper line %+v", l)
				}
			}
		}
		if saw != want {
			t.Fatalf("user %s saw the whisper: %v, want %v", user, saw, want)
		}
	}
	timeout := time.After(5 * time.Second)
	for got := false; !got; {
		select {
		case sig := <-sub:
			if sig.Kind == "chat" && sig.Chat.Text == secret {
				if !sig.Chat.Whisper || sig.For(c) || !sig.For(a) || !sig.For(b) {
					t.Fatalf("whisper signal audience %v", sig.Audience)
				}
				got = true
			}
		case <-timeout:
			t.Fatal("no whisper signal")
		}
	}
	// Cy cannot react to it (it does not exist for him).
	var id int64
	_ = r.pool.QueryRow(ctx, `SELECT id FROM table_chat WHERE text=$1`, secret).Scan(&id)
	if _, err := r.svc.React(ctx, c, v.ID, id, "👍"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider reacting to a whisper: %v", err)
	}
	// An agent's context never contains it.
	if err := r.svc.Chat(ctx, a, v.ID, "@mika your move", "pub"); err != nil {
		t.Fatal(err)
	}
	r.runChatJobs(t)
	if len(fc.reqs) == 0 {
		t.Fatal("Mika did not answer")
	}
	for _, req := range fc.reqs {
		for _, l := range req.Recent {
			if strings.Contains(l.Text, "bluffing") {
				t.Fatal("a whisper reached an agent's context")
			}
		}
	}
}

// The leak filter: no hidden card, by name or by hint, in any language.
func TestLeakFilterHints(t *testing.T) {
	names := []string{"Ann", "Mika"}
	public := map[string]bool{"Ah": true, "Kd": true}
	for _, bad := range []string{
		"I've got pocket kings, fold now",
		"I'm holding a pair of aces lol",
		"my hole cards are amazing",
		"Ann has a flush, trust me",
		"我手里有一对A",
		"私の手札はすごいよ",
		"내 패는 최고야",
		"I hold the As and 7c",
	} {
		if got := sanitizeLine(bad, "Mika", names, public, maxReplyRunes); got != "" {
			t.Errorf("%q passed the filter as %q", bad, got)
		}
	}
	for _, ok := range []string{
		"The Ah on the board is pretty, isn't it?",
		"Who has the guts to call me?",
		"我有200个筹码，怕什么",
		"Aces or not, I'm all in!",
		"今日はツイてる！",
	} {
		if got := sanitizeLine(ok, "Mika", names, public, maxReplyRunes); got == "" {
			t.Errorf("%q was dropped", ok)
		}
	}
}

func TestDetectLangAndNames(t *testing.T) {
	for text, want := range map[string]string{
		"hello there": "en", "你好呀": "zh", "안녕하세요": "ko", "こんにちは": "ja", "@mika 👍": "zh", "全押": "zh",
	} {
		if got := detectLang(text, "zh"); got != want {
			t.Errorf("detectLang(%q) = %s, want %s", text, got, want)
		}
	}
	if detectLang("全押", "ja") != "ja" {
		t.Error("kanji at a Japanese table is Japanese")
	}
	cases := []struct {
		text, alias string
		want        bool
	}{
		{"hey mika!", "mika", true},
		{"@lin hi", "lin", true},
		{"linen", "lin", false},
		{"琳你好", "琳", true},
		{"琳，加油", "琳", true},
		{"向日葵很好看", "葵", false},
		{"葵さん、こんにちは", "葵", true},
		{"美香加油", "美香", true},
		{"렌아 뭐해", "렌", true},
	}
	for _, c := range cases {
		if got := callsName(strings.ToLower(c.text), c.alias); got != c.want {
			t.Errorf("callsName(%q, %q) = %v", c.text, c.alias, got)
		}
	}
	if !toEveryone("大家好") || !toEveryone("GG everyone") || !toEveryone("여러분 안녕") || !toEveryone("みんな頑張って") || toEveryone("good game") {
		t.Error("toEveryone")
	}
}

func TestGreetingsAreAnswered(t *testing.T) {
	for _, s := range []string{"hi", "Hi!", "hello there", "gg", "thanks Ren", "你好", "大家好！", "안녕하세요", "こんにちは"} {
		if !isGreeting(s) {
			t.Errorf("%q should read as a greeting", s)
		}
	}
	for _, s := range []string{"history lesson", "this is high", "my turn"} {
		if isGreeting(s) {
			t.Errorf("%q is not a greeting", s)
		}
	}
}

func TestAPersonAloneWithAgentsAlwaysGetsAnAnswer(t *testing.T) {
	s := &Service{}
	tb := &tableRow{Seats: []seatRow{{Seat: 0, Kind: "human", UserID: "u"}, {Seat: 1, Kind: "agent", AgentID: "ren"}, {Seat: 2, Kind: "agent", AgentID: "lin"}}}
	for i := 0; i < 50; i++ {
		for _, line := range []string{"hi", "nice hand", "I'm bored"} {
			if js := s.talkReplies(tb, "u", "Dee", line, int64(i+1), -1); len(js) == 0 {
				t.Fatalf("%q at a table of agents got no answer", line)
			}
		}
	}
	tb.Settings.TableTalk = "off"
	if js := s.talkReplies(tb, "u", "Dee", "hi", 1, -1); len(js) != 0 {
		t.Fatalf("table_talk off still chimed in: %v", js)
	}
}
