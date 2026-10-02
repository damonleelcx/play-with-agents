// Package agent is the conversational front of Vera: it routes each message to
// an intent, opens or steers durable goals, and answers in her voice. It keeps
// no state of its own between turns — every turn is rebuilt from the database.
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
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/persona"
	"github.com/damonleelcx/play-with-agents/internal/skills"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

type Agent struct {
	Store       *engine.Store
	Model       *engine.Model
	LLM         string
	FastLLM     string
	Mailer      mail.Mailer
	OnCallEmail string
}

// Sink receives the streamed turn.
type Sink interface {
	Meta(v map[string]any)
	Delta(s string)
}

type Route struct {
	Intent     string  `json:"intent"`
	Confidence float64 `json:"confidence"`
	Domain     string  `json:"domain"`
	Skill      string  `json:"skill"`
	GoalID     string  `json:"goal_id"`
	Title      string  `json:"title"`
	Objective  string  `json:"objective"`
	Clarify    string  `json:"clarify"`
	Language   string  `json:"language"`
	Preference struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"preference"`
}

var intents = `legal.intake, legal.explain, legal.research, legal.case_assessment, legal.draft_document, legal.review_document,
legal.deadlines, legal.court_prep, legal.court_filing, legal.hearing_support, legal.negotiation, legal.evidence, legal.client_update,
med.emergency, med.intake, med.triage, med.differential, med.results, med.medications, med.treatment_plan, med.prescription,
med.test_order, med.referral, med.follow_up, med.records, med.explain, med.visit_prep,
medlegal.injury_claim, medlegal.insurance_denial, medlegal.workers_comp, medlegal.malpractice, medlegal.records_request, medlegal.disability,
goal.status, goal.pause, goal.resume, goal.cancel, goal.change, approval.respond, schedule.reminder, account.preferences, smalltalk, refuse`

// Intent → playbook. Intents not listed are answered in the conversation.
var intentSkill = map[string]string{
	"legal.intake": "new-matter", "legal.research": "legal-research-memo", "legal.case_assessment": "case-assessment",
	"legal.draft_document": "demand-letter", "legal.review_document": "contract-review", "legal.deadlines": "deadline-docketing",
	"legal.court_prep": "hearing-preparation", "legal.court_filing": "motion-drafting", "legal.negotiation": "settlement-negotiation",
	"legal.evidence": "evidence-organisation",
	"med.intake":     "symptom-assessment", "med.triage": "symptom-assessment", "med.differential": "symptom-assessment",
	"med.treatment_plan": "symptom-assessment", "med.prescription": "symptom-assessment", "med.test_order": "symptom-assessment",
	"med.referral": "symptom-assessment", "med.results": "lab-review", "med.medications": "medication-reconciliation",
	"med.follow_up": "chronic-care-plan", "med.records": "records-summary", "med.visit_prep": "pre-visit-summary",
	"medlegal.injury_claim": "personal-injury-case", "medlegal.insurance_denial": "insurance-appeal",
	"medlegal.workers_comp": "workers-comp-claim", "medlegal.malpractice": "malpractice-screen",
	"medlegal.records_request": "records-request", "medlegal.disability": "disability-claim",
}

type User struct {
	ID, Email, Name string
	Lang            string
}

// Turn handles one user message end to end. Idempotent on clientMsgID: a
// retried POST returns the reply that was already produced.
func (a *Agent) Turn(ctx context.Context, u User, convID, text, clientMsgID string, sink Sink) (int64, error) {
	// The language the client CHOSE wins. Guessing from the message is only a
	// fallback for an account with no preference: a Chinese-speaking client
	// who types three English words still expects to be answered in Chinese.
	lang := u.Lang
	if lang == "" {
		lang = DetectLang(text, "")
	}
	userMsgID, dup, err := a.insertUserMessage(ctx, convID, text, clientMsgID)
	if err != nil {
		return 0, err
	}
	if dup {
		var id int64
		var content string
		err := a.Store.Pool.QueryRow(ctx, `SELECT id, content FROM messages WHERE conversation_id=$1 AND role='assistant' AND id > $2 ORDER BY id LIMIT 1`,
			convID, userMsgID).Scan(&id, &content)
		if err == nil {
			sink.Meta(map[string]any{"duplicate": true})
			sink.Delta(content)
			return id, nil
		}
	}
	_, _ = a.Store.Pool.Exec(ctx, `UPDATE conversations SET updated_at=now() WHERE id=$1`, convID)

	meta := map[string]any{}
	var preface string

	// 1. Emergencies: deterministic, before any model.
	if flags := tools.CheckRedFlags(text); len(flags) > 0 {
		codes := make([]string, len(flags))
		for i, f := range flags {
			codes[i] = f.Code
		}
		// Guidance goes out before anything else, including the page: a slow
		// relay must never delay "call 911". Whether a clinician was alerted is
		// said only after the page was actually delivered.
		preface = tools.EmergencyText(flags, lang, false) + "\n\n"
		sink.Meta(map[string]any{"intent": "med.emergency", "flags": codes, "expression": "concerned"})
		sink.Delta(preface)
		if a.OnCallEmail != "" {
			env := &tools.Env{Pool: a.Store.Pool, Mailer: a.Mailer, UserID: u.ID, UserEmail: u.Email, UserName: u.Name,
				OnCallEmail: a.OnCallEmail, Scope: fmt.Sprint(userMsgID)}
			args, _ := json.Marshal(map[string]any{"summary": truncate(text, 1500), "flags": codes})
			res, err := tools.Invoke(ctx, env, "escalate_emergency", args, false)
			if err != nil {
				slog.Error("escalate", "err", err)
			} else if res.Output["paged"] == true {
				note := map[string]string{"en": "_I've alerted the ACT on-call clinician._\n\n", "zh": "_我已通知 ACT 值班临床医生。_\n\n"}[lang]
				preface += note
				sink.Delta(note)
			}
		}
		a.Store.Event(ctx, u.ID, "", "", "emergency", map[string]any{"flags": codes, "why": "red-flag rules matched the client's message"})
		meta["intent"], meta["flags"] = "med.emergency", codes
	}

	// 2. Route.
	var route Route
	if preface == "" {
		route = a.route(ctx, u, convID, text, lang)
		meta["intent"], meta["confidence"] = route.Intent, route.Confidence
	}
	if lang == "" {
		lang = "en"
	}

	// 3. Act on the route.
	note := ""
	switch {
	case preface != "":
		note = "An emergency protocol message was just shown above your reply. Keep your reply short and steady: ask whether they are safe and whether help is on the way, offer to stay with them. Do not diagnose."
	case route.Confidence < 0.55 && route.Clarify != "":
		note = "You are not sure what the client needs. Ask exactly this clarifying question in your own words: " + route.Clarify
	case intentSkill[route.Intent] != "":
		goalID, err := a.openGoal(ctx, u, convID, route, text, lang)
		if err != nil {
			return 0, err
		}
		sk, _ := skills.Get(intentSkill[route.Intent])
		meta["goal_id"], meta["kind"] = goalID, "goal_created"
		steps := make([]string, 0, len(sk.Steps))
		for _, s := range sk.Steps {
			if s.WaitDays == 0 {
				steps = append(steps, s.Title)
			}
		}
		note = fmt.Sprintf("You just opened a case file %q and a plan is starting in the background (playbook: %s — %s). "+
			"Tell the client warmly and briefly what you will do (in plain words, not the step names verbatim: %s), what, if anything, you still need from them, "+
			"and that you will post updates here. If a licensed attorney or physician must review before anything is final, say so once.",
			route.Title, sk.Title, sk.Description, strings.Join(steps, "; "))
	case strings.HasPrefix(route.Intent, "goal."):
		note = a.controlGoal(ctx, u, route, text, meta)
	case route.Intent == "account.preferences" && route.Preference.Key != "":
		note = a.applyPreference(ctx, u.ID, route.Preference.Key, route.Preference.Value)
		if route.Preference.Key == "language" {
			lang = persona.Normalize(route.Preference.Value)
		}
	case route.Intent == "approval.respond":
		note = "The client is answering an approval request. Approvals are made with the Approve / Decline buttons on the card in this conversation (or in Approvals), so the exact action is on record. Point them to it kindly."
	case route.Intent == "refuse":
		note = "This request is something you will not help with (see WHAT YOU WILL NOT DO). Decline plainly and kindly, say why in one sentence, and offer the lawful or safe path."
	}
	if preface == "" {
		sink.Meta(meta)
	}

	// 4. Answer, in her voice, from rebuilt context.
	sys := persona.ChatSystem(lang, u.Name, time.Now(), a.chatContext(ctx, u, convID, note))
	msgs := []llm.Message{{Role: "system", Content: sys}}
	msgs = append(msgs, a.history(ctx, convID, userMsgID, 14)...)
	msgs = append(msgs, llm.Message{Role: "user", Content: text})
	var reply strings.Builder
	reply.WriteString(preface)
	_, err = a.Model.Stream(ctx, engine.CallMeta{Purpose: "chat", UserID: u.ID}, llm.Request{Model: a.LLM, Messages: msgs, Temperature: 0.6},
		func(d string) { reply.WriteString(d); sink.Delta(d) })
	if err != nil {
		var budget *engine.ErrBudget
		fallback := map[string]string{"en": "I'm having trouble reaching my reasoning service right now. Your message is saved, and any case work already started continues in the background. Please try again in a minute.",
			"zh": "我暂时无法连接到推理服务。你的消息已保存，已开始的案件工作会在后台继续。请稍后再试。"}[lang]
		if errors.As(err, &budget) {
			fallback = map[string]string{"en": "I've reached today's usage limit for your account (" + budget.Reason + "). Your message is saved.",
				"zh": "你的账户今日用量已达上限（" + budget.Reason + "）。你的消息已保存。"}[lang]
		}
		slog.Error("chat stream", "err", err)
		sink.Delta(fallback)
		reply.WriteString(fallback)
		meta["error"] = true
	}
	metaRaw, _ := json.Marshal(meta)
	var replyID int64
	if err := a.Store.Pool.QueryRow(context.WithoutCancel(ctx), `INSERT INTO messages (conversation_id, role, content, meta) VALUES ($1,'assistant',$2,$3) RETURNING id`,
		convID, reply.String(), metaRaw).Scan(&replyID); err != nil {
		return 0, err
	}
	go a.afterTurn(context.WithoutCancel(ctx), u, convID, text, route)
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

func (a *Agent) route(ctx context.Context, u User, convID, text, lang string) Route {
	goals, _ := a.Store.Goals(ctx, u.ID, 8)
	var gl strings.Builder
	for _, g := range goals {
		if g.Status == "completed" || g.Status == "cancelled" {
			continue
		}
		fmt.Fprintf(&gl, "- id=%s status=%s title=%q\n", g.ID, g.Status, g.Title)
	}
	var sk strings.Builder
	for _, s := range skills.All() {
		fmt.Fprintf(&sk, "- %s: %s\n", s.Name, s.Description)
	}
	prompt := fmt.Sprintf(`Classify the client's latest message for a legal+medical assistant.

INTENTS: %s

Choose a *.intake / *.draft / med.* / medlegal.* work intent only when the client wants real work on THEIR situation (not a general question — that is legal.explain or med.explain).
If the message adds facts to, changes, or answers a question from an OPEN CASE below, use goal.change with its goal_id.
goal.status for "where are we / what's next"; goal.pause/resume/cancel with goal_id.
"refuse" for requests to deceive a court, fabricate evidence, obtain controlled drugs, or other illegal acts.

OPEN CASES:
%s
RECENT CONVERSATION:
%s

LATEST MESSAGE: %q

Return JSON: {"intent","confidence":0-1,"domain":"legal|medical|medlegal|general","goal_id":"","title":"short case title in %s","objective":"one sentence restating what the client wants done","clarify":"one question if confidence<0.6","language":"en|zh (the language of the latest message)","preference":{"key":"language|verbosity|tone|timezone","value":""}}`,
		intents, gl.String(), a.Store.RecentConversation(ctx, convID, 6), text, persona.LangName(lang))
	resp, err := a.Model.Chat(ctx, engine.CallMeta{Purpose: "route", UserID: u.ID}, llm.Request{
		Model: a.FastLLM, JSON: true, Temperature: 0, Messages: []llm.Message{{Role: "user", Content: prompt}}})
	var r Route
	if err == nil {
		err = json.Unmarshal([]byte(llm.ExtractJSON(resp.Message.Content)), &r)
	}
	if err != nil {
		slog.Warn("route failed; answering conversationally", "err", err)
		return Route{Intent: "smalltalk", Confidence: 1}
	}
	r.Language = persona.Normalize(r.Language)
	slog.Info("route", "intent", r.Intent, "confidence", r.Confidence, "goal_id", r.GoalID, "title", r.Title)
	return r
}

func (a *Agent) openGoal(ctx context.Context, u User, convID string, r Route, text, lang string) (string, error) {
	sk := intentSkill[r.Intent]
	skill, _ := skills.Get(sk)
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = skill.Title
		if lang == "zh" {
			title = skill.TitleZH
		}
	}
	obj := strings.TrimSpace(r.Objective)
	if obj == "" {
		obj = text
	}
	limits := engine.Limits{}
	c, _ := a.Store.Client(ctx, u.ID)
	if v, ok := c.Prefs["goal_max_cost_usd"].(float64); ok && v > 0 {
		limits.MaxCostUSD = v
	}
	if v, ok := c.Prefs["goal_max_days"].(float64); ok && v > 0 {
		limits.MaxDays = int(v)
	}
	g := &engine.Goal{UserID: u.ID, ConversationID: convID, Title: truncate(title, 120), Objective: obj + "\n\nClient's words: " + truncate(text, 2000),
		Domain: skill.Domain, Skill: skill.Name, Criteria: skill.Criteria, Milestones: skill.Milestones, Limits: limits, Language: lang}
	if err := a.Store.CreateGoal(ctx, g); err != nil {
		return "", err
	}
	_, _ = a.Store.Pool.Exec(ctx, `SELECT pg_notify('act_work', $1)`, g.ID)
	return g.ID, nil
}

func (a *Agent) controlGoal(ctx context.Context, u User, r Route, text string, meta map[string]any) string {
	g, err := a.Store.GoalForUser(ctx, r.GoalID, u.ID)
	if err != nil {
		return "The client referred to a case you could not identify. Ask which case they mean (list the open ones)."
	}
	meta["goal_id"] = g.ID
	switch r.Intent {
	case "goal.pause":
		if g.Status == "active" || g.Status == "planning" {
			_ = a.Store.SetGoalStatus(ctx, g.ID, "paused", "client asked to pause")
		}
		return "You paused the case " + g.Title + ". Nothing will run until they resume it. Any action already sent stays sent."
	case "goal.resume":
		if g.Status == "paused" || g.Status == "needs_attention" {
			_ = a.Store.SetGoalStatus(ctx, g.ID, "active", "client asked to resume")
			_, _ = a.Store.EnqueuePlan(ctx, g.ID, fmt.Sprintf("resume-%d", time.Now().Unix()/60), "review", "the client resumed the case; check the plan still fits")
		}
		return "You resumed the case " + g.Title + " from where it stopped."
	case "goal.cancel":
		_ = a.Store.SetGoalStatus(ctx, g.ID, "cancelled", "client cancelled")
		return "You cancelled the case " + g.Title + ". Unfinished work is stopped and pending approvals are withdrawn. Documents already produced remain in their file; anything already sent cannot be unsent — say so honestly."
	case "goal.change":
		if g.Status == "needs_attention" || g.Status == "paused" {
			_ = a.Store.SetGoalStatus(ctx, g.ID, "active", "client provided new information")
		}
		_, _ = a.Store.EnqueuePlan(ctx, g.ID, fmt.Sprintf("change-%d", time.Now().UnixNano()), "change", "the client said: "+truncate(text, 1500))
		return "The client gave new information for the case " + g.Title + ". You have passed it to the plan, which will adapt. Acknowledge what changed and what you will do differently."
	default: // goal.status
		return "The client asked for a status update on " + g.Title + ". Use the ACTIVE WORK section: what is done, what is in progress, what waits on whom, and the next date."
	}
}

func (a *Agent) applyPreference(ctx context.Context, userID, key, value string) string {
	switch key {
	case "language":
		value = persona.Normalize(value)
	case "verbosity", "tone", "timezone":
	default:
		return ""
	}
	patch, _ := json.Marshal(map[string]string{key: value})
	_, _ = a.Store.Pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET data = user_preferences.data || EXCLUDED.data, updated_at=now()`, userID, patch)
	return fmt.Sprintf("You updated the client's preference %s = %s. Confirm briefly.", key, value)
}

