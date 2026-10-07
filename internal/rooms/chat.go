package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/damonleelcx/play-with-agents/internal/agents"
	"github.com/damonleelcx/play-with-agents/internal/games"
)

// Limits on table talk.
const (
	maxChatRunes  = 500 // a person's line
	maxAgentRunes = 140 // an agent's unprompted line
	maxReplyRunes = 220 // an agent answering someone who addressed it
	// agentGap: an unprompted line waits this long after any agent line.
	agentGap = 4 * time.Second
	// Lines written because someone addressed the agent have their own,
	// generous table budget; unprompted lines (reactions to play,
	// greetings, chiming in, agent-to-agent banter) the tight one, and each
	// agent a cooldown that grows the quieter its persona is (agentCooldown).
	agentDirectPer10Min     = 40
	agentUnpromptedPer10Min = 8
	agentCooldownBase       = 45 * time.Second
	// agentStreakMax: conversation lines (chiming in, banter) are never
	// written once this many agent lines followed the last person's line.
	agentStreakMax = 2
	// directPerLine: at most this many agents answer one person's line.
	directPerLine = 3
	// Lines remembered per agent to avoid repeating itself.
	ownLines = 5
	// chatContext: chat lines an agent sees before writing.
	chatContext = 12
	// A person may post humanPerWindow lines per humanWindow at a table.
	humanPerWindow = 20
	humanWindow    = 30 * time.Second
	// A line meant as a reaction is pointless once the moment has passed.
	talkMaxAge = 45 * time.Second
)

// talkJob is the payload of an agent_chat job.
type talkJob struct {
	Seat    int    `json:"seat"`
	Trigger string `json:"trigger"`
	About   string `json:"about"`
	// Mention: someone addressed this agent (by name in any language, @, a
	// reply to its line, or a question to everyone). Such replies skip the
	// per-agent cooldown, the unprompted budget and table_talk "off".
	Mention bool `json:"mention,omitempty"`
	// Chain: a conversation line nobody asked for (chiming in on people's
	// chat, answering another agent); dropped once agentStreakMax agent
	// lines followed the last person's line, so agents never loop.
	Chain bool `json:"chain,omitempty"`
	// From is who said About; Lang the language to answer in; ReplyTo the
	// line answered.
	From    string `json:"from,omitempty"`
	Lang    string `json:"lang,omitempty"`
	ReplyTo int64  `json:"reply_to,omitempty"`
	// key overrides the job's state_version in the idempotency key: replies
	// to a line use -chat_id, so each line gets at most one reply per
	// agent, independent of the table version.
	key   int64
	delay time.Duration
}

// talkFactor scales the chance of unprompted talk by the host's setting.
func talkFactor(setting string) float64 {
	switch setting {
	case "off":
		return 0
	case "quiet":
		return 0.2
	}
	return 1
}

func agentSeats(t *tableRow) []seatRow {
	var out []seatRow
	for _, st := range t.Seats {
		if st.Kind == "agent" {
			out = append(out, st)
		}
	}
	return out
}

func talkDelay() time.Duration {
	return time.Duration((0.8 + rand.Float64()*1.7) * float64(time.Second))
}

// triggerSmallPot is internal: a pot under smallPotBlinds big blinds. It is
// said out loud as a win (or loss), but only ever at the lowest rate.
const (
	triggerSmallPot = "small_pot"
	smallPotBlinds  = 10
	bigPotBlinds    = 15
)

// classify names the table-talk trigger an event is, if any. Only public
// events are considered: talk must never be prompted by a hidden one. bb is
// the big blind of the hand (0 for games without blinds).
func classify(e games.Event, bb float64) string {
	if len(e.Only) > 0 {
		return ""
	}
	t := strings.ToLower(e.Type)
	switch {
	case strings.Contains(t, "bust"), strings.Contains(t, "eliminat"):
		return agents.TriggerBust
	case strings.Contains(t, "allin"), strings.Contains(t, "all_in"):
		return agents.TriggerAllIn
	case strings.Contains(t, "win"), strings.Contains(t, "won"), strings.Contains(t, "showdown"):
		if amt, ok := number(e.Data["amount"]); ok && bb > 0 {
			switch {
			case amt >= bigPotBlinds*bb:
				return agents.TriggerBigPot
			case amt < smallPotBlinds*bb:
				return triggerSmallPot
			}
		}
		return agents.TriggerWin
	}
	return ""
}

// number reads a numeric event or option value: Go games put ints in
// Data, while anything that went through JSON holds float64.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

var triggerWeight = map[string]float64{
	agents.TriggerGameOver: 0.9,
	agents.TriggerBust:     0.8,
	agents.TriggerAllIn:    0.6,
	agents.TriggerBigPot:   0.55,
	agents.TriggerWin:      0.3,
	agents.TriggerJoin:     0.7,
	triggerSmallPot:        0.04,
}

