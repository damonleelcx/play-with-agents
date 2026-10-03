package holdem

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// randomMove picks a legal move with weights that keep games long enough to
// reach every street: mostly checks and calls, some raises, few all-ins.
func randomMove(rng *rand.Rand, specs []games.MoveSpec) games.Move {
	weight := map[string]int{"fold": 15, "check": 40, "call": 40, "raise": 12, "allin": 2}
	total := 0
	for _, sp := range specs {
		total += weight[sp.Type]
	}
	r := rng.Intn(total)
	for _, sp := range specs {
		if r -= weight[sp.Type]; r < 0 {
			return moveFromSpec(rng, sp)
		}
	}
	return moveFromSpec(rng, specs[0])
}

func moveFromSpec(rng *rand.Rand, sp games.MoveSpec) games.Move {
	m := games.Move{Type: sp.Type}
	if sp.Range != nil {
		lo, hi := sp.Range.Min, sp.Range.Max
		var to int
		switch rng.Intn(4) {
		case 0:
			to = lo
		case 1:
			to = hi
		default:
			to = lo + rng.Intn(hi-lo+1)
		}
		// Half the time send it the way decoded JSON would: a float64.
		if rng.Intn(2) == 0 {
			m.Args = map[string]any{sp.Range.Arg: float64(to)}
		} else {
			m.Args = map[string]any{sp.Range.Arg: to}
		}
	}
	return m
}

// illegalMoves builds moves that must be refused in the current position.
func illegalMoves(s *state, specs []games.MoveSpec) []struct {
	seat int
	m    games.Move
} {
	type sm = struct {
		seat int
		m    games.Move
	}
	var out []sm
	have := map[string]*games.MoveSpec{}
	for i := range specs {
		have[specs[i].Type] = &specs[i]
	}
	for _, typ := range []string{"fold", "check", "call", "allin"} {
		if have[typ] == nil {
			out = append(out, sm{s.ToAct, games.Move{Type: typ}})
		}
	}
	if r := have["raise"]; r != nil {
		out = append(out,
			sm{s.ToAct, games.Move{Type: "raise", Args: map[string]any{"to": r.Range.Min - 1}}},
			sm{s.ToAct, games.Move{Type: "raise", Args: map[string]any{"to": r.Range.Max + 1}}},
			sm{s.ToAct, games.Move{Type: "raise", Args: map[string]any{"to": float64(r.Range.Min) + 0.5}}},
			sm{s.ToAct, games.Move{Type: "raise", Args: map[string]any{"to": "all"}}},
			sm{s.ToAct, games.Move{Type: "raise"}})
	} else {
		out = append(out, sm{s.ToAct, games.Move{Type: "raise", Args: map[string]any{"to": s.CurrentBet + s.MinRaise}}})
	}
	other := (s.ToAct + 1) % s.n()
	out = append(out, sm{other, specsMove(specs[0])}, sm{s.ToAct, games.Move{Type: "muck"}})
	return out
}

func specsMove(sp games.MoveSpec) games.Move {
	m := games.Move{Type: sp.Type}
	if sp.Range != nil {
		m.Args = map[string]any{"to": sp.Range.Min}
	}
	return m
}

// checkInvariants verifies the conservation and consistency rules that must
// hold after every Apply.
func checkInvariants(t *testing.T, s *state, total int) {
	t.Helper()
	if got := chips(s); got != total {
		t.Fatalf("hand %d: chips %d, want %d", s.HandNo, got, total)
	}
	for i, p := range s.Players {
		if p.Stack < 0 || p.Bet < 0 || p.TotalBet < p.Bet {
			t.Fatalf("seat %d: %+v", i, p)
		}
		if p.Status == statusOut && (p.Stack != 0 || p.TotalBet != 0) {
			t.Fatalf("busted seat %d holds chips: %+v", i, p)
		}
		if p.Status == statusAllin && p.Stack != 0 {
			t.Fatalf("all-in seat %d has chips behind: %+v", i, p)
		}
	}
	if s.Street == streetOver {
		return
	}
	if p := s.Players[s.ToAct]; p.Status != statusActive || p.Stack == 0 {
		t.Fatalf("seat to act %d cannot act: %+v", s.ToAct, p)
	}
	if s.Players[s.Button].Status == statusOut || s.Players[s.BB].Status == statusOut {
		t.Fatalf("button or big blind on a busted seat")
	}
}

