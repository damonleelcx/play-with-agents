package holdem

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

var roster = []string{"aoi", "ren", "mika", "bram", "nova", "lin"}

// isLegal reports whether m is one of specs (raise amounts inside the range).
func isLegal(specs []games.MoveSpec, m games.Move) bool {
	for _, sp := range specs {
		if sp.Type != m.Type {
			continue
		}
		if sp.Range == nil {
			return len(m.Args) == 0
		}
		to, ok := toInt(m.Args[sp.Range.Arg])
		return ok && to >= sp.Range.Min && to <= sp.Range.Max
	}
	return false
}

func TestPersonaFor(t *testing.T) {
	ren, lin, mika, bram := PersonaFor("ren", 2), PersonaFor("lin", 1), PersonaFor("mika", 0), PersonaFor("bram", 1)
	if ren.ID != "ren" || ren.Skill != 2 || !(ren.Tightness > 0.7 && ren.Aggression > 0.7) {
		t.Fatalf("ren %+v", ren)
	}
	if !(lin.Tightness > 0.7 && lin.Aggression < 0.3 && lin.Bluff < 0.1) {
		t.Fatalf("lin %+v", lin)
	}
	if !(mika.Tightness < 0.3 && mika.Aggression > 0.8 && mika.Bluff > 0.6) {
		t.Fatalf("mika %+v", mika)
	}
	if !(bram.Tightness < 0.3 && bram.Aggression < 0.3) {
		t.Fatalf("bram %+v", bram)
	}
	if p := PersonaFor("stranger", 9); p.Skill != 2 || p.Tightness != PersonaFor("nova", 0).Tightness {
		t.Fatalf("unknown agent %+v", p)
	}
	if p := PersonaFor("aoi", -3); p.Skill != 0 {
		t.Fatalf("skill not clamped: %+v", p)
	}
}

// playBrains runs a whole table of Brains, checking every decision against
// Legal, and returns the final state.
func playBrains(t *testing.T, seats, hands, skill int, seed int64) games.State {
	t.Helper()
	g := New()
	st, err := g.Setup(games.Config{Seats: seats, Options: map[string]any{"max_hands": hands, "blinds_double_every": 6}}, seed)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(seed))
	for step := 0; step < 20_000; step++ {
		toMove, _ := g.ToMove(st)
		if len(toMove) == 0 {
			return st
		}
		seat := toMove[0]
		specs, _ := g.Legal(st, seat)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		m, err := Brain{}.Choose(ctx, g, st, seat, PersonaFor(roster[seat%len(roster)], skill), rng)
		cancel()
		if err != nil {
			t.Fatalf("brain error: %v", err)
		}
		if !isLegal(specs, m) {
			t.Fatalf("brain chose %+v, legal %+v", m, specs)
		}
		st, _, err = g.Apply(st, seat, m)
		if err != nil {
			t.Fatalf("brain move %+v rejected: %v", m, err)
		}
	}
	t.Fatal("brain game did not finish")
	return nil
}

func TestBrainAlwaysChoosesLegalMoves(t *testing.T) {
	for seats := 2; seats <= 9; seats++ {
		playBrains(t, seats, 20, 0, int64(seats))
	}
	playBrains(t, 4, 10, 2, 77)
}

func TestBrainUsesOnlyWhatItsSeatCanSee(t *testing.T) {
	g := New()
	st, _ := g.Setup(games.Config{Seats: 4}, 3)
	rng := rand.New(rand.NewSource(1))
	checked := 0
	for step := 0; step < 2000 && checked < 150; step++ {
		toMove, _ := g.ToMove(st)
		if len(toMove) == 0 {
			st, _ = g.Setup(games.Config{Seats: 2 + step%8}, int64(step))
			continue
		}
		seat := toMove[0]
		p := PersonaFor(roster[step%len(roster)], 1)

		// Same position, but every opponent holds different cards and the
		// future deck is different: the decision must not change.
		s := decodeT(t, st)
		seen := map[card]bool{}
		for _, c := range s.Board {
			seen[c] = true
		}
		for _, c := range s.Players[seat].Cards {
			seen[c] = true
		}
		next := card(0)
		for i := range s.Players {
			if i == seat || len(s.Players[i].Cards) == 0 || s.Players[i].Shown {
				continue
			}
			for k := range s.Players[i].Cards {
				for seen[next] {
					next++
				}
				s.Players[i].Cards[k] = next
				seen[next] = true
			}
		}
		s.Seed ^= 0x5DEECE66D
		alt := encodeT(t, s)

		a, err := Brain{}.Choose(context.Background(), g, st, seat, p, rand.New(rand.NewSource(int64(step))))
		if err != nil {
			t.Fatal(err)
		}
		b, err := Brain{}.Choose(context.Background(), g, alt, seat, p, rand.New(rand.NewSource(int64(step))))
		if err != nil {
			t.Fatal(err)
		}
		if a.Type != b.Type || a.Args["to"] != b.Args["to"] {
			t.Fatalf("decision depends on hidden cards: %+v vs %+v", a, b)
		}
		checked++
		specs, _ := g.Legal(st, seat)
		st, _, _ = g.Apply(st, seat, randomMove(rng, specs))
	}
	if checked < 150 {
		t.Fatalf("only %d decisions checked", checked)
	}
}

