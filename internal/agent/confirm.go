package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// Confirmations. Destructive or irreversible actions — deleting a draft,
// cancelling a build, making a game public, approving a publish, leaving a
// table, forgetting everything — are never done on the turn that asks for
// them. Aoi asks ("Delete the draft "X"? Say yes to confirm") and stores the
// action as the pending confirmation of her reply (messages.meta.pending: the
// conversation's state). Only the very next player message can confirm it,
// only with an explicit yes, and only within confirmTTL; anything else drops
// it. The action is re-checked when it runs: the capability refuses whatever
// the player may no longer do.

// Pending is an action waiting for the player's explicit yes.
type Pending struct {
	Action     string    `json:"action"` // see pendingActions
	GameID     string    `json:"game_id,omitempty"`
	GoalID     string    `json:"goal_id,omitempty"`
	TableID    string    `json:"table_id,omitempty"`
	ApprovalID string    `json:"approval_id,omitempty"`
	Label      string    `json:"label"` // what is being confirmed, in words
	At         time.Time `json:"at"`
}

const (
	pendDeleteGame  = "delete_game"
	pendCancelBuild = "cancel_build"
	pendMakePublic  = "make_public"
	pendPublish     = "approve_publish"
	pendLeaveTable  = "leave_table"
	pendForgetAll   = "forget_all"
)

var pendingActions = []string{pendDeleteGame, pendCancelBuild, pendMakePublic, pendPublish, pendLeaveTable, pendForgetAll}

const confirmTTL = 15 * time.Minute

// ask returns the outcome that asks the player to confirm p.
func ask(p *Pending, mood persona.Mood, why string) outcome {
	p.At = time.Now().UTC()
	note := fmt.Sprintf("Nothing has been done yet. Ask the player to confirm: %s. %sTell them to say yes to confirm (anything else cancels). Keep it to one or two lines.", p.Label, why)
	return outcome{mood: mood, pending: p, note: note}
}

