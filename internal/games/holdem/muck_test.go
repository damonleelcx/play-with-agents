package holdem

import (
	"reflect"
	"testing"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// showdownTable deals three seats with fixed hole cards and a fixed board.
// Seat 0 has the button, seat 1 the small blind, seat 2 the big blind.
func showdownTable(t *testing.T, stacks []int, board string, hole ...string) *state {
	t.Helper()
	s := tableWith(t, stacks, 10, 20)
	s.Board = mustCards(t, board)
	for i, h := range hole {
		s.Players[i].Cards = mustCards(t, h)
	}
	return s
}

func seatsOf(ev []games.Event) []int {
	out := []int{}
	for _, e := range ev {
		out = append(out, e.Seat)
	}
	return out
}

// checkAround checks the street through, starting with the seat to act.
func checkAround(t *testing.T, s *state, n int) *state {
	t.Helper()
	for k := 0; k < n; k++ {
		s, _ = play(t, s, s.ToAct, "check")
	}
	return s
}

func TestShowdownRiverAggressorShowsFirstLoserMucks(t *testing.T) {
	s := showdownTable(t, []int{1000, 1000, 1000}, "2c 7d 9h Js 3s",
		"9c 5d", // seat 0: pair of nines, worse than what is shown before it
		"Ah Jd", // seat 1: jacks, ace kicker (wins)
		"Jh 4c") // seat 2: jacks, the river bettor
	s, _ = play(t, s, 0, "call")
	s, _ = play(t, s, 1, "call")
	s, _ = play(t, s, 2, "check")
	s = checkAround(t, s, 3) // flop
	s = checkAround(t, s, 3) // turn
	s, _ = play(t, s, 1, "check")
	s, _ = play(t, s, 2, "raise", 100)
	s, _ = play(t, s, 0, "call")
	s, ev := play(t, s, 1, "call")

	if got := seatsOf(eventsOfType(ev, "showdown")); !reflect.DeepEqual(got, []int{2, 1}) {
		t.Fatalf("shown in order %v, want the river bettor (2) first, then 1", got)
	}
	if got := seatsOf(eventsOfType(ev, "muck")); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("mucked %v, want [0]", got)
	}
	lh := s.LastHand
	if len(lh.Shown) != 2 || lh.Shown[0].Seat != 2 || lh.Shown[1].Seat != 1 {
		t.Fatalf("last hand shown %+v", lh.Shown)
	}
	if len(lh.Winners) != 1 || lh.Winners[0].Seat != 1 {
		t.Fatalf("winners %+v", lh.Winners)
	}
	// Reveal events come before the win announcement.
	lastShow, firstWin := -1, -1
	for i, e := range ev {
		if e.Type == "showdown" || e.Type == "muck" {
			lastShow = i
		}
		if e.Type == "win" && firstWin < 0 {
			firstWin = i
		}
	}
	if lastShow > firstWin {
		t.Fatalf("wins announced before the showdown finished: %+v", ev)
	}
}

func TestShowdownNoRiverBetStartsLeftOfButton(t *testing.T) {
	s := showdownTable(t, []int{1000, 1000, 1000}, "2c 7d 9h Js 3s",
		"Kc Ks", // seat 0 (button): ties the shown kings: shows
		"Kh Kd", // seat 1: first left of the button
		"Qh Qd") // seat 2: worse than the kings: mucks
	s, _ = play(t, s, 0, "call")
	s, _ = play(t, s, 1, "call")
	s, _ = play(t, s, 2, "check")
	s = checkAround(t, s, 3)
	s = checkAround(t, s, 3)
	s = checkAround(t, s, 2)
	s, ev := play(t, s, s.ToAct, "check")

	if got := seatsOf(eventsOfType(ev, "showdown")); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("shown %v, want [1 0]", got)
	}
	if got := seatsOf(eventsOfType(ev, "muck")); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("mucked %v, want [2]", got)
	}
	if len(s.LastHand.Winners) != 2 {
		t.Fatalf("expected a split pot, winners %+v", s.LastHand.Winners)
	}
}

func TestShowdownAllinAlwaysShownWinnersAlwaysShow(t *testing.T) {
	s := showdownTable(t, []int{100, 1000, 1000}, "2c 7d 9h Js 3s",
		"4c 5d", // seat 0: all-in preflop with the worst hand: still tabled
		"Ah Ad", // seat 1: river bettor, wins everything
		"Kh Kd") // seat 2: loses main and side pot: mucks
	s, _ = play(t, s, 0, "allin")
	s, _ = play(t, s, 1, "call")
	s, _ = play(t, s, 2, "call")
	s = checkAround(t, s, 2) // flop
	s = checkAround(t, s, 2) // turn
	s, _ = play(t, s, 1, "raise", 100)
	s, ev := play(t, s, 2, "call")

	if got := seatsOf(eventsOfType(ev, "showdown")); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("shown %v, want the all-in (0) first, then the bettor (1)", got)
	}
	if got := seatsOf(eventsOfType(ev, "muck")); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("mucked %v, want [2]", got)
	}
	// A spectator sees the shown hands, not the mucked one.
	d := viewOf(t, encodeT(t, s), games.Spectator)
	if len(d.LastHand.Shown) != 2 {
		t.Fatalf("spectator's last hand %+v", d.LastHand)
	}
	for _, sh := range d.LastHand.Shown {
		if sh.Seat == 2 {
			t.Fatal("the mucked hand is in last_hand.shown")
		}
	}
}

// A winner shows even when an earlier, better hand was shown for a pot it
// was not in: the short all-in has the best hand, the side pot goes to a
// hand that would otherwise be mucked.
func TestShowdownSidePotWinnerShowsUnderBetterAllin(t *testing.T) {
	s := showdownTable(t, []int{100, 1000, 1000}, "2c 7d 9h Js 3s",
		"Jc Jh", // seat 0: all-in, trips: wins the main pot
		"Ah Ad", // seat 1: wins the side pot with aces
		"Kh Kd") // seat 2: river bettor, loses the side pot
	s, _ = play(t, s, 0, "allin")
	s, _ = play(t, s, 1, "call")
	s, _ = play(t, s, 2, "call")
	s = checkAround(t, s, 2)
	s = checkAround(t, s, 2)
	s, _ = play(t, s, 1, "check")
	s, _ = play(t, s, 2, "raise", 100)
	s, ev := play(t, s, 1, "call")

	if got := seatsOf(eventsOfType(ev, "showdown")); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("shown %v, want [0 1]", got)
	}
	if got := seatsOf(eventsOfType(ev, "muck")); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("mucked %v, want [2]", got)
	}
	if len(s.LastHand.Winners) != 2 {
		t.Fatalf("winners %+v", s.LastHand.Winners)
	}
}