func TestSharkBeatsRandomPlayer(t *testing.T) {
	g := New()
	shark := PersonaFor("nova", 2)
	profit, hands := 0, 0
	for game := 0; hands < 300; game++ {
		st, err := g.Setup(games.Config{Seats: 2, Options: map[string]any{
			"starting_stack": 2000, "blinds_double_every": 0, "max_hands": 60}}, int64(1000+game))
		if err != nil {
			t.Fatal(err)
		}
		sharkSeat := game % 2
		brng, rrng := rand.New(rand.NewSource(int64(game))), rand.New(rand.NewSource(int64(-game)))
		for {
			toMove, _ := g.ToMove(st)
			if len(toMove) == 0 {
				break
			}
			seat := toMove[0]
			specs, _ := g.Legal(st, seat)
			var m games.Move
			if seat == sharkSeat {
				m, err = Brain{}.Choose(context.Background(), g, st, seat, shark, brng)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				m = moveFromSpec(rrng, specs[rrng.Intn(len(specs))]) // uniformly random
			}
			if st, _, err = g.Apply(st, seat, m); err != nil {
				t.Fatal(err)
			}
		}
		s := decodeT(t, st)
		profit += s.Players[sharkSeat].Stack - 2000
		hands += s.HandNo
	}
	t.Logf("shark profit over %d hands: %+d chips (%.1f bb/hand)", hands, profit, float64(profit)/20/float64(hands))
	if profit <= 0 {
		t.Fatalf("shark lost %d chips to a random player over %d hands", -profit, hands)
	}
}

// spot is a reusable decision situation for persona statistics.
func spot() *situation {
	return &situation{
		street: streetPreflop, stack: 1980, bet: 20, currentBet: 20, pot: 30, bb: 20,
		opponents: 5, position: 0.8, effStack: 2000,
	}
}

type tally struct{ fold, call, raise, shove int }

func (t tally) vpip() float64 {
	return float64(t.call+t.raise+t.shove) / float64(t.fold+t.call+t.raise+t.shove)
}
func (t tally) aggr() float64 {
	return float64(t.raise+t.shove) / float64(max(1, t.call+t.raise+t.shove))
}

func (t *tally) add(d decision) {
	switch d.kind {
	case passive:
		t.fold++
	case callIt:
		t.call++
	case raiseIt:
		t.raise++
	case shove:
		t.shove++
	}
}

func randomHole(rng *rand.Rand) []card {
	a := card(rng.Intn(deckSize))
	b := card(rng.Intn(deckSize - 1))
	if b >= a {
		b++
	}
	return []card{a, b}
}

