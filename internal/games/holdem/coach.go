package holdem

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// CoachTip is what the host agent turns into a coaching line for a human
// player. Every number is computed from what that seat can see.
type CoachTip struct {
	HandName   string  `json:"hand_name"`  // the seat's current best hand
	Equity     float64 `json:"equity"`     // 0..1, share of the pot won against the live opponents
	PotOdds    float64 `json:"pot_odds"`   // 0..1, the equity a call needs; 0 when there is nothing to call
	Opponents  int     `json:"opponents"`  // opponents still in the hand
	ToCall     int     `json:"to_call"`    // chips needed to call
	Suggestion string  `json:"suggestion"` // one plain-English line
}

const coachSims = 2000

// Coach analyses seat's spot. Like the Brain it works only from the seat's
// own view, so a tip can never leak an opponent's cards. The Monte Carlo
// stream is seeded from the hand and the seat's own cards, so asking twice
// gives the same tip.
func Coach(st games.State, seat games.Seat) (CoachTip, error) {
	g := New()
	v, err := g.View(st, seat)
	if err != nil {
		return CoachTip{}, err
	}
	d, err := viewDataOf(v)
	if err != nil {
		return CoachTip{}, err
	}
	if d.Street == streetOver {
		return CoachTip{}, errors.New("holdem: the game is over")
	}
	if seat < 0 || seat >= len(d.Players) {
		return CoachTip{}, fmt.Errorf("holdem: no seat %d", seat)
	}
	if status := d.Players[seat].Status; status == statusFolded || status == statusOut {
		return CoachTip{}, errors.New("holdem: seat is not in this hand")
	}
	sit, err := readSituation(d, seat)
	if err != nil {
		return CoachTip{}, err
	}

	h := fnv.New64a()
	fmt.Fprintf(h, "%d/%d/%v/%v", d.HandNo, seat, d.Players[seat].Cards, d.Board)
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	eq := estimateEquity(context.Background(), sit.hole, sit.board, sit.opponents, coachSims, rng)

	tip := CoachTip{
		HandName:  evalHoleBoard(sit.hole, sit.board).name(),
		Equity:    math.Round(eq*1000) / 1000,
		Opponents: sit.opponents,
		ToCall:    sit.toCall,
	}
	if sit.toCall > 0 {
		tip.PotOdds = math.Round(float64(sit.toCall)/float64(sit.pot+sit.toCall)*1000) / 1000
	}
	tip.Suggestion = suggest(sit, tip)
	return tip, nil
}

func pct(x float64) string { return fmt.Sprintf("%.0f%%", x*100) }

func suggest(sit *situation, t CoachTip) string {
	opp := "1 opponent"
	if t.Opponents != 1 {
		opp = fmt.Sprintf("%d opponents", t.Opponents)
	}
	fair := 1 / float64(max(t.Opponents, 1)+1)

	if sit.street == streetPreflop {
		s := preflopStrength(sit.hole[0], sit.hole[1])
		bbs := float64(sit.stack+sit.bet) / float64(sit.bb)
		switch {
		case bbs < 12 && s >= 0.75:
			return fmt.Sprintf("You are short (about %.0f big blinds) with a good hand: moving all-in is strong.", bbs)
		case bbs < 12:
			return fmt.Sprintf("You are short (about %.0f big blinds): wait for a better hand to go all-in.", bbs)
		case s >= 0.93:
			return "A premium hand: raise (about 3 big blinds, or 3x a raise in front of you)."
		case s >= 0.75 && sit.currentBet <= sit.bb:
			return "A solid hand: open with a raise to about 2.5-3 big blinds."
		case s >= 0.75:
			return "A decent hand facing a raise: calling is reasonable, re-raising is aggressive."
		case s >= 0.55 && sit.toCall == 0:
			return "A playable hand: checking is fine, a raise from late position works too."
		case s >= 0.55 && sit.position >= 0.7:
			return "A playable hand in late position: calling or raising to steal is fine."
		case sit.toCall == 0:
			return "A weak hand, but it is free to see the flop: just check."
		default:
			return "A weak hand: folding before the flop saves chips."
		}
	}

	if t.ToCall > 0 {
		switch {
		case t.Equity >= t.PotOdds+0.2 && t.Equity >= 1.6*fair:
			return fmt.Sprintf("You are ahead (about %s against %s, and a call needs only %s): raise for value.",
				pct(t.Equity), opp, pct(t.PotOdds))
		case t.Equity >= t.PotOdds:
			return fmt.Sprintf("Calling is profitable: a call needs %s and you have about %s against %s.",
				pct(t.PotOdds), pct(t.Equity), opp)
		default:
			return fmt.Sprintf("Folding is fine: a call needs %s but you have about %s against %s.",
				pct(t.PotOdds), pct(t.Equity), opp)
		}
	}
	switch {
	case t.Equity >= 1.6*fair && t.Equity >= 0.5:
		return fmt.Sprintf("Strong hand (about %s against %s): bet around two-thirds of the pot for value.",
			pct(t.Equity), opp)
	case t.Equity >= fair:
		return fmt.Sprintf("You have a share of the pot (about %s against %s): check, or bet small to keep control.",
			pct(t.Equity), opp)
	default:
		return fmt.Sprintf("Behind for now (about %s against %s): checking is the cheap option.",
			pct(t.Equity), opp)
	}
}