var triggerRank = map[string]int{
	agents.TriggerGameOver: 6, agents.TriggerBust: 5, agents.TriggerAllIn: 4, agents.TriggerBigPot: 3, agents.TriggerWin: 2,
	triggerSmallPot: 1,
}

// talkiness turns a persona's Talk (0..1) into a rate multiplier. Squared,
// so the quiet agents are rare rather than merely less frequent: Ren (0.2)
// speaks ~20x less often than Mika (0.9), not 4.5x.
func talkiness(talk float64) float64 { return talk * talk }

// agentCooldown is how long an agent stays quiet after its own line:
// 45s for the chattiest, about 2.5 minutes for the quietest.
func agentCooldown(talk float64) time.Duration {
	talk = min(max(talk, 0), 1)
	return time.Duration(float64(agentCooldownBase) * (1 + 3*(1-talk)))
}

// pickSpeaker chooses a bystander to react, weighted by talkiness.
func pickSpeaker(ags []seatRow) seatRow {
	total := 0.0
	for _, a := range ags {
		total += talkiness(agents.Persona(a.AgentID, "").Talk)
	}
	x := rand.Float64() * total
	for _, a := range ags {
		x -= talkiness(agents.Persona(a.AgentID, "").Talk)
		if x < 0 {
			return a
		}
	}
	return ags[len(ags)-1]
}

// bigBlind is the big blind of the hand a transition ended, from the old
// state's public view, else the table options. 0 when the game has none.
func (s *Service) bigBlind(g games.Game, st games.State, options map[string]any) float64 {
	if st != nil {
		if v, err := g.View(st, games.Spectator); err == nil {
			if raw, err := json.Marshal(v.Data); err == nil {
				var d struct {
					BigBlind float64 `json:"big_blind"`
				}
				if json.Unmarshal(raw, &d) == nil && d.BigBlind > 0 {
					return d.BigBlind
				}
			}
		}
	}
	bb, _ := number(options["big_blind"])
	return bb
}

// talkAfter decides whether an agent says something about a transition: at
// most one line, about its most notable public event, with a probability
// from the speaker's Talk and the host's table_talk.
func (s *Service) talkAfter(t *tableRow, evs []games.Event, st games.State, g games.Game) []talkJob {
	factor := talkFactor(t.Settings.TableTalk)
	ags := agentSeats(t)
	if factor == 0 || len(ags) == 0 || t.PausedAt != nil || allAway(t) {
		return nil
	}
	bb := s.bigBlind(g, t.State, t.Options)
	trigger, about, seat := "", "", -1
	for _, e := range evs {
		tr := classify(e, bb)
		if tr != "" && triggerRank[tr] > triggerRank[trigger] {
			trigger, about, seat = tr, e.Text, e.Seat
		}
	}
	if o, _ := g.Outcome(st); o != nil {
		trigger, about, seat = agents.TriggerGameOver, o.Summary, -1
	}
	if trigger == "" {
		return nil
	}
	// The agent the event is about speaks for itself; otherwise a random
	// agent reacts, and first-person lines are reframed for a bystander.
	// The weight is fixed by what happened, before it is reframed below.
	p := triggerWeight[trigger]
	if p == 0 {
		p = 0.3
	}
	if trigger == triggerSmallPot {
		trigger = agents.TriggerWin
	}
	var speaker seatRow
	if sr := t.seat(seat); sr != nil && sr.Kind == "agent" {
		speaker = *sr
	} else {
		speaker = pickSpeaker(ags)
		switch trigger {
		case agents.TriggerWin, agents.TriggerBigPot:
			trigger = agents.TriggerLoss
		case agents.TriggerBust:
			trigger = agents.TriggerBanter
		}
	}
	if rand.Float64() >= p*talkiness(agents.Persona(speaker.AgentID, "").Talk)*factor*1.5 {
		return nil
	}
	return []talkJob{{Seat: speaker.Seat, Trigger: trigger, About: Subst(about, t.names()), delay: talkDelay()}}
}

// talkJoin: someone sat down; an agent may greet them.
func (s *Service) talkJoin(t *tableRow, name string) []talkJob {
	factor := talkFactor(t.Settings.TableTalk)
	ags := agentSeats(t)
	if factor == 0 || len(ags) == 0 {
		return nil
	}
	sp := pickSpeaker(ags)
	if rand.Float64() >= triggerWeight[agents.TriggerJoin]*factor*(0.3+agents.Persona(sp.AgentID, "").Talk) {
		return nil
	}
	return []talkJob{{Seat: sp.Seat, Trigger: agents.TriggerJoin, About: name + " joined the table", delay: talkDelay()}}
}

