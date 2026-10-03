package holdem

import (
	"reflect"
	"strings"
	"testing"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

func TestRegistered(t *testing.T) {
	g, ok := games.Builtin("holdem")
	if !ok {
		t.Fatal("holdem is not registered")
	}
	m := g.Meta()
	if m.ID != "holdem" || m.MinSeats != 2 || m.MaxSeats != 9 || !m.HiddenInfo {
		t.Fatalf("meta %+v", m)
	}
	want := map[string]any{"starting_stack": 1000, "small_blind": 10, "big_blind": 20,
		"blinds_double_every": 10, "max_hands": 0}
	if !reflect.DeepEqual(m.Options, want) {
		t.Fatalf("options %v, want %v", m.Options, want)
	}
}

func TestSetupValidation(t *testing.T) {
	g := New()
	bad := []games.Config{
		{Seats: 1}, {Seats: 10},
		{Seats: 2, Options: map[string]any{"big_blind": 5}}, // below the small blind
		{Seats: 2, Options: map[string]any{"starting_stack": 0}},
		{Seats: 2, Options: map[string]any{"small_blind": "ten"}},
		{Seats: 2, Options: map[string]any{"small_blind": 2.5}},
		{Seats: 2, Options: map[string]any{"max_hands": -1}},
	}
	for _, cfg := range bad {
		if _, err := g.Setup(cfg, 1); err == nil {
			t.Errorf("Setup(%+v) accepted", cfg)
		}
	}
	// JSON-decoded numbers arrive as float64 and must be accepted.
	st, err := g.Setup(games.Config{Seats: 3, Options: map[string]any{
		"starting_stack": 500.0, "small_blind": 5.0, "big_blind": 10.0, "turn_seconds": 30}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	s := decodeT(t, st)
	if s.Opts.StartingStack != 500 || s.BigBlind != 10 || chips(s) != 1500 {
		t.Fatalf("options not applied: %+v", s.Opts)
	}
}

func TestHeadsUpBlindsAndActionOrder(t *testing.T) {
	st, err := New().Setup(games.Config{Seats: 2}, 9)
	if err != nil {
		t.Fatal(err)
	}
	s := decodeT(t, st)
	// The button posts the small blind and acts first preflop.
	if s.Button != 0 || s.SB != 0 || s.BB != 1 || s.ToAct != 0 {
		t.Fatalf("button %d sb %d bb %d to act %d", s.Button, s.SB, s.BB, s.ToAct)
	}
	if s.Players[0].Bet != 10 || s.Players[1].Bet != 20 {
		t.Fatalf("blinds %d/%d", s.Players[0].Bet, s.Players[1].Bet)
	}
	lt := legalTypes(t, s, 0)
	if lt["call"].Label != "Call 10" || lt["raise"].Label != "Raise to" ||
		lt["raise"].Range.Min != 40 || lt["raise"].Range.Max != 1000 || lt["raise"].Range.Arg != "to" {
		t.Fatalf("preflop menu %+v", lt)
	}
	if _, ok := lt["check"]; ok {
		t.Fatal("small blind may not check facing the big blind")
	}

	s, _ = play(t, s, 0, "call")
	if s.ToAct != 1 {
		t.Fatalf("big blind should get the option, to act %d", s.ToAct)
	}
	s, ev := play(t, s, 1, "check")
	if s.Street != streetFlop || len(s.Board) != 3 || len(eventsOfType(ev, "street")) != 1 {
		t.Fatalf("expected the flop, got %s %v", s.Street, s.Board)
	}
	// Postflop the big blind acts first and the button last.
	if s.ToAct != 1 {
		t.Fatalf("postflop first to act %d, want the big blind", s.ToAct)
	}
	if lt := legalTypes(t, s, 1); lt["raise"].Label != "Bet" || lt["raise"].Range.Min != 20 {
		t.Fatalf("flop menu %+v", lt["raise"])
	}
	s, _ = play(t, s, 1, "check")
	if s.ToAct != 0 {
		t.Fatalf("button should act last, to act %d", s.ToAct)
	}
	s, _ = play(t, s, 0, "check")
	if s.Street != streetTurn || s.ToAct != 1 {
		t.Fatalf("turn: street %s to act %d", s.Street, s.ToAct)
	}

	// An uncontested pot: nothing is shown, the uncalled bet comes back.
	s, _ = play(t, s, 1, "raise", 60)
	s, ev = play(t, s, 0, "fold")
	if len(eventsOfType(ev, "refund")) != 1 || len(eventsOfType(ev, "showdown")) != 0 {
		t.Fatalf("events %+v", ev)
	}
	win := eventsOfType(ev, "win")
	if len(win) != 1 || win[0].Text != "{s:1} wins 40" {
		t.Fatalf("win events %+v", win)
	}
	lh := s.LastHand
	if lh == nil || lh.HandNo != 1 || len(lh.Winners) != 1 || lh.Winners[0].Seat != 1 ||
		lh.Winners[0].Amount != 40 || lh.Winners[0].Cards != nil || len(lh.Shown) != 0 {
		t.Fatalf("last hand %+v", lh)
	}
	// Hand 2 was dealt in the same Apply; the button moved to seat 1.
	if s.HandNo != 2 || s.Button != 1 || s.SB != 1 || s.BB != 0 || s.ToAct != 1 {
		t.Fatalf("hand 2: hand %d button %d sb %d bb %d to act %d", s.HandNo, s.Button, s.SB, s.BB, s.ToAct)
	}
	if got := chips(s); got != 2000 {
		t.Fatalf("chips %d", got)
	}
}

func TestBetAndRaiseLabels(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000}, 10, 20)
	if s.ToAct != 0 {
		t.Fatalf("3-handed, the button acts first preflop; got %d", s.ToAct)
	}
	if lt := legalTypes(t, s, 0); lt["raise"].Label != "Raise to" {
		t.Fatalf("preflop label %q", lt["raise"].Label)
	}
	s, _ = play(t, s, 0, "call")
	s, _ = play(t, s, 1, "call")
	s, _ = play(t, s, 2, "check")
	if s.Street != streetFlop || s.ToAct != 1 {
		t.Fatalf("flop: street %s to act %d", s.Street, s.ToAct)
	}
	lt := legalTypes(t, s, 1)
	if lt["raise"].Label != "Bet" || lt["raise"].Range.Min != 20 || lt["raise"].Range.Max != 980 {
		t.Fatalf("flop bet spec %+v", lt["raise"])
	}
	if _, ok := lt["fold"]; ok {
		t.Fatal("fold is offered when checking is free")
	}
	s, ev := play(t, s, 1, "raise", 50)
	if ev[0].Type != "bet" || ev[0].Text != "{s:1} bets 50" {
		t.Fatalf("bet event %+v", ev[0])
	}
	lt = legalTypes(t, s, 2)
	if lt["raise"].Label != "Raise to" || lt["raise"].Range.Min != 100 {
		t.Fatalf("raise spec %+v", lt["raise"])
	}
	_, ev = play(t, s, 2, "raise", 120)
	if ev[0].Text != "{s:2} raises to 120" {
		t.Fatalf("raise text %q", ev[0].Text)
	}
}

func TestFullRaiseReopensAction(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000}, 10, 20)
	s, _ = play(t, s, 0, "raise", 60) // raise of 40
	s, _ = play(t, s, 1, "call")
	mustReject(t, s, 2, games.Move{Type: "raise", Args: map[string]any{"to": 99}}) // below 60+40
	s, _ = play(t, s, 2, "raise", 200)                                             // full raise of 140
	if s.ToAct != 0 {
		t.Fatalf("to act %d", s.ToAct)
	}
	lt := legalTypes(t, s, 0)
	if lt["raise"].Range == nil || lt["raise"].Range.Min != 340 {
		t.Fatalf("a full raise must reopen the action with min 340, got %+v", lt)
	}
	if _, ok := lt["allin"]; !ok {
		t.Fatal("all-in should be open after a full raise")
	}
}

