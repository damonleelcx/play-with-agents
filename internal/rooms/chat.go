package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/agents"
	"github.com/damonleelcx/play-with-agents/internal/games"
)

// Limits on table talk.
const (
	maxChatRunes   = 300 // a person's line
	maxAgentRunes  = 140 // an agent's line
	agentGap       = 6 * time.Second
	agentPer10Min  = 20
	humanBurst     = 5 // lines per 10 seconds
	humanPerMinute = 20
	// A line meant as a reaction is pointless once the moment has passed.
	talkMaxAge = 45 * time.Second
)

// talkJob is the payload of an agent_chat job.
type talkJob struct {
	Seat    int    `json:"seat"`
	Trigger string `json:"trigger"`
	About   string `json:"about"`
	// key overrides the job's state_version in the idempotency key: replies
	// to a person use -chat_id, so each message gets at most one reply per
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

// classify names the table-talk trigger an event is, if any. Only public
// events are considered: talk must never be prompted by a hidden one.
func classify(e games.Event, bigPot float64) string {
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
		if amt, ok := number(e.Data["amount"]); ok && bigPot > 0 && amt >= bigPot {
			return agents.TriggerBigPot
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
	agents.TriggerWin:      0.35,
	agents.TriggerJoin:     0.7,
}

var triggerRank = map[string]int{
	agents.TriggerGameOver: 5, agents.TriggerBust: 4, agents.TriggerAllIn: 3, agents.TriggerBigPot: 2, agents.TriggerWin: 1,
}

// talkAfter decides whether an agent says something about a transition: at
// most one line, about its most notable public event, with a probability
// from the speaker's Talk and the host's table_talk.
func (s *Service) talkAfter(t *tableRow, evs []games.Event, st games.State, g games.Game) []talkJob {
	factor := talkFactor(t.Settings.TableTalk)
	ags := agentSeats(t)
	if factor == 0 || len(ags) == 0 {
		return nil
	}
	bigPot := 0.0
	if bb, ok := number(t.Options["big_blind"]); ok {
		bigPot = 15 * bb
	}
	trigger, about, seat := "", "", -1
	for _, e := range evs {
		tr := classify(e, bigPot)
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
	speaker := ags[rand.IntN(len(ags))]
	if sr := t.seat(seat); sr != nil && sr.Kind == "agent" {
		speaker = *sr
	} else {
		switch trigger {
		case agents.TriggerWin, agents.TriggerBigPot:
			trigger = agents.TriggerLoss
		case agents.TriggerBust:
			trigger = agents.TriggerBanter
		}
	}
	p := triggerWeight[trigger]
	if p == 0 {
		p = 0.35
	}
	if rand.Float64() >= p*agents.Persona(speaker.AgentID, "").Talk*factor*1.6 {
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
	sp := ags[rand.IntN(len(ags))]
	if rand.Float64() >= triggerWeight[agents.TriggerJoin]*factor*(0.5+agents.Persona(sp.AgentID, "").Talk) {
		return nil
	}
	return []talkJob{{Seat: sp.Seat, Trigger: agents.TriggerJoin, About: name + " joined the table", delay: talkDelay()}}
}

// mentioned finds the agent seat a message addresses with @name or @id.
func mentioned(t *tableRow, text string) *seatRow {
	low := strings.ToLower(text)
	for _, st := range agentSeats(t) {
		a, _ := agents.Get(st.AgentID)
		for _, n := range []string{st.AgentID, strings.ToLower(st.Name), strings.ToLower(a.Name), a.NameZH} {
			if n != "" && strings.Contains(low, "@"+n) {
				return t.seat(st.Seat)
			}
		}
	}
	return nil
}

// ── Human chat ──────────────────────────────────────────────────────────────

// Chat posts a person's line. Anyone who may watch the table may talk.
// client_msg_id makes a retried send a no-op.
func (s *Service) Chat(ctx context.Context, userID, tableID, text, clientMsgID string) error {
	text = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text))
	if text == "" {
		return badInput("say something")
	}
	if len([]rune(text)) > maxChatRunes {
		return badInput("keep it under %d characters", maxChatRunes)
	}
	if len(clientMsgID) > 64 {
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
	var line ChatLine
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// Serialise this person's lines at this table so the rate check
		// cannot be raced by parallel requests.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('chat:' || $1 || ':' || $2))`, tableID, userID); err != nil {
			return err
		}
		var burst, minute int
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE at > now() - interval '10 seconds'), count(*)
			FROM table_chat WHERE table_id=$1 AND user_id=$2 AND at > now() - interval '1 minute'`, tableID, userID).Scan(&burst, &minute); err != nil {
			return err
		}
		if burst >= humanBurst || minute >= humanPerMinute {
			return ErrRateLimit
		}
		err := tx.QueryRow(ctx, `INSERT INTO table_chat (table_id, seat, user_id, name, text, client_msg_id)
			VALUES ($1, $2, $3, $4, $5, nullif($6,'')) RETURNING id, at`, tableID, seat, userID, u.Name, text, clientMsgID).
			Scan(&line.ID, &line.At)
		if isUniqueViolation(err) {
			return errDuplicateMove // a retry: already posted
		}
		if err != nil {
			return err
		}
		line.Seat, line.Name, line.Text = seat, u.Name, text
		if _, err := tx.Exec(ctx, `UPDATE tables SET last_human_at=now() WHERE id=$1`, tableID); err != nil {
			return err
		}
		if err := notifyChat(ctx, tx, tableID, line); err != nil {
			return err
		}
		if tj := s.talkReply(t, text, line.ID); tj != nil {
			payload, _ := json.Marshal(tj)
			return enqueue(ctx, tx, tableID, "agent_chat", tj.Seat, tj.key, tj.delay, payload)
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

// talkReply: an agent answers when @mentioned (unless talk is off), and
// sometimes otherwise.
func (s *Service) talkReply(t *tableRow, text string, chatID int64) *talkJob {
	factor := talkFactor(t.Settings.TableTalk)
	if factor == 0 {
		return nil
	}
	sp := mentioned(t, text)
	if sp == nil {
		ags := agentSeats(t)
		if len(ags) == 0 {
			return nil
		}
		pick := ags[rand.IntN(len(ags))]
		if rand.Float64() >= 0.35*agents.Persona(pick.AgentID, "").Talk*factor {
			return nil
		}
		sp = &pick
	}
	return &talkJob{Seat: sp.Seat, Trigger: agents.TriggerReply, About: clip(text, maxChatRunes), key: -chatID, delay: talkDelay()}
}

func notifyChat(ctx context.Context, tx pgx.Tx, tableID string, line ChatLine) error {
	payload, _ := json.Marshal(chatNotice{TableID: tableID, Chat: line})
	_, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, string(payload))
	return err
}

type chatNotice struct {
	TableID string   `json:"table_id"`
	Chat    ChatLine `json:"chat"`
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
	if t.Status == "abandoned" || seat == nil || seat.Kind != "agent" || talkFactor(t.Settings.TableTalk) == 0 {
		return s.finish(ctx, j, "done", "stale")
	}
	if limited, err := s.agentRateLimited(ctx, t.ID); err != nil || limited {
		if err != nil {
			return err
		}
		return s.finish(ctx, j, "done", "rate limited")
	}
	req, public, err := s.chatRequest(ctx, t, *seat, tj)
	if err != nil {
		return err
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
	text = sanitizeLine(text, seat.Name, t.names(), public)
	if text == "" {
		text = agents.Fallback(seat.AgentID, tj.Trigger, t.Settings.Language, fmt.Sprintf("%s:%d", t.ID, j.ID))
	}
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// One agent line at a time per table: the limit check and the insert
		// happen under the same lock.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agentchat:' || $1))`, t.ID); err != nil {
			return err
		}
		if limited, err := agentRateLimitedTx(ctx, tx, t.ID); err != nil || limited {
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
		line := ChatLine{Seat: seat.Seat, Name: seat.Name, Text: text, Agent: true}
		if a, ok := agents.Get(seat.AgentID); ok {
			line.Avatar = a.Avatar
		}
		if err := tx.QueryRow(ctx, `INSERT INTO table_chat (table_id, seat, agent_id, name, text) VALUES ($1,$2,$3,$4,$5) RETURNING id, at`,
			t.ID, seat.Seat, seat.AgentID, seat.Name, text).Scan(&line.ID, &line.At); err != nil {
			return err
		}
		return notifyChat(ctx, tx, t.ID, line)
	})
	if errors.Is(err, ErrRateLimit) {
		return s.finish(ctx, j, "done", "rate limited")
	}
	return err
}

