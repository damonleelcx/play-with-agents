package holdem

import (
	"fmt"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// ViewData is the `data` of a "holdem" view, field for field as in
// docs/00-architecture.md.
//
// Pots and pot_total: Pots are the chips already gathered into the middle
// from completed streets, layered into main and side pots; the current
// street's bets are still in front of the players (PlayerView.Bet).
// PotTotal is everything committed this hand, pots plus the street's bets,
// which is the "Pot: N" figure players reason about.
type ViewData struct {
	HandNo     int          `json:"hand_no"`
	HandsLeft  *int         `json:"hands_left"` // null when there is no hand limit
	Street     string       `json:"street"`
	Board      []string     `json:"board"`
	Pots       []Pot        `json:"pots"`
	PotTotal   int          `json:"pot_total"`
	Button     int          `json:"button"`
	SBSeat     int          `json:"sb_seat"`
	BBSeat     int          `json:"bb_seat"`
	SmallBlind int          `json:"small_blind"`
	BigBlind   int          `json:"big_blind"`
	CurrentBet int          `json:"current_bet"`
	MinRaiseTo int          `json:"min_raise_to"`
	ToAct      int          `json:"to_act"` // -1 when no one
	Players    []PlayerView `json:"players"`
	LastHand   *LastHand    `json:"last_hand"`
}

type Pot struct {
	Amount   int   `json:"amount"`
	Eligible []int `json:"eligible"`
}

type PlayerView struct {
	Seat     int    `json:"seat"`
	Stack    int    `json:"stack"`
	Bet      int    `json:"bet"`
	TotalBet int    `json:"total_bet"`
	Status   string `json:"status"`
	// Cards is null unless they are the viewer's own or were shown at
	// showdown.
	Cards []string `json:"cards"`
	// LastAction is the seat's most recent action this hand; it carries
	// over to later streets until the seat acts again ("" before it has).
	LastAction string `json:"last_action"`
	// HandName is the viewer's own current best hand, or a shown hand.
	HandName string `json:"hand_name"`
}

// LastHand is the result of the most recently finished hand, kept so the
// client can show it after the next hand has already been dealt.
type LastHand struct {
	HandNo  int         `json:"hand_no"`
	Board   []string    `json:"board"`
	Winners []Winner    `json:"winners"`
	Shown   []ShownHand `json:"shown"`
}

// Winner.Cards and HandName are empty for an uncontested pot: nothing is
// shown when everyone else folds.
type Winner struct {
	Seat     int      `json:"seat"`
	Amount   int      `json:"amount"`
	HandName string   `json:"hand_name"`
	Cards    []string `json:"cards"`
}

type ShownHand struct {
	Seat     int      `json:"seat"`
	Cards    []string `json:"cards"`
	HandName string   `json:"hand_name"`
}

func (g *Game) View(st games.State, seat games.Seat) (games.View, error) {
	s, err := decodeState(st)
	if err != nil {
		return games.View{}, err
	}
	if seat != games.Spectator && (seat < 0 || seat >= s.n()) {
		return games.View{}, fmt.Errorf("holdem: no seat %d at this table", seat)
	}
	return games.View{Kind: ID, Data: s.view(seat), Status: s.status()}, nil
}

// view builds what seat may see. Hole cards are copied only for the viewer
// and for hands shown at showdown; the deck is never part of the state.
func (s *state) view(seat int) ViewData {
	d := ViewData{
		HandNo: s.HandNo, Street: s.Street, Board: cardStrings(s.Board),
		Button: s.Button, SBSeat: s.SB, BBSeat: s.BB,
		SmallBlind: s.SmallBlind, BigBlind: s.BigBlind,
		CurrentBet: s.CurrentBet, MinRaiseTo: s.CurrentBet + s.MinRaise,
		ToAct: s.ToAct, LastHand: s.LastHand, Pots: []Pot{},
		Players: make([]PlayerView, s.n()),
	}
	if d.Board == nil {
		d.Board = []string{}
	}
	if s.Opts.MaxHands > 0 {
		left := max(0, s.Opts.MaxHands-s.HandNo)
		d.HandsLeft = &left
	}
	if s.Street == streetOver {
		d.ToAct, d.MinRaiseTo, d.CurrentBet = -1, 0, 0
	}
	for _, pt := range s.buildPots(func(p *player) int { return p.TotalBet - p.Bet }) {
		d.Pots = append(d.Pots, Pot(pt))
	}
	for i := range s.Players {
		p := &s.Players[i]
		d.PotTotal += p.TotalBet
		pv := PlayerView{Seat: i, Stack: p.Stack, Bet: p.Bet, TotalBet: p.TotalBet,
			Status: p.Status, LastAction: p.LastAction}
		if len(p.Cards) > 0 && (i == seat || p.Shown) {
			pv.Cards = cardStrings(p.Cards)
			pv.HandName = evalHoleBoard(p.Cards, s.Board).name()
		}
		d.Players[i] = pv
	}
	return d
}

func (s *state) status() string {
	if s.Street == streetOver {
		return "Game over: " + s.summary()
	}
	return seatRef(s.ToAct) + " to act"
}
