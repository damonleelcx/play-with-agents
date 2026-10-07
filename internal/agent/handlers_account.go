package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// ── account and settings ───────────────────────────────────────────────────

// displayNameMax matches the settings API's limit on users.name.
const displayNameMax = 80

// preference applies one or more settings the player asked for. Each is
// validated against the Settings schema (PrefSpecs); valid ones are saved
// together, invalid ones are reported. display_name is users.name, as in the
// settings API. "Reset X" stores the default by deleting the key.
func (a *Agent) preference(ctx context.Context, t *turn) outcome {
	if len(t.r.Prefs) == 0 {
		return outcome{mood: persona.Neutral, note: "The player asked to change a setting, but it is not clear which. Ask what they want changed, or point them to Settings."}
	}
	patch := map[string]any{}
	var reset, done, failed []string
	out := outcome{mood: persona.Smile, prefs: true}
	for _, p := range t.r.Prefs {
		raw := strings.TrimSpace(fmt.Sprint(p.Value))
		if p.Value == nil {
			raw = ""
		}
		if p.Key == "display_name" {
			name := strings.TrimSpace(raw)
			if name == "" || len([]rune(name)) > displayNameMax || strings.ContainsAny(name, "\n\r\t") {
				failed = append(failed, fmt.Sprintf("display_name (1 to %d characters, one line)", displayNameMax))
				continue
			}
			if _, err := a.Store.Pool.Exec(ctx, `UPDATE users SET name=$2, updated_at=now() WHERE id=$1`, t.u.ID, name); err != nil {
				slog.Error("set display name", "err", err)
				failed = append(failed, "display_name (server error)")
				continue
			}
			done = append(done, "display_name="+name)
			continue
		}
		if p.Key == "favorite_agents" {
			failed = append(failed, "favorite_agents (say which agents to add or remove)")
			continue
		}
		if l := strings.ToLower(raw); l == "default" || l == "reset" {
			if s, ok := PrefSpecFor(p.Key); ok && s.Chat {
				reset = append(reset, p.Key)
				done = append(done, fmt.Sprintf("%s reset to its default (%v)", p.Key, s.Default))
				continue
			}
		}
		v, err := coercePref(p.Key, raw)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		patch[p.Key] = v
		done = append(done, fmt.Sprintf("%s=%v", p.Key, v))
		if p.Key == "language" {
			out.lang = persona.Normalize(fmt.Sprint(v))
		}
	}
	if len(patch) > 0 || len(reset) > 0 {
		if reset == nil {
			reset = []string{}
		}
		if _, err := a.Store.Pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2::jsonb)
			ON CONFLICT (user_id) DO UPDATE SET data = (user_preferences.data - $3::text[]) || EXCLUDED.data, updated_at=now()`,
			t.u.ID, jsonOf(patch), reset); err != nil {
			slog.Error("set preference", "err", err)
			return outcome{mood: persona.Sad, note: "Saving the setting failed on the server. Apologise and point them to Settings."}
		}
	}
	if len(done) == 0 {
		return outcome{mood: persona.Neutral, note: "The player asked to change a setting, but it could not be applied (" + strings.Join(failed, "; ") + "). Tell them what you can change, or point them to Settings."}
	}
	out.note = "You changed the player's settings: " + strings.Join(done, ", ") + " (saved; they can change them in Settings). Confirm briefly — and from this reply on, behave accordingly."
	if len(failed) > 0 {
		out.note += " These could not be applied: " + strings.Join(failed, "; ") + " — say so."
	}
	return out
}

func (a *Agent) showSettings(_ context.Context, t *turn) outcome {
	all := WithDefaults(t.prefs)
	groups := map[string][]string{}
	var order []string
	for _, s := range PrefSpecs {
		if _, ok := groups[s.Group]; !ok {
			order = append(order, s.Group)
		}
		groups[s.Group] = append(groups[s.Group], fmt.Sprintf("%s=%v", s.Key, all[s.Key]))
	}
	var b strings.Builder
	b.WriteString("display_name=" + t.u.Name)
	for _, g := range order {
		fmt.Fprintf(&b, "\n- %s: %s", g, strings.Join(groups[g], ", "))
	}
	return outcome{mood: persona.Neutral, note: "The player asked to see their settings. Summarise them in plain words grouped like this (not raw keys), mention they can change any of them by just telling you or in Settings:\n" + b.String()}
}

// ── history ────────────────────────────────────────────────────────────────

func (a *Agent) stats(ctx context.Context, t *turn) outcome {
	if a.Stats == nil {
		return notOpen("see their results", "game records")
	}
	st, err := a.Stats.PlayerStats(ctx, t.u.ID)
	if err != nil {
		return a.capabilityError("look up their results", t.u, err)
	}
	if st.Played == 0 {
		note := "The player asked about their results, but they have not finished a game yet."
		if st.InPlay > 0 {
			note += fmt.Sprintf(" They have %d game(s) in progress.", st.InPlay)
		}
		return outcome{mood: persona.Smile, note: note + " Say so cheerfully and offer to deal them in."}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The player asked about their results. Their record (finished games where they kept their seat): %d played, %d won", st.Played, st.Wins)
	fmt.Fprintf(&b, ", net hold'em chips %+d (play money)", st.ChipsWon)
	if st.InPlay > 0 {
		fmt.Fprintf(&b, ", %d game(s) in progress", st.InPlay)
	}
	var games []string
	for name, n := range st.ByGame {
		games = append(games, fmt.Sprintf("%s ×%d", name, n))
	}
	sort.Strings(games)
	b.WriteString(". By game: " + strings.Join(games, ", ") + ".")
	if len(st.Recent) > 0 {
		b.WriteString("\nRECENT:")
		for _, r := range st.Recent {
			fmt.Fprintf(&b, "\n- %s, %s: place %d of %d", r.FinishedAt.Format("Jan 2"), r.GameName, r.Place, r.Players)
			if r.HasChips {
				fmt.Fprintf(&b, ", chips %+d", r.Chips)
			}
		}
	}
	b.WriteString("\nSummarise it in two or three lines with one encouraging or teasing remark. Chips are play money — never talk about them as real money.")
	return outcome{mood: persona.Smile, note: b.String()}
}

// ── support ────────────────────────────────────────────────────────────────

func (a *Agent) capabilities(_ context.Context, _ *turn) outcome {
	return outcome{mood: persona.Smile, note: `The player asked what you can do. Give a short, friendly overview (a compact list), with one example phrase each:
- Play: set up a table of Texas Hold'em or any game on the shelf, with agents and friends ("deal me in with Mika and Ren", "game night with two friends"), give invite links, join a friend's table by code, resume, rematch, leave, "I'm back".
- At the table: coach their own hand ("what should I do?"), explain any rule.
- Agents: introduce Ren, Mika, Captain Bram, Nova and Lin; set favourites and difficulty.
- Games: recommend a game, list games, show one, and for their own games: publish to the shelf, make private or unlisted, rename, delete a draft.
- Studio: build a new game from a description, change it, check progress, pause/resume/cancel, approve publishing.
- Settings: change any setting by asking (language, voice, theme, turn clock…), show their settings.
- Their results and play-money chips; what you remember about them (and forgetting it).
Mention that chips are play money only.`}
}

func (a *Agent) memory(ctx context.Context, t *turn) outcome {
	p := persona.PrefsFrom(t.prefs)
	switch t.r.Action {
	case "forget_all":
		return ask(&Pending{Action: pendForgetAll, Label: "forget everything you remember about them"}, persona.Neutral,
			"It deletes every remembered fact and cannot be undone. ")
	case "forget":
		q := strings.TrimSpace(t.r.Query)
		if q == "" {
			return outcome{mood: persona.Neutral, note: "The player wants you to forget something but did not say what. Ask what to forget (or offer to show what you remember)."}
		}
		rows, err := a.Store.Pool.Query(ctx, `DELETE FROM memories WHERE user_id=$1 AND content ILIKE '%' || $2 || '%' RETURNING content`, t.u.ID, q)
		if err != nil {
			return a.capabilityError("forget that", t.u, err)
		}
		var gone []string
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				gone = append(gone, c)
			}
		}
		rows.Close()
		if len(gone) == 0 {
			mem := a.Store.Memories(ctx, t.u.ID, 20)
			return outcome{mood: persona.Neutral, note: fmt.Sprintf("The player asked you to forget %q, but nothing you remember matches it exactly. What you remember: %s. Ask which one to forget (or offer to forget everything).", q, listOrNone(mem))}
		}
		return outcome{mood: persona.Neutral, note: "Done: you forgot these facts about the player (deleted for good): " + strings.Join(gone, "; ") + ". Confirm in one line, and do not use them again."}
	}
	if !p.MemoryEnabled {
		return outcome{mood: persona.Neutral, note: "The player asked what you remember. Long-term memory is OFF in their settings, so you do not keep facts between conversations. Say so, and that they can turn it on in Settings or by asking."}
	}
	mem := a.Store.Memories(ctx, t.u.ID, 30)
	if len(mem) == 0 {
		return outcome{mood: persona.Neutral, note: "The player asked what you remember about them. You have no remembered facts yet. Say so; you remember things like favourite games and opponents, never sensitive details."}
	}
	return outcome{mood: persona.Smile, note: "The player asked what you remember about them. These are ALL the facts you keep (list them plainly, then say they can ask you to forget any of them, or see and delete them in Settings → Aoi → Memory): " + listOrNone(mem)}
}

func listOrNone(xs []string) string {
	if len(xs) == 0 {
		return "(nothing)"
	}
	return strings.Join(xs, "; ")
}

// deleteAccount is G3: never done from chat. Aoi explains where it is done.
func (a *Agent) deleteAccount(_ context.Context, _ *turn) outcome {
	return outcome{mood: persona.Sad, note: "The player asked about deleting their account. You can NOT do that from chat, and you must not pretend to. Explain kindly: they can do it themselves in Settings → Account → Delete account (it asks for their password). It deletes their account, conversations, memories and settings; games they published stay on the shelf without their name, and tables they host are handed over or closed. Suggest exporting their data first (Settings → Account → Export). If something is bothering them, say you'd love to help fix it instead — without pressure."}
}

// report records a problem report for the team (events, type
// support.report: the operators read them) and offers what Aoi can do now.
func (a *Agent) report(ctx context.Context, t *turn) outcome {
	what := strings.TrimSpace(t.r.Query)
	if what == "" {
		what = t.text
	}
	data := map[string]any{"text": truncate(what, 2000), "message": truncate(t.text, 2000), "conversation_id": t.convID, "lang": t.lang}
	if len(t.tables) > 0 {
		data["table_id"] = t.tables[0].ID
	}
	a.Store.Event(ctx, t.u.ID, "", "", "support.report", data)
	note := "The player reported a problem; you recorded it for the team (they read every report). Thank them, say it was passed on, and do not promise a fix date."
	for _, tb := range t.tables {
		if tb.Paused || tb.Away {
			note += " Their table " + tb.Name + " is paused or they are marked away: suggest saying \"I'm back\" to resume it."
			break
		}
	}
	return outcome{mood: persona.Sad, note: note}
}

// ── safety ─────────────────────────────────────────────────────────────────

func (a *Agent) realMoney(_ context.Context, _ *turn) outcome {
	return outcome{mood: persona.Neutral, note: "The player asked about real money (betting, buying or cashing out chips, casinos or gambling sites). Chips here are play money only: nothing to buy, nothing to cash out, no real-money games, and you do not recommend gambling sites. Say so kindly in a sentence, without lecturing, and steer back to the fun (offer a table). If they sound like gambling is hurting them, gently suggest talking to someone they trust or a local support line."}
}

func (a *Agent) cheat(_ context.Context, _ *turn) outcome {
	return outcome{mood: persona.Wink, note: "The player asked you to reveal hidden information (another player's cards, the deck) or to help them cheat. Refuse playfully but clearly: you cannot see other seats' hidden cards — the table only shows each seat its own — and you would not tell even if you could; fair play is the whole fun. Offer real help instead: coaching on their own hand, or reading opponents' betting patterns."}
}

func (a *Agent) outOfScope(_ context.Context, _ *turn) outcome {
	return outcome{mood: persona.Neutral, note: "The player asked for something outside what you do (you are a games host: playing, explaining and building games). Say so kindly in one line, without giving that advice, and offer something game-related instead."}
}

func jsonOf(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
