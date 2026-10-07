package rooms

// Live table chat: cleaning people's lines, working out who a line
// addresses and in which language, typing indicators, reactions and
// presence (who has the table open right now).

import (
	"context"
	"errors"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/agents"
)

// ── Cleaning ────────────────────────────────────────────────────────────────

// cleanText makes a person's line safe to store and show: control
// characters become spaces, invisible formatting characters (bidi
// overrides, zero-width spaces) are dropped (the zero-width joiner stays:
// emoji sequences need it), invalid UTF-8 is dropped, runs of spaces
// collapse and the ends are trimmed.
func cleanText(text string) string {
	var b strings.Builder
	space := false
	for _, r := range text {
		switch {
		case r == utf8.RuneError:
			continue
		case unicode.IsControl(r) || unicode.IsSpace(r):
			if !space {
				b.WriteRune(' ')
			}
			space = true
			continue
		case r == '‍':
		case unicode.Is(unicode.Cf, r):
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// ── Who a line addresses ────────────────────────────────────────────────────

// agentAliases are the names a person may call an agent by: its id, its
// seat name and its name in every table language, with and without a
// title ("Captain Bram" is also "Bram", "布拉姆船长" also "布拉姆").
func agentAliases(st seatRow) []string {
	a, _ := agents.Get(st.AgentID)
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	names := []string{st.AgentID, st.Name, a.Name, a.NameZH}
	for _, lang := range []string{"en", "zh", "ko", "ja"} {
		names = append(names, a.Localized(lang).Name)
	}
	for _, n := range names {
		add(n)
		short := n
		for _, t := range []string{"Captain ", "captain "} {
			short = strings.TrimPrefix(short, t)
		}
		for _, t := range []string{"船长", "船長", " 선장", "선장"} {
			short = strings.TrimSuffix(short, t)
		}
		add(short)
	}
	return out
}

// addressSuffix: honorifics and particles a one-character name may be
// followed by when someone calls it ("葵さん", "琳你", "렌아").
var addressSuffix = []string{"さん", "ちゃん", "くん", "君", "様", "你", "酱", "姐", "哥", "야", "아", "씨", "님"}

// callsName reports whether low (lower-cased) calls alias: "@alias"
// anywhere; a Latin name as a whole word; a longer CJK/Hangul name
// anywhere; a one-character name only on its own or with an honorific.
func callsName(low, alias string) bool {
	if strings.Contains(low, "@"+alias) {
		return true
	}
	for i := 0; i < len(low); {
		j := strings.Index(low[i:], alias)
		if j < 0 {
			return false
		}
		at := i + j
		end := at + len(alias)
		i = at + 1
		before, _ := utf8.DecodeLastRuneInString(low[:at])
		after, _ := utf8.DecodeRuneInString(low[end:])
		prevOK := at == 0 || !isWordRune(before)
		nextOK := end == len(low) || !isWordRune(after)
		if isASCII(alias) {
			if prevOK && nextOK {
				return true
			}
			continue
		}
		if utf8.RuneCountInString(alias) >= 2 {
			return true
		}
		if !nextOK {
			for _, sfx := range addressSuffix {
				if strings.HasPrefix(low[end:], sfx) {
					nextOK = true
					break
				}
			}
		}
		if prevOK && nextOK {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// addressedAgents returns the agent seats a line calls by name, in the
// order they appear.
func addressedAgents(t *tableRow, text string) []int {
	low := strings.ToLower(text)
	type hit struct{ seat, at int }
	var hits []hit
	for _, st := range agentSeats(t) {
		best := -1
		for _, al := range agentAliases(st) {
			if callsName(low, al) {
				at := strings.Index(low, al)
				if best < 0 || at < best {
					best = at
				}
			}
		}
		if best >= 0 {
			hits = append(hits, hit{st.Seat, best})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return a.at - b.at })
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.seat
	}
	return out
}

var everyoneRE = regexp.MustCompile(`(?i)(\b(everyone|everybody|anyone|anybody|you guys|y'?all|you all|all of you|guys|folks|team)\b|大家|你们|各位|诸位|みんな|皆さん|みなさん|皆様|お前ら|여러분|다들|너희)`)

// toEveryone: the line speaks to the whole table.
func toEveryone(text string) bool { return everyoneRE.MatchString(text) }

var questionRE = regexp.MustCompile(`(?i)([?？]\s*$|^(who|what|why|how|when|where|which|do|does|did|are|is|can|could|should|would|will|anyone)\b|吗[。！!]?$|呢[。！!]?$|か[。！!]?$|까[。！!]?$|니[。！!]?$)`)

// isQuestion: the line asks something.
func isQuestion(text string) bool { return questionRE.MatchString(strings.TrimSpace(text)) }

var mentionTok = regexp.MustCompile(`@\S+`)

// detectLang is the language a person wrote in (en | zh | ko | ja), so an
// agent answers in kind; def when the line has no letters to tell by
// (only emoji, numbers or names).
func detectLang(text, def string) string {
	text = mentionTok.ReplaceAllString(text, " ")
	var han, kana, hangul, latin int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Han, r):
			han++
		case r < 0x250 && unicode.IsLetter(r):
			latin++
		}
	}
	switch {
	case hangul > 0 && hangul >= kana:
		return "ko"
	case kana > 0:
		return "ja"
	case han > 0:
		if def == "ja" {
			return "ja" // kanji only: Japanese at a Japanese table
		}
		return "zh"
	case latin >= 2:
		return "en"
	}
	if def == "" {
		return "en"
	}
	return def
}

// ── Typing ──────────────────────────────────────────────────────────────────

// typingEvery throttles a person's typing pings at a table (per pod).
const typingEvery = 2 * time.Second

var typingSeen sync.Map // tableID|userID → time.Time

// Typing tells the table that userID is writing a line. Pings closer than
// typingEvery are dropped without a database write; the client sends one
// every few seconds while the person types.
func (s *Service) Typing(ctx context.Context, userID, tableID string) error {
	key := tableID + "|" + userID
	if v, ok := typingSeen.Load(key); ok && time.Since(v.(time.Time)) < typingEvery {
		return nil
	}
	typingSeen.Store(key, time.Now())
	if rand.IntN(200) == 0 {
		typingSeen.Range(func(k, v any) bool {
			if time.Since(v.(time.Time)) > time.Minute {
				typingSeen.Delete(k)
			}
			return true
		})
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
	return notify(ctx, s.Pool, chatNotice{TableID: t.ID, Typing: &Typing{Seat: t.seatOf(userID), Name: u.Name}, From: userID})
}

// ── Reactions ───────────────────────────────────────────────────────────────

// ReactionEmoji are the quick reactions a line can get.
var ReactionEmoji = []string{"👍", "😂", "😮", "🔥", "❤️", "👏", "😢", "🎉"}

// ReactionSet is every reaction on one line with who chose it. It travels
// between pods; the stream turns it into the viewer's []Reaction (For).
type ReactionSet struct {
	ChatID int64          `json:"chat_id"`
	Items  []reactionItem `json:"items"`
}

type reactionItem struct {
	Emoji string   `json:"e"`
	Users []string `json:"u"`
	Names []string `json:"n"`
}

// For is the set as viewer sees it.
func (rs *ReactionSet) For(viewer string) []Reaction {
	out := []Reaction{}
	if rs == nil {
		return out
	}
	for _, it := range rs.Items {
		out = append(out, Reaction{Emoji: it.Emoji, Count: len(it.Users), Names: it.Names, Mine: slices.Contains(it.Users, viewer)})
	}
	return out
}

// React toggles userID's emoji on a chat line and returns the line's
// reactions as userID sees them. Everyone who can see the line may react;
// a whisper only its two people.
func (s *Service) React(ctx context.Context, userID, tableID string, chatID int64, emoji string) ([]Reaction, error) {
	if !slices.Contains(ReactionEmoji, emoji) {
		return nil, badInput("pick one of the reactions")
	}
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return nil, err
	}
	ok, err := s.canWatch(ctx, t, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrForbidden
	}
	var sender, whisperTo *string
	err = s.Pool.QueryRow(ctx, `SELECT user_id::text, whisper_to::text FROM table_chat WHERE id=$1 AND table_id=$2`, chatID, t.ID).Scan(&sender, &whisperTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var audience []string
	if whisperTo != nil {
		if sender == nil || (userID != *sender && userID != *whisperTo) {
			return nil, ErrNotFound
		}
		audience = []string{*sender, *whisperTo}
	}
	var set *ReactionSet
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM table_chat_reactions WHERE chat_id=$1 AND user_id=$2 AND emoji=$3`, chatID, userID, emoji)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO table_chat_reactions (chat_id, user_id, emoji) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, chatID, userID, emoji); err != nil {
				return err
			}
		}
		sets, err := loadReactions(ctx, tx, []int64{chatID})
		if err != nil {
			return err
		}
		set = sets[chatID]
		if set == nil {
			set = &ReactionSet{ChatID: chatID, Items: []reactionItem{}}
		}
		return notify(ctx, tx, chatNotice{TableID: t.ID, Reaction: set, Audience: audience})
	})
	if err != nil {
		return nil, err
	}
	return set.For(userID), nil
}

// loadReactions reads the reactions on the given lines, emoji in the
// order they were first used.
func loadReactions(ctx context.Context, q querier, ids []int64) (map[int64]*ReactionSet, error) {
	out := map[int64]*ReactionSet{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT r.chat_id, r.emoji, r.user_id::text, coalesce(u.name,'')
		FROM table_chat_reactions r LEFT JOIN users u ON u.id = r.user_id
		WHERE r.chat_id = ANY($1) ORDER BY r.chat_id, r.at, r.user_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var emoji, user, name string
		if err := rows.Scan(&id, &emoji, &user, &name); err != nil {
			return nil, err
		}
		set := out[id]
		if set == nil {
			set = &ReactionSet{ChatID: id}
			out[id] = set
		}
		i := slices.IndexFunc(set.Items, func(it reactionItem) bool { return it.Emoji == emoji })
		if i < 0 {
			set.Items = append(set.Items, reactionItem{Emoji: emoji})
			i = len(set.Items) - 1
		}
		set.Items[i].Users = append(set.Items[i].Users, user)
		if len(set.Items[i].Names) < 12 {
			set.Items[i].Names = append(set.Items[i].Names, name)
		}
	}
	return out, rows.Err()
}

// ── Presence ────────────────────────────────────────────────────────────────

// PresenceTTL is how long an open stream counts as "here" after its last
// refresh; streams refresh well within it (on every keepalive).
const PresenceTTL = 45 * time.Second

// Here records that userID has the table open on stream connID (and
// refreshes it). The first stream of a person who was not here tells the
// table.
func (s *Service) Here(ctx context.Context, tableID, userID, connID string) error {
	var was bool
	err := s.Pool.QueryRow(ctx, `WITH before AS (
			SELECT EXISTS (SELECT 1 FROM table_presence WHERE table_id=$1 AND user_id=$2 AND seen_at > now() - make_interval(secs => $4::float8)) AS was
		), up AS (
			INSERT INTO table_presence (table_id, user_id, conn_id) VALUES ($1,$2,$3)
			ON CONFLICT (table_id, user_id, conn_id) DO UPDATE SET seen_at=now()
		)
		SELECT was FROM before`, tableID, userID, connID, PresenceTTL.Seconds()).Scan(&was)
	if err != nil {
		return err
	}
	if !was {
		// Old rows of streams that ended without saying so (a pod died).
		_, _ = s.Pool.Exec(ctx, `DELETE FROM table_presence WHERE table_id=$1 AND seen_at < now() - interval '10 minutes'`, tableID)
		return notify(ctx, s.Pool, chatNotice{TableID: tableID, Presence: true})
	}
	return nil
}

// Gone ends stream connID; when it was the person's last one, the table
// is told.
func (s *Service) Gone(ctx context.Context, tableID, userID, connID string) error {
	var still bool
	err := s.Pool.QueryRow(ctx, `WITH del AS (
			DELETE FROM table_presence WHERE table_id=$1 AND user_id=$2 AND conn_id=$3
		)
		SELECT EXISTS (SELECT 1 FROM table_presence WHERE table_id=$1 AND user_id=$2 AND conn_id<>$3 AND seen_at > now() - make_interval(secs => $4::float8))`,
		tableID, userID, connID, PresenceTTL.Seconds()).Scan(&still)
	if err != nil {
		return err
	}
	if !still {
		return notify(ctx, s.Pool, chatNotice{TableID: tableID, Presence: true})
	}
	return nil
}

// Presence is who has the table open now: seated people by seat, everyone
// else by name.
func (s *Service) Presence(ctx context.Context, tableID string) (*Presence, error) {
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return nil, err
	}
	return s.presence(ctx, t)
}

func (s *Service) presence(ctx context.Context, t *tableRow) (*Presence, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT p.user_id::text, u.name FROM table_presence p JOIN users u ON u.id = p.user_id
		WHERE p.table_id=$1 AND p.seen_at > now() - make_interval(secs => $2::float8) ORDER BY u.name, p.user_id::text`, t.ID, PresenceTTL.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p := &Presence{Seats: []int{}, Watchers: []string{}}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if seat := t.seatOf(id); seat >= 0 {
			p.Seats = append(p.Seats, seat)
		} else {
			p.Watchers = append(p.Watchers, name)
		}
	}
	slices.Sort(p.Seats)
	return p, rows.Err()
}

// Equal reports whether two presence snapshots say the same.
func (p *Presence) Equal(o *Presence) bool {
	if p == nil || o == nil {
		return p == o
	}
	return slices.Equal(p.Seats, o.Seats) && slices.Equal(p.Watchers, o.Watchers)
}