func TestShortAllinDoesNotReopen(t *testing.T) {
	// Seat 2 is the big blind with 90 chips.
	s := tableWith(t, []int{1000, 1000, 90}, 10, 20)
	s, _ = play(t, s, 0, "raise", 60) // min raise is now 40
	s, _ = play(t, s, 1, "call")
	s, ev := play(t, s, 2, "allin") // to 90: a raise of 30, short of 40
	if ev[0].Type != "allin" || ev[0].Text != "{s:2} raises to 90 and is all-in" {
		t.Fatalf("all-in event %+v", ev[0])
	}
	for _, seat := range []int{0, 1} {
		if s.ToAct != seat {
			t.Fatalf("to act %d, want %d", s.ToAct, seat)
		}
		lt := legalTypes(t, s, seat)
		if len(lt) != 2 || lt["fold"].Type == "" || lt["call"].Label != "Call 30" {
			t.Fatalf("seat %d may only call or fold, got %+v", seat, lt)
		}
		mustReject(t, s, seat, games.Move{Type: "raise", Args: map[string]any{"to": 200}})
		mustReject(t, s, seat, games.Move{Type: "allin"})
		s, _ = play(t, s, seat, "call")
	}
	if s.Street != streetFlop || s.ToAct != 1 {
		t.Fatalf("flop: %s to act %d", s.Street, s.ToAct)
	}
	if lt := legalTypes(t, s, 1); lt["raise"].Label != "Bet" {
		t.Fatalf("a new street reopens betting, got %+v", lt)
	}
}