// ── Human chat ──────────────────────────────────────────────────────────────

// Chat posts a person's line. Anyone who may watch the table may talk.
// client_msg_id makes a retried send a no-op.
func (s *Service) Chat(ctx context.Context, userID, tableID, text, clientMsgID string) error {
	return s.ChatWith(ctx, userID, tableID, ChatInput{Text: text, ClientMsgID: clientMsgID})
}

// ChatWith posts a person's line with its options: a reply to a line, or a
// whisper to another person seated at the table. A whisper is delivered to
// the two of them only, is never shown to agents and never prompts one.
func (s *Service) ChatWith(ctx context.Context, userID, tableID string, in ChatInput) error {
	text := cleanText(in.Text)
	if text == "" {
		return badInput("say something")
	}
	if len([]rune(text)) > maxChatRunes {
		return badInput("keep it under %d characters", maxChatRunes)
	}
	if len(in.ClientMsgID) > 64 {
		return badInput("client_msg_id is too long")
	}
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return err
	}
	ok, err := s.canWatch(ctx, t, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	u, err := loadUser(ctx, s.Pool, userID)
	if err != nil {
		return err
	}
	seat := t.seatOf(userID)
	var to *seatRow
	if in.WhisperSeat != nil {
		to = t.seat(*in.WhisperSeat)
		switch {
		case to == nil || to.Kind == "open":
			return badInput("nobody is in that seat")
		case to.Kind == "agent":
			return badInput("agents can't be whispered to")
		case to.UserID == userID:
			return badInput("you can't whisper to yourself")
		case to.Kind != "human" || to.UserID == "":
			return badInput("you can only whisper to a person at the table")
		}
	}
	// A reply names a line the sender can see; replying to an agent's line
	// addresses that agent.
	replyTo, replySeat := int64(0), -1
	if in.ReplyTo > 0 {
		var agentID string
		var rseat int
		err := s.Pool.QueryRow(ctx, `SELECT seat, coalesce(agent_id,'') FROM table_chat
			WHERE id=$1 AND table_id=$2 AND (whisper_to IS NULL OR whisper_to=$3 OR user_id=$3)`, in.ReplyTo, t.ID, userID).Scan(&rseat, &agentID)
		if err == nil {
			replyTo = in.ReplyTo
			if agentID != "" {
				replySeat = rseat
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	var line ChatLine
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// Serialise this person's lines at this table so the rate check
		// cannot be raced by parallel requests.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('chat:' || $1 || ':' || $2))`, tableID, userID); err != nil {
			return err
		}
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM table_chat WHERE table_id=$1 AND user_id=$2
			AND at > now() - make_interval(secs => $3::float8)`, tableID, userID, humanWindow.Seconds()).Scan(&recent); err != nil {
			return err
		}
		if recent >= humanPerWindow {
			return ErrRateLimit
		}
		var whisperTo *string
		if to != nil {
			whisperTo = &to.UserID
		}
		err := tx.QueryRow(ctx, `INSERT INTO table_chat (table_id, seat, user_id, name, text, client_msg_id, reply_to, whisper_to)
			VALUES ($1, $2, $3, $4, $5, nullif($6,''), nullif($7,0), $8) RETURNING id, at`,
			tableID, seat, userID, u.Name, text, in.ClientMsgID, replyTo, whisperTo).Scan(&line.ID, &line.At)
		if isUniqueViolation(err) {
			return errDuplicateMove // a retry: already posted
		}
		if err != nil {
			return err
		}
		line.Seat, line.Name, line.Text, line.ReplyTo = seat, u.Name, text, replyTo
		if _, err := tx.Exec(ctx, `UPDATE tables SET last_human_at=now() WHERE id=$1`, tableID); err != nil {
			return err
		}
		if to != nil {
			line.Whisper, line.To = true, to.Name
			ts := to.Seat
			line.ToSeat = &ts
			return notifyWhisper(ctx, tx, tableID, line, []string{userID, to.UserID})
		}
		if err := notifyChat(ctx, tx, tableID, line); err != nil {
			return err
		}
		for _, tj := range s.talkReplies(t, userID, u.Name, text, line.ID, replySeat) {
			payload, _ := json.Marshal(tj)
			if err := enqueue(ctx, tx, tableID, "agent_chat", tj.Seat, tj.key, tj.delay, payload); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errDuplicateMove) {
		return nil
	}
	if err == nil {
		s.poke()
	}
	return err
}

// talkReplies decides which agents answer a person's public line:
//
//   - every agent it addresses (by @name or name in any table language, or
//     a reply to that agent's line) answers, up to directPerLine;
//   - a line to everyone ("everyone", "大家", "여러분", "みんな", ...) gets
//     an answer from one or two agents, the chattier more likely;
//   - so does a question when no other person is seated to answer it;
//   - otherwise an agent may chime in, by its talkiness and table_talk.
//
// Addressed agents answer even when table_talk is "off".
func (s *Service) talkReplies(t *tableRow, userID, from, text string, chatID int64, replySeat int) []talkJob {
	ags := agentSeats(t)
	if len(ags) == 0 {
		return nil
	}
	lang := detectLang(text, t.Settings.Language)
	direct := addressedAgents(t, text)
	if replySeat >= 0 && !slices.Contains(direct, replySeat) {
		direct = append([]int{replySeat}, direct...)
	}
	if len(direct) == 0 {
		switch {
		case toEveryone(text):
			direct = pickSpeakers(ags, min(2, len(ags)))
		case isQuestion(text) && othersSeated(t, userID) == 0:
			direct = pickSpeakers(ags, 1)
		}
	}
	about := clip(text, maxChatRunes)
	if len(direct) > 0 {
		var out []talkJob
		delay := directDelay()
		for _, seat := range direct[:min(len(direct), directPerLine)] {
			out = append(out, talkJob{Seat: seat, Trigger: agents.TriggerReply, About: about, Mention: true,
				From: from, Lang: lang, ReplyTo: chatID, key: -chatID, delay: delay})
			// The next agent answers after this one has had its say.
			delay += 2500*time.Millisecond + time.Duration(rand.Float64()*1500)*time.Millisecond
		}
		return out
	}
	factor := talkFactor(t.Settings.TableTalk)
	if factor == 0 {
		return nil
	}
	pick := ags[rand.IntN(len(ags))]
	if rand.Float64() >= 0.35*talkiness(agents.Persona(pick.AgentID, "").Talk)*factor {
		return nil
	}
	return []talkJob{{Seat: pick.Seat, Trigger: agents.TriggerReply, About: about, Chain: true,
		From: from, Lang: lang, ReplyTo: chatID, key: -chatID, delay: talkDelay()}}
}

// directDelay: an addressed agent starts "typing" almost at once.
func directDelay() time.Duration {
	return time.Duration((0.2 + rand.Float64()*0.4) * float64(time.Second))
}

// othersSeated counts the people seated at the table other than userID.
func othersSeated(t *tableRow, userID string) int {
	n := 0
	for _, st := range t.Seats {
		if st.Kind == "human" && st.UserID != userID {
			n++
		}
	}
	return n
}

// pickSpeakers chooses up to n distinct agent seats, weighted by talkiness
// (never zero, so a table of quiet agents still answers).
func pickSpeakers(ags []seatRow, n int) []int {
	pool := slices.Clone(ags)
	var out []int
	for len(out) < n && len(pool) > 0 {
		total := 0.0
		for _, a := range pool {
			total += 0.05 + talkiness(agents.Persona(a.AgentID, "").Talk)
		}
		x := rand.Float64() * total
		i := 0
		for ; i < len(pool)-1; i++ {
			x -= 0.05 + talkiness(agents.Persona(pool[i].AgentID, "").Talk)
			if x < 0 {
				break
			}
		}
		out = append(out, pool[i].Seat)
		pool = slices.Delete(pool, i, i+1)
	}
	return out
}

type chatNotice struct {
	TableID string    `json:"table_id"`
	Chat    *ChatLine `json:"chat,omitempty"`
	// Whisper is a private line, under its own key so that a pod running an
	// older build (which fans every "chat" out to the whole table) ignores
	// it. Audience is who may receive it (or a reaction on it).
	Whisper  *ChatLine    `json:"whisper,omitempty"`
	Audience []string     `json:"aud,omitempty"`
	Typing   *Typing      `json:"typing,omitempty"`
	From     string       `json:"from,omitempty"` // the person typing: not echoed back to them
	Reaction *ReactionSet `json:"reaction,omitempty"`
	Presence bool         `json:"presence,omitempty"`
}

func notifyChat(ctx context.Context, q execer, tableID string, line ChatLine) error {
	return notify(ctx, q, chatNotice{TableID: tableID, Chat: &line})
}

func notifyWhisper(ctx context.Context, q execer, tableID string, line ChatLine, audience []string) error {
	return notify(ctx, q, chatNotice{TableID: tableID, Whisper: &line, Audience: audience})
}

// execer is a pool or a transaction: a notice sent in a transaction is
// delivered on commit.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func notify(ctx context.Context, q execer, n chatNotice) error {
	payload, _ := json.Marshal(n)
	_, err := q.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, string(payload))
	return err
}

// ── Agent chat jobs ─────────────────────────────────────────────────────────

func (s *Service) chatJob(ctx context.Context, j *job) error {
	var tj talkJob
	_ = json.Unmarshal(j.Payload, &tj)
	if time.Since(j.CreatedAt) > talkMaxAge+agentGap {
		return s.finish(ctx, j, "done", "moment passed")
	}
	t, err := loadTable(ctx, s.Pool, j.TableID, false)
	if errors.Is(err, ErrNotFound) {
		return s.finish(ctx, j, "done", "table gone")
	}
	if err != nil {
		return err
	}
	seat := t.seat(j.Seat)
	direct := tj.Mention
	if t.Status == "abandoned" || seat == nil || seat.Kind != "agent" || (talkFactor(t.Settings.TableTalk) == 0 && !direct) {
		return s.finish(ctx, j, "done", "stale")
	}
	// Nobody is there to hear it: no model call for a paused or all-away
	// table, unless someone just spoke to the agent.
	if t.Status == "playing" && !direct && (t.PausedAt != nil || allAway(t)) {
		return s.finish(ctx, j, "done", "nobody listening")
	}
	if limited, err := s.agentRateLimited(ctx, t.ID, direct); err != nil || limited {
		if err != nil {
			return err
		}
		return s.finish(ctx, j, "done", "rate limited")
	}
	if tj.Chain && !direct {
		if n, err := agentStreak(ctx, s.Pool, t.ID); err != nil || n >= agentStreakMax {
			if err != nil {
				return err
			}
			return s.finish(ctx, j, "done", "enough agent talk")
		}
	}
	own, last, err := s.agentHistory(ctx, t.ID, seat.Seat)
	if err != nil {
		return err
	}
	if !direct && !last.IsZero() && time.Since(last) < agentCooldown(agents.Persona(seat.AgentID, "").Talk) {
		return s.finish(ctx, j, "done", "cooling down")
	}
	req, public, err := s.chatRequest(ctx, t, *seat, tj)
	if err != nil {
		return err
	}
	req.Own = own
	// The table sees "Mika is typing…" while the model writes.
	typing := Typing{Seat: seat.Seat, Name: seat.Name, Agent: true}
	_ = notify(ctx, s.Pool, chatNotice{TableID: t.ID, Typing: &typing})
	began := time.Now()
	stopTyping := func() {
		stop := typing
		stop.Stop = true
		_ = notify(context.WithoutCancel(ctx), s.Pool, chatNotice{TableID: t.ID, Typing: &stop})
	}
	text := ""
	if s.Chatter != nil {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		text, err = s.Chatter.Line(cctx, req)
		cancel()
		if err != nil {
			text = ""
		}
	}
	limit := maxAgentRunes
	if direct {
		limit = maxReplyRunes
	}
	text = sanitizeLine(text, seat.Name, t.names(), public, limit)
	if text != "" && repeatsOwn(text, own) {
		// Saying nothing beats a catchphrase on loop; someone who asked
		// still gets an answer, from the canned lines.
		if !direct {
			stopTyping()
			return s.finish(ctx, j, "done", "repetitive")
		}
		text = ""
	}
	if text == "" {
		text = agents.Fallback(seat.AgentID, tj.Trigger, req.Language, fmt.Sprintf("%s:%d", t.ID, j.ID))
		if repeatsOwn(text, own) {
			stopTyping()
			return s.finish(ctx, j, "done", "repetitive")
		}
	}
	// A reply that lands the instant it is asked for reads as a machine:
	// keep the typing indicator up for a moment that fits the line.
	if wait := s.typeTime(len([]rune(text))) - time.Since(began); wait > 0 {
		sleepCtx(ctx, wait)
	}
	var follow *talkJob
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// One agent line at a time per table: the limit check and the insert
		// happen under the same lock.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agentchat:' || $1))`, t.ID); err != nil {
			return err
		}
		if limited, err := agentRateLimitedTx(ctx, tx, t.ID, direct); err != nil || limited {
			if err == nil {
				err = ErrRateLimit
			}
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE table_jobs SET status='done', result='said', finished_at=now(), lease_owner=NULL, lease_expires_at=NULL
			WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, j.ID, j.Epoch)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrLeaseLost
		}
		line := ChatLine{Seat: seat.Seat, Name: seat.Name, Text: text, Agent: true, ReplyTo: tj.ReplyTo}
		if a, ok := agents.Get(seat.AgentID); ok {
			line.Avatar = a.Avatar
		}
		if err := tx.QueryRow(ctx, `INSERT INTO table_chat (table_id, seat, agent_id, name, text, reply_to, direct)
			VALUES ($1,$2,$3,$4,$5,nullif($6,0),$7) RETURNING id, at`,
			t.ID, seat.Seat, seat.AgentID, seat.Name, text, tj.ReplyTo, direct).Scan(&line.ID, &line.At); err != nil {
			return err
		}
		if err := notifyChat(ctx, tx, t.ID, line); err != nil {
			return err
		}
		// Another agent may answer this one, while the streak allows.
		streak, err := agentStreak(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if follow = banterAfter(t, *seat, line, req.Language, streak); follow != nil {
			payload, _ := json.Marshal(follow)
			return enqueue(ctx, tx, t.ID, "agent_chat", follow.Seat, follow.key, follow.delay, payload)
		}
		return nil
	})
	if errors.Is(err, ErrRateLimit) {
		stopTyping()
		return s.finish(ctx, j, "done", "rate limited")
	}
	if err != nil {
		stopTyping()
	}
	if err == nil && follow != nil {
		s.poke()
	}
	return err
}