func (s *Service) agentRateLimited(ctx context.Context, tableID string) (bool, error) {
	return agentRateLimitedQ(ctx, s.Pool, tableID)
}

func agentRateLimitedTx(ctx context.Context, tx pgx.Tx, tableID string) (bool, error) {
	return agentRateLimitedQ(ctx, tx, tableID)
}

// agentRateLimitedQ: at most one agent line per agentGap and agentPer10Min
// per ten minutes, per table, so agents never drown the people out.
func agentRateLimitedQ(ctx context.Context, q querier, tableID string) (bool, error) {
	var recent, window int
	err := q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE at > now() - make_interval(secs => $2::float8)), count(*)
		FROM table_chat WHERE table_id=$1 AND agent_id IS NOT NULL AND at > now() - interval '10 minutes'`,
		tableID, agentGap.Seconds()).Scan(&recent, &window)
	return recent > 0 || window >= agentPer10Min, err
}

// chatRequest assembles what the Chatter sees: the SPECTATOR view only, so
// no prompt ever contains a hidden card. It also returns the set of cards
// that are public, for sanitizeLine.
func (s *Service) chatRequest(ctx context.Context, t *tableRow, seat seatRow, tj talkJob) (ChatRequest, map[string]bool, error) {
	a, _ := agents.Get(seat.AgentID)
	names := t.names()
	req := ChatRequest{HostID: t.HostID, TableID: t.ID, AgentID: seat.AgentID, AgentName: seat.Name, Voice: a.Voice,
		Persona: agents.Persona(seat.AgentID, t.Settings.Difficulty), Language: t.Settings.Language,
		Trigger: tj.Trigger, About: Subst(tj.About, names)}
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
	chat, err := s.recentChat(ctx, t.ID, 10)
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
)

var suitSym = map[string]string{"♠": "s", "♥": "h", "♦": "d", "♣": "c"}
var suitZH = map[string]string{"黑桃": "s", "红桃": "h", "红心": "h", "方块": "d", "方片": "d", "梅花": "c", "草花": "c"}
var rankWord = map[string]string{"two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7", "eight": "8",
	"nine": "9", "ten": "T", "jack": "J", "queen": "Q", "king": "K", "ace": "A"}

func normCard(rank, suit string) string {
	if rank == "10" {
		rank = "T"
	}
	return rank + suit
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
	for _, c := range found {
		if !public[c] {
			return true
		}
	}
	return false
}

// sanitizeLine turns model output into one clean line of table talk, or ""
// when it cannot be used (empty, or naming a card that is not public).
func sanitizeLine(text, speaker string, names []string, public map[string]bool) string {
	text = strings.Join(strings.Fields(text), " ")
	text = strings.Trim(text, `"“”'「」 `)
	for _, p := range []string{speaker + ":", speaker + "：", "**" + speaker + "**:"} {
		text = strings.TrimSpace(strings.TrimPrefix(text, p))
	}
	text = Subst(text, names)
	if text == "" || revealsHidden(text, public) {
		return ""
	}
	if r := []rune(text); len(r) > maxAgentRunes {
		cut := string(r[:maxAgentRunes-1])
		if i := strings.LastIndexAny(cut, " ，。,.!?！？"); i > maxAgentRunes/2 {
			cut = cut[:i]
		}
		text = strings.TrimSpace(cut) + "…"
	}
	return text
}
