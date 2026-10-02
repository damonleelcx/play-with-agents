package holdem

import (
	"fmt"
)

// card is one playing card packed as rank*4 + suit. Rank 0 is the deuce and
// 12 the ace; suit indexes suitChars. The packed form keeps the evaluator and
// the Monte Carlo loops allocation-free; JSON uses the "As" text form so a
// stored state stays readable when a table has to be debugged by hand.
type card uint8

const (
	rankChars = "23456789TJQKA"
	suitChars = "shdc"
	deckSize  = 52
)

func newCard(rank, suit int) card { return card(rank<<2 | suit) }

func (c card) rank() int { return int(c) >> 2 }
func (c card) suit() int { return int(c) & 3 }

func (c card) String() string {
	if int(c) >= deckSize {
		return "??"
	}
	return string([]byte{rankChars[c.rank()], suitChars[c.suit()]})
}

// MarshalText makes []card encode as a JSON array of strings rather than the
// base64 blob encoding/json uses for byte-sized slices.
func (c card) MarshalText() ([]byte, error) {
	if int(c) >= deckSize {
		return nil, fmt.Errorf("holdem: invalid card %d", c)
	}
	return []byte(c.String()), nil
}

func (c *card) UnmarshalText(b []byte) error {
	v, err := parseCard(string(b))
	if err != nil {
		return err
	}
	*c = v
	return nil
}

// parseCard reads the contract's card notation: rank from "23456789TJQKA"
// then suit from "shdc", e.g. "As", "Td", "9c".
func parseCard(s string) (card, error) {
	if len(s) != 2 {
		return 0, fmt.Errorf("holdem: bad card %q", s)
	}
	r, su := -1, -1
	for i := 0; i < len(rankChars); i++ {
		if rankChars[i] == s[0] {
			r = i
		}
	}
	for i := 0; i < len(suitChars); i++ {
		if suitChars[i] == s[1] {
			su = i
		}
	}
	if r < 0 || su < 0 {
		return 0, fmt.Errorf("holdem: bad card %q", s)
	}
	return newCard(r, su), nil
}

func parseCards(ss []string) ([]card, error) {
	out := make([]card, 0, len(ss))
	for _, s := range ss {
		c, err := parseCard(s)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// cardStrings returns the text form; nil in gives nil out so a hidden hand
// serialises as JSON null, as the view contract requires.
func cardStrings(cs []card) []string {
	if cs == nil {
		return nil
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}

func joinCards(cs []card) string {
	b := make([]byte, 0, len(cs)*3)
	for i, c := range cs {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, c.String()...)
	}
	return string(b)
}

// splitmix64 is a tiny, well-mixed generator. The deck shuffle uses it
// instead of math/rand so the deal for a given (seed, hand) is fixed by this
// file alone and can never shift under a toolchain upgrade, which would break
// replaying stored tables.
type splitmix64 struct{ x uint64 }

func (r *splitmix64) next() uint64 {
	r.x += 0x9E3779B97F4A7C15
	z := r.x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// handDeck is the shuffled deck for one hand, derived only from the table
// seed and the hand number. The state stores a position into it rather than
// the cards, so the undealt deck is never serialised anywhere.
func handDeck(seed int64, handNo int) [deckSize]card {
	var d [deckSize]card
	for i := range d {
		d[i] = card(i)
	}
	r := splitmix64{x: uint64(seed)*0xD6E8FEB86659FD93 ^ uint64(handNo)*0xD1B54A32D192ED03}
	r.next()
	for i := deckSize - 1; i > 0; i-- {
		// The modulo bias over a 64-bit draw is below 2^-58: irrelevant here.
		j := int(r.next() % uint64(i+1))
		d[i], d[j] = d[j], d[i]
	}
	return d
}
