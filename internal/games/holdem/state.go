package holdem

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// Streets. "showdown" is part of the view contract but never persists: a hand
// that reaches showdown is settled and the next one dealt in the same Apply.
const (
	streetPreflop  = "preflop"
	streetFlop     = "flop"
	streetTurn     = "turn"
	streetRiver    = "river"
	streetShowdown = "showdown"
	streetOver     = "over"
)

// Player statuses, as the view reports them.
const (
	statusActive = "active" // in the hand with chips behind
	statusFolded = "folded"
	statusAllin  = "allin"
	statusOut    = "out" // busted; skipped by the button and the deal
)

const stateVersion = 1

type options struct {
	StartingStack     int `json:"starting_stack"`
	SmallBlind        int `json:"small_blind"`
	BigBlind          int `json:"big_blind"`
	BlindsDoubleEvery int `json:"blinds_double_every"`
	MaxHands          int `json:"max_hands"`
}

func defaultOptions() options {
	return options{StartingStack: 1000, SmallBlind: 10, BigBlind: 20, BlindsDoubleEvery: 10}
}

type player struct {
	Stack    int    `json:"stack"`
	Bet      int    `json:"bet"`       // this street
	TotalBet int    `json:"total_bet"` // this hand, including Bet
	Status   string `json:"status"`
	Cards    []card `json:"cards,omitempty"`
	// LastAction is the seat's most recent action this hand. It is kept
	// across streets (not cleared on the flop) because it is the only
	// public trace of who was the aggressor, which the Brain uses for
	// continuation bets.
	LastAction string `json:"last_action,omitempty"`
	// Acted is set once the seat has acted since the last full raise; a
	// full raise clears it for everyone else, reopening their action.
	Acted bool `json:"acted,omitempty"`
	// ActedAt is the street bet level the seat faced when it last acted.
	// Short all-ins raise the level without clearing Acted; once their sum
	// since ActedAt reaches a full raise the seat may raise again (the TDA
	// cumulative rule).
	ActedAt int  `json:"acted_at,omitempty"`
	Shown   bool `json:"shown,omitempty"` // cards revealed at showdown
	// StartStack is the stack at the start of the hand; it orders seats
	// that bust in the same hand (the bigger starting stack places higher).
	StartStack int `json:"start_stack"`
	BustHand   int `json:"bust_hand,omitempty"`
}

// state is the whole table, serialised as the games.State document. The
// undealt deck is not stored: it is re-derived from (Seed, HandNo) and
// DeckPos says how much of it has been dealt.
type state struct {
	V          int       `json:"v"`
	Seed       int64     `json:"seed"`
	Opts       options   `json:"opts"`
	HandNo     int       `json:"hand_no"`
	Street     string    `json:"street"`
	Board      []card    `json:"board"`
	DeckPos    int       `json:"deck_pos"`
	Button     int       `json:"button"`
	SB         int       `json:"sb"`
	BB         int       `json:"bb"`
	SmallBlind int       `json:"small_blind"`
	BigBlind   int       `json:"big_blind"`
	CurrentBet int       `json:"current_bet"`
	MinRaise   int       `json:"min_raise"` // size of the last full bet or raise this street
	ToAct      int       `json:"to_act"`
	Players    []player  `json:"players"`
	LastHand   *LastHand `json:"last_hand"`

	ev []games.Event // events of the Apply in progress; never serialised
}