func TestShortAllinsAddUpToAFullRaise(t *testing.T) {
	// Button 0, small blind 1 (85 chips), big blind 2 (120 chips), UTG 3.
	s := tableWith(t, []int{1000, 85, 120, 1000}, 10, 20)
	if s.ToAct != 3 {
		t.Fatalf("UTG to act, got %d", s.ToAct)
	}
	s, _ = play(t, s, 3, "raise", 60) // raise of 40
	s, _ = play(t, s, 0, "call")
	s, _ = play(t, s, 1, "allin") // 85: short by 15
	// The big blind has not acted yet, so it may still raise normally.
	if lt := legalTypes(t, s, 2); lt["raise"].Range != nil {
		t.Fatalf("BB has only 120: no full raise to 125 is possible, got %+v", lt["raise"])
	} else if _, ok := lt["allin"]; !ok {
		t.Fatal("BB may move all-in")
	}
	s, _ = play(t, s, 2, "allin") // 120: two short all-ins, 60 above seat 3's 60
	if s.ToAct != 3 {
		t.Fatalf("to act %d", s.ToAct)
	}
	lt := legalTypes(t, s, 3)
	if lt["raise"].Range == nil || lt["raise"].Range.Min != 160 {
		t.Fatalf("cumulative short all-ins worth a full raise must reopen, got %+v", lt)
	}
}

func TestOnlyCallOrFoldAgainstLoneAllin(t *testing.T) {
	st, _ := New().Setup(games.Config{Seats: 2}, 3)
	s := decodeT(t, st)
	s, _ = play(t, s, 0, "allin")
	lt := legalTypes(t, s, 1)
	if _, ok := lt["raise"]; ok || lt["call"].Label != "Call 980 (all-in)" {
		t.Fatalf("menu %+v", lt)
	}
	if lt["allin"].Label != "All-in 980" {
		t.Fatalf("all-in for a call should be offered: %+v", lt)
	}
	s, ev := play(t, s, 1, "call")
	if len(eventsOfType(ev, "showdown")) != 2 || s.LastHand == nil || len(s.LastHand.Board) != 5 {
		t.Fatalf("expected a run-out and showdown, events %+v", ev)
	}
}

// TestMultiwayAllinSidePots: five all-in players with stacks 100..500: one main pot and
// three side pots, each won by a different seat.
func TestMultiwayAllinSidePots(t *testing.T) {
	s := tableWith(t, []int{100, 200, 300, 400, 500}, 10, 20)
	s.Board = mustCards(t, "2c 2d 7h 9s Kd")
	hole := []string{"Kh Ks", "9h 9c", "7c 7s", "Ah Qd", "Jh Tc"}
	for i, h := range hole {
		s.Players[i].Cards = mustCards(t, h)
	}
	var ev []games.Event
	for _, seat := range []int{3, 4, 0, 1, 2} {
		if s.ToAct != seat {
			t.Fatalf("to act %d, want %d", s.ToAct, seat)
		}
		var e []games.Event
		s, e = play(t, s, seat, "allin")
		ev = append(ev, e...)
	}
	refund := eventsOfType(ev, "refund")
	if len(refund) != 1 || refund[0].Seat != 4 || refund[0].Data["amount"] != 100 {
		t.Fatalf("refund %+v", refund)
	}
	wantWins := []string{
		"{s:0} wins 500 from the main pot with Full House, Kings full of Twos",
		"{s:1} wins 400 from side pot 1 with Full House, Nines full of Twos",
		"{s:2} wins 300 from side pot 2 with Full House, Sevens full of Twos",
		"{s:3} wins 200 from side pot 3 with Pair of Twos",
	}
	var gotWins []string
	for _, e := range eventsOfType(ev, "win") {
		gotWins = append(gotWins, e.Text)
	}
	if !reflect.DeepEqual(gotWins, wantWins) {
		t.Fatalf("wins:\n%s\nwant:\n%s", strings.Join(gotWins, "\n"), strings.Join(wantWins, "\n"))
	}
	// Seat 4's excess came back, so it is not all-in at showdown; it lost
	// every pot it was in and mucks.
	if n := len(eventsOfType(ev, "showdown")); n != 4 {
		t.Fatalf("%d showdown reveals, want 4", n)
	}
	if m := eventsOfType(ev, "muck"); len(m) != 1 || m[0].Seat != 4 {
		t.Fatalf("mucks %+v", m)
	}
	lh := s.LastHand
	if len(lh.Winners) != 4 || len(lh.Shown) != 4 || lh.Winners[0].Cards[0] != "Kh" ||
		lh.Winners[0].HandName != "Full House, Kings full of Twos" {
		t.Fatalf("last hand %+v", lh)
	}
	// The next hand has started; each seat's chips are what it won.
	for i, want := range []int{500, 400, 300, 200, 100} {
		if got := s.Players[i].Stack + s.Players[i].TotalBet; got != want {
			t.Errorf("seat %d has %d chips, want %d", i, got, want)
		}
	}
}