// typeTime is how long an agent "types" a line of n runes, model time
// included: about 1 to 2.5 seconds.
func (s *Service) typeTime(n int) time.Duration {
	if s.TypeTime != nil {
		return s.TypeTime(n)
	}
	return min(time.Second+time.Duration(n)*12*time.Millisecond, 2500*time.Millisecond)
}

// banterAfter: another agent may answer an agent's line, more likely when
// it was named in it. Bounded by the streak (agentStreakMax agent lines
// after the last person's line) and the unprompted budget, so two agents
// never talk to each other for long, and never at a table set to "off".
func banterAfter(t *tableRow, speaker seatRow, line ChatLine, lang string, streak int) *talkJob {
	factor := talkFactor(t.Settings.TableTalk)
	if factor == 0 || streak >= agentStreakMax || (t.Status == "playing" && (t.PausedAt != nil || allAway(t))) {
		return nil
	}
	var others []seatRow
	for _, a := range agentSeats(t) {
		if a.Seat != speaker.Seat {
			others = append(others, a)
		}
	}
	if len(others) == 0 {
		return nil
	}
	var pick seatRow
	p := 0.0
	if named := addressedAgents(t, line.Text); len(named) > 0 && named[0] != speaker.Seat {
		pick, p = *t.seat(named[0]), 0.6
	} else {
		pick = others[rand.IntN(len(others))]
		p = 0.3 * talkiness(agents.Persona(pick.AgentID, "").Talk)
	}
	if rand.Float64() >= p*factor {
		return nil
	}
	return &talkJob{Seat: pick.Seat, Trigger: agents.TriggerReply, About: line.Text, Chain: true, From: speaker.Name,
		Lang: lang, ReplyTo: line.ID, key: -line.ID, delay: agentGap + time.Duration((0.5+rand.Float64()*2.5)*float64(time.Second))}
}

