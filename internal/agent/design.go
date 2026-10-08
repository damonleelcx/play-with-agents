package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// Game-play design mode: a conversation where the player and Aoi explore a
// game before anything is built. Every exchange updates a living design
// document; when the player is happy, a build plan is written from it, and
// only their "build it" hands that plan to the studio.

// IntentDesign marks a reply in a design conversation.
const IntentDesign = "design"

// Design is the living design document of a design conversation. Every
// field is optional; the doc grows as the conversation does.
type Design struct {
	Title         string   `json:"title,omitempty"`
	Pitch         string   `json:"pitch,omitempty"`
	Players       string   `json:"players,omitempty"`
	Length        string   `json:"length,omitempty"`
	Board         string   `json:"board,omitempty"`
	Components    []string `json:"components,omitempty"`
	Loop          []string `json:"loop,omitempty"`
	Mechanics     []string `json:"mechanics,omitempty"`
	Twist         string   `json:"twist,omitempty"`
	Win           string   `json:"win,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
	Parked        []string `json:"parked,omitempty"`
	Readiness     int      `json:"readiness,omitempty"` // 0..100: how buildable the design is
}

// DesignState is a design conversation as the page shows it.
type DesignState struct {
	Mode      string     `json:"mode"`
	Design    Design     `json:"design"`
	Plan      string     `json:"plan,omitempty"`
	PlanAt    *time.Time `json:"plan_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	GoalID    string     `json:"goal_id,omitempty"`
	// Stale: the design changed after the plan was written.
	Stale bool `json:"stale,omitempty"`
}

// ErrNoDesign: the conversation is not a design conversation.
var ErrNoDesign = errors.New("not a design conversation")

// ConversationDesign reads a conversation's mode and design state.
func (a *Agent) ConversationDesign(ctx context.Context, convID string) (*DesignState, error) {
	var st DesignState
	var raw []byte
	var goal *string
	err := a.Store.Pool.QueryRow(ctx, `SELECT mode, design, design_plan, design_plan_at, design_updated_at, design_goal_id::text
		FROM conversations WHERE id=$1`, convID).Scan(&st.Mode, &raw, &st.Plan, &st.PlanAt, &st.UpdatedAt, &goal)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &st.Design)
	if goal != nil {
		st.GoalID = *goal
	}
	st.Stale = st.Plan != "" && st.PlanAt != nil && st.UpdatedAt != nil && st.UpdatedAt.After(*st.PlanAt)
	return &st, nil
}

func (a *Agent) conversationMode(ctx context.Context, convID string) string {
	var mode string
	if err := a.Store.Pool.QueryRow(ctx, `SELECT mode FROM conversations WHERE id=$1`, convID).Scan(&mode); err != nil {
		return "chat"
	}
	return mode
}

// designOutcome briefs Aoi for one design reply.
func (a *Agent) designOutcome(ctx context.Context, t *turn) outcome {
	st, _ := a.ConversationDesign(ctx, t.convID)
	doc := "(empty: the conversation is just starting)"
	if st != nil {
		if b, err := json.Marshal(st.Design); err == nil && string(b) != "{}" {
			doc = string(b)
		}
	}
	return outcome{mood: persona.Smile, note: designBrief + "\n\nTHE DESIGN SO FAR (your shared notes, JSON):\n" + doc}
}

const designBrief = `GAME DESIGN MODE. The player opened a design session with you: you are their game-design partner, not a builder. Nothing gets built in this mode; when they are happy with the design they press "Make the build plan" and then "Build this game" (tell them so when the design is ready).

How to design together:
- Explore, then converge. Early on, offer two or three genuinely different directions (different core mechanics, not reskins) with one line each on why each is fun, and ask which pulls them. Later, deepen the one they choose.
- Hunt for the novel: an unusual combination of mechanics, a twist that changes how players think, a decision that is hard in an interesting way, a theme that shapes the rules. Name the "aha" of the game.
- Stress-test out loud: a dominant strategy, a runaway leader, a turn with nothing to do, analysis paralysis, a game that cannot end. Propose a fix for each problem you raise.
- Make it buildable: turn-based, 1 to 8 players, every rule decidable by a program. Any board is fine (grid, hex, track, a map of places and paths, or just cards on a table); agents can fill any seat.
- Ask at most one question per reply, and make it a real choice ("A or B?").
- Keep replies tight: short paragraphs or a few bullets, no walls of text. Use the player's words for things they named.`

