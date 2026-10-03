package holdem

import (
	"math/bits"
)

// handScore orders poker hands: a larger score is a better hand and equal
// scores split. Layout: category in bits 20-23, then up to five ranks (0 =
// deuce .. 12 = ace) in descending significance, one nibble each. Comparing
// two scores as integers therefore compares category first and then the
// kickers in order, which is exactly the poker rule.
type handScore uint32

const (
	catHighCard = iota
	catPair
	catTwoPair
	catTrips
	catStraight
	catFlush
	catFullHouse
	catQuads
	catStraightFlush
)

func makeScore(cat int, ranks ...int) handScore {
	var r [5]int
	n := copy(r[:], ranks)
	return packScore(cat, &r, n)
}

// packScore is the allocation-free core of makeScore; the evaluator's hot
// path uses it directly.
func packScore(cat int, r *[5]int, n int) handScore {
	v := uint32(cat) << 20
	for i := 0; i < n; i++ {
		v |= uint32(r[i]) << (16 - 4*uint(i))
	}
	return handScore(v)
}

func (h handScore) category() int { return int(h >> 20) }
func (h handScore) rankAt(i int) int {
	return int(h>>(16-4*uint(i))) & 0xF
}

// straightHigh returns the top rank of the best straight in a 13-bit rank
// mask, or -1. The ace is copied below the deuce so A-2-3-4-5 (the wheel)
// counts, with the five as its top card.
func straightHigh(mask uint16) int {
	m := uint32(mask)<<1 | uint32(mask>>12)&1 // bit 0 = ace-low, bit k = rank k-1
	run := m & (m >> 1) & (m >> 2) & (m >> 3) & (m >> 4)
	if run == 0 {
		return -1
	}
	// Highest run start j covers bits j..j+4, i.e. ranks j-1..j+3.
	return bits.Len32(run) - 1 + 3
}

// topRanks fills dst with the highest ranks set in mask, skipping exclude,
// and returns how many it wrote.
func topRanks(dst []int, mask uint16, exclude uint16) int {
	m := mask &^ exclude
	n := 0
	for n < len(dst) && m != 0 {
		r := bits.Len16(m) - 1
		dst[n] = r
		n++
		m &^= 1 << uint(r)
	}
	return n
}

// evaluate scores the best five-card hand within cs. It accepts 1 to 7 cards
// (fewer than five are scored on what is there, which is what the "your hand"
// label needs preflop) and allocates nothing, because the Brain calls it
// tens of thousands of times per decision.
func evaluate(cs []card) handScore {
	var suitMask [4]uint16
	var counts [13]uint8
	var all uint16
	for _, c := range cs {
		r := c.rank()
		suitMask[c.suit()] |= 1 << uint(r)
		counts[r]++
		all |= 1 << uint(r)
	}

	flushSuit := -1
	for s := 0; s < 4; s++ {
		if bits.OnesCount16(suitMask[s]) >= 5 {
			flushSuit = s
			if h := straightHigh(suitMask[s]); h >= 0 {
				return makeScore(catStraightFlush, h)
			}
			break // seven cards can hold only one five-card suit
		}
	}

	quad := -1
	var trips, pairs [3]int
	nt, np := 0, 0
	for r := 12; r >= 0; r-- {
		switch counts[r] {
		case 4:
			if quad < 0 {
				quad = r
			}
		case 3:
			if nt < len(trips) {
				trips[nt] = r
				nt++
			}
		case 2:
			if np < len(pairs) {
				pairs[np] = r
				np++
			}
		}
	}

	// r collects the ranks that make up the score, most significant first.
	var r [5]int
	switch {
	case quad >= 0:
		r[0] = quad
		n := topRanks(r[1:2], all, 1<<uint(quad))
		return packScore(catQuads, &r, 1+n)
	case nt > 0 && (nt > 1 || np > 0):
		pair := -1
		if nt > 1 {
			pair = trips[1]
		}
		if np > 0 && pairs[0] > pair {
			pair = pairs[0]
		}
		r[0], r[1] = trips[0], pair
		return packScore(catFullHouse, &r, 2)
	case flushSuit >= 0:
		n := topRanks(r[:], suitMask[flushSuit], 0)
		return packScore(catFlush, &r, n)
	}
	if h := straightHigh(all); h >= 0 {
		r[0] = h
		return packScore(catStraight, &r, 1)
	}
	switch {
	case nt > 0:
		r[0] = trips[0]
		n := topRanks(r[1:3], all, 1<<uint(trips[0]))
		return packScore(catTrips, &r, 1+n)
	case np >= 2:
		r[0], r[1] = pairs[0], pairs[1]
		n := topRanks(r[2:3], all, 1<<uint(pairs[0])|1<<uint(pairs[1]))
		return packScore(catTwoPair, &r, 2+n)
	case np == 1:
		r[0] = pairs[0]
		n := topRanks(r[1:4], all, 1<<uint(pairs[0]))
		return packScore(catPair, &r, 1+n)
	}
	n := topRanks(r[:], all, 0)
	return packScore(catHighCard, &r, n)
}

var (
	rankNames   = [13]string{"Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten", "Jack", "Queen", "King", "Ace"}
	rankPlurals = [13]string{"Twos", "Threes", "Fours", "Fives", "Sixes", "Sevens", "Eights", "Nines", "Tens", "Jacks", "Queens", "Kings", "Aces"}
)

// name is the English description players see in the log and at showdown,
// e.g. "Full House, Queens full of Fives".
func (h handScore) name() string {
	r1, r2 := h.rankAt(0), h.rankAt(1)
	switch h.category() {
	case catStraightFlush:
		if r1 == 12 {
			return "Royal Flush"
		}
		return "Straight Flush, " + rankNames[r1] + " high"
	case catQuads:
		return "Four of a Kind, " + rankPlurals[r1]
	case catFullHouse:
		return "Full House, " + rankPlurals[r1] + " full of " + rankPlurals[r2]
	case catFlush:
		return "Flush, " + rankNames[r1] + " high"
	case catStraight:
		return "Straight, " + rankNames[r1] + " high"
	case catTrips:
		return "Three of a Kind, " + rankPlurals[r1]
	case catTwoPair:
		return "Two Pair, " + rankPlurals[r1] + " and " + rankPlurals[r2]
	case catPair:
		return "Pair of " + rankPlurals[r1]
	default:
		return "High Card, " + rankNames[r1]
	}
}

// evalHoleBoard scores hole+board without allocating.
func evalHoleBoard(hole []card, board []card) handScore {
	var buf [7]card
	n := copy(buf[:], hole)
	n += copy(buf[n:], board)
	return evaluate(buf[:n])
}