// checkView verifies that viewer sees no hidden information.
func checkView(t *testing.T, st games.State, s *state, viewer int) {
	t.Helper()
	v, err := New().View(st, viewer)
	if err != nil {
		t.Fatal(err)
	}
	d := v.Data.(ViewData)
	for i, pv := range d.Players {
		own := i == viewer
		if want := (own || s.Players[i].Shown) && len(s.Players[i].Cards) > 0; (pv.Cards != nil) != want {
			t.Fatalf("viewer %d sees seat %d cards=%v (shown=%v)", viewer, i, pv.Cards, s.Players[i].Shown)
		}
		if pv.Cards == nil && pv.HandName != "" {
			t.Fatalf("viewer %d sees seat %d's hand name %q", viewer, i, pv.HandName)
		}
	}
	// No other seat's unshown hole card may appear anywhere in the view
	// apart from last_hand, which describes a previous, finished hand.
	d.LastHand = nil
	b, _ := json.Marshal(d)
	if bytes.Contains(b, []byte("deck")) {
		t.Fatalf("view mentions the deck: %s", b)
	}
	onBoard := map[string]bool{}
	for _, c := range d.Board {
		onBoard[c] = true
	}
	for i, p := range s.Players {
		if i == viewer || p.Shown {
			continue
		}
		for _, c := range p.Cards {
			if !onBoard[c.String()] && bytes.Contains(b, []byte(`"`+c.String()+`"`)) {
				t.Fatalf("viewer %d can see seat %d's %s: %s", viewer, i, c, b)
			}
		}
	}
	if !strings.Contains(v.Status, "{s:") && s.Street != streetOver {
		t.Fatalf("status %q", v.Status)
	}
}

func TestPropertyRandomPlay(t *testing.T) {
	g := New()
	nGames := 320
	if testing.Short() {
		nGames = 60
	}
	rng := rand.New(rand.NewSource(20261002))
	hands, moves := 0, 0
	for gi := 0; gi < nGames; gi++ {
		seats := 2 + gi%8
		opts := map[string]any{
			"starting_stack":      100 * (2 + rng.Intn(20)),
			"small_blind":         5,
			"big_blind":           10,
			"blinds_double_every": rng.Intn(8),
			"max_hands":           20 + rng.Intn(60),
		}
		st, err := g.Setup(games.Config{Seats: seats, Options: opts}, rng.Int63())
		if err != nil {
			t.Fatal(err)
		}
		total := seats * opts["starting_stack"].(int)
		over := false
		for step := 0; step < 50_000; step++ {
			s := decodeT(t, st)
			checkInvariants(t, s, total)
			if step%5 == 0 {
				checkView(t, st, s, rng.Intn(seats))
				checkView(t, st, s, games.Spectator)
			}
			toMove, err := g.ToMove(st)
			if err != nil {
				t.Fatal(err)
			}
			if len(toMove) == 0 {
				out, err := g.Outcome(st)
				if err != nil || out == nil {
					t.Fatalf("game over without outcome: %v", err)
				}
				hands += s.HandNo
				over = true
				break
			}
			seat := toMove[0]
			specs, err := g.Legal(st, seat)
			if err != nil || len(specs) == 0 {
				t.Fatalf("no legal moves for seat %d: %v", seat, err)
			}
			for other := 0; other < seats; other++ {
				if l, _ := g.Legal(st, other); other != seat && len(l) != 0 {
					t.Fatalf("seat %d has moves when it is %d's turn", other, seat)
				}
			}
			// Every legal move is accepted; every illegal one refused.
			if step%4 == 0 {
				for _, sp := range specs {
					if _, _, err := g.Apply(st, seat, moveFromSpec(rng, sp)); err != nil {
						t.Fatalf("legal %+v rejected: %v", sp, err)
					}
				}
				for _, bad := range illegalMoves(s, specs) {
					if _, _, err := g.Apply(st, bad.seat, bad.m); !errors.Is(err, games.ErrIllegal) {
						t.Fatalf("illegal seat %d %+v gave %v (legal: %+v)", bad.seat, bad.m, err, specs)
					}
				}
			}
			m := randomMove(rng, specs)
			next, ev, err := g.Apply(st, seat, m)
			if err != nil {
				t.Fatalf("legal %+v rejected: %v", m, err)
			}
			for _, e := range ev {
				if e.Type == "deal" && (len(e.Only) != 1 || e.Only[0] != e.Seat) {
					t.Fatalf("deal event visible beyond its owner: %+v", e)
				}
				if e.Type != "deal" && len(e.Only) != 0 {
					t.Fatalf("public event restricted: %+v", e)
				}
			}
			st = next
			moves++
		}
		if !over {
			t.Fatalf("game %d (%d seats) did not terminate", gi, seats)
		}
	}
	if !testing.Short() && hands < 2000 {
		t.Fatalf("only %d hands played", hands)
	}
	t.Logf("%d games, %d hands, %d moves", nGames, hands, moves)
}