// designDocPrompt asks the fast model to fold the latest exchange into the
// design document.
const designDocPrompt = `You maintain the shared design notes of a board-game design conversation. Update THE DESIGN SO FAR with what the LATEST EXCHANGE decided, proposed or left open. Keep what still holds; drop what the player rejected (move a rejected idea into parked only if it might come back). Write in the conversation's language, terse, concrete.

Fields (all optional, keep each string under 240 characters, lists under 8 items of under 160 characters):
title (a short evocative name, once there is one), pitch (one sentence: what you do and why it is fun), players (e.g. "2-4"), length (e.g. "20 min"), board (the board or table: kind and layout), components (pieces, cards, tokens), loop (the turn, step by step), mechanics (the core mechanics, each with its one-line point), twist (what makes it new), win (how you win and how it ends), open_questions (what is still undecided), parked (ideas set aside), readiness (0-100: how completely and unambiguously the rules are decided; 80+ means a build plan can be written).

Reply with the complete updated JSON object only.`

// updateDesign folds the latest exchange into the design document. It
// returns the new document (or the old one when the model fails).
func (a *Agent) updateDesign(ctx context.Context, u User, convID, said, reply string) (*Design, error) {
	st, err := a.ConversationDesign(ctx, convID)
	if err != nil {
		return nil, err
	}
	cur, _ := json.Marshal(st.Design)
	resp, err := a.Model.Chat(ctx, engine.CallMeta{Purpose: "design_doc", UserID: u.ID}, llm.Request{Model: a.FastLLM, JSON: true, Temperature: 0.2, MaxTokens: 1400,
		Messages: []llm.Message{{Role: "system", Content: designDocPrompt},
			{Role: "user", Content: fmt.Sprintf("THE DESIGN SO FAR:\n%s\n\nLATEST EXCHANGE\nPLAYER: %s\nAOI: %s", cur, truncate(said, 4000), truncate(reply, 6000))}}})
	if err != nil {
		return &st.Design, err
	}
	d, err := designFrom([]byte(llm.ExtractJSON(resp.Message.Content)))
	if err != nil {
		return &st.Design, err
	}
	d.clip()
	raw, _ := json.Marshal(d)
	if _, err := a.Store.Pool.Exec(ctx, `UPDATE conversations SET design=$2, design_updated_at=now(),
		title=CASE WHEN $3 <> '' AND (title='' OR title=$4) THEN $3 ELSE title END WHERE id=$1`,
		convID, raw, truncate(d.Title, 60), truncate(st.Design.Title, 60)); err != nil {
		return &st.Design, err
	}
	return d, nil
}

// designFrom reads a model-written design document loosely: a model may
// write a list as one string, a field as a number, or readiness as "65%".
// One odd field must not throw away the whole update.
func designFrom(raw []byte) (*Design, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	text := func(k string) string {
		switch v := m[k].(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case []any:
			return strings.Join(listOf(v), "; ")
		case map[string]any:
			return strings.Join(listOf([]any{v}), "; ")
		}
		return ""
	}
	list := func(k string) []string {
		switch v := m[k].(type) {
		case []any:
			return listOf(v)
		case string:
			var out []string
			for _, line := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == ';' || r == '；' }) {
				if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-•*")); line != "" {
					out = append(out, line)
				}
			}
			return out
		case map[string]any:
			return listOf([]any{v})
		}
		return nil
	}
	d := &Design{Title: text("title"), Pitch: text("pitch"), Players: text("players"), Length: text("length"), Board: text("board"),
		Components: list("components"), Loop: list("loop"), Mechanics: list("mechanics"), Twist: text("twist"), Win: text("win"),
		OpenQuestions: list("open_questions"), Parked: list("parked")}
	switch v := m["readiness"].(type) {
	case float64:
		d.Readiness = int(v)
	case string:
		d.Readiness, _ = strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "%")))
	}
	return d, nil
}