func TestPersonasPlayDistinctly(t *testing.T) {
	const n = 6000
	pre := map[string]tally{}
	callDown := map[string]float64{} // continues facing a pot-sized bet with a marginal hand
	bluffs := map[string]float64{}   // bets with weak equity when checked to
	for _, id := range roster {
		p := PersonaFor(id, 1)
		rng := rand.New(rand.NewSource(5))
		var tl tally
		calls, bets := 0, 0
		for i := 0; i < n; i++ {
			sit := spot()
			sit.hole = randomHole(rng)
			tl.add(decidePreflop(sit, styleFor(p, sit), rng))

			// A pot-sized bet needs 33% equity; the hand has 25-40%.
			post := &situation{street: streetTurn, stack: 1500, pot: 800, bb: 20, opponents: 1, position: 0.5,
				effStack: 1500, toCall: 400, currentBet: 400}
			if d := decidePostflop(post, styleFor(p, post), 0.25+0.15*rng.Float64(), rng); d.kind != passive {
				calls++
			}
			post.toCall, post.currentBet, post.pot = 0, 0, 400
			if d := decidePostflop(post, styleFor(p, post), 0.15+0.15*rng.Float64(), rng); d.kind == raiseIt || d.kind == shove {
				bets++
			}
		}
		pre[id] = tl
		callDown[id] = float64(calls) / n
		bluffs[id] = float64(bets) / n
		t.Logf("%-5s vpip %.2f  aggr %.2f  call-down %.2f  bluff %.2f", id, tl.vpip(), tl.aggr(), callDown[id], bluffs[id])
	}
	gt := func(what string, a, b string, x, y float64) {
		t.Helper()
		if !(x > y) {
			t.Errorf("%s: %s (%.2f) should exceed %s (%.2f)", what, a, x, b, y)
		}
	}
	// Loose players play more hands than tight ones.
	for _, loose := range []string{"mika", "bram"} {
		for _, tight := range []string{"ren", "lin", "nova"} {
			gt("vpip", loose, tight, pre[loose].vpip(), pre[tight].vpip())
		}
	}
	gt("vpip", "nova", "lin", pre["nova"].vpip(), pre["lin"].vpip())
	// Aggressive players raise rather than call.
	for _, aggr := range []string{"mika", "ren"} {
		for _, passive := range []string{"bram", "lin"} {
			gt("aggression", aggr, passive, pre[aggr].aggr(), pre[passive].aggr())
		}
	}
	// Bram is the calling station; Lin and Ren let go.
	gt("call-down", "bram", "lin", callDown["bram"], callDown["lin"])
	gt("call-down", "bram", "ren", callDown["bram"], callDown["ren"])
	// Mika bluffs the most, Lin and Bram almost never.
	for _, other := range []string{"ren", "nova", "lin", "bram"} {
		gt("bluff", "mika", other, bluffs["mika"], bluffs[other])
	}
	gt("bluff", "ren", "lin", bluffs["ren"], bluffs["lin"])
}

func TestAoiAdapts(t *testing.T) {
	p := PersonaFor("aoi", 1)
	hu := styleFor(p, &situation{opponents: 1, effStack: 2000, bb: 20})
	crowd := styleFor(p, &situation{opponents: 5, effStack: 2000, bb: 20})
	if !(hu.aggr > crowd.aggr && hu.tight < crowd.tight && hu.bluff > crowd.bluff) {
		t.Fatalf("heads-up %+v vs multiway %+v", hu, crowd)
	}
	if n := styleFor(PersonaFor("nova", 1), &situation{opponents: 1, effStack: 2000, bb: 20}); n.aggr != 0.55 {
		t.Fatalf("only Aoi adapts, nova got %+v", n)
	}
}

func TestBrainSizing(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	for _, id := range roster {
		p := PersonaFor(id, 1)
		for i := 0; i < 3000; i++ {
			sit := spot()
			sit.hole = randomHole(rng)
			if d := decidePreflop(sit, styleFor(p, sit), rng); d.kind == raiseIt && (d.to < 50 || d.to > 60) {
				t.Fatalf("%s opened to %d, want 2.5-3 big blinds", id, d.to)
			}
			// Continuation bets are half to three-quarters of the pot.
			post := &situation{street: streetFlop, stack: 1800, pot: 120, bb: 20, opponents: 1, position: 1,
				effStack: 1800, aggressor: true}
			if d := decidePostflop(post, styleFor(p, post), rng.Float64(), rng); d.kind == raiseIt && (d.to < 60 || d.to > 90) {
				t.Fatalf("%s bet %d into 120", id, d.to)
			}
			// Short-stacked preflop: shove or get out.
			short := spot()
			short.stack, short.effStack = 200, 220
			short.hole = randomHole(rng)
			if d := decidePreflop(short, styleFor(p, short), rng); d.kind == raiseIt || d.kind == callIt {
				t.Fatalf("%s with 11bb chose %+v", id, d)
			}
		}
	}
	// Premium hands shove when short.
	short := spot()
	short.stack, short.effStack, short.hole = 200, 220, mustCards(t, "As Ad")
	if d := decidePreflop(short, styleFor(PersonaFor("lin", 1), short), rng); d.kind != shove {
		t.Fatalf("aces at 11bb: %+v", d)
	}
}