func decodeState(st games.State) (*state, error) {
	var s state
	if err := json.Unmarshal(st, &s); err != nil {
		return nil, fmt.Errorf("holdem: decode state: %w", err)
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	return &s, nil
}

// check rejects documents that would make the engine index out of range, so
// a corrupted or hand-edited state yields an error instead of a panic.
func (s *state) check() error {
	n := len(s.Players)
	bad := func(what string) error { return fmt.Errorf("holdem: corrupt state: %s", what) }
	switch {
	case s.V != stateVersion:
		return bad("version")
	case n < 2 || n > 9:
		return bad("seat count")
	case s.Button < 0 || s.Button >= n || s.SB < 0 || s.SB >= n || s.BB < 0 || s.BB >= n:
		return bad("blind seats")
	case len(s.Board) > 5 || s.DeckPos < 0 || s.DeckPos > deckSize:
		return bad("deck")
	case s.Street != streetOver && (s.ToAct < 0 || s.ToAct >= n):
		return bad("seat to act")
	case s.BigBlind <= 0 || s.MinRaise <= 0:
		return bad("blinds")
	}
	for i := range s.Players {
		p := &s.Players[i]
		if p.Stack < 0 || p.Bet < 0 || p.TotalBet < p.Bet || len(p.Cards) > 2 {
			return bad("player " + strconv.Itoa(i))
		}
	}
	return nil
}

func (s *state) encode() (games.State, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("holdem: encode state: %w", err)
	}
	return b, nil
}

func seatRef(seat int) string { return "{s:" + strconv.Itoa(seat) + "}" }

func (s *state) emit(typ string, seat int, text string, data map[string]any) {
	s.ev = append(s.ev, games.Event{Type: typ, Seat: seat, Text: text, Data: data})
}

func (s *state) n() int { return len(s.Players) }

// nextLive is the next seat clockwise from seat that still has chips.
func (s *state) nextLive(seat int) int {
	for i := 1; i <= s.n(); i++ {
		j := (seat + i) % s.n()
		if s.Players[j].Status != statusOut {
			return j
		}
	}
	return seat
}

func (s *state) liveCount() int {
	c := 0
	for i := range s.Players {
		if s.Players[i].Status != statusOut {
			c++
		}
	}
	return c
}

// inHand counts seats that have not folded (active or all-in).
func (s *state) inHand() int {
	c := 0
	for i := range s.Players {
		if st := s.Players[i].Status; st == statusActive || st == statusAllin {
			c++
		}
	}
	return c
}

// ── dealing ────────────────────────────────────────────────────────────────

// startHand deals the next hand. Button handling is the simplified "moving
// button": the button always advances to the next seat that still has chips,
// and the blinds are the next live seats after it (heads-up, the button
// posts the small blind). This never skips a live player's turn on the
// button, but when a neighbour busts a player can occasionally pay the big
// blind twice in a row or skip it once; a full dead-button implementation is
// not worth the complexity for a play-money table.
func (s *state) startHand() {
	s.HandNo++
	if every := s.Opts.BlindsDoubleEvery; every > 0 && s.HandNo > 1 && (s.HandNo-1)%every == 0 {
		// Stop doubling once the big blind already covers every chip in
		// play: the blinds can then no longer change anything and the
		// integers stay small however long the table runs.
		if s.BigBlind < s.totalChips() {
			s.SmallBlind *= 2
			s.BigBlind *= 2
			s.emit("blinds_up", -1, fmt.Sprintf("Blinds up to %d/%d", s.SmallBlind, s.BigBlind),
				map[string]any{"small_blind": s.SmallBlind, "big_blind": s.BigBlind})
		}
	}

	for i := range s.Players {
		p := &s.Players[i]
		p.Bet, p.TotalBet, p.Cards, p.LastAction, p.Acted, p.ActedAt, p.Shown = 0, 0, nil, "", false, 0, false
		if p.Status != statusOut {
			p.Status = statusActive
			p.StartStack = p.Stack
		}
	}
	s.Board = []card{}
	s.DeckPos = 0
	s.Street = streetPreflop

	if s.HandNo == 1 {
		s.Button = s.nextLive(s.n() - 1) // the first live seat from 0
	} else {
		s.Button = s.nextLive(s.Button)
	}
	if s.liveCount() == 2 {
		s.SB = s.Button
	} else {
		s.SB = s.nextLive(s.Button)
	}
	s.BB = s.nextLive(s.SB)

	s.emit("hand", -1, fmt.Sprintf("Hand #%d", s.HandNo), map[string]any{
		"hand_no": s.HandNo, "button": s.Button, "small_blind": s.SmallBlind, "big_blind": s.BigBlind,
	})

	// Deal one card at a time starting left of the button, as at a real
	// table. Each seat's cards are announced only to that seat.
	deck := handDeck(s.Seed, s.HandNo)
	for round := 0; round < 2; round++ {
		seat := s.Button
		for k := 0; k < s.liveCount(); k++ {
			seat = s.nextLive(seat)
			s.Players[seat].Cards = append(s.Players[seat].Cards, deck[s.DeckPos])
			s.DeckPos++
		}
	}
	for i := range s.Players {
		if p := &s.Players[i]; p.Status == statusActive {
			s.ev = append(s.ev, games.Event{Type: "deal", Seat: i,
				Text: seatRef(i) + " is dealt " + joinCards(p.Cards),
				Data: map[string]any{"cards": cardStrings(p.Cards)}, Only: []games.Seat{i}})
		}
	}

	s.post(s.SB, s.SmallBlind, "sb", "small blind")
	s.post(s.BB, s.BigBlind, "bb", "big blind")
	// The bet to match is the full big blind even when the big blind
	// posted short; the uncalled-bet refund sorts out the excess.
	s.CurrentBet = s.BigBlind
	s.MinRaise = s.BigBlind
	s.advance(s.BB)
}