func TestBuildPotsLayers(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000, 1000, 1000}, 10, 20)
	for i, tb := range []int{100, 200, 300, 400, 400} {
		s.Players[i].TotalBet, s.Players[i].Status = tb, statusAllin
	}
	s.Players[4].Status = statusFolded
	pots := s.buildPots(func(p *player) int { return p.TotalBet })
	want := []pot{
		{500, []int{0, 1, 2, 3}},
		{400, []int{1, 2, 3}},
		{300, []int{2, 3}},
		{200, []int{3}},
	}
	if !reflect.DeepEqual(pots, want) {
		t.Fatalf("pots %+v, want %+v", pots, want)
	}
}

func TestOddChipGoesLeftOfButton(t *testing.T) {
	for _, tc := range []struct{ button, bigShare int }{{0, 1}, {2, 3}} {
		s := tableWith(t, []int{1000, 1000, 1000, 1000}, 10, 20)
		s.Street, s.Button, s.ToAct = streetRiver, tc.button, 1
		s.Board = mustCards(t, "2c 7d 9h Jc Kd")
		hole := []string{"5h 4h", "Ah 3s", "Qh 4s", "Ad 3h"}
		for i := range s.Players {
			p := &s.Players[i]
			p.Cards, p.Bet, p.TotalBet, p.Stack, p.Status = mustCards(t, hole[i]), 0, 50, 950, statusActive
		}
		s.Players[0].TotalBet, s.Players[0].Stack, s.Players[0].Status = 5, 995, statusFolded
		s.ev = nil
		s.settle()
		won := map[int]int{}
		for _, e := range eventsOfType(s.ev, "win") {
			won[e.Seat] += e.Data["amount"].(int)
		}
		other := 4 - tc.bigShare // the other tied seat (1 or 3)
		if won[tc.bigShare] != 78 || won[other] != 77 || len(won) != 2 {
			t.Fatalf("button %d: shares %v, want seat %d 78 and seat %d 77", tc.button, won, tc.bigShare, other)
		}
	}
}

func TestBlindsDoubleAndGameEndsAtMaxHands(t *testing.T) {
	g := New()
	st, err := g.Setup(games.Config{Seats: 3, Options: map[string]any{"blinds_double_every": 2, "max_hands": 3}}, 5)
	if err != nil {
		t.Fatal(err)
	}
	var all []games.Event
	for i := 0; i < 100; i++ {
		seats, _ := g.ToMove(st)
		if len(seats) == 0 {
			break
		}
		m, err := g.DefaultMove(st, seats[0])
		if err != nil {
			t.Fatal(err)
		}
		var ev []games.Event
		st, ev, err = g.Apply(st, seats[0], m)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, ev...)
	}
	up := eventsOfType(all, "blinds_up")
	if len(up) != 1 || up[0].Text != "Blinds up to 20/40" {
		t.Fatalf("blinds_up events %+v", up)
	}
	s := decodeT(t, st)
	if s.Street != streetOver || s.HandNo != 3 || s.ToAct != -1 {
		t.Fatalf("street %s hand %d", s.Street, s.HandNo)
	}
	if over := eventsOfType(all, "game_over"); len(over) != 1 {
		t.Fatalf("game_over events %+v", over)
	}
	out, err := g.Outcome(st)
	if err != nil || out == nil {
		t.Fatalf("outcome %v %v", out, err)
	}
	total := 0.0
	for _, sc := range out.Score {
		total += sc
	}
	if total != 3000 || len(out.Rank) != 3 || !strings.Contains(out.Summary, "{s:") {
		t.Fatalf("outcome %+v", out)
	}
	if seats, _ := g.ToMove(st); len(seats) != 0 {
		t.Fatal("ToMove after the end")
	}
	if l, _ := g.Legal(st, 0); len(l) != 0 {
		t.Fatal("Legal after the end")
	}
	mustReject(t, s, 0, games.Move{Type: "check"})
	v := viewOf(t, st, 0)
	if v.Street != "over" || v.ToAct != -1 || *v.HandsLeft != 0 {
		t.Fatalf("final view %+v", v)
	}
}

