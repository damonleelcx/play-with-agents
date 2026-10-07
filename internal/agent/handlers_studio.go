package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// ── studio: build, revise, control, approve ────────────────────────────────

func (a *Agent) build(ctx context.Context, t *turn) outcome {
	if a.Studio == nil {
		return outcome{mood: persona.Sad, note: "The player described a game to build, but the game studio is not open yet on this server. Say so honestly, say you love the idea (one specific thing about it), and offer to talk the rules through so they are ready when it opens."}
	}
	prompt := strings.TrimSpace(t.r.Prompt)
	if prompt == "" {
		prompt = t.text
	}
	base := ""
	if t.r.BaseGameID != "" {
		if g, ok := findGame(t.games, t.r.BaseGameID); ok {
			base = g.ID
		}
	}
	goalID, gameID, err := a.Studio.StartBuild(ctx, t.u.ID, t.convID, prompt, base, t.lang)
	if err != nil {
		return a.capabilityError("start the build", t.u, err)
	}
	slog.Info("build started", "user", t.u.ID, "goal", goalID, "game", gameID)
	return outcome{mood: persona.Smile, goalID: goalID, cards: []Card{{Kind: "mission", GoalID: goalID}},
		note: "You just started a build mission for the player's game idea; its card is shown under your message. The studio agents are on it: the rules first, then the game itself, then hundreds of simulated games, then a review. The player approves before it is published, and can play the draft as soon as it is built. React to the idea with real curiosity (one specific detail you like). If one important thing is unclear (player count, how you win), ask exactly one question — their answer will reach the build."}
}

// revise is also the second half of the "change it, then republish" skill:
// a running build takes the change into its plan; a finished game gets a new
// build based on it, which ends at the same publish approval (G1) — the
// owner approves on the card or by telling Aoi.
func (a *Agent) revise(ctx context.Context, t *turn) outcome {
	r := t.r
	change := strings.TrimSpace(r.Prompt)
	if change == "" {
		change = t.text
	}
	// 1. A build still running: the change goes into its plan.
	var target *engine.Goal
	for _, g := range t.open {
		if g.ID == r.GoalID || (r.GameID != "" && g.GameID == r.GameID) {
			target = g
		}
	}
	if target == nil && r.GoalID == "" && r.GameID == "" && len(t.open) == 1 {
		target = t.open[0]
	}
	republish := " If they also asked to publish it again, say the new version goes to their approval when it passes the playtest and review — they can approve on the card or just tell you."
	if target != nil {
		if target.Status == "paused" || target.Status == "needs_attention" {
			_ = a.Store.SetGoalStatus(ctx, target.ID, "active", "the owner sent a change")
		}
		if _, err := a.Store.EnqueuePlan(ctx, target.ID, fmt.Sprintf("change-%d", time.Now().UnixNano()), "change", "the player said: "+truncate(change, 1500)); err != nil {
			slog.Error("enqueue change", "goal", target.ID, "err", err)
			return outcome{mood: persona.Sad, note: "You could not pass the change to the build because of a server error. Apologise and ask them to try again."}
		}
		return outcome{mood: persona.Smile, goalID: target.ID, cards: []Card{{Kind: "mission", GoalID: target.ID}},
			note: fmt.Sprintf("You passed the player's change (%q) to the running build %q; the plan will adapt. Confirm what will change in one line.", truncate(change, 200), target.Title) + republish}
	}
	// 2. A game they own that is no longer building: a new build based on it.
	if r.GameID != "" {
		g, ok := a.resolveGame(ctx, t, r.GameID)
		if !ok || !g.Mine {
			return outcome{mood: persona.Neutral, note: "The player wants to change a game you could not find among their own games. Ask which of their games they mean."}
		}
		if a.Studio == nil {
			return outcome{mood: persona.Sad, note: "The player wants to change their game, but the game studio is not open yet on this server. Say so honestly."}
		}
		goalID, _, err := a.Studio.StartBuild(ctx, t.u.ID, t.convID, change, g.ID, t.lang)
		if err != nil {
			return a.capabilityError("start the revision", t.u, err)
		}
		return outcome{mood: persona.Smile, goalID: goalID, cards: []Card{{Kind: "mission", GoalID: goalID}},
			note: fmt.Sprintf("You started a new build that revises their game %q with this change: %q. Its card is shown under your message. The current version stays playable until they approve the new one.", g.Name, truncate(change, 200)) + republish}
	}
	return outcome{mood: persona.Neutral, note: "The player wants to change a game, but it is not clear which one. Ask which game (or build) they mean."}
}