func (s *state) post(seat, amount int, action, what string) {
	p := &s.Players[seat]
	amt := min(amount, p.Stack)
	p.Stack -= amt
	p.Bet += amt
	p.TotalBet += amt
	p.LastAction = action
	text := fmt.Sprintf("%s posts %s %d", seatRef(seat), what, amt)
	if p.Stack == 0 {
		p.Status = statusAllin
		text += " and is all-in"
	}
	s.emit("blinds", seat, text, map[string]any{"amount": amt, "blind": action})
}

func (s *state) dealStreet() {
	deck := handDeck(s.Seed, s.HandNo)
	var name string
	var k int
	switch s.Street {
	case streetPreflop:
		name, k = streetFlop, 3
	case streetFlop:
		name, k = streetTurn, 1
	default:
		name, k = streetRiver, 1
	}
	// A test may have fixed the board in advance; only top it up.
	want := map[string]int{streetFlop: 3, streetTurn: 4, streetRiver: 5}[name]
	for len(s.Board) < want && k > 0 && s.DeckPos < deckSize {
		s.Board = append(s.Board, deck[s.DeckPos])
		s.DeckPos++
		k--
	}
	s.Street = name
	var shown []card
	if name == streetFlop {
		shown = s.Board
	} else {
		shown = s.Board[len(s.Board)-1:]
	}
	label := map[string]string{streetFlop: "Flop", streetTurn: "Turn", streetRiver: "River"}[name]
	s.emit("street", -1, label+": "+joinCards(shown),
		map[string]any{"street": name, "board": cardStrings(s.Board)})
}

// ── betting ────────────────────────────────────────────────────────────────

// legality is everything a seat may do right now, derived in one place so
// that Legal, Apply and DefaultMove can never disagree.
type legality struct {
	toCall   int // chips needed to match the current bet (may exceed the stack)
	callAmt  int // chips a call actually puts in
	canFold  bool
	canCheck bool
	canCall  bool
	canRaise bool // a full raise via "raise"
	raiseMin int  // total street bet
	raiseMax int  // total street bet when all-in
	canAllin bool
}