// agentStreak counts the agent lines since the last person's public line
// (within the last ten minutes).
func agentStreak(ctx context.Context, q querier, tableID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM table_chat WHERE table_id=$1 AND agent_id IS NOT NULL
		AND at > now() - interval '10 minutes'
		AND id > coalesce((SELECT max(id) FROM table_chat WHERE table_id=$1 AND agent_id IS NULL AND whisper_to IS NULL), 0)`, tableID).Scan(&n)
	return n, err
}

func (s *Service) agentRateLimited(ctx context.Context, tableID string, direct bool) (bool, error) {
	return agentRateLimitedQ(ctx, s.Pool, tableID, direct)
}

func agentRateLimitedTx(ctx context.Context, tx pgx.Tx, tableID string, direct bool) (bool, error) {
	return agentRateLimitedQ(ctx, tx, tableID, direct)
}

// agentRateLimitedQ: an addressed agent answers within agentDirectPer10Min
// answers per table per ten minutes. An unprompted line waits agentGap
// after any agent line and fits in agentUnpromptedPer10Min, so agents
// never drown the people out.
func agentRateLimitedQ(ctx context.Context, q querier, tableID string, direct bool) (bool, error) {
	var recent, directs, unprompted int
	err := q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE at > now() - make_interval(secs => $2::float8)),
			count(*) FILTER (WHERE direct), count(*) FILTER (WHERE NOT direct)
		FROM table_chat WHERE table_id=$1 AND agent_id IS NOT NULL AND at > now() - interval '10 minutes'`,
		tableID, agentGap.Seconds()).Scan(&recent, &directs, &unprompted)
	if direct {
		return directs >= agentDirectPer10Min, err
	}
	return recent > 0 || unprompted >= agentUnpromptedPer10Min, err
}

