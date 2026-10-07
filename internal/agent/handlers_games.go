package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/damonleelcx/play-with-agents/internal/agents"
	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// ── rules and game knowledge ───────────────────────────────────────────────

func (a *Agent) rules(ctx context.Context, t *turn) outcome {
	out := outcome{mood: persona.Neutral, note: "The player asked about rules or strategy. Answer clearly: the idea in one line, then the details, then a tiny example."}
	if t.r.GameID == "" {
		return out
	}
	g, ok := a.resolveGame(ctx, t, t.r.GameID)
	if !ok {
		return out
	}
	out.cards = []Card{{Kind: "game", GameID: g.ID}}
	if a.Catalog != nil {
		if text, err := a.Catalog.Rules(ctx, t.u.ID, g.ID); err == nil && strings.TrimSpace(text) != "" {
			out.note += fmt.Sprintf("\nThe question is about %s. Base your answer on its rules as written here (they are authoritative for this platform; if they do not cover the question, say so and say how it is usually played):\n<<<RULES\n%s\nRULES>>>", g.Name, truncate(text, 6000))
			return out
		}
	}
	out.note += "\nThe question is about " + g.Name + "."
	return out
}

func (a *Agent) gameInfo(ctx context.Context, t *turn) outcome {
	g, ok := a.resolveGame(ctx, t, t.r.GameID)
	if !ok {
		return outcome{mood: persona.Neutral, note: fmt.Sprintf("The player asked about a game you could not find (%q). Say so and name a few they can play: %s.", t.r.GameID, gameNames(t.games, 6))}
	}
	note := "The player asked about this game. Describe it in two or three lines (what it is like to play, how many players, who made it) and offer to deal them in or explain the rules. The game card is under your message.\nGAME: " + gameLine(g)
	if a.Catalog != nil {
		if text, err := a.Catalog.Rules(ctx, t.u.ID, g.ID); err == nil && strings.TrimSpace(text) != "" {
			note += "\nRULES (for reference; do not recite them): " + truncate(text, 2000)
		}
	}
	if g.Mine && g.Visibility == "unlisted" {
		note += "\nIt is unlisted: anyone with this link can play it: " + a.link("/app/games/"+g.ID)
	}
	return outcome{mood: persona.Smile, cards: []Card{{Kind: "game", GameID: g.ID}}, note: note}
}

func (a *Agent) listGames(ctx context.Context, t *turn) outcome {
	mineOnly := t.r.Action == "mine"
	var b strings.Builder
	var cards []Card
	n := 0
	for _, g := range t.games {
		if mineOnly && !g.Mine {
			continue
		}
		if n >= 25 {
			break
		}
		n++
		fmt.Fprintf(&b, "\n- %s", gameLine(g))
		if mineOnly && len(cards) < 4 {
			cards = append(cards, Card{Kind: "game", GameID: g.ID})
		}
	}
	if mineOnly && n == 0 {
		return outcome{mood: persona.Neutral, note: "The player asked about their own games, but they have not made any yet. Say so and invite them to describe one — the studio agents will build it."}
	}
	what := "the games they can play"
	if mineOnly {
		what = "their own games (with status and who can see each)"
	}
	return outcome{mood: persona.Smile, cards: cards, note: "The player asked about " + what + ". List them briefly (name and one line each), then offer to deal them into one:" + b.String()}
}

func (a *Agent) recommend(ctx context.Context, t *turn) outcome {
	var b strings.Builder
	for i, g := range t.games {
		if i >= 25 {
			break
		}
		if g.Status == "building" {
			continue
		}
		fmt.Fprintf(&b, "\n- %s", gameLine(g))
	}
	note := "The player wants a game recommendation. Pick one game that fits what they said (players, mood, time) and say why in two lines; mention one alternative. Offer to deal them in. Only recommend games from this list:" + b.String()
	if n := int(t.r.Seats); n > 0 {
		note += fmt.Sprintf("\nThey want to play with %d players in total: only games that seat that many.", n)
	}
	out := outcome{mood: persona.Smile, note: note}
	if g, ok := findGame(t.games, t.r.GameID); ok {
		if n := int(t.r.Seats); n == 0 || (n >= g.MinSeats && n <= g.MaxSeats) {
			out.cards = []Card{{Kind: "game", GameID: g.ID}}
			out.note += "\nYour pick (its card is under your message): " + g.Name
		}
	}
	return out
}

// ── the player's own games: visibility, rename, delete ─────────────────────

func (a *Agent) visibility(ctx context.Context, t *turn) outcome {
	g, refused := a.myGame(ctx, t, "change who can see a game")
	if refused != nil {
		return *refused
	}
	vis := t.r.Visibility
	switch vis {
	case "public", "unlisted", "private":
	case "publish", "published", "shelf", "everyone":
		vis = "public"
	case "link", "share":
		vis = "unlisted"
	default:
		return outcome{mood: persona.Neutral, note: fmt.Sprintf("The player wants to change who can see %s but it is unclear how. Ask: public (on the shelf for everyone), unlisted (anyone with the link) or private (only them)? It is %s now.", g.Name, g.Visibility)}
	}
	if vis == g.Visibility {
		return outcome{mood: persona.Smile, cards: []Card{{Kind: "game", GameID: g.ID}}, note: fmt.Sprintf("%s is already %s. Say so in one line.", g.Name, vis)}
	}
	if vis == "public" {
		why := "Anyone on the platform will be able to find and play it. "
		if g.Status != "published" {
			why += "It is still a " + g.Status + ", so it appears on the shelf once it is published. "
		}
		return ask(&Pending{Action: pendMakePublic, GameID: g.ID, Label: fmt.Sprintf("make %q public on the shelf", g.Name)}, persona.Neutral, why)
	}
	if err := a.Catalog.SetVisibility(ctx, t.u.ID, g.ID, vis); err != nil {
		return a.capabilityError("change who can see "+g.Name, t.u, err)
	}
	note := fmt.Sprintf("Done: %s is now %s.", g.Name, vis)
	if vis == "private" {
		note += " Only the player can see and play it now; tables already running keep going."
	} else {
		note += " It is off the public shelf, but anyone with this link can play it: " + a.link("/app/games/"+g.ID) + " — give them the link."
		if g.Status != "published" {
			note += " (The link works once the game is published.)"
		}
	}
	return outcome{mood: persona.Smile, cards: []Card{{Kind: "game", GameID: g.ID}}, note: note}
}