func (s *state) legalFor(seat int) (legality, bool) {
	if s.Street == streetOver || seat != s.ToAct || seat < 0 || seat >= s.n() {
		return legality{}, false
	}
	p := &s.Players[seat]
	if p.Status != statusActive || p.Stack <= 0 {
		return legality{}, false
	}
	var l legality
	l.toCall = max(0, s.CurrentBet-p.Bet)
	l.callAmt = min(l.toCall, p.Stack)
	l.canCheck = l.toCall == 0
	l.canFold = l.toCall > 0
	l.canCall = l.toCall > 0

	// Raising needs someone left who could call it, and an action that is
	// open to this seat: either it has not acted since the last full
	// raise, or short all-ins since then add up to a full raise.
	othersWithChips := 0
	for i := range s.Players {
		if i != seat && s.Players[i].Status == statusActive {
			othersWithChips++
		}
	}
	reopened := !p.Acted || s.CurrentBet-p.ActedAt >= s.MinRaise
	mayRaise := reopened && othersWithChips > 0
	l.raiseMax = p.Bet + p.Stack
	l.raiseMin = s.CurrentBet + s.MinRaise
	l.canRaise = mayRaise && l.raiseMax >= l.raiseMin
	// All-in is a raise (full or short) when it exceeds the current bet,
	// and a call for less otherwise; the latter is always allowed.
	l.canAllin = p.Stack > 0 && (p.Stack <= l.toCall || mayRaise)
	return l, true
}

// act validates and plays one move for the seat to act, then advances the
// hand (possibly through the end of the hand and the next deal).
func (s *state) act(seat int, m games.Move) error {
	l, ok := s.legalFor(seat)
	if !ok {
		if s.Street == streetOver {
			return games.Illegal("the game is over")
		}
		return games.Illegal("it is not seat %d's turn", seat)
	}
	p := &s.Players[seat]
	ref := seatRef(seat)
	switch m.Type {
	case "fold":
		if !l.canFold {
			return games.Illegal("cannot fold when you can check")
		}
		p.Status = statusFolded
		p.LastAction = "fold"
		s.emit("fold", seat, ref+" folds", nil)
	case "check":
		if !l.canCheck {
			return games.Illegal("cannot check facing a bet of %d", l.toCall)
		}
		p.LastAction = "check"
		s.emit("check", seat, ref+" checks", nil)
	case "call":
		if !l.canCall {
			return games.Illegal("nothing to call")
		}
		s.putIn(seat, l.callAmt)
		p.LastAction = "call"
		text := fmt.Sprintf("%s calls %d", ref, l.callAmt)
		if p.Stack == 0 {
			p.LastAction = "allin"
			text += " and is all-in"
		}
		s.emit("call", seat, text, map[string]any{"amount": l.callAmt})
	case "raise":
		if !l.canRaise {
			return games.Illegal("raising is not open to you")
		}
		to, err := intArg(m.Args, "to")
		if err != nil {
			return err
		}
		if to < l.raiseMin || to > l.raiseMax {
			return games.Illegal("raise to %d is outside %d..%d", to, l.raiseMin, l.raiseMax)
		}
		s.raiseTo(seat, to)
	case "allin":
		if !l.canAllin {
			return games.Illegal("going all-in would reopen a closed action; call or fold")
		}
		if l.raiseMax <= s.CurrentBet {
			amt := p.Stack
			s.putIn(seat, amt)
			p.LastAction = "allin"
			s.emit("allin", seat, fmt.Sprintf("%s calls %d and is all-in", ref, amt), map[string]any{"amount": amt})
		} else {
			s.raiseTo(seat, l.raiseMax)
		}
	default:
		return games.Illegal("unknown move %q", m.Type)
	}
	p.Acted = true
	p.ActedAt = s.CurrentBet
	s.advance(seat)
	return nil
}

func (s *state) putIn(seat, amt int) {
	p := &s.Players[seat]
	p.Stack -= amt
	p.Bet += amt
	p.TotalBet += amt
	if p.Stack == 0 {
		p.Status = statusAllin
	}
}