// agentHistory is the agent seat's last ownLines lines at the table, oldest
// first, and when it last spoke (zero if never).
func (s *Service) agentHistory(ctx context.Context, tableID string, seat int) ([]string, time.Time, error) {
	rows, err := s.Pool.Query(ctx, `SELECT text, at FROM table_chat WHERE table_id=$1 AND seat=$2 AND agent_id IS NOT NULL
		ORDER BY id DESC LIMIT $3`, tableID, seat, ownLines)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	var lines []string
	var last time.Time
	for rows.Next() {
		var text string
		var at time.Time
		if err := rows.Scan(&text, &at); err != nil {
			return nil, time.Time{}, err
		}
		if last.IsZero() {
			last = at
		}
		lines = append(lines, text)
	}
	reverse(lines)
	return lines, last, rows.Err()
}

// chatRequest assembles what the Chatter sees: the SPECTATOR view only, so
// no prompt ever contains a hidden card. It also returns the set of cards
// that are public, for sanitizeLine.
func (s *Service) chatRequest(ctx context.Context, t *tableRow, seat seatRow, tj talkJob) (ChatRequest, map[string]bool, error) {
	a, _ := agents.Get(seat.AgentID)
	names := t.names()
	req := ChatRequest{HostID: t.HostID, TableID: t.ID, AgentID: seat.AgentID, AgentName: seat.Name, Voice: a.Voice,
		Persona: agents.Persona(seat.AgentID, t.Settings.Difficulty), Language: t.Settings.Language,
		Trigger: tj.Trigger, About: Subst(tj.About, names), Direct: tj.Mention, From: tj.From}
	if tj.Lang != "" {
		req.Language = tj.Lang
	}
	public := map[string]bool{}
	var b strings.Builder
	fmt.Fprintf(&b, "Game: %s. Status: %s.\nSeats:", t.GameName, t.Status)
	for _, st := range t.Seats {
		kind := st.Kind
		if st.Kind == "agent" {
			kind = "AI"
		}
		fmt.Fprintf(&b, " %s (%s)%s;", names[st.Seat], kind, map[bool]string{true: " ← you", false: ""}[st.Seat == seat.Seat])
	}
	if t.State != nil {
		g, err := s.Game(ctx, t.GameID, t.GameVersion)
		if err != nil {
			return req, nil, err
		}
		v, err := g.View(t.State, games.Spectator)
		if err != nil {
			return req, nil, err
		}
		sv, err := substView(v, names)
		if err != nil {
			return req, nil, err
		}
		collectCards(sv.Data, public)
		raw, _ := json.Marshal(sv.Data)
		fmt.Fprintf(&b, "\nPublic table: %s\n%s", sv.Status, clip(string(raw), 1500))
	}
	log, err := s.log(ctx, t.ID, games.Spectator, names, 8)
	if err != nil {
		return req, nil, err
	}
	if len(log) > 0 {
		b.WriteString("\nRecent play:")
		for _, e := range log {
			if e.Text != "" {
				b.WriteString("\n- " + e.Text)
			}
		}
	}
	req.Table = b.String()
	// Public lines only: a whisper never reaches an agent.
	chat, err := s.recentChat(ctx, t, "", chatContext)
	if err != nil {
		return req, nil, err
	}
	req.Recent = chat
	return req, public, nil
}