func (a *Agent) renameGame(ctx context.Context, t *turn) outcome {
	g, refused := a.myGame(ctx, t, "rename a game")
	if refused != nil {
		return *refused
	}
	name := strings.TrimSpace(strings.Trim(t.r.Name, `"'“”「」`))
	if name == "" {
		return outcome{mood: persona.Neutral, note: "The player wants to rename " + g.Name + " but did not say the new name. Ask for it."}
	}
	if len([]rune(name)) > 60 {
		return outcome{mood: persona.Neutral, note: "The new name is too long (60 characters at most). Ask for a shorter one."}
	}
	if err := a.Catalog.Rename(ctx, t.u.ID, g.ID, name); err != nil {
		return a.capabilityError("rename "+g.Name, t.u, err)
	}
	return outcome{mood: persona.Smile, cards: []Card{{Kind: "game", GameID: g.ID}}, note: fmt.Sprintf("Done: you renamed %q to %q. React to the new name in one line.", g.Name, name)}
}

func (a *Agent) deleteGame(ctx context.Context, t *turn) outcome {
	g, refused := a.myGame(ctx, t, "delete a game")
	if refused != nil {
		return *refused
	}
	switch g.Status {
	case "draft":
		return ask(&Pending{Action: pendDeleteGame, GameID: g.ID, Label: fmt.Sprintf("delete the draft %q", g.Name)}, persona.Neutral, "It cannot be undone. ")
	case "building":
		return outcome{mood: persona.Neutral, note: g.Name + " is still being built. To stop it, the build can be cancelled first (offer that); a cancelled build leaves a draft that can then be deleted."}
	default:
		return outcome{mood: persona.Neutral, cards: []Card{{Kind: "game", GameID: g.ID}}, note: g.Name + " is published, and published games are not deleted because other people's tables and records refer to it. Offer to make it private instead, so only they can see it."}
	}
}

// ── agents ─────────────────────────────────────────────────────────────────

func (a *Agent) agentsInfo(_ context.Context, t *turn) outcome {
	ids := cleanAgents(t.r.AgentIDs)
	if len(ids) == 0 {
		ids = agents.IDs()
	}
	var b strings.Builder
	for _, id := range ids {
		ag, ok := agents.Get(id)
		if !ok {
			continue
		}
		tx := ag.Localized(t.lang)
		fmt.Fprintf(&b, "\n- %s — %s: %s Play style: %s. Tightness %.0f%%, aggression %.0f%%, bluffing %.0f%%, talkative %.0f%%.",
			tx.Name, tx.Title, tx.Bio, rosterLine(id, t.lang), ag.Style.Tightness*100, ag.Style.Aggression*100, ag.Style.Bluff*100, ag.Style.Talk*100)
	}
	fav := favoriteAgents(t.prefs)
	if len(fav) > 0 {
		b.WriteString("\nTheir favourites: " + strings.Join(fav, ", "))
	}
	what := "the AI agents"
	if len(t.r.AgentIDs) > 0 {
		what = "these agents"
	}
	return outcome{mood: persona.Smile, note: "The player asked about " + what + ". Introduce them in your own words, affectionately, one or two lines each (you know them well; tease a little), and offer to seat them at a table:" + b.String()}
}

func favoriteAgents(prefs map[string]any) []string {
	var out []string
	switch v := prefs["favorite_agents"].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && IsAgentID(s) {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	}
	return out
}

func (a *Agent) favorites(ctx context.Context, t *turn) outcome {
	cur := favoriteAgents(t.prefs)
	ids := cleanAgents(t.r.AgentIDs)
	names := func(ids []string) string {
		var n []string
		for _, id := range ids {
			n = append(n, agentName(id, t.lang))
		}
		if len(n) == 0 {
			return "none"
		}
		return strings.Join(n, ", ")
	}
	action := t.r.Action
	if action == "" {
		action = "add"
	}
	if action == "show" || len(ids) == 0 {
		if action != "show" {
			return outcome{mood: persona.Neutral, note: "The player wants to change their favourite agents but did not say which. Ask which agents."}
		}
		return outcome{mood: persona.Smile, note: "The player's favourite agents: " + names(cur) + ". Favourites are the first agents Aoi seats in empty seats. Say so briefly."}
	}
	next := []string{}
	switch action {
	case "remove":
		for _, id := range cur {
			if !contains(ids, id) {
				next = append(next, id)
			}
		}
	case "set":
		next = ids
	default:
		next = cleanAgents(append(append([]string{}, cur...), ids...))
	}
	if err := ValidatePref("favorite_agents", next); err != nil {
		return outcome{mood: persona.Neutral, note: "That favourites list is not valid (" + err.Error() + "). Say so."}
	}
	if err := a.savePrefs(ctx, t.u.ID, map[string]any{"favorite_agents": next}); err != nil {
		return a.capabilityError("save their favourites", t.u, err)
	}
	return outcome{mood: persona.Smile, prefs: true, note: "Done: their favourite agents are now " + names(next) + " (saved; also in Settings → Agents). Favourites fill empty seats first. Confirm in one line with a playful remark about the pick."}
}