// raiseTo makes seat's street bet total `to` (> CurrentBet). A raise of at
// least MinRaise is "full": it sets the new minimum and reopens the action
// for everyone. A smaller one can only be an all-in, and leaves the seats
// that already acted able to call or fold only.
func (s *state) raiseTo(seat, to int) {
	p := &s.Players[seat]
	wasBet := s.CurrentBet == 0
	inc := to - s.CurrentBet
	s.putIn(seat, to-p.Bet)
	if inc >= s.MinRaise {
		s.MinRaise = inc
		for i := range s.Players {
			if i != seat {
				s.Players[i].Acted = false
			}
		}
	}
	s.CurrentBet = to

	ref := seatRef(seat)
	var typ, text string
	switch {
	case p.Stack == 0:
		typ = "allin"
		if wasBet {
			text = fmt.Sprintf("%s bets %d and is all-in", ref, to)
		} else {
			text = fmt.Sprintf("%s raises to %d and is all-in", ref, to)
		}
	case wasBet:
		typ, text = "bet", fmt.Sprintf("%s bets %d", ref, to)
	default:
		typ, text = "raise", fmt.Sprintf("%s raises to %d", ref, to)
	}
	if p.Stack == 0 {
		p.LastAction = "allin"
	} else {
		p.LastAction = typ
	}
	s.emit(typ, seat, text, map[string]any{"to": to})
}

// nextToAct finds the next seat after from that owes an action this street,
// or -1 when the street's betting is complete.
func (s *state) nextToAct(from int) int {
	active, last := 0, -1
	for i := range s.Players {
		if s.Players[i].Status == statusActive {
			active++
			last = i
		}
	}
	if active == 0 {
		return -1
	}
	if active == 1 {
		// A lone seat with chips only acts if it still faces a bet from an
		// all-in player; it can never be raised, so there is nothing else
		// for it to decide.
		maxOther := 0
		for i := range s.Players {
			if i != last && s.Players[i].Status == statusAllin {
				maxOther = max(maxOther, s.Players[i].Bet)
			}
		}
		if s.Players[last].Bet >= maxOther {
			return -1
		}
		return last
	}
	for i := 1; i <= s.n(); i++ {
		j := (from + i) % s.n()
		p := &s.Players[j]
		if p.Status == statusActive && (!p.Acted || p.Bet < s.CurrentBet) {
			return j
		}
	}
	return -1
}

// advance moves play forward after an action: to the next seat, the next
// street (dealing streets straight through when nobody can bet), or the end
// of the hand.
func (s *state) advance(from int) {
	for {
		if s.inHand() <= 1 {
			s.returnUncalled()
			s.settle()
			return
		}
		if nx := s.nextToAct(from); nx >= 0 {
			s.ToAct = nx
			return
		}
		s.returnUncalled()
		for i := range s.Players {
			p := &s.Players[i]
			p.Bet, p.Acted, p.ActedAt = 0, false, 0
		}
		s.CurrentBet = 0
		s.MinRaise = s.BigBlind
		if s.Street == streetRiver {
			s.settle()
			return
		}
		s.dealStreet()
		from = s.Button
	}
}

// returnUncalled gives back the part of the largest street bet that nobody
// matched, so it never sits in a pot its owner cannot lose.
func (s *state) returnUncalled() {
	top, second := -1, 0
	for i := range s.Players {
		b := s.Players[i].Bet
		if top < 0 || b > s.Players[top].Bet {
			if top >= 0 {
				second = max(second, s.Players[top].Bet)
			}
			top = i
		} else {
			second = max(second, b)
		}
	}
	if top < 0 {
		return
	}
	p := &s.Players[top]
	if diff := p.Bet - second; diff > 0 {
		p.Bet -= diff
		p.TotalBet -= diff
		p.Stack += diff
		if p.Status == statusAllin {
			p.Status = statusActive
		}
		s.CurrentBet = min(s.CurrentBet, p.Bet)
		s.emit("refund", top, fmt.Sprintf("Uncalled %d returned to %s", diff, seatRef(top)),
			map[string]any{"amount": diff})
	}
}

// ── pots and settlement ────────────────────────────────────────────────────

type pot struct {
	Amount   int   `json:"amount"`
	Eligible []int `json:"eligible"`
}