// listOf flattens list items: strings as they are, an object as its values
// ("name: point"), numbers as text.
func listOf(xs []any) []string {
	var out []string
	for _, x := range xs {
		switch v := x.(type) {
		case string:
			out = append(out, v)
		case float64:
			out = append(out, strconv.FormatFloat(v, 'f', -1, 64))
		case map[string]any:
			var parts []string
			for _, k := range []string{"name", "title", "mechanic", "idea", "point", "description", "detail", "count"} {
				if s, ok := v[k].(string); ok && s != "" {
					parts = append(parts, s)
				} else if n, ok := v[k].(float64); ok {
					parts = append(parts, strconv.FormatFloat(n, 'f', -1, 64))
				}
			}
			if len(parts) > 0 {
				out = append(out, strings.Join(parts, ": "))
			}
		}
	}
	return out
}

// clip keeps a model-written document within the page's limits.
func (d *Design) clip() {
	s := func(x *string, n int) { *x = truncate(strings.TrimSpace(*x), n) }
	l := func(xs []string) []string {
		var out []string
		for _, x := range xs {
			if x = truncate(strings.TrimSpace(x), 200); x != "" && len(out) < 10 {
				out = append(out, x)
			}
		}
		return out
	}
	s(&d.Title, 60)
	for _, p := range []*string{&d.Pitch, &d.Players, &d.Length, &d.Board, &d.Twist, &d.Win} {
		s(p, 300)
	}
	d.Components, d.Loop, d.Mechanics, d.OpenQuestions, d.Parked = l(d.Components), l(d.Loop), l(d.Mechanics), l(d.OpenQuestions), l(d.Parked)
	d.Readiness = max(0, min(100, d.Readiness))
}

// ── the build plan ──────────────────────────────────────────────────────────

const planPrompt = `You turn a finished board-game design conversation into the BUILD PLAN the studio will build from. The studio team: a Designer writes the final rules document, an Engineer implements it as a JavaScript game module for the platform's renderer, a Playtester simulates hundreds of games, a Critic reviews rules against code. The renderer draws square grids, hex grids, map boards (spaces anywhere with paths and regions: tracks, routes, islands, networks) and tables of cards (hands, markets, piles); pieces, stacks and rich cards; agents fill empty seats and must be able to play from legal moves alone.

Write the plan in Markdown, in %s, with exactly these sections:

# <Game title>
One-sentence pitch.

## What makes it new
2-4 bullets: the novel ideas this design is built around.

## Players and length

## Components
Every piece, card (list each distinct card with its exact effect if cards differ), token and track, with counts.

## Board and table
The exact board (kind: grid, hex, track, map or no board; size, every space and connection that matters, special spaces), and what sits where on the table (shared areas, each player's area, what is hidden).

## Setup

## A turn
The exact sequence; every action with its legality conditions and costs.

## Scoring and the end
Exact scoring, end condition(s), tie-breaks. The game must always end.

## Edge cases
Each situation the conversation surfaced or that the rules imply (no legal move, empty deck, simultaneous effects, ...) and its ruling.

## Build plan for the studio
Numbered steps for Designer, Engineer (state, legal moves and their UI hints, the view: which board kind and layout, what each player sees), Playtester (what to check: game length, balance, a dominant strategy to look for), and Critic (the rules most likely to be implemented wrongly).

## Decisions made for the build
Anything the conversation left open, decided here with the simplest fun choice (say what was chosen).

Be exact and complete: the studio will build exactly this. Do not invent features the player rejected.`