// chatContext is what the conversational reply needs from persisted state.
func (a *Agent) chatContext(ctx context.Context, u User, convID, note string) string {
	var b strings.Builder
	c, _ := a.Store.Client(ctx, u.ID)
	if c != nil {
		if v, ok := c.Prefs["verbosity"].(string); ok && v != "" {
			fmt.Fprintf(&b, "CLIENT PREFERS: %s answers", v)
			if t, ok := c.Prefs["tone"].(string); ok && t != "" {
				fmt.Fprintf(&b, ", %s tone", t)
			}
			b.WriteString(".\n")
		}
	}
	if mem := a.Store.Memories(ctx, u.ID, 20); len(mem) > 0 && (c == nil || c.Prefs["memory_enabled"] != false) {
		b.WriteString("WHAT YOU KNOW ABOUT THE CLIENT:\n")
		for _, m := range mem {
			b.WriteString("- " + m + "\n")
		}
	}
	goals, _ := a.Store.Goals(ctx, u.ID, 6)
	if len(goals) > 0 {
		b.WriteString("\nACTIVE WORK:\n")
		for _, g := range goals {
			if g.Status == "cancelled" {
				continue
			}
			fmt.Fprintf(&b, "* %s — %s", g.Title, g.Status)
			if g.AttentionReason != "" {
				fmt.Fprintf(&b, " (needs: %s)", g.AttentionReason)
			}
			b.WriteString("\n")
			ts, _ := a.Store.Tasks(ctx, g.ID)
			for _, t := range ts {
				if t.Kind != "llm" && t.Kind != "wait" {
					continue
				}
				fmt.Fprintf(&b, "   - %s: %s\n", t.Title, t.Status)
			}
			for _, d := range a.Store.GoalDocuments(ctx, g.ID) {
				fmt.Fprintf(&b, "   · document: %s\n", d)
			}
		}
	}
	var pending int
	_ = a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE user_id=$1 AND status='pending'`, u.ID).Scan(&pending)
	if pending > 0 {
		fmt.Fprintf(&b, "\nPENDING APPROVALS: %d (shown as cards in the app)\n", pending)
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
func (a *Agent) afterTurn(ctx context.Context, u User, convID, text string, r Route) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if r.Title != "" {
		_, _ = a.Store.Pool.Exec(ctx, `UPDATE conversations SET title=$2 WHERE id=$1 AND title=''`, convID, truncate(r.Title, 80))
	} else {
		_, _ = a.Store.Pool.Exec(ctx, `UPDATE conversations SET title=$2 WHERE id=$1 AND title=''`, convID, truncate(text, 48))
	}
	c, err := a.Store.Client(ctx, u.ID)
	if err != nil || c.Prefs["memory_enabled"] == false || len([]rune(text)) < 25 {
		return
	}
	resp, err := a.Model.Chat(ctx, engine.CallMeta{Purpose: "memory", UserID: u.ID}, llm.Request{Model: a.FastLLM, JSON: true, Temperature: 0,
		Messages: []llm.Message{{Role: "user", Content: `Extract durable facts about the client worth remembering across future cases (location/jurisdiction, allergies, chronic conditions, current medications, employer, family situation, communication preferences). ` +
			`Not transient details of one request. Reply with JSON only: {"facts": ["..."]} (max 3, each under 20 words, in the message's language), or {"facts": []}.` + "\n\nMESSAGE: " + text}}})
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

// DetectLang: a message with Han characters is Chinese; otherwise English
// unless the client's preference says Chinese and the message has no letters.
func DetectLang(text, pref string) string {
	han, latin := 0, 0
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case r < 128 && unicode.IsLetter(r):
			latin++
		}
	}
	switch {
	case han > 0 && han*3 >= latin/4:
		return "zh"
	case latin > 0:
		return "en"
	case pref != "":
		return pref
	}
	return "en"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
