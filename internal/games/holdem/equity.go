package holdem

import (
	"context"
	"math/rand"
	"sort"
)

// estimateEquity is the Monte Carlo share of the pot hole wins against opps
// opponents holding unknown random cards, given the known board. It uses
// only the seat's own knowledge: everything not in hole or board is treated
// as unseen. Ties count as fractional wins. It stops early when ctx is done,
// returning the estimate so far (or the fair share if nothing ran).
func estimateEquity(ctx context.Context, hole, board []card, opps, sims int, rng *rand.Rand) float64 {
	if opps <= 0 {
		return 1
	}
	if opps > 8 {
		opps = 8
	}
	var used uint64
	for _, c := range hole {
		used |= 1 << uint(c)
	}
	for _, c := range board {
		used |= 1 << uint(c)
	}
	var rest [deckSize]card
	nr := 0
	for c := 0; c < deckSize; c++ {
		if used&(1<<uint(c)) == 0 {
			rest[nr] = card(c)
			nr++
		}
	}
	missing := 5 - len(board)
	need := 2*opps + missing
	if need > nr {
		return 1 / float64(opps+1)
	}

	var mine, theirs [7]card
	nb := copy(mine[2:], board)
	copy(mine[:2], hole)
	copy(theirs[2:], board)
	total, done := 0.0, 0
	for i := 0; i < sims; i++ {
		if i&63 == 0 && i > 0 && ctx.Err() != nil {
			break
		}
		// Partial Fisher-Yates: only the cards this trial needs.
		for k := 0; k < need; k++ {
			j := k + rng.Intn(nr-k)
			rest[k], rest[j] = rest[j], rest[k]
		}
		copy(mine[2+nb:], rest[2*opps:need])
		copy(theirs[2+nb:], rest[2*opps:need])
		my := evaluate(mine[:])
		ties, lost := 0, false
		for o := 0; o < opps; o++ {
			theirs[0], theirs[1] = rest[2*o], rest[2*o+1]
			sc := evaluate(theirs[:])
			if sc > my {
				lost = true
				break
			}
			if sc == my {
				ties++
			}
		}
		if !lost {
			total += 1 / float64(ties+1)
		}
		done++
	}
	if done == 0 {
		return 1 / float64(opps+1)
	}
	return total / float64(done)
}

// preflopIndex maps two hole cards to their row/column in the 13x13
// starting-hand grid: pairs on the diagonal, suited hands above it
// ([high][low]) and offsuit hands below it ([low][high]).
func preflopIndex(a, b card) (int, int) {
	hi, lo := a.rank(), b.rank()
	if lo > hi {
		hi, lo = lo, hi
	}
	if a.suit() == b.suit() && hi != lo {
		return hi, lo
	}
	return lo, hi
}

// preflopPercentile[r][c] is the share of all 1326 starting hands that the
// class at (r,c) beats or equals in heads-up equity: 1.0 for aces, close to
// 0 for seven-deuce offsuit. Ranking by combos (6 per pair, 4 per suited and
// 12 per offsuit class) is what makes "play the top 20% of hands" mean the
// same thing it does at a real table.
var preflopPercentile [13][13]float64

func init() {
	type cls struct {
		r, c  int
		eq    float32
		combo int
	}
	var all []cls
	for r := 0; r < 13; r++ {
		for c := 0; c < 13; c++ {
			combos := 12
			switch {
			case r == c:
				combos = 6
			case r > c:
				combos = 4
			}
			all = append(all, cls{r, c, preflopEquity[r][c], combos})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].eq < all[j].eq })
	cum := 0
	for _, x := range all {
		cum += x.combo
		preflopPercentile[x.r][x.c] = float64(cum) / 1326
	}
}

// preflopStrength is the starting-hand percentile, 0..1 with 1 the best.
func preflopStrength(a, b card) float64 {
	r, c := preflopIndex(a, b)
	return preflopPercentile[r][c]
}