// pendingConfirmation returns the confirmation Aoi's latest reply asked for,
// if it is still fresh. Only the reply right before this message counts.
func (a *Agent) pendingConfirmation(ctx context.Context, convID string, beforeID int64) *Pending {
	var raw []byte
	var role string
	err := a.Store.Pool.QueryRow(ctx, `SELECT role, meta FROM messages WHERE conversation_id=$1 AND id < $2 AND role IN ('user','assistant')
		ORDER BY id DESC LIMIT 1`, convID, beforeID).Scan(&role, &raw)
	if err != nil || role != "assistant" {
		return nil
	}
	var m struct {
		Pending *Pending `json:"pending"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Pending == nil || !contains(pendingActions, m.Pending.Action) {
		return nil
	}
	if time.Since(m.Pending.At) > confirmTTL {
		return nil
	}
	return m.Pending
}

type answer int

const (
	answerOther answer = iota
	answerYes
	answerNo
)

// Explicit answers, per language. A yes must be unambiguous: the whole
// message is a yes word ("yes", "好的", "네", "はい"), or a short message that
// starts with one and carries no negation or hesitation ("yes, delete it").
var yesWords = []string{
	"yes", "y", "yeah", "yep", "yup", "sure", "confirm", "confirmed", "ok", "okay", "do it", "go ahead", "yes please", "please do", "absolutely", "of course", "affirmative",
	"是", "是的", "好", "好的", "确认", "确定", "对", "可以", "行", "没问题", "删吧", "删除吧", "发布吧", "取消吧", "嗯",
	"네", "예", "응", "어", "좋아", "좋아요", "확인", "그래", "그래요", "해 줘", "해줘", "네 좋아요",
	"はい", "うん", "ええ", "いいよ", "いいです", "お願い", "お願いします", "確認", "オッケー", "どうぞ",
}

var noWords = []string{
	"no", "n", "nope", "nah", "cancel", "never mind", "nevermind", "don't", "dont", "stop", "wait", "no thanks", "not now", "keep it",
	"不", "不要", "不用", "算了", "取消", "别", "等等", "先不要", "不了",
	"아니", "아니요", "아니야", "아뇨", "취소", "됐어", "잠깐", "하지 마",
	"いいえ", "いや", "やめて", "やめる", "やめとく", "キャンセル", "待って", "だめ",
}

var hedges = []string{"no", "not", "don't", "dont", "wait", "but", "cancel", "不", "别", "等", "아니", "말고", "잠깐", "いや", "やめ", "待", "でも"}

func normAnswer(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimFunc(s, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r) })
	return strings.Join(strings.Fields(s), " ")
}

func confirmAnswer(text string) answer {
	n := normAnswer(text)
	if n == "" {
		return answerOther
	}
	for _, w := range noWords {
		if n == w {
			return answerNo
		}
	}
	for _, w := range yesWords {
		if n == w {
			return answerYes
		}
	}
	// "yes, delete it" / "好的，删掉吧" / "네, 삭제해 줘": a yes word first,
	// short, and no negation or hesitation anywhere.
	if len([]rune(n)) > 40 {
		return answerOther
	}
	for _, h := range hedges {
		if containsWord(n, h) {
			if startsWithAny(n, noWords) {
				return answerNo
			}
			return answerOther
		}
	}
	if startsWithAny(n, yesWords) {
		return answerYes
	}
	if startsWithAny(n, noWords) {
		return answerNo
	}
	return answerOther
}

// startsWithAny: n starts with one of ws followed by a separator (Latin) or
// directly (CJK, which has no spaces).
func startsWithAny(n string, ws []string) bool {
	for _, w := range ws {
		if !strings.HasPrefix(n, w) || len(w) == 0 {
			continue
		}
		rest := []rune(n[len(w):])
		if len(rest) == 0 {
			return true
		}
		if r := rest[0]; unicode.IsSpace(r) || unicode.IsPunct(r) {
			return true
		}
		if isCJK([]rune(w)[0]) {
			return true
		}
	}
	return false
}

// containsWord finds w as a whole word in Latin text, or as a substring in
// CJK text.
func containsWord(n, w string) bool {
	if isCJK([]rune(w)[0]) {
		return strings.Contains(n, w)
	}
	for _, f := range strings.FieldsFunc(n, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) }) {
		if f == w {
			return true
		}
	}
	return false
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}

// confirm runs a pending action after the player's explicit yes.
func (a *Agent) confirm(ctx context.Context, t *turn, p *Pending) outcome {
	u := t.u
	switch p.Action {
	case pendDeleteGame:
		if a.Catalog == nil {
			return notOpen("delete a game", "the game catalog")
		}
		if err := a.Catalog.DeleteDraft(ctx, u.ID, p.GameID); err != nil {
			return a.capabilityError("delete the draft", u, err)
		}
		return outcome{mood: persona.Neutral, note: "Confirmed and done: you deleted " + p.Label + ". It is gone for good. Confirm in one line."}
	case pendCancelBuild:
		g, err := a.Store.GoalForUser(ctx, p.GoalID, u.ID)
		if err != nil {
			return outcome{mood: persona.Neutral, note: "The build to cancel no longer exists. Say so briefly."}
		}
		if err := a.Store.GoalControl(ctx, g, "cancel", "the owner cancelled from chat"); err != nil {
			slog.Error("cancel build", "goal", g.ID, "err", err)
			return a.capabilityError("cancel the build", u, err)
		}
		return outcome{mood: persona.Sad, goalID: g.ID, cards: []Card{{Kind: "mission", GoalID: g.ID}},
			note: "Confirmed and done: you cancelled the build " + g.Title + ". Unfinished work stopped and any pending approval was withdrawn. A draft that was already playable stays in their games."}
	case pendMakePublic:
		if a.Catalog == nil {
			return notOpen("change who can see a game", "the game catalog")
		}
		if err := a.Catalog.SetVisibility(ctx, u.ID, p.GameID, "public"); err != nil {
			return a.capabilityError("make the game public", u, err)
		}
		g, _ := a.Catalog.Game(ctx, u.ID, p.GameID)
		note := "Confirmed and done: " + p.Label + " is now public — anyone can find it on the shelf and play it."
		if g.Status != "" && g.Status != "published" {
			note = "Confirmed and done: " + p.Label + " is set to public; it appears on the shelf once it is published (it is still a " + g.Status + ")."
		}
		return outcome{mood: persona.Smile, cards: []Card{{Kind: "game", GameID: p.GameID}}, note: note}
	case pendPublish:
		if err := a.Store.DecideAsOwner(ctx, p.ApprovalID, u.ID, true, "approved in chat"); err != nil {
			return outcome{mood: persona.Neutral, note: "You tried to approve publishing " + p.Label + ", but that approval is no longer waiting (it was decided or withdrawn meanwhile). Say so briefly."}
		}
		return outcome{mood: persona.Smile, goalID: p.GoalID, cards: []Card{{Kind: "mission", GoalID: p.GoalID}},
			note: "Confirmed and done: you approved publishing " + p.Label + ". The studio publishes it now; the mission card shows when it is live."}
	case pendLeaveTable:
		if a.Tables == nil {
			return notOpen("leave the table", "the tables")
		}
		if err := a.Tables.LeaveTable(ctx, u.ID, p.TableID); err != nil {
			return a.capabilityError("leave the table", u, err)
		}
		return outcome{mood: persona.Neutral, note: "Confirmed and done: the player left " + p.Label + ". If the game was running, an agent took over their seat so the others play on, and they can still watch. Say goodbye to the table in one line."}
	case pendForgetAll:
		tag, err := a.Store.Pool.Exec(ctx, `DELETE FROM memories WHERE user_id=$1`, u.ID)
		if err != nil {
			slog.Error("forget all", "err", err)
			return outcome{mood: persona.Sad, note: "Deleting their memories failed on the server. Apologise and point them to Settings → Aoi → Memory."}
		}
		return outcome{mood: persona.Neutral, note: fmt.Sprintf("Confirmed and done: you deleted everything you remembered about the player (%d facts). From now on you do not know those things. If they also want you to stop remembering new things, that is the memory setting.", tag.RowsAffected())}
	}
	return outcome{mood: persona.Neutral, note: "The thing they confirmed is no longer available. Say so briefly."}
}