// ── Sanitising agent lines ──────────────────────────────────────────────────

var (
	cardCode = regexp.MustCompile(`\b(10|[2-9TJQKA])([shdc])\b`)
	cardSuit = regexp.MustCompile(`(10|[2-9TJQKA])\s?([♠♥♦♣])|([♠♥♦♣])\s?(10|[2-9TJQKA])`)
	cardWord = regexp.MustCompile(`(?i)\b(two|three|four|five|six|seven|eight|nine|ten|jack|queen|king|ace)s? of (spade|heart|diamond|club)s?\b`)
	cardZH   = regexp.MustCompile(`(黑桃|红桃|红心|方块|方片|梅花|草花)\s?(10|[2-9JQKA])`)
	// Japanese and Korean: "スペードのA", "ハートのエース", "스페이드 A", "하트 에이스".
	cardJK = regexp.MustCompile(`(スペード|ハート|ダイヤモンド|ダイヤ|クラブ|스페이드|하트|다이아몬드|다이아|클로버|클럽)\s?の?\s?(10|[2-9JQKA]|エース|キング|クイーン|ジャック|에이스|킹|퀸|잭)`)
)

var suitSym = map[string]string{"♠": "s", "♥": "h", "♦": "d", "♣": "c"}
var suitZH = map[string]string{"黑桃": "s", "红桃": "h", "红心": "h", "方块": "d", "方片": "d", "梅花": "c", "草花": "c"}
var suitJK = map[string]string{"スペード": "s", "ハート": "h", "ダイヤモンド": "d", "ダイヤ": "d", "クラブ": "c",
	"스페이드": "s", "하트": "h", "다이아몬드": "d", "다이아": "d", "클로버": "c", "클럽": "c"}