// targetGoal resolves the build a control or approval is about: the router's
// goal_id (if it is the player's), a goal of the named game, or the only
// open one.
func (a *Agent) targetGoal(ctx context.Context, t *turn, includeFinished bool) (*engine.Goal, *outcome) {
	r := t.r
	if r.GoalID != "" {
		if g, err := a.Store.GoalForUser(ctx, r.GoalID, t.u.ID); err == nil {
			return g, nil
		}
	}
	pool := t.open
	if includeFinished {
		all, _ := a.Store.Goals(ctx, t.u.ID, 10)
		pool = all
	}
	if r.GameID != "" {
		for _, g := range pool {
			if g.GameID == r.GameID {
				return g, nil
			}
		}
	}
	if len(t.open) == 1 {
		return t.open[0], nil
	}
	if len(t.open) == 0 && includeFinished && len(pool) > 0 {
		return pool[0], nil // the most recent build, e.g. "why did it fail?"
	}
	var titles []string
	for _, x := range t.open {
		titles = append(titles, x.Title)
	}
	if len(titles) == 0 {
		return nil, &outcome{mood: persona.Neutral, note: "The player referred to a build, but they have no builds running. Say so, and offer to start one."}
	}
	return nil, &outcome{mood: persona.Neutral, note: "The player referred to a build you could not identify. Ask which one they mean: " + strings.Join(titles, "; ")}
}

func (a *Agent) control(ctx context.Context, t *turn) outcome {
	g, refused := a.targetGoal(ctx, t, t.r.Action == "why" || t.r.Action == "status")
	if refused != nil {
		return *refused
	}
	card := []Card{{Kind: "mission", GoalID: g.ID}}
	switch t.r.Action {
	case "pause":
		if g.Status != "active" && g.Status != "planning" {
			return outcome{mood: persona.Neutral, goalID: g.ID, cards: card, note: fmt.Sprintf("The build %s is %s, so there is nothing to pause. Say so.", g.Title, g.Status)}
		}
		if err := a.Store.GoalControl(ctx, g, "pause", "paused by the owner in chat"); err != nil {
			return a.capabilityError("pause the build", t.u, err)
		}
		return outcome{mood: persona.Neutral, goalID: g.ID, cards: card, note: "You paused the build " + g.Title + ". Nothing runs until they resume it."}
	case "resume":
		if g.Status != "paused" && g.Status != "needs_attention" {
			return outcome{mood: persona.Neutral, goalID: g.ID, cards: card, note: fmt.Sprintf("The build %s is %s, not paused. Say so.", g.Title, g.Status)}
		}
		if err := a.Store.GoalControl(ctx, g, "resume", "resumed by the owner in chat"); err != nil {
			return a.capabilityError("resume the build", t.u, err)
		}
		return outcome{mood: persona.Smile, goalID: g.ID, cards: card, note: "You resumed the build " + g.Title + " from where it stopped."}
	case "cancel":
		if terminal(g.Status) {
			return outcome{mood: persona.Neutral, goalID: g.ID, cards: card, note: fmt.Sprintf("The build %s already ended (%s); there is nothing to cancel.", g.Title, g.Status)}
		}
		return ask(&Pending{Action: pendCancelBuild, GoalID: g.ID, Label: fmt.Sprintf("cancel the build %q", g.Title)}, persona.Neutral,
			"Unfinished work stops for good and any pending approval is withdrawn (a draft already playable stays). ")
	case "why":
		return outcome{mood: persona.Neutral, goalID: g.ID, cards: card, note: a.whyNote(ctx, g)}
	default:
		return outcome{mood: persona.Neutral, goalID: g.ID, cards: card, note: "The player asked how the build " + g.Title + " is going (status: " + g.Status + "). Use the MISSIONS section: what is done, what is running, and what waits on them (an approval, an answer)."}
	}
}