func TestRankingTiesAndBustOrder(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000, 1000, 1000}, 10, 20)
	s.Players[0] = player{Stack: 2000, Status: statusActive}
	s.Players[1] = player{Stack: 2000, Status: statusActive}
	s.Players[2] = player{Status: statusOut, BustHand: 9, StartStack: 300}
	s.Players[3] = player{Status: statusOut, BustHand: 9, StartStack: 700}
	s.Players[4] = player{Status: statusOut, BustHand: 4, StartStack: 100}
	if got, want := s.ranking(), []int{1, 1, 4, 3, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ranking %v, want %v", got, want)
	}
	if got := s.summary(); !strings.HasPrefix(got, "{s:0} and {s:1} tie with 2000 chips") {
		t.Fatalf("summary %q", got)
	}
}

func TestGameEndsWhenOnePlayerHasEveryChip(t *testing.T) {
	g := New()
	st, _ := g.Setup(games.Config{Seats: 3}, 11)
	for i := 0; i < 1000; i++ {
		seats, _ := g.ToMove(st)
		if len(seats) == 0 {
			break
		}
		var err error
		st, _, err = g.Apply(st, seats[0], games.Move{Type: "allin"})
		if err != nil {
			// All-in is closed for this seat (a short all-in in front): call.
			st, _, err = g.Apply(st, seats[0], games.Move{Type: "call"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	out, err := g.Outcome(st)
	if err != nil || out == nil {
		t.Fatal("game did not end")
	}
	s := decodeT(t, st)
	alive := 0
	for i, p := range s.Players {
		if p.Status != statusOut {
			alive++
			if out.Rank[i] != 1 || p.Stack != 3000 {
				t.Fatalf("winner seat %d rank %d stack %d", i, out.Rank[i], p.Stack)
			}
		} else if out.Rank[i] == 1 {
			t.Fatalf("busted seat %d ranked first", i)
		}
	}
	if alive != 1 || !strings.Contains(out.Summary, "wins with all 3000 chips") {
		t.Fatalf("alive %d summary %q", alive, out.Summary)
	}
}

func TestMalformedMovesAreRejected(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000}, 10, 20)
	bad := []struct {
		seat int
		m    games.Move
	}{
		{1, games.Move{Type: "call"}}, // not your turn
		{-1, games.Move{Type: "call"}},
		{7, games.Move{Type: "call"}},
		{0, games.Move{Type: "check"}}, // facing the big blind
		{0, games.Move{Type: "bet"}},
		{0, games.Move{Type: ""}},
		{0, games.Move{Type: "raise"}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": "100"}}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": 100.5}}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": nil}}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": true}}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": []any{100}}}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": 39}}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": 1001}}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": -5}}},
		{0, games.Move{Type: "raise", Args: map[string]any{"to": 1e300}}},
	}
	for _, b := range bad {
		mustReject(t, s, b.seat, b.m)
	}
	// A whole float, as JSON delivers it, is fine.
	if _, _, err := New().Apply(encodeT(t, s), 0, games.Move{Type: "raise", Args: map[string]any{"to": 100.0}}); err != nil {
		t.Fatal(err)
	}
	// Corrupt states are errors, never panics.
	for _, st := range []string{``, `null`, `{}`, `[]`, `{"v":1,"players":[{},{}],"to_act":5,"big_blind":20,"min_raise":20}`,
		`{"v":1,"players":[{"cards":["Zz"]},{}],"big_blind":20,"min_raise":20}`} {
		g := New()
		if _, _, err := g.Apply(games.State(st), 0, games.Move{Type: "fold"}); err == nil {
			t.Errorf("Apply on %q accepted", st)
		}
		if _, err := g.View(games.State(st), 0); err == nil {
			t.Errorf("View on %q accepted", st)
		}
	}
}

func TestDefaultMove(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000}, 10, 20)
	g := New()
	m, err := g.DefaultMove(encodeT(t, s), 0)
	if err != nil || m.Type != "fold" {
		t.Fatalf("facing a bet the default is fold, got %v %v", m, err)
	}
	s, _ = play(t, s, 0, "call")
	s, _ = play(t, s, 1, "call")
	if m, _ := g.DefaultMove(encodeT(t, s), 2); m.Type != "check" {
		t.Fatalf("big blind option default %v", m)
	}
	if _, err := g.DefaultMove(encodeT(t, s), 0); err == nil {
		t.Fatal("DefaultMove for a seat that is not to act")
	}
}