var rankJK = map[string]string{"エース": "A", "キング": "K", "クイーン": "Q", "ジャック": "J", "에이스": "A", "킹": "K", "퀸": "Q", "잭": "J"}
var rankWord = map[string]string{"two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7", "eight": "8",
	"nine": "9", "ten": "T", "jack": "J", "queen": "Q", "king": "K", "ace": "A"}

func normCard(rank, suit string) string {
	if rank == "10" {
		rank = "T"
	}
	return rank + suit
}

// handClaim catches a line that hints at hidden cards without naming one
// ("I've got pocket kings", "my cards are...", "我有一对A", "私の手札は"):
// even an invented hint is misleading table talk, and the model never knows
// anyone's hidden cards anyway.
var handClaim = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(pocket|holding|holds|hold|got|has|have|with)\s+(a\s+)?(pair of\s+)?(aces|kings|queens|jacks|tens|nines|eights|sevens|sixes|fives|fours|threes|deuces|rockets|cowboys|ladies|hooks|the nuts|nut flush|a flush|a straight|a set|trips|quads|full house)\b`),
	regexp.MustCompile(`(?i)\b(my|his|her|their|your)\s+(hole\s+)?(cards|hand)\s+(is|are|was|were)\b`),
	regexp.MustCompile(`(我|俺|本小姐|老子|本船长|他|她|你)(手里|手上|的底牌|的牌|有|拿着|拿了|握着)[^。！？!?]{0,6}(一对|两张|对子|同花|顺子|葫芦|三条|四条|口袋|[AKQJ]{2}\b)`),
	regexp.MustCompile(`(私|俺|僕|あたし|わたし)の(手札|ハンド|カード)は`),
	regexp.MustCompile(`(내|제)\s?(패|카드|핸드)는`),
}

func claimsHand(text string) bool {
	for _, re := range handClaim {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// collectCards gathers every card code visible in a public view.
func collectCards(v any, into map[string]bool) {
	switch x := v.(type) {
	case string:
		if m := cardCode.FindStringSubmatch(x); m != nil && m[0] == x {
			into[normCard(m[1], m[2])] = true
		}
	case []any:
		for _, e := range x {
			collectCards(e, into)
		}
	case map[string]any:
		for _, e := range x {
			collectCards(e, into)
		}
	}
}

// revealsHidden reports whether text names a card that is not public. The
// Chatter never sees hidden cards, but a model can invent one ("I have
// the Ace of spades"), and an invented reveal is still misleading table
// talk. "Ah" and "As" are English words, so as card codes they only count
// next to another card.
func revealsHidden(text string, public map[string]bool) bool {
	var found []string
	codes := cardCode.FindAllStringSubmatchIndex(text, -1)
	for _, m := range codes {
		tok := text[m[0]:m[1]]
		if (tok == "Ah" || tok == "As") && len(codes) < 2 {
			continue
		}
		found = append(found, normCard(text[m[2]:m[3]], text[m[4]:m[5]]))
	}
	for _, m := range cardSuit.FindAllStringSubmatch(text, -1) {
		if m[1] != "" {
			found = append(found, normCard(m[1], suitSym[m[2]]))
		} else {
			found = append(found, normCard(m[4], suitSym[m[3]]))
		}
	}
	for _, m := range cardWord.FindAllStringSubmatch(text, -1) {
		found = append(found, rankWord[strings.ToLower(m[1])]+strings.ToLower(m[2][:1]))
	}
	for _, m := range cardZH.FindAllStringSubmatch(text, -1) {
		found = append(found, normCard(m[2], suitZH[m[1]]))
	}
	for _, m := range cardJK.FindAllStringSubmatch(text, -1) {
		rank := m[2]
		if r, ok := rankJK[rank]; ok {
			rank = r
		}
		found = append(found, normCard(rank, suitJK[m[1]]))
	}
	for _, c := range found {
		if !public[c] {
			return true
		}
	}
	return false
}

// sanitizeLine turns model output into one clean line of table talk, or ""
// when it cannot be used (empty, or naming a card that is not public).
func sanitizeLine(text, speaker string, names []string, public map[string]bool, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	text = strings.Trim(text, `"“”'「」『』 `)
	for _, p := range []string{speaker + ":", speaker + "：", "**" + speaker + "**:"} {
		text = strings.TrimSpace(strings.TrimPrefix(text, p))
	}
	text = Subst(text, names)
	if text == "" || revealsHidden(text, public) || claimsHand(text) {
		return ""
	}
	if r := []rune(text); len(r) > limit {
		cut := string(r[:limit-1])
		if i := strings.LastIndexAny(cut, " ，。、,.!?！？"); i > limit/2 {
			cut = cut[:i]
		}
		text = strings.TrimSpace(cut) + "…"
	}
	return text
}
