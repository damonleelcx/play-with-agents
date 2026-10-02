package holdem

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

func mustCards(t testing.TB, s string) []card {
	t.Helper()
	cs, err := parseCards(strings.Fields(s))
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func score(t testing.TB, s string) handScore {
	t.Helper()
	return evaluate(mustCards(t, s))
}

func TestCardRoundTrip(t *testing.T) {
	for c := card(0); c < deckSize; c++ {
		got, err := parseCard(c.String())
		if err != nil || got != c {
			t.Fatalf("round trip %d: %v %v", c, got, err)
		}
	}
	for _, bad := range []string{"", "A", "1s", "Ax", "AsK", "as"} {
		if _, err := parseCard(bad); err == nil {
			t.Errorf("parseCard(%q) accepted", bad)
		}
	}
}

func TestEvaluateNames(t *testing.T) {
	cases := []struct{ cards, name string }{
		{"As Ks Qs Js Ts 2d 3c", "Royal Flush"},
		{"9h 8h 7h 6h 5h Ah Kd", "Straight Flush, Nine high"},
		{"Ad 2d 3d 4d 5d Kc Qh", "Straight Flush, Five high"},
		{"Ac Ad Ah As Kd 2c 3h", "Four of a Kind, Aces"},
		{"Qc Qd Qh 5s 5d 2c 3h", "Full House, Queens full of Fives"},
		{"Qc Qd Qh 5s 5d 5c 3h", "Full House, Queens full of Fives"},
		{"7c 7d 7h 9s 9d 9c 3h", "Full House, Nines full of Sevens"},
		{"Ah 9h 7h 4h 2h Kd Qc", "Flush, Ace high"},
		{"Ah 2c 3d 4s 5h 9d Kc", "Straight, Five high"},
		{"Ah Kc Qd Js Th 9d 2c", "Straight, Ace high"},
		{"7c 7d 7h As Kd 2c 4h", "Three of a Kind, Sevens"},
		{"Kc Kd Tc Td 2s 3h 7h", "Two Pair, Kings and Tens"},
		{"Kc Kd Tc Td 2s 2h 7h", "Two Pair, Kings and Tens"},
		{"Qc Qd 2s 5h 7h 9d Jc", "Pair of Queens"},
		{"Ac 3d 5s 7h 9h Jd Qc", "High Card, Ace"},
		// Fewer than five cards: the preflop label.
		{"Ah Ad", "Pair of Aces"},
		{"Ah Kd", "High Card, Ace"},
	}
	for _, c := range cases {
		if got := score(t, c.cards).name(); got != c.name {
			t.Errorf("%s: got %q, want %q", c.cards, got, c.name)
		}
	}
}

func TestEvaluateCategoryOrder(t *testing.T) {
	// One hand per category, weakest first.
	hands := []string{
		"Ac 3d 5s 7h 9h Jd Qc",
		"2c 2d 5s 7h 9h Jd Qc",
		"2c 2d 5s 5h 9h Jd Qc",
		"2c 2d 2s 7h 9h Jd Qc",
		"2c 3d 4s 5h 6h Jd Qc",
		"2h 3h 4h 5h 9h Jd Qc",
		"2c 2d 2s 5h 5c Jd Qc",
		"2c 2d 2s 2h 9h Jd Qc",
		"2h 3h 4h 5h 6h Jd Qc",
		"Th Jh Qh Kh Ah 2d 3c",
	}
	for i := 1; i < len(hands); i++ {
		lo, hi := score(t, hands[i-1]), score(t, hands[i])
		if !(hi > lo) {
			t.Errorf("%q (%s) should beat %q (%s)", hands[i], hi.name(), hands[i-1], lo.name())
		}
	}
}

func TestEvaluateKickersAndTies(t *testing.T) {
	type cmp int
	const (
		less cmp = iota - 1
		equal
		greater
	)
	cases := []struct {
		a, b string
		want cmp
	}{
		// Pair kickers decide.
		{"Ac Ad Kh 9s 7d 3c 2h", "As Ah Qh 9c 7c 3d 2d", greater},
		// Board plays: identical best five.
		{"2c 3d Ah Kh Qh Jd 9s", "2s 4d Ah Kh Qh Jd 9s", equal},
		// Two pair: kicker counts, third pair does not.
		{"Kc Kd Tc Td 4s 4h Ah", "Kh Ks Th Ts 9c 9d Qh", greater},
		// Two pair with the same pairs, higher kicker.
		{"Kc Kd Tc Td Qs 2h 3h", "Kh Ks Th Ts Js 2d 3d", greater},
		// Trips kickers.
		{"7c 7d 7h As 2d 3c 4h", "7c 7d 7h Ks Qd 3c 4h", greater},
		{"7c 7d 7h Ks 2d 3c 9h", "7c 7d 7h Ks Qd 3c 4h", less},
		// Flush compares all five cards.
		{"Ah 9h 7h 4h 3h Kd Qc", "Ah 9h 7h 4h 2h Kd Qc", greater},
		// Straight: six-high beats the wheel.
		{"2c 3d 4s 5h 6h Kd Qc", "Ac 2d 3s 4h 5h Kd 9c", greater},
		// Full house: trips first, then the pair.
		{"3c 3d 3h 2s 2d Ac Kh", "2c 2h 2s Ad Ac Kc Qh", greater},
		{"Qc Qd Qh 5s 5d 2c 3h", "Qc Qd Qh 4s 4d Ac Kh", greater},
		// Quads kicker.
		{"9c 9d 9h 9s Ad 2c 3h", "9c 9d 9h 9s Kd Qc Jh", greater},
		// Straight flush beats quads; higher straight flush wins.
		{"5h 6h 7h 8h 9h 2c 2d", "Ac Ad Ah As Kd Qc Jh", greater},
		{"6h 7h 8h 9h Th 2c 2d", "5c 6c 7c 8c 9c 2s 2h", greater},
		// High card with five kickers.
		{"Ac Qd 9s 7h 5h 3d 2c", "Ac Qd 9s 7h 4h 3d 2c", greater},
	}
	for _, c := range cases {
		a, b := score(t, c.a), score(t, c.b)
		got := equal
		if a > b {
			got = greater
		} else if a < b {
			got = less
		}
		if got != c.want {
			t.Errorf("%s (%s) vs %s (%s): got %d want %d", c.a, a.name(), c.b, b.name(), got, c.want)
		}
	}
}

// TestEvaluateCategoryFrequencies is a known-answer check over all 7-card
// hands sampled at random: the category frequencies must match the exact
// combinatorial values within sampling error.
func TestEvaluateCategoryFrequencies(t *testing.T) {
	exact := [9]float64{ // per 133,784,560 seven-card hands
		23294460, 58627800, 31433400, 6461620, 6180020, 4047644, 3473184, 224848, 41584,
	}
	const total = 133784560.0
	const n = 400_000
	rng := rand.New(rand.NewSource(7))
	var counts [9]int
	deck := make([]card, deckSize)
	for i := range deck {
		deck[i] = card(i)
	}
	for i := 0; i < n; i++ {
		for k := 0; k < 7; k++ {
			j := k + rng.Intn(deckSize-k)
			deck[k], deck[j] = deck[j], deck[k]
		}
		counts[evaluate(deck[:7]).category()]++
	}
	for c := 0; c < 9; c++ {
		p := exact[c] / total
		got := float64(counts[c]) / n
		sd := math.Sqrt(p * (1 - p) / n)
		if diff := got - p; diff > 5*sd+1e-4 || -diff > 5*sd+1e-4 {
			t.Errorf("category %d: frequency %.5f, want %.5f", c, got, p)
		}
	}
}

func TestEvaluateExhaustiveFiveCardCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("exhaustive")
	}
	// All 2,598,960 five-card hands, against the textbook counts.
	want := [9]int{1302540, 1098240, 123552, 54912, 10200, 5108, 3744, 624, 40}
	var got [9]int
	var h [5]card
	for a := 0; a < 48; a++ {
		for b := a + 1; b < 49; b++ {
			for c := b + 1; c < 50; c++ {
				for d := c + 1; d < 51; d++ {
					for e := d + 1; e < 52; e++ {
						h = [5]card{card(a), card(b), card(c), card(d), card(e)}
						got[evaluate(h[:]).category()]++
					}
				}
			}
		}
	}
	if got != want {
		t.Fatalf("five-card category counts %v, want %v", got, want)
	}
}

func BenchmarkEvaluate7(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	hands := make([][7]card, 1024)
	deck := make([]card, deckSize)
	for i := range deck {
		deck[i] = card(i)
	}
	for i := range hands {
		rng.Shuffle(len(deck), func(x, y int) { deck[x], deck[y] = deck[y], deck[x] })
		copy(hands[i][:], deck)
	}
	b.ReportAllocs()
	b.ResetTimer()
	var sink handScore
	for i := 0; i < b.N; i++ {
		sink ^= evaluate(hands[i&1023][:])
	}
	_ = sink
}