// buildPots layers contributions into the main pot and side pots. contrib
// gives each seat's chips in; folded seats' chips count toward the amounts
// but those seats are never eligible. A layer nobody remaining is eligible
// for (impossible after the uncalled-bet refund, kept as a safety net) is
// folded into the pot below it so no chip is ever lost.
func (s *state) buildPots(contrib func(p *player) int) []pot {
	var levels []int
	for i := range s.Players {
		p := &s.Players[i]
		if c := contrib(p); c > 0 && (p.Status == statusActive || p.Status == statusAllin) {
			levels = append(levels, c)
		}
	}
	sort.Ints(levels)
	var pots []pot
	prev := 0
	for _, lv := range levels {
		if lv == prev {
			continue
		}
		pt := pot{Eligible: []int{}}
		for i := range s.Players {
			p := &s.Players[i]
			c := contrib(p)
			pt.Amount += min(c, lv) - min(c, prev)
			if c >= lv && (p.Status == statusActive || p.Status == statusAllin) {
				pt.Eligible = append(pt.Eligible, i)
			}
		}
		pots = append(pots, pt)
		prev = lv
	}
	// Chips above the highest live level (folded money only).
	extra := 0
	for i := range s.Players {
		if c := contrib(&s.Players[i]); c > prev {
			extra += c - prev
		}
	}
	if extra > 0 {
		if len(pots) == 0 {
			pots = append(pots, pot{Eligible: []int{}})
		}
		pots[len(pots)-1].Amount += extra
	}
	return pots
}

// orderFromButton sorts seats by position starting left of the button,
// which is the order the odd chip and showdown reveals follow.
func (s *state) orderFromButton(seats []int) {
	n := s.n()
	sort.Slice(seats, func(a, b int) bool {
		return (seats[a]-s.Button-1+2*n)%n < (seats[b]-s.Button-1+2*n)%n
	})
}

func (s *state) settle() {
	pots := s.buildPots(func(p *player) int { return p.TotalBet })
	for i := range s.Players {
		s.Players[i].Bet = 0
	}
	s.CurrentBet = 0
	lh := &LastHand{HandNo: s.HandNo, Board: cardStrings(s.Board), Winners: []Winner{}, Shown: []ShownHand{}}

	if s.inHand() == 1 {
		// Uncontested: no showdown, nothing is shown.
		w := -1
		for i := range s.Players {
			if st := s.Players[i].Status; st == statusActive || st == statusAllin {
				w = i
			}
		}
		total := 0
		for _, pt := range pots {
			total += pt.Amount
		}
		s.Players[w].Stack += total
		lh.Winners = append(lh.Winners, Winner{Seat: w, Amount: total})
		s.emit("win", w, fmt.Sprintf("%s wins %d", seatRef(w), total), map[string]any{"amount": total})
	} else {
		s.Street = streetShowdown
		var live []int
		scores := make([]handScore, s.n())
		for i := range s.Players {
			if st := s.Players[i].Status; st == statusActive || st == statusAllin {
				live = append(live, i)
				scores[i] = evalHoleBoard(s.Players[i].Cards, s.Board)
			}
		}
		s.orderFromButton(live)
		for _, i := range live {
			p := &s.Players[i]
			p.Shown = true
			name := scores[i].name()
			lh.Shown = append(lh.Shown, ShownHand{Seat: i, Cards: cardStrings(p.Cards), HandName: name})
			s.emit("showdown", i, fmt.Sprintf("%s shows %s (%s)", seatRef(i), joinCards(p.Cards), name),
				map[string]any{"cards": cardStrings(p.Cards), "hand_name": name})
		}
		won := map[int]int{}
		var order []int // winners in the order they first collect
		for k, pt := range pots {
			var best handScore
			var winners []int
			for _, i := range pt.Eligible {
				switch sc := scores[i]; {
				case len(winners) == 0 || sc > best:
					best, winners = sc, []int{i}
				case sc == best:
					winners = append(winners, i)
				}
			}
			if len(winners) == 0 { // unreachable: buildPots keeps a live seat in every pot
				continue
			}
			s.orderFromButton(winners)
			share, odd := pt.Amount/len(winners), pt.Amount%len(winners)
			for j, w := range winners {
				amt := share
				if j < odd {
					amt++ // odd chips go to the first winners left of the button
				}
				s.Players[w].Stack += amt
				if _, seen := won[w]; !seen {
					order = append(order, w)
				}
				won[w] += amt
				potName := ""
				if len(pots) > 1 {
					if k == 0 {
						potName = " from the main pot"
					} else {
						potName = fmt.Sprintf(" from side pot %d", k)
					}
				}
				s.emit("win", w, fmt.Sprintf("%s wins %d%s with %s", seatRef(w), amt, potName, scores[w].name()),
					map[string]any{"amount": amt, "pot": k, "hand_name": scores[w].name()})
			}
		}
		for _, w := range order {
			lh.Winners = append(lh.Winners, Winner{Seat: w, Amount: won[w],
				HandName: scores[w].name(), Cards: cardStrings(s.Players[w].Cards)})
		}
	}
	// Every chip committed has now been paid out.
	for i := range s.Players {
		p := &s.Players[i]
		p.TotalBet = 0
		if p.Status == statusAllin && p.Stack > 0 {
			p.Status = statusActive
		}
	}
	s.LastHand = lh
	s.endHand()
}

