package rooms

import (
	"strings"
	"unicode"
)

// Repetition check for agent table talk: a model given a persona tends to
// settle on a catchphrase and say it every few hands. A new line that is
// too close to one of the agent's own recent lines is dropped (silence),
// not replaced with a canned line.

// stopwords are ignored by the overlap ratio (not by the phrase check).
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true, "of": true, "to": true, "in": true,
	"on": true, "at": true, "for": true, "with": true, "is": true, "are": true, "was": true, "be": true, "it": true,
	"its": true, "it's": true, "i": true, "i'm": true, "me": true, "my": true, "you": true, "your": true, "we": true,
	"that": true, "this": true, "so": true, "just": true, "not": true, "no": true, "do": true, "all": true,
}

// tokens splits a line into lower-case words; each Han character is a
// token of its own, so Chinese lines compare too.
func tokens(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			out = append(out, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'':
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// phraseLen is the length of a shared run of tokens that counts as a reused
// phrase ("the vault of my considerable savings" shares many).
const phraseLen = 4

// tooSimilar reports whether a and b are the same line, share a phrase of
// phraseLen tokens, or share most of their content words.
func tooSimilar(a, b string) bool {
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	if strings.Join(ta, " ") == strings.Join(tb, " ") {
		return true
	}
	grams := map[string]bool{}
	for i := 0; i+phraseLen <= len(tb); i++ {
		grams[strings.Join(tb[i:i+phraseLen], " ")] = true
	}
	for i := 0; i+phraseLen <= len(ta); i++ {
		if grams[strings.Join(ta[i:i+phraseLen], " ")] {
			return true
		}
	}
	content := func(ts []string) map[string]bool {
		m := map[string]bool{}
		for _, t := range ts {
			if !stopwords[t] {
				m[t] = true
			}
		}
		return m
	}
	ca, cb := content(ta), content(tb)
	n := min(len(ca), len(cb))
	if n < 3 {
		return false
	}
	shared := 0
	for w := range ca {
		if cb[w] {
			shared++
		}
	}
	return float64(shared)/float64(n) >= 0.6
}

// repeatsOwn reports whether line is too close to any of the agent's own
// recent lines.
func repeatsOwn(line string, own []string) bool {
	for _, o := range own {
		if tooSimilar(line, o) {
			return true
		}
	}
	return false
}
