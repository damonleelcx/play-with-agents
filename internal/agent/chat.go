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

// outcome is what acting on a route produced, for the reply and its meta.
type outcome struct {
	note    string // what happened, for the reply prompt ("FOR THIS REPLY")
	mood    persona.Mood
	cards   []Card
	goalID  string
	lang    string   // set when the player just changed their language
	pending *Pending // a confirmation this reply asks for (see confirm.go)
	prefs   bool     // the player's settings changed: rebuild the persona
}

// turn is one message as the handlers see it.
type turn struct {
	u      User
	convID string
	text   string
	lang   string
	r      Route
	games  []GameInfo
	open   []*engine.Goal
	tables []TableInfo // open tables (lobby, playing) the player is part of
	prefs  map[string]any
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
	t := &turn{u: u, convID: convID, text: text, lang: lang, games: a.games(ctx, u.ID), open: a.openMissions(ctx, u.ID),
		tables: a.openTables(ctx, u.ID), prefs: prefs}

	// A confirmation asked for in the previous reply is answered first, and
	// only an explicit yes executes it. Anything else drops it and the
	// message is routed as usual.
	var out outcome
	if pend := a.pendingConfirmation(ctx, convID, userMsgID); pend != nil {
		switch confirmAnswer(text) {
		case answerYes:
			t.r = Route{Intent: IntentConfirm, Action: "yes", Confidence: 1}
			out = a.confirm(ctx, t, pend)
		case answerNo:
			t.r = Route{Intent: IntentConfirm, Action: "no", Confidence: 1}
			out = outcome{mood: persona.Neutral, note: "The player said no to: " + pend.Label + ". Nothing was done. Acknowledge in one short line."}
		}
	}
	design := a.conversationMode(ctx, convID) == "design"
	if design && t.r.Intent == "" {
		// A design session: Aoi designs with the player; nothing is routed
		// to tables or builds (the page's buttons make the plan and build).
		t.r = Route{Intent: IntentDesign, Confidence: 1}
		out = a.designOutcome(ctx, t)
	}
	if t.r.Intent == "" {
		t.r = a.route(ctx, u, convID, text, lang, t.games, t.open, t.tables)
		out = a.act(ctx, t)
	}
	if out.lang != "" {
		lang = out.lang
	}
	if out.prefs {
		// "Call me Captain" should already be honoured in this very reply.
		p = persona.PrefsFrom(a.prefs(ctx, u.ID))
	}

	meta := map[string]any{"intent": t.r.Intent, "confidence": t.r.Confidence, "mood": out.mood}
	if t.r.Action != "" {
		meta["action"] = t.r.Action
	}
	if len(out.cards) > 0 {
		meta["cards"] = out.cards
	}
	if out.goalID != "" {
		meta["goal_id"] = out.goalID
	}
	if out.pending != nil {
		meta["pending"] = out.pending
	}
	sink.Meta(meta)

	sys := persona.ChatSystem(lang, p, u.Name, time.Now(), a.chatContext(ctx, u, p, t.open, out.note))
	msgs := []llm.Message{{Role: "system", Content: sys}}
	depth := 14
	if design {
		depth = 30 // a design builds on everything said so far
	}
	msgs = append(msgs, a.history(ctx, convID, userMsgID, depth)...)
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
		if out.pending != nil {
			// The player cannot have read the question; do not leave it armed.
			delete(meta, "pending")
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
	if design && !hasKey(meta, "error") {
		// The notes catch up with the exchange before the turn ends, so the
		// page's design panel updates with the reply.
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		if d, err := a.updateDesign(dctx, u, convID, text, reply.String()); err == nil {
			sink.Meta(map[string]any{"design": d})
		} else {
			slog.Warn("design doc", "conv", convID, "err", err)
		}
		cancel()
	}
	go a.afterTurn(context.WithoutCancel(ctx), u, p, convID, text, t.r)
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
		fmt.Fprintf(&b, "\nWAITING FOR THE PLAYER'S APPROVAL: %d (they approve on the mission card, or by telling you and then confirming; either way the decision is on record)\n", pending)
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

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }
