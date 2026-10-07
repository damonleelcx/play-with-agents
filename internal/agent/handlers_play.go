package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// ── Acting on the route ────────────────────────────────────────────────────

// acting intents change something; an unsure router does not get to run
// them. Read-only intents are answered even when unsure.
var acting = []string{IntentPlay, IntentInvite, IntentJoinTable, IntentResumeTable, IntentRematch, IntentLeaveTable, IntentBack,
	IntentFavorites, IntentVisibility, IntentDeleteGame, IntentRenameGame, IntentBuild, IntentRevise, IntentControl, IntentApprove,
	IntentPreference, IntentMemory, IntentReport}

func (a *Agent) act(ctx context.Context, t *turn) outcome {
	r := t.r
	if r.Confidence < 0.5 && strings.TrimSpace(r.Clarify) != "" && contains(acting, r.Intent) {
		return outcome{mood: persona.Neutral, note: "You are not sure what the player wants. Ask exactly this, in your own words: " + r.Clarify}
	}
	switch r.Intent {
	case IntentPlay:
		return a.play(ctx, t)
	case IntentInvite:
		return a.invite(ctx, t)
	case IntentJoinTable:
		return a.joinTable(ctx, t)
	case IntentListTables:
		return a.listTables(ctx, t)
	case IntentResumeTable:
		return a.resumeTable(ctx, t)
	case IntentRematch:
		return a.rematch(ctx, t)
	case IntentLeaveTable:
		return a.leaveTable(ctx, t)
	case IntentBack:
		return a.back(ctx, t)
	case IntentCoach:
		return a.coach(ctx, t)
	case IntentRules:
		return a.rules(ctx, t)
	case IntentAgentsInfo:
		return a.agentsInfo(ctx, t)
	case IntentFavorites:
		return a.favorites(ctx, t)
	case IntentRecommend:
		return a.recommend(ctx, t)
	case IntentListGames:
		return a.listGames(ctx, t)
	case IntentGameInfo:
		return a.gameInfo(ctx, t)
	case IntentVisibility:
		return a.visibility(ctx, t)
	case IntentDeleteGame:
		return a.deleteGame(ctx, t)
	case IntentRenameGame:
		return a.renameGame(ctx, t)
	case IntentBuild:
		return a.build(ctx, t)
	case IntentRevise:
		return a.revise(ctx, t)
	case IntentControl:
		return a.control(ctx, t)
	case IntentApprove:
		return a.approve(ctx, t)
	case IntentPreference:
		return a.preference(ctx, t)
	case IntentShowSettings:
		return a.showSettings(ctx, t)
	case IntentStats:
		return a.stats(ctx, t)
	case IntentCapabilities:
		return a.capabilities(ctx, t)
	case IntentMemory:
		return a.memory(ctx, t)
	case IntentDeleteAccount:
		return a.deleteAccount(ctx, t)
	case IntentReport:
		return a.report(ctx, t)
	case IntentRealMoney:
		return a.realMoney(ctx, t)
	case IntentCheat:
		return a.cheat(ctx, t)
	case IntentOutOfScope:
		return a.outOfScope(ctx, t)
	}
	mood, ok := persona.ParseMood(r.Mood)
	if !ok {
		mood = persona.Smile
	}
	return outcome{mood: mood}
}

// notOpen is the reply when a capability is not wired on this server.
func notOpen(what, part string) outcome {
	return outcome{mood: persona.Sad, note: fmt.Sprintf("The player wants to %s, but %s are not open yet on this server. Say so honestly and briefly, and offer something you can do instead (explain a game, talk strategy).", what, part)}
}

// capabilityError turns a capability failure into the reply note.
func (a *Agent) capabilityError(what string, u User, err error) outcome {
	if errors.Is(err, ErrRejected) {
		return outcome{mood: persona.Sad, note: "You tried to " + what + ", but it was refused: " + strings.TrimPrefix(err.Error(), ErrRejected.Error()+": ") + ". Nothing changed. Explain in one line and suggest a fix."}
	}
	slog.Error(what, "user", u.ID, "err", err)
	return outcome{mood: persona.Sad, note: "You tried to " + what + ", but something went wrong on the server. Nothing changed. Apologise briefly and suggest trying again in a moment."}
}

// openTables is the player's unfinished tables (for the router and the
// table intents). A missing or failing table service gives none.
func (a *Agent) openTables(ctx context.Context, userID string) []TableInfo {
	if a.Tables == nil {
		return nil
	}
	ts, err := a.Tables.MyTables(ctx, userID, false)
	if err != nil {
		slog.Warn("my tables", "err", err)
		return nil
	}
	return ts
}

// link makes an absolute link on this site when the origin is known.
func (a *Agent) link(path string) string {
	return strings.TrimRight(a.PublicOrigin, "/") + path
}

func (a *Agent) inviteLink(code string) string { return a.link("/join/" + code) }

// pickTable resolves which of the player's tables a request is about: the
// router's table_id or code if it is one of theirs, else the most recently
// active table that passes ok (tables come most recent first).
func pickTable(tables []TableInfo, r Route, ok func(TableInfo) bool) (TableInfo, bool) {
	for _, t := range tables {
		if (r.TableID != "" && t.ID == r.TableID) || (r.Code != "" && strings.EqualFold(t.Code, strings.TrimSpace(r.Code))) {
			return t, true
		}
	}
	for _, t := range tables {
		if ok(t) {
			return t, true
		}
	}
	return TableInfo{}, false
}

func describeTable(t TableInfo) string {
	s := fmt.Sprintf("%q (%s, %s", t.Name, t.GameName, t.Status)
	if t.Paused {
		s += ", paused"
	}
	s += fmt.Sprintf(", %d/%d seats taken", t.Seats-t.OpenSeats, t.Seats)
	if len(t.Players) > 0 {
		s += ": " + strings.Join(t.Players, ", ")
	}
	if t.Code != "" && (t.Status == "lobby" || t.Status == "playing") {
		s += ", invite code " + t.Code
	}
	return s + ")"
}

// ── play: start a table (and the game-night skill) ─────────────────────────

// tableSettingKeys are what the router may put in options that are table
// settings, not game options.
func splitSettings(opts map[string]any) (map[string]any, TableSettings) {
	var ts TableSettings
	game := map[string]any{}
	for k, v := range opts {
		switch strings.ToLower(k) {
		case "difficulty", "agent_difficulty":
			if s, ok := v.(string); ok && contains([]string{"casual", "regular", "shark"}, strings.ToLower(s)) {
				ts.Difficulty = strings.ToLower(s)
			}
		case "agent_speed", "speed":
			if s, ok := v.(string); ok && contains([]string{"fast", "natural", "slow"}, strings.ToLower(s)) {
				ts.AgentSpeed = strings.ToLower(s)
			}
		case "table_talk", "talk":
			if s, ok := v.(string); ok && contains([]string{"all", "quiet", "off"}, strings.ToLower(s)) {
				ts.TableTalk = strings.ToLower(s)
			}
		case "turn_seconds", "clock", "turn_clock":
			if n, ok := wholeNumber(v); ok && (n == 0 || (n >= 5 && n <= 600)) {
				ts.TurnSeconds = &n
			} else if s, ok := v.(string); ok {
				var n int
				if _, err := fmt.Sscan(strings.TrimSuffix(strings.TrimSpace(s), "s"), &n); err == nil && (n == 0 || (n >= 5 && n <= 600)) {
					ts.TurnSeconds = &n
				}
			}
		default:
			game[k] = v
		}
	}
	return game, ts
}

// play sets up a table: the game, the player, the agents they named, and
// open seats for the friends they mentioned. With friends coming it is the
// game-night skill: the table waits in the lobby and the reply carries the
// invite link; with no open seat it starts at once.
func (a *Agent) play(ctx context.Context, t *turn) outcome {
	r, u, lang := t.r, t.u, t.lang
	if a.Tables == nil {
		return outcome{mood: persona.Sad, note: "The player wants to play, but the tables are not open yet on this server. Say so honestly and briefly, and offer to explain a game or talk strategy meanwhile."}
	}
	want := r.GameID
	if strings.TrimSpace(want) == "" {
		want = "holdem"
	}
	g, ok := findGame(t.games, want)
	if !ok {
		return outcome{mood: persona.Surprised, note: fmt.Sprintf("The player asked to play %q, which is not a game they can play here. Say you couldn't find it and name a few they can play: %s. Offer to build it in the studio if it is a new idea.", want, gameNames(t.games, 6))}
	}
	if g.Status == "building" {
		return outcome{mood: persona.Neutral, cards: []Card{{Kind: "game", GameID: g.ID}}, note: g.Name + " is still being built; there is nothing to play yet. Say so and offer to check on the build."}
	}
	agents := cleanAgents(r.AgentIDs)
	friends := max(int(r.Friends), 0)
	seats := int(r.Seats)
	if seats < 1+len(agents)+friends {
		seats = 1 + len(agents) + friends
	}
	if g.MinSeats > 0 && seats < g.MinSeats {
		seats = g.MinSeats
	}
	if g.MaxSeats > 0 && seats > g.MaxSeats {
		return outcome{mood: persona.Surprised, note: fmt.Sprintf("%s seats at most %d players, and the player asked for %d (themselves, %d agents, %d friends). Nothing was created. Say so and ask who should sit out.", g.Name, g.MaxSeats, seats, len(agents), friends)}
	}
	opts, settings := splitSettings(r.Options)
	info, err := a.Tables.CreateTable(ctx, u.ID, TableRequest{GameID: g.ID, AgentIDs: agents, Seats: seats, Options: opts, Lang: lang, Settings: settings})
	if err != nil {
		if errors.Is(err, ErrRejected) {
			return outcome{mood: persona.Sad, note: "You tried to set up the table, but it was refused: " + strings.TrimPrefix(err.Error(), ErrRejected.Error()+": ") + ". Nothing was created. Explain in one line and suggest a fix."}
		}
		slog.Error("create table", "user", u.ID, "game", g.ID, "err", err)
		return outcome{mood: persona.Sad, note: "You tried to set up the table, but something went wrong on the server. Nothing was created. Apologise briefly and suggest trying again in a moment."}
	}
	names := make([]string, len(agents))
	for i, id := range agents {
		names[i] = agentName(id, lang)
	}
	who := "just the player so far"
	if len(names) > 0 {
		who = "the player, " + strings.Join(names, ", ")
	}
	openSeats := seats - 1 - len(agents)
	var extra []string
	if settings.Difficulty != "" {
		extra = append(extra, "agents at "+settings.Difficulty+" difficulty")
	}
	if settings.TurnSeconds != nil {
		if *settings.TurnSeconds == 0 {
			extra = append(extra, "no turn clock")
		} else {
			extra = append(extra, fmt.Sprintf("%d-second turn clock", *settings.TurnSeconds))
		}
	}
	if settings.AgentSpeed != "" {
		extra = append(extra, "agent speed "+settings.AgentSpeed)
	}
	if settings.TableTalk != "" {
		extra = append(extra, "table talk "+settings.TableTalk)
	}
	how := ""
	if len(extra) > 0 {
		how = " Table settings: " + strings.Join(extra, ", ") + "."
	}
	note := fmt.Sprintf("You just created a %s table with %d seats: %s; %d seat(s) open.%s The table card is shown under your message. Say it is ready, add one line of banter about the opponents (their styles), and tell them to tap the card to sit down. Don't list the rules unless asked.",
		g.Name, seats, who, openSeats, how)
	if openSeats > 0 && info.Code != "" {
		note += fmt.Sprintf(" Friends join with the invite code %s or this link: %s — give both to the player so they can share them. The game starts when the player presses start (empty seats are filled per their settings).", info.Code, a.inviteLink(info.Code))
	}
	return outcome{mood: persona.Wink, cards: []Card{{Kind: "table", TableID: info.ID}}, note: note}
}

// ── invite / join / list / resume / rematch / leave / back ─────────────────

func (a *Agent) invite(ctx context.Context, t *turn) outcome {
	if a.Tables == nil {
		return notOpen("invite friends to a table", "the tables")
	}
	tb, ok := pickTable(t.tables, t.r, func(x TableInfo) bool { return x.Status == "lobby" && (x.IsHost || x.Seated) })
	if !ok {
		tb, ok = pickTable(t.tables, t.r, func(x TableInfo) bool { return x.Status == "lobby" || x.Status == "playing" })
	}
	if !ok {
		return outcome{mood: persona.Neutral, note: "The player wants to invite friends, but they have no open table. Say so and offer to set one up with seats for friends (ask how many friends and which game)."}
	}
	note := fmt.Sprintf("The player wants to invite friends to %s. Give them the invite code %s and the link %s to share.", describeTable(tb), tb.Code, a.inviteLink(tb.Code))
	switch {
	case tb.Status == "playing":
		note += " The game has already started, so friends who join now watch as spectators until a rematch."
	case tb.OpenSeats == 0:
		note += " Every seat is taken: friends who join now watch as spectators — the host can open a seat on the table page."
	default:
		note += fmt.Sprintf(" %d seat(s) are open.", tb.OpenSeats)
	}
	return outcome{mood: persona.Smile, cards: []Card{{Kind: "table", TableID: tb.ID}}, note: note}
}

// inviteCode extracts a 6-character invite code from the router's slot or
// the message itself.
func inviteCode(r Route, text string) string {
	norm := func(s string) string {
		var b strings.Builder
		for _, c := range strings.ToUpper(s) {
			if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
				b.WriteRune(c)
			}
		}
		return b.String()
	}
	if c := norm(r.Code); len(c) == 6 {
		return c
	}
	for _, f := range strings.FieldsFunc(text, func(c rune) bool { return !(c < unicode.MaxASCII && (unicode.IsLetter(c) || unicode.IsDigit(c))) }) {
		if len(f) == 6 && strings.ToUpper(f) == f && strings.ContainsAny(f, "ABCDEFGHJKMNPQRSTUVWXYZ") {
			return f
		}
	}
	if c := norm(r.Code); c != "" {
		return c
	}
	return ""
}

func (a *Agent) joinTable(ctx context.Context, t *turn) outcome {
	if a.Tables == nil {
		return notOpen("join a table", "the tables")
	}
	code := inviteCode(t.r, t.text)
	if len(code) != 6 {
		return outcome{mood: persona.Neutral, note: "The player wants to join a table but did not give a valid invite code (6 letters and digits, like K7M2QX). Ask for the code their friend shared."}
	}
	info, err := a.Tables.JoinTable(ctx, t.u.ID, code)
	if err != nil {
		if errors.Is(err, ErrRejected) {
			return outcome{mood: persona.Sad, note: fmt.Sprintf("No live table has the invite code %s (it may have finished, or there is a typo). Say so and ask them to check the code.", code)}
		}
		return a.capabilityError("join the table", t.u, err)
	}
	note := fmt.Sprintf("The player joined %s with the code %s; the card is under your message.", describeTable(info), code)
	if !info.Seated {
		note += " There was no open seat (or the game had started), so they are watching as a spectator."
	} else {
		note += " They have a seat; the host starts the game."
	}
	return outcome{mood: persona.Wink, cards: []Card{{Kind: "table", TableID: info.ID}}, note: note}
}

func (a *Agent) listTables(ctx context.Context, t *turn) outcome {
	if a.Tables == nil {
		return notOpen("see their tables", "the tables")
	}
	ts := t.tables
	if t.r.Action == "all" {
		var err error
		if ts, err = a.Tables.MyTables(ctx, t.u.ID, true); err != nil {
			return a.capabilityError("list the tables", t.u, err)
		}
	}
	if len(ts) == 0 {
		return outcome{mood: persona.Neutral, note: "The player has no open tables right now. Say so and offer to deal them in (or set up a game night with friends)."}
	}
	var b strings.Builder
	var cards []Card
	for i, x := range ts {
		if i >= 8 {
			break
		}
		fmt.Fprintf(&b, "\n- %s", describeTable(x))
		if x.IsHost {
			b.WriteString(" [they host]")
		}
		if x.Away {
			b.WriteString(" [they are marked away]")
		}
		if len(cards) < 3 && (x.Status == "lobby" || x.Status == "playing") {
			cards = append(cards, Card{Kind: "table", TableID: x.ID})
		}
	}
	return outcome{mood: persona.Neutral, cards: cards, note: "The player asked about their tables. Summarise them briefly (cards for the open ones are under your message):" + b.String()}
}

func (a *Agent) resumeTable(ctx context.Context, t *turn) outcome {
	if a.Tables == nil {
		return notOpen("resume a table", "the tables")
	}
	tb, ok := pickTable(t.tables, t.r, func(x TableInfo) bool { return x.Seated || x.IsHost })
	if !ok {
		return outcome{mood: persona.Neutral, note: "The player wants to resume a game, but they have no unfinished table. Say so and offer a new table or a rematch."}
	}
	info, err := a.Tables.BackToTable(ctx, t.u.ID, tb.ID)
	if err != nil {
		return a.capabilityError("take them back to the table", t.u, err)
	}
	note := "You took the player back to " + describeTable(info) + "; tap the card to sit down."
	if tb.Paused && !info.Paused {
		note += " It was paused while nobody was there; it is running again."
	}
	if info.Paused && info.PausedReason == "fault" {
		note += " It is paused after a problem; only the host can resume it from the table."
	}
	return outcome{mood: persona.Smile, cards: []Card{{Kind: "table", TableID: info.ID}}, note: note}
}

func (a *Agent) rematch(ctx context.Context, t *turn) outcome {
	if a.Tables == nil {
		return notOpen("start a rematch", "the tables")
	}
	all, err := a.Tables.MyTables(ctx, t.u.ID, true)
	if err != nil {
		return a.capabilityError("find the last game", t.u, err)
	}
	// The router only sees open tables, so a table_id it names may be one
	// still playing; a finished table (the one they mean by "again") wins.
	finishedOnly := func(x TableInfo) bool { return x.Status == "finished" }
	tb, ok := pickTable(all, Route{TableID: t.r.TableID}, finishedOnly)
	if ok && !finishedOnly(tb) {
		if f, found := pickTable(all, Route{}, finishedOnly); found {
			tb = f
		}
	}
	if !ok {
		return outcome{mood: persona.Neutral, note: "The player wants a rematch, but they have no finished game to rematch. Offer to deal a new table instead."}
	}
	if tb.Status != "finished" {
		return outcome{mood: persona.Neutral, cards: []Card{{Kind: "table", TableID: tb.ID}}, note: "The player asked for a rematch, but the game at " + describeTable(tb) + " is not over yet. Say so: finish it first (or leave)."}
	}
	info, err := a.Tables.Rematch(ctx, t.u.ID, tb.ID)
	if err != nil {
		return a.capabilityError("start the rematch", t.u, err)
	}
	note := "You started a rematch of " + tb.Name + " with the same game, options and people: " + describeTable(info) + "."
	if info.Status == "lobby" {
		note += fmt.Sprintf(" It waits in the lobby for the people from last time (invite code %s, link %s).", info.Code, a.inviteLink(info.Code))
	} else {
		note += " It is already dealing."
	}
	return outcome{mood: persona.Wink, cards: []Card{{Kind: "table", TableID: info.ID}}, note: note + " Add a line of banter about revenge."}
}

func (a *Agent) leaveTable(ctx context.Context, t *turn) outcome {
	if a.Tables == nil {
		return notOpen("leave a table", "the tables")
	}
	tb, ok := pickTable(t.tables, t.r, func(x TableInfo) bool { return x.Seated || x.IsHost })
	if !ok {
		return outcome{mood: persona.Neutral, note: "The player wants to leave a table, but they are not at any open table. Say so briefly."}
	}
	why := ""
	switch {
	case tb.Status == "playing" && tb.Seated:
		why = "The game is running: an agent takes over their seat so the others can play on, and they cannot take it back. "
	case tb.Status == "lobby" && tb.IsHost:
		why = "They host this table and it has not started: leaving closes it for everyone. "
	}
	return ask(&Pending{Action: pendLeaveTable, TableID: tb.ID, Label: "leave the table " + describeTable(tb)}, persona.Neutral, why)
}

func (a *Agent) back(ctx context.Context, t *turn) outcome {
	if a.Tables == nil {
		return notOpen("get back to their table", "the tables")
	}
	tb, ok := pickTable(t.tables, t.r, func(x TableInfo) bool { return x.Away || x.Paused })
	if !ok {
		tb, ok = pickTable(t.tables, t.r, func(x TableInfo) bool { return x.Seated && x.Status == "playing" })
	}
	if !ok {
		return outcome{mood: persona.Smile, note: "The player says they are back, but they have no table waiting for them. Welcome them back warmly and offer to deal them in."}
	}
	info, err := a.Tables.BackToTable(ctx, t.u.ID, tb.ID)
	if err != nil {
		return a.capabilityError("mark them back at the table", t.u, err)
	}
	note := "Welcome back: you cleared their away mark at " + describeTable(info) + "; their turns wait for them again."
	if tb.Paused && !info.Paused {
		note += " The table was paused and is running again."
	}
	return outcome{mood: persona.Smile, cards: []Card{{Kind: "table", TableID: info.ID}}, note: note}
}

// ── coach (the player's own seat only) ─────────────────────────────────────

func (a *Agent) coach(ctx context.Context, t *turn) outcome {
	general := outcome{mood: persona.Neutral, note: "The player asked what to do, but they are not playing at a table right now, so you cannot see a hand of theirs. Give one or two general tips for the situation they describe (if any), and offer to deal them in."}
	if a.Coach == nil || a.Tables == nil {
		return general
	}
	tb, ok := pickTable(t.tables, t.r, func(x TableInfo) bool { return x.Seated && x.Status == "playing" })
	if !ok || !tb.Seated || tb.Status != "playing" {
		return general
	}
	adv, err := a.Coach.Advice(ctx, t.u.ID, tb.ID)
	if err != nil {
		if errors.Is(err, ErrRejected) {
			general.note = "You could not coach the player's hand: " + strings.TrimPrefix(err.Error(), ErrRejected.Error()+": ") + ". Say so in one line and offer a general tip."
			return general
		}
		return a.capabilityError("look at their hand", t.u, err)
	}
	return outcome{mood: persona.Neutral, cards: []Card{{Kind: "table", TableID: tb.ID}}, note: coachNote(adv)}
}

// coachNote is the reply's brief for coaching. It holds only what the
// player's own seat can see (CoachAdvice is built from their seat view), and
// it tells the reply never to guess at other players' hidden cards.
func coachNote(adv CoachAdvice) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The player asked for coaching at their %s table. Everything below is from THEIR OWN seat only. Coach them: what you would do and why, in two or three sentences, with the key number if there is one. Never claim to know or guess other players' hidden cards; reason only from what is public and their own hand.", adv.GameName)
	if !adv.YourTurn {
		b.WriteString(" It is not their turn right now; say what to plan for when it is.")
	}
	if h := adv.Holdem; h != nil {
		fmt.Fprintf(&b, "\nHOLD'EM COACH (computed from their seat's view by simulation): best hand now %s; equity about %.0f%% against %d opponent(s)", h.HandName, h.Equity*100, h.Opponents)
		if h.ToCall > 0 {
			fmt.Fprintf(&b, "; %d chips to call, which needs %.0f%% equity", h.ToCall, h.PotOdds*100)
		}
		fmt.Fprintf(&b, ". Suggestion: %s", h.Suggestion)
	}
	if len(adv.Legal) > 0 {
		b.WriteString("\nTHEIR LEGAL MOVES: " + strings.Join(adv.Legal, "; "))
	}
	if adv.YourView != "" {
		b.WriteString("\nTHEIR SEAT'S VIEW (JSON): " + truncate(adv.YourView, 3000))
	}
	return b.String()
}

// ── agents ─────────────────────────────────────────────────────────────────

func rosterLine(id, lang string) string {
	for _, r := range Roster {
		if r.ID == id {
			return fmt.Sprintf("%s (%s)", agentName(id, lang), r.Style)
		}
	}
	return id
}

// ── helpers shared by the handlers ─────────────────────────────────────────

// findGame matches a catalog entry by id, then by name (case-insensitive),
// then by a name that contains the query ("hold'em" → "Texas Hold'em").
func findGame(games []GameInfo, q string) (GameInfo, bool) {
	q = strings.TrimSpace(q)
	lq := strings.ToLower(q)
	if q == "" {
		return GameInfo{}, false
	}
	for _, g := range games {
		if g.ID == q {
			return g, true
		}
	}
	for _, g := range games {
		if strings.ToLower(g.Name) == lq {
			return g, true
		}
	}
	if len([]rune(lq)) >= 3 {
		for _, g := range games {
			if strings.Contains(strings.ToLower(g.Name), lq) {
				return g, true
			}
		}
	}
	return GameInfo{}, false
}

// resolveGame finds the game a request is about: in the catalog, else (an
// unlisted game reachable by id) through Catalog.Game.
func (a *Agent) resolveGame(ctx context.Context, t *turn, q string) (GameInfo, bool) {
	if g, ok := findGame(t.games, q); ok {
		return g, true
	}
	if a.Catalog != nil && strings.TrimSpace(q) != "" {
		if g, err := a.Catalog.Game(ctx, t.u.ID, strings.TrimSpace(q)); err == nil && g.ID != "" {
			return g, true
		}
	}
	return GameInfo{}, false
}

// myGame resolves one of the player's own games, or explains why not.
func (a *Agent) myGame(ctx context.Context, t *turn, what string) (GameInfo, *outcome) {
	if a.Catalog == nil {
		o := notOpen(what, "the game catalog")
		return GameInfo{}, &o
	}
	q := t.r.GameID
	if q == "" {
		var mine []GameInfo
		for _, g := range t.games {
			if g.Mine {
				mine = append(mine, g)
			}
		}
		if len(mine) == 1 {
			return mine[0], nil
		}
		o := outcome{mood: persona.Neutral, note: fmt.Sprintf("The player wants to %s but did not say which game. Ask which of their games they mean: %s.", what, gameNames(mine, 8))}
		if len(mine) == 0 {
			o.note = fmt.Sprintf("The player wants to %s, but they have no games of their own yet. Say so, and offer to build one in the studio.", what)
		}
		return GameInfo{}, &o
	}
	g, ok := a.resolveGame(ctx, t, q)
	if !ok || !g.Mine {
		o := outcome{mood: persona.Neutral, note: fmt.Sprintf("The player wants to %s, but %q is not one of their own games (only the owner can do that). Say so and ask which of their games they mean.", what, q)}
		return GameInfo{}, &o
	}
	return g, nil
}

func gameNames(games []GameInfo, n int) string {
	var out []string
	for i, g := range games {
		if i >= n {
			break
		}
		out = append(out, g.Name)
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, ", ")
}

func gameLine(g GameInfo) string {
	s := fmt.Sprintf("%s (id %s, %s", g.Name, g.ID, seatRange(g))
	if g.Mine {
		s += ", theirs: " + g.Status + ", " + g.Visibility
	} else if g.Owner != "" {
		s += ", by " + g.Owner
	}
	if g.Plays > 0 {
		s += fmt.Sprintf(", played %d times", g.Plays)
	}
	s += ")"
	if g.Summary != "" {
		s += ": " + truncate(g.Summary, 140)
	}
	return s
}

func seatRange(g GameInfo) string {
	if g.MinSeats == g.MaxSeats {
		return fmt.Sprintf("%d players", g.MinSeats)
	}
	return fmt.Sprintf("%d-%d players", g.MinSeats, g.MaxSeats)
}

// savePrefs merges a patch into the player's stored preferences.
func (a *Agent) savePrefs(ctx context.Context, userID string, patch map[string]any) error {
	raw, _ := json.Marshal(patch)
	_, err := a.Store.Pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET data = user_preferences.data || EXCLUDED.data, updated_at=now()`, userID, raw)
	return err
}