func TestDeterminism(t *testing.T) {
	run := func(seed int64) []games.State {
		g := New()
		rng := rand.New(rand.NewSource(99))
		st, err := g.Setup(games.Config{Seats: 6, Options: map[string]any{"max_hands": 15}}, seed)
		if err != nil {
			t.Fatal(err)
		}
		out := []games.State{st}
		for {
			seats, _ := g.ToMove(st)
			if len(seats) == 0 {
				return out
			}
			specs, _ := g.Legal(st, seats[0])
			st, _, err = g.Apply(st, seats[0], randomMove(rng, specs))
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, st)
		}
	}
	a, b := run(12345), run(12345)
	if len(a) != len(b) {
		t.Fatalf("different lengths %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			t.Fatalf("state %d differs:\n%s\n%s", i, a[i], b[i])
		}
	}
	c := run(54321)
	if bytes.Equal(a[0], c[0]) {
		t.Fatal("different seeds dealt the same opening state")
	}
	// Each hand's deck depends only on (seed, hand number).
	if handDeck(1, 3) != handDeck(1, 3) || handDeck(1, 3) == handDeck(1, 4) || handDeck(1, 3) == handDeck(2, 3) {
		t.Fatal("handDeck is not a function of (seed, hand)")
	}
}

func TestViewHidesHiddenInformation(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 1000, 1000}, 10, 20)
	st := encodeT(t, s)
	for viewer := -1; viewer < 4; viewer++ {
		checkView(t, st, s, viewer)
		d := viewOf(t, st, viewer)
		for i, p := range d.Players {
			if i == viewer {
				if len(p.Cards) != 2 || p.HandName == "" {
					t.Fatalf("seat %d cannot see its own cards", i)
				}
			} else if p.Cards != nil {
				t.Fatalf("viewer %d sees seat %d", viewer, i)
			}
		}
	}
	// The JSON shape: hidden cards are null, not missing or empty.
	m := jsonRound(t, viewOf(t, st, 0))
	pl := m["players"].([]any)
	if c, ok := pl[1].(map[string]any)["cards"]; !ok || c != nil {
		t.Fatalf("hidden cards must be null, got %v", c)
	}
	for _, key := range []string{"hand_no", "hands_left", "street", "board", "pots", "pot_total", "button",
		"sb_seat", "bb_seat", "small_blind", "big_blind", "current_bet", "min_raise_to", "to_act", "players", "last_hand"} {
		if _, ok := m[key]; !ok {
			t.Errorf("view is missing %q", key)
		}
	}
	if _, err := New().View(st, 4); err == nil {
		t.Fatal("View for a seat that does not exist")
	}

	// After a showdown, the shown hands are visible to everyone through
	// last_hand while the new hand's cards stay private.
	s, _ = play(t, s, 3, "call")
	s, _ = play(t, s, 0, "call")
	s, _ = play(t, s, 1, "call")
	s, _ = play(t, s, 2, "check")
	for s.HandNo == 1 {
		s, _ = play(t, s, s.ToAct, "check")
	}
	st = encodeT(t, s)
	d := viewOf(t, st, games.Spectator)
	// A checked-down hand: the first player left of the button shows, the
	// others show only to beat or tie it (or because they won).
	if d.LastHand == nil || len(d.LastHand.Shown) < 1 || len(d.LastHand.Board) != 5 {
		t.Fatalf("last hand %+v", d.LastHand)
	}
	for _, w := range d.LastHand.Winners {
		found := false
		for _, sh := range d.LastHand.Shown {
			found = found || sh.Seat == w.Seat
		}
		if !found {
			t.Fatalf("winner %d was not shown: %+v", w.Seat, d.LastHand)
		}
	}
	for _, p := range d.Players {
		if p.Cards != nil {
			t.Fatalf("spectator sees seat %d's new hand", p.Seat)
		}
	}
}

func TestViewPotsAndTotals(t *testing.T) {
	s := tableWith(t, []int{1000, 1000, 300}, 10, 20)
	s, _ = play(t, s, 0, "raise", 100)
	s, _ = play(t, s, 1, "call")
	s, _ = play(t, s, 2, "call")
	// Flop: seat 1 bets, seat 2 shoves short, seat 0 calls the shove.
	s, _ = play(t, s, 1, "raise", 100)
	s, _ = play(t, s, 2, "allin") // 200
	st := encodeT(t, s)
	d := viewOf(t, st, 0)
	if len(d.Pots) != 1 || d.Pots[0].Amount != 300 || len(d.Pots[0].Eligible) != 3 {
		t.Fatalf("collected pots %+v", d.Pots)
	}
	if d.PotTotal != 600 || d.CurrentBet != 200 || d.MinRaiseTo != 300 || d.ToAct != 0 {
		t.Fatalf("view %+v", d)
	}
	if d.Players[2].Status != "allin" || d.Players[2].LastAction != "allin" || d.Players[1].LastAction != "bet" {
		t.Fatalf("players %+v", d.Players)
	}
	if d.HandsLeft != nil {
		t.Fatal("hands_left must be null without a hand limit")
	}
}