func TestBrainHonoursDeadline(t *testing.T) {
	g := New()
	s := tableWith(t, []int{1000, 1000, 1000}, 10, 20)
	s, _ = play(t, s, 0, "call")
	s, _ = play(t, s, 1, "call")
	s, _ = play(t, s, 2, "check") // flop: Monte Carlo territory
	st := encodeT(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	m, err := Brain{}.Choose(ctx, g, st, s.ToAct, PersonaFor("ren", 2), rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	specs, _ := g.Legal(st, s.ToAct)
	if !isLegal(specs, m) {
		t.Fatalf("illegal %+v", m)
	}
	if d := time.Since(start); d > 50*time.Millisecond && !raceEnabled {
		t.Fatalf("cancelled context still took %v", d)
	}
	if _, err := (Brain{}).Choose(context.Background(), g, st, (s.ToAct+1)%3, PersonaFor("ren", 2), nil); err == nil {
		t.Fatal("brain moved out of turn")
	}
}

func TestMenuPickAlwaysLegal(t *testing.T) {
	menus := [][]games.MoveSpec{
		{{Type: "fold"}, {Type: "call"}},
		{{Type: "fold"}, {Type: "call"}, {Type: "allin"}},
		{{Type: "check"}, {Type: "allin"}},
		{{Type: "check"}, {Type: "raise", Range: &games.Range{Arg: "to", Min: 40, Max: 500}}, {Type: "allin"}},
		{{Type: "fold"}, {Type: "call"}, {Type: "raise", Range: &games.Range{Arg: "to", Min: 400, Max: 400}}, {Type: "allin"}},
	}
	for _, specs := range menus {
		for _, d := range []decision{{passive, 0}, {callIt, 0}, {raiseIt, 10}, {raiseIt, 100}, {raiseIt, 10_000}, {shove, 0}} {
			if m := newMenu(specs).pick(d); !isLegal(specs, m) {
				t.Errorf("menu %+v decision %+v gave %+v", specs, d, m)
			}
		}
	}
}

func TestEquityEstimates(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(3))
	aa := estimateEquity(ctx, mustCards(t, "As Ah"), nil, 1, 20000, rng)
	if aa < 0.83 || aa > 0.87 {
		t.Fatalf("AA vs one random hand: %.3f, want about 0.85", aa)
	}
	aa3 := estimateEquity(ctx, mustCards(t, "As Ah"), nil, 3, 20000, rng)
	if aa3 < 0.60 || aa3 > 0.68 {
		t.Fatalf("AA vs three random hands: %.3f, want about 0.64", aa3)
	}
	nuts := estimateEquity(ctx, mustCards(t, "As Ks"), mustCards(t, "Qs Js Ts 2d 3c"), 4, 2000, rng)
	if nuts != 1 {
		t.Fatalf("royal flush equity %.3f", nuts)
	}
	if preflopStrength(newCard(12, 0), newCard(12, 1)) != 1 {
		t.Fatal("aces must be the top starting hand")
	}
	if s := preflopStrength(mustCards(t, "7c")[0], mustCards(t, "2d")[0]); s > 0.05 {
		t.Fatalf("72o percentile %.3f", s)
	}
}

func TestCoach(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000}, 10, 20)
	s.Players[0].Cards = mustCards(t, "As Ad")
	st := encodeT(t, s)
	tip, err := Coach(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if tip.HandName != "Pair of Aces" || tip.Opponents != 2 || tip.Equity < 0.68 || tip.Equity > 0.78 ||
		tip.ToCall != 20 || tip.PotOdds != 0.4 || tip.Suggestion == "" {
		t.Fatalf("tip %+v", tip)
	}
	// Deterministic, and independent of the opponents' actual cards.
	again, _ := Coach(st, 0)
	s.Players[1].Cards = mustCards(t, "Kc Kd")
	s.Players[2].Cards = mustCards(t, "Ks Kh")
	other, _ := Coach(encodeT(t, s), 0)
	if again != tip || other != tip {
		t.Fatalf("tips differ: %+v / %+v / %+v", tip, again, other)
	}
	// Postflop facing a bet, with a weak hand: fold advice.
	s = tableWith(t, []int{1000, 1000}, 10, 20)
	s, _ = play(t, s, 0, "call")
	s, _ = play(t, s, 1, "check")
	s.Players[0].Cards = mustCards(t, "7c 2d")
	s.Board = mustCards(t, "As Kh Qd")
	s, _ = play(t, s, 1, "raise", 40)
	tip, err = Coach(encodeT(t, s), 0)
	if err != nil {
		t.Fatal(err)
	}
	if tip.PotOdds != 0.333 || tip.Equity > 0.3 || tip.Suggestion[:7] != "Folding" {
		t.Fatalf("weak-hand tip %+v", tip)
	}
	if _, err := Coach(encodeT(t, s), games.Spectator); err == nil {
		t.Fatal("coaching a spectator")
	}
}

func TestCoachRefusesFoldedSeat(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000}, 10, 20)
	s, _ = play(t, s, 0, "fold")
	if _, err := Coach(encodeT(t, s), 0); err == nil {
		t.Fatal("coached a folded seat")
	}
}