// endHand busts empty stacks, then either ends the game or deals the next
// hand, all inside the Apply that finished this one.
func (s *state) endHand() {
	for i := range s.Players {
		p := &s.Players[i]
		if p.Status != statusOut && p.Stack == 0 {
			p.Status = statusOut
			p.BustHand = s.HandNo
			s.emit("bust", i, seatRef(i)+" is out of chips", nil)
		}
	}
	if s.liveCount() <= 1 || (s.Opts.MaxHands > 0 && s.HandNo >= s.Opts.MaxHands) {
		s.Street = streetOver
		s.ToAct = -1
		s.emit("game_over", -1, s.summary(), nil)
		return
	}
	s.startHand()
}

func (s *state) totalChips() int {
	t := 0
	for i := range s.Players {
		t += s.Players[i].Stack + s.Players[i].TotalBet
	}
	return t
}

// ranking orders seats for the outcome: players still holding chips by
// stack (equal stacks share a rank), then busted players, later busts
// first; seats that bust in the same hand are ordered by the stack they
// started that hand with, and share a rank when that is equal too.
func (s *state) ranking() []int {
	seats := make([]int, s.n())
	for i := range seats {
		seats[i] = i
	}
	key := func(i int) [3]int {
		p := &s.Players[i]
		if p.Status != statusOut {
			return [3]int{1, p.Stack, 0}
		}
		return [3]int{0, p.BustHand, p.StartStack}
	}
	less := func(a, b [3]int) bool { // a ranks better than b
		for k := 0; k < 3; k++ {
			if a[k] != b[k] {
				return a[k] > b[k]
			}
		}
		return false
	}
	sort.SliceStable(seats, func(a, b int) bool { return less(key(seats[a]), key(seats[b])) })
	rank := make([]int, s.n())
	for pos, seat := range seats {
		if pos > 0 && key(seat) == key(seats[pos-1]) {
			rank[seat] = rank[seats[pos-1]]
		} else {
			rank[seat] = pos + 1
		}
	}
	return rank
}

func (s *state) summary() string {
	rank := s.ranking()
	var top []int
	for i, r := range rank {
		if r == 1 {
			top = append(top, i)
		}
	}
	chips := s.Players[top[0]].Stack
	if s.liveCount() == 1 {
		return fmt.Sprintf("%s wins with all %d chips after %d hands", seatRef(top[0]), chips, s.HandNo)
	}
	if len(top) == 1 {
		return fmt.Sprintf("%s wins with %d chips after %d hands", seatRef(top[0]), chips, s.HandNo)
	}
	names := ""
	for k, i := range top {
		switch {
		case k == 0:
		case k == len(top)-1:
			names += " and "
		default:
			names += ", "
		}
		names += seatRef(i)
	}
	return fmt.Sprintf("%s tie with %d chips each after %d hands", names, chips, s.HandNo)
}
