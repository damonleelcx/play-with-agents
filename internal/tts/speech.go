package tts

import (
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Synthesizer turns text into MP3. *Fish is the production one.
type Synthesizer interface {
	SpeakMP3(ctx context.Context, text string) (audio []byte, contentType string, err error)
}

// MaxSpokenChars is the most Aoi reads aloud in one request, after cleaning.
// Her chat replies are short; a longer one is read up to a sentence boundary.
const MaxSpokenChars = 600

// ErrEmpty means nothing speakable was left after cleaning.
var ErrEmpty = errors.New("nothing to speak")

// Service is what the web tier calls: it cleans the text, serves repeats from
// an in-memory LRU (a replayed line costs nothing), and collapses concurrent
// requests for the same line into one vendor call. Safe for concurrent use.
type Service struct {
	voice    Synthesizer
	capacity int

	mu       sync.Mutex
	order    *list.List // front = most recently used
	items    map[[32]byte]*list.Element
	inflight map[[32]byte]*call
}

type entry struct {
	key   [32]byte
	audio []byte
	ct    string
}

type call struct {
	done  chan struct{}
	audio []byte
	ct    string
	err   error
}

// NewService wraps a synthesizer with a cache of capacity lines (~200 is a
// few MB of MP3).
func NewService(v Synthesizer, capacity int) *Service {
	if capacity < 1 {
		capacity = 1
	}
	return &Service{voice: v, capacity: capacity, order: list.New(), items: map[[32]byte]*list.Element{}, inflight: map[[32]byte]*call{}}
}

// Speak cleans text and returns its audio. ErrEmpty when nothing speakable
// remains.
func (s *Service) Speak(ctx context.Context, text string) ([]byte, string, error) {
	return s.SpeakCharged(ctx, text, nil)
}

// SpeakCharged is Speak with an allowance: before a line is sent to the
// vendor, charge is called with the number of characters to synthesise, and
// an error from it (a daily cap reached) is returned instead of calling the
// vendor. A line served from the cache, or joined to a synthesis already in
// flight, costs nothing and is not charged.
func (s *Service) SpeakCharged(ctx context.Context, text string, charge func(chars int) error) ([]byte, string, error) {
	clean := Clean(text, MaxSpokenChars)
	if clean == "" {
		return nil, "", ErrEmpty
	}
	key := sha256.Sum256([]byte(clean))

	s.mu.Lock()
	if el, ok := s.items[key]; ok {
		s.order.MoveToFront(el)
		e := el.Value.(*entry)
		s.mu.Unlock()
		return e.audio, e.ct, nil
	}
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
			return c.audio, c.ct, c.err
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	s.mu.Unlock()

	if charge != nil {
		if err := charge(utf8.RuneCountInString(clean)); err != nil {
			// Anyone who joined this call meanwhile gets the same answer
			// and pays nothing; the next request tries again.
			c.err = err
			s.mu.Lock()
			delete(s.inflight, key)
			s.mu.Unlock()
			close(c.done)
			return nil, "", err
		}
	}

	// The vendor call is not tied to this request: a second caller may be
	// waiting on the same line, and a finished synthesis is worth caching
	// even if the first listener left.
	c.audio, c.ct, c.err = s.voice.SpeakMP3(context.WithoutCancel(ctx), clean)

	s.mu.Lock()
	delete(s.inflight, key)
	if c.err == nil {
		s.items[key] = s.order.PushFront(&entry{key: key, audio: c.audio, ct: c.ct})
		for s.order.Len() > s.capacity {
			old := s.order.Back()
			s.order.Remove(old)
			delete(s.items, old.Value.(*entry).key)
		}
	}
	s.mu.Unlock()
	close(c.done)
	return c.audio, c.ct, c.err
}

// Len is the number of cached lines.
func (s *Service) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.order.Len()
}

var (
	reFence     = regexp.MustCompile("(?s)```.*?```")
	reImage     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	reLink      = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reURL       = regexp.MustCompile(`https?://\S+`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
	reLineMark  = regexp.MustCompile(`(?m)^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+|\d+[.)]\s+)`)
	reRule      = regexp.MustCompile(`(?m)^\s*([-*_]\s*){3,}$`)
	reEmphasis  = regexp.MustCompile("[*_~`]+")
	reTableBar  = regexp.MustCompile(`\s*\|\s*`)
	reSpaces    = regexp.MustCompile(`[ \t\x{00A0}]+`)
	reLineEdges = regexp.MustCompile(`[ \t]*\n[ \t]*`)
	reBlankRuns = regexp.MustCompile(`\n{2,}`)
)

// Clean turns a chat message into something a voice should read: no
// Markdown syntax, no code, no links' URLs, no emoji, collapsed whitespace,
// and at most max characters — cut at a sentence end when there is one in
// the second half, so she never stops mid-word.
func Clean(text string, max int) string {
	s := reFence.ReplaceAllString(text, " ")
	s = reImage.ReplaceAllString(s, "$1")
	s = reLink.ReplaceAllString(s, "$1")
	s = reURL.ReplaceAllString(s, "")
	s = reTag.ReplaceAllString(s, "")
	s = reRule.ReplaceAllString(s, "")
	s = reLineMark.ReplaceAllString(s, "")
	s = reEmphasis.ReplaceAllString(s, "")
	s = reTableBar.ReplaceAllString(s, ", ")
	s = strings.Map(func(r rune) rune {
		if isEmoji(r) {
			return -1
		}
		return r
	}, s)
	s = reSpaces.ReplaceAllString(s, " ")
	s = reLineEdges.ReplaceAllString(s, "\n")
	s = reBlankRuns.ReplaceAllString(s, "\n")
	return capAtSentence(strings.TrimSpace(s), max)
}

// isEmoji covers pictographs, dingbats, flags, keycaps and their joiners and
// modifiers — but keeps the card suits (♠♥♦♣), which a poker line needs.
func isEmoji(r rune) bool {
	switch {
	case r >= 0x2660 && r <= 0x2667: // ♠♡♢♣♤♥♦♧
		return false
	case r >= 0x1F000 && r <= 0x1FAFF, // emoji, pictographs, flags, skin tones
		r >= 0x2600 && r <= 0x27BF, // misc symbols and dingbats
		r >= 0x2B00 && r <= 0x2BFF, // arrows and stars (⭐)
		r >= 0xFE00 && r <= 0xFE0F, // variation selectors
		r == 0x200D, r == 0x20E3,   // zero-width joiner, keycap
		r >= 0xE0020 && r <= 0xE007F: // tag characters (subdivision flags)
		return true
	}
	return unicode.Is(unicode.Co, r) // private use: never speakable
}

func capAtSentence(s string, max int) string {
	r := []rune(s)
	if max <= 0 || len(r) <= max {
		return s
	}
	cut := r[:max]
	for i := len(cut) - 1; i >= max/2; i-- {
		switch cut[i] {
		case '.', '!', '?', '。', '！', '？', '\n':
			return strings.TrimSpace(string(cut[:i+1]))
		}
	}
	for i := len(cut) - 1; i >= max/2; i-- {
		if cut[i] == ' ' || cut[i] == '，' || cut[i] == ',' {
			return strings.TrimSpace(string(cut[:i]))
		}
	}
	return string(cut)
}