// DesignPlan writes the build plan for a design conversation and stores it.
func (a *Agent) DesignPlan(ctx context.Context, u User, convID string) (*DesignState, error) {
	st, err := a.ConversationDesign(ctx, convID)
	if err != nil {
		return nil, err
	}
	if st.Mode != "design" {
		return nil, ErrNoDesign
	}
	doc, _ := json.Marshal(st.Design)
	var b strings.Builder
	fmt.Fprintf(&b, "DESIGN NOTES (JSON):\n%s\n\nCONVERSATION (oldest first):\n", doc)
	for _, m := range a.history(ctx, convID, 1<<62, 60) {
		who := "PLAYER"
		if m.Role == "assistant" {
			who = "AOI"
		}
		fmt.Fprintf(&b, "%s: %s\n\n", who, truncate(m.Content, 3000))
	}
	lang := u.Lang
	if lang == "" {
		lang = "en"
	}
	resp, err := a.Model.Chat(ctx, engine.CallMeta{Purpose: "design_plan", UserID: u.ID}, llm.Request{Model: a.LLM, Temperature: 0.3, MaxTokens: 6000,
		Messages: []llm.Message{{Role: "system", Content: fmt.Sprintf(planPrompt, persona.LangName(lang))}, {Role: "user", Content: truncate(b.String(), 60000)}}})
	if err != nil {
		return nil, err
	}
	plan := strings.TrimSpace(resp.Message.Content)
	if plan == "" {
		return nil, errors.New("the plan came back empty")
	}
	if _, err := a.Store.Pool.Exec(ctx, `UPDATE conversations SET design_plan=$2, design_plan_at=now() WHERE id=$1`, convID, truncate(plan, 11500)); err != nil {
		return nil, err
	}
	return a.ConversationDesign(ctx, convID)
}

// DesignBuild hands the design's build plan to the studio, as the player
// asked by pressing "Build this game". The mission card lands in the design
// conversation.
func (a *Agent) DesignBuild(ctx context.Context, u User, convID string) (*DesignState, error) {
	if a.Studio == nil {
		return nil, errors.New("the studio is not open on this server")
	}
	st, err := a.ConversationDesign(ctx, convID)
	if err != nil {
		return nil, err
	}
	if st.Mode != "design" {
		return nil, ErrNoDesign
	}
	if strings.TrimSpace(st.Plan) == "" {
		return nil, errors.New("make the build plan first")
	}
	lang := u.Lang
	if lang == "" {
		lang = "en"
	}
	goalID, _, err := a.Studio.StartBuild(ctx, u.ID, convID, st.Plan, "", lang)
	if err != nil {
		return nil, err
	}
	if _, err := a.Store.Pool.Exec(ctx, `UPDATE conversations SET design_goal_id=$2::uuid WHERE id=$1`, convID, goalID); err != nil {
		return nil, err
	}
	name := st.Design.Title
	if name == "" {
		name = map[string]string{"en": "our game", "zh": "我们的游戏", "ko": "우리 게임", "ja": "私たちのゲーム"}[lang]
	}
	msg := map[string]string{
		"en": fmt.Sprintf("Here we go — the studio team is building **%s** from our plan. Rules first, then the game itself, hundreds of playtests and an independent review. You decide when it's published, and you can play the draft as soon as it's built.", name),
		"zh": fmt.Sprintf("出发！工作室团队正在按我们的计划打造《%s》：先写规则，再做游戏，然后试玩数百局、独立评审。什么时候发布由你决定；草稿一做好你就能先玩。", name),
		"ko": fmt.Sprintf("시작할게요! 스튜디오 팀이 우리 계획대로 **%s**을(를) 만들고 있어요. 규칙부터, 그다음 게임, 수백 번의 플레이테스트와 독립 리뷰까지. 공개는 당신이 정하고, 초안이 완성되면 바로 플레이할 수 있어요.", name),
		"ja": fmt.Sprintf("いくよ！スタジオのみんなが、私たちのプランどおりに「%s」を作り始めたよ。まずルール、次にゲーム本体、何百回ものテストプレイと独立レビュー。公開するかはあなたが決めてね。下書きができたらすぐ遊べるよ。", name),
	}[lang]
	a.Store.PostMessage(ctx, convID, u.ID, msg, map[string]any{"intent": IntentBuild, "mood": persona.Smile, "goal_id": goalID,
		"cards": []map[string]any{{"kind": "mission", "goal_id": goalID}}}, "design-build-"+goalID)
	return a.ConversationDesign(ctx, convID)
}

// NewDesign opens a design conversation for the player.
func (a *Agent) NewDesign(ctx context.Context, userID string) (string, error) {
	var id string
	err := a.Store.Pool.QueryRow(ctx, `INSERT INTO conversations (user_id, mode) VALUES ($1, 'design') RETURNING id`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	return id, err
}