// whyNote explains a build that failed, stopped or waits: the goal's own
// reason and the failed tasks' errors, as the engine recorded them.
func (a *Agent) whyNote(ctx context.Context, g *engine.Goal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The player asked why the build %s is %s. Explain in plain words, without jargon or ids, and say what they can do next (resume it, change the idea, or start over).", g.Title, g.Status)
	if g.AttentionReason != "" {
		b.WriteString("\nREASON RECORDED: " + truncate(g.AttentionReason, 400))
	}
	if g.Summary != "" {
		b.WriteString("\nSUMMARY: " + truncate(g.Summary, 400))
	}
	ts, _ := a.Store.Tasks(ctx, g.ID)
	n := 0
	for _, x := range ts {
		if (x.Status == "failed" || x.Error != "") && n < 4 {
			n++
			fmt.Fprintf(&b, "\nTASK %q (%s): %s", x.Title, x.Status, truncate(x.Error, 300))
		}
	}
	if g.AttentionReason == "" && n == 0 {
		switch g.Status {
		case "completed", "active", "planning":
			b.WriteString("\nNothing has failed: it is " + g.Status + ".")
		case "cancelled":
			b.WriteString("\nIt was cancelled by the owner.")
		}
	}
	return b.String()
}

// approve decides a publish approval from chat through the same engine path
// as the card's buttons (Store.DecideAsOwner), only for the player's own
// approvals. Approving publishes, so it is confirmed first; declining keeps
// the game as a draft and needs no confirmation.
func (a *Agent) approve(ctx context.Context, t *turn) outcome {
	goalID := t.r.GoalID
	if goalID == "" && t.r.GameID != "" {
		for _, g := range t.open {
			if g.GameID == t.r.GameID {
				goalID = g.ID
			}
		}
	}
	aps, err := a.Store.PendingApprovals(ctx, t.u.ID, goalID)
	if err != nil {
		return a.capabilityError("look up the approval", t.u, err)
	}
	if len(aps) == 0 && goalID != "" {
		aps, _ = a.Store.PendingApprovals(ctx, t.u.ID, "")
	}
	if len(aps) == 0 {
		return outcome{mood: persona.Neutral, note: "The player wants to approve or decline publishing, but nothing is waiting for their approval right now. Say so; when a build is ready you will ask them."}
	}
	if len(aps) > 1 && goalID == "" {
		var titles []string
		for _, x := range aps {
			titles = append(titles, x.GoalTitle)
		}
		return outcome{mood: persona.Neutral, note: "Several builds wait for the player's approval: " + strings.Join(titles, "; ") + ". Ask which one they mean."}
	}
	ap := aps[0]
	label := fmt.Sprintf("%q", ap.GoalTitle)
	card := []Card{{Kind: "mission", GoalID: ap.GoalID}}
	if t.r.Action == "decline" || t.r.Action == "reject" {
		if err := a.Store.DecideAsOwner(ctx, ap.ID, t.u.ID, false, "declined in chat"); err != nil {
			return outcome{mood: persona.Neutral, cards: card, note: "That approval is no longer waiting (decided or withdrawn meanwhile). Say so briefly."}
		}
		return outcome{mood: persona.Neutral, goalID: ap.GoalID, cards: card, note: "Done: you declined publishing " + label + " for now. It stays a draft they can play and revise, and they can publish later."}
	}
	why := ""
	if ap.Preview != "" {
		why = "What will happen: " + truncate(ap.Preview, 300) + ". "
	}
	o := ask(&Pending{Action: pendPublish, ApprovalID: ap.ID, GoalID: ap.GoalID, Label: "publish " + label}, persona.Smile, why)
	o.cards, o.goalID = card, ap.GoalID
	return o
}
