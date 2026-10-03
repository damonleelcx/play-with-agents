package holdem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// PersonaFor returns the poker persona of a roster agent (see the roster in
// docs/00-architecture.md). Unknown ids get Nova's balanced numbers. skill is
// clamped to 0 (casual), 1 (regular) or 2 (shark).
//
//	id    tight  aggr  bluff  talk   style
//	aoi   0.55   0.60  0.35   0.70   balanced; adapts to the number of opponents and stack depth
//	ren   0.80   0.80  0.25   0.20   tight-aggressive
//	mika  0.20   0.90  0.75   0.90   loose-aggressive, bluffs and loves all-ins
//	bram  0.15   0.15  0.05   0.80   loose-passive calling station
//	nova  0.55   0.55  0.30   0.70   balanced, math-driven
//	lin   0.85   0.20  0.05   0.15   tight-passive, rarely bluffs
func PersonaFor(agentID string, skill int) games.Persona {
	p := games.Persona{ID: agentID, Skill: min(max(skill, 0), 2)}
	switch agentID {
	case "aoi":
		p.Tightness, p.Aggression, p.Bluff, p.Talk = 0.55, 0.60, 0.35, 0.70
	case "ren":
		p.Tightness, p.Aggression, p.Bluff, p.Talk = 0.80, 0.80, 0.25, 0.20
	case "mika":
		p.Tightness, p.Aggression, p.Bluff, p.Talk = 0.20, 0.90, 0.75, 0.90
	case "bram":
		p.Tightness, p.Aggression, p.Bluff, p.Talk = 0.15, 0.15, 0.05, 0.80
	case "lin":
		p.Tightness, p.Aggression, p.Bluff, p.Talk = 0.85, 0.20, 0.05, 0.15
	default: // "nova" and anyone we do not know
		p.Tightness, p.Aggression, p.Bluff, p.Talk = 0.55, 0.55, 0.30, 0.70
	}
	return p
}

// simsForSkill is the Monte Carlo budget per postflop decision.
func simsForSkill(skill int) int {
	switch {
	case skill <= 0:
		return 300
	case skill == 1:
		return 900
	default:
		return 2500
	}
}

// Brain is the Hold'em AI player. It sees exactly what its seat sees: it
// reads g.View(st, seat) and g.Legal(st, seat) and never decodes the state
// itself, so it cannot peek at opponents' cards or the deck even by
// accident.
//
// Preflop it ranks its hand with the starting-hand table; postflop it
// estimates equity by Monte Carlo against the live opponents. Decisions mix
// pot odds, position, stack-to-pot ratio and the persona, drawing from rng so
// the same persona plays a mixed (less exploitable, more human) strategy.
type Brain struct{}

var _ games.Brain = Brain{}

func (Brain) Choose(ctx context.Context, g games.Game, st games.State, seat games.Seat, p games.Persona, rng *rand.Rand) (games.Move, error) {
	legal, err := g.Legal(st, seat)
	if err != nil {
		return games.Move{}, err
	}
	if len(legal) == 0 {
		return games.Move{}, errors.New("holdem: brain asked to move when it has no legal move")
	}
	v, err := g.View(st, seat)
	if err != nil {
		return games.Move{}, err
	}
	d, err := viewDataOf(v)
	if err != nil {
		return games.Move{}, err
	}
	sit, err := readSituation(d, seat)
	if err != nil {
		return games.Move{}, err
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(d.HandNo)*31 + int64(seat)))
	}
	sty := styleFor(p, sit)
	var dec decision
	if sit.street == streetPreflop {
		dec = decidePreflop(sit, sty, rng)
	} else {
		eq := estimateEquity(ctx, sit.hole, sit.board, sit.opponents, simsForSkill(p.Skill), rng)
		dec = decidePostflop(sit, sty, eq, rng)
	}
	return newMenu(legal).pick(dec), nil
}

// viewDataOf accepts the in-process ViewData or, for a wrapped or remote
// game, any JSON-shaped equivalent.
func viewDataOf(v games.View) (ViewData, error) {
	switch d := v.Data.(type) {
	case ViewData:
		return d, nil
	case *ViewData:
		if d == nil {
			return ViewData{}, errors.New("holdem: empty view")
		}
		return *d, nil
	}
	b, err := json.Marshal(v.Data)
	if err != nil {
		return ViewData{}, fmt.Errorf("holdem: view: %w", err)
	}
	var d ViewData
	if err := json.Unmarshal(b, &d); err != nil {
		return ViewData{}, fmt.Errorf("holdem: view: %w", err)
	}
	return d, nil
}

// situation is the public picture plus the seat's own cards.
type situation struct {
	street     string
	hole       []card
	board      []card
	stack      int // chips behind
	bet        int // chips in front this street
	toCall     int
	currentBet int
	pot        int // everything committed this hand, current bets included
	bb         int
	opponents  int     // others still in the hand
	position   float64 // 0 = first to act after the flop, 1 = the button
	effStack   int     // the most this seat can win or lose against one opponent
	aggressor  bool    // this seat made the last bet or raise it took part in
	limpers    int     // preflop callers of the big blind before us
}

func readSituation(d ViewData, seat int) (*situation, error) {
	if seat < 0 || seat >= len(d.Players) {
		return nil, fmt.Errorf("holdem: no seat %d", seat)
	}
	me := d.Players[seat]
	hole, err := parseCards(me.Cards)
	if err != nil || len(hole) != 2 {
		return nil, errors.New("holdem: brain cannot see its own hole cards")
	}
	board, err := parseCards(d.Board)
	if err != nil {
		return nil, err
	}
	s := &situation{
		street: d.Street, hole: hole, board: board,
		stack: me.Stack, bet: me.Bet, currentBet: d.CurrentBet,
		toCall: max(0, d.CurrentBet-me.Bet), pot: d.PotTotal, bb: max(1, d.BigBlind),
		aggressor: me.LastAction == "bet" || me.LastAction == "raise",
	}
	maxOpp := 0
	n := len(d.Players)
	var order []int // seats dealt in, from left of the button round to it
	for k := 1; k <= n; k++ {
		i := (d.Button + k) % n
		pl := d.Players[i]
		if pl.Status == statusOut {
			continue
		}
		order = append(order, i)
		if i == seat || pl.Status == statusFolded {
			continue
		}
		s.opponents++
		maxOpp = max(maxOpp, pl.Stack+pl.Bet)
		if d.Street == streetPreflop && pl.LastAction == "call" && pl.Bet == d.BigBlind {
			s.limpers++
		}
	}
	s.effStack = min(me.Stack+me.Bet, maxOpp)
	if len(order) > 1 {
		for idx, i := range order {
			if i == seat {
				s.position = float64(idx) / float64(len(order)-1)
			}
		}
	}
	return s, nil
}

// style is the persona after situational adjustment.
type style struct{ tight, aggr, bluff float64 }

func clamp01(x float64) float64 { return math.Min(1, math.Max(0, x)) }

func styleFor(p games.Persona, sit *situation) style {
	s := style{clamp01(p.Tightness), clamp01(p.Aggression), clamp01(p.Bluff)}
	if p.ID == "aoi" {
		// Aoi adapts: she opens up and attacks short-handed, tightens and
		// stops bluffing into crowds, and leans on the pressure of the
		// blinds when stacks get shallow.
		switch {
		case sit.opponents >= 3:
			s.tight += 0.12
			s.bluff *= 0.5
		case sit.opponents == 1:
			s.tight -= 0.12
			s.aggr += 0.12
			s.bluff += 0.08
		}
		if sit.effStack < 25*sit.bb {
			s.aggr += 0.1
		}
		s = style{clamp01(s.tight), clamp01(s.aggr), clamp01(s.bluff)}
	}
	return s
}

type intent int

const (
	passive intent = iota // check if free, else fold
	callIt                // check if free, else call
	raiseIt               // bet/raise to decision.to
	shove                 // all-in
)

type decision struct {
	kind intent
	to   int // raise target (total street bet)
}

func decidePreflop(sit *situation, sty style, rng *rand.Rand) decision {
	bb := float64(sit.bb)
	// A little noise turns hard thresholds into a mixed strategy around
	// the borderline hands.
	s := preflopStrength(sit.hole[0], sit.hole[1]) + (rng.Float64()-0.5)*0.04
	raised := sit.currentBet > sit.bb
	effBB := float64(sit.effStack) / bb

	// Short-stacked: push or fold, with a range that widens as the stack
	// shrinks, from late position and for looser personas.
	if float64(sit.stack+sit.bet)/bb < 12 {
		rangeFrac := 0.10 + 1.5/math.Max(effBB, 1) + 0.25*(1-sty.tight) + 0.10*sit.position -
			0.03*float64(max(sit.opponents-1, 0))
		if raised {
			rangeFrac *= 0.55 // calling off needs a better hand than shoving
		}
		if s >= 1-clampRange(rangeFrac, 0.06, 0.75) {
			return decision{kind: shove}
		}
		return decision{kind: passive}
	}

	vpip := 0.52 - 0.40*sty.tight + 0.12*sit.position - 0.025*float64(max(sit.opponents-3, 0))
	open := sit.bb*2 + sit.bb/2 + int(rng.Float64()*0.5*bb) + sit.limpers*sit.bb // 2.5-3bb, +1bb per limper

	if !raised {
		if s < 1-vpip {
			// Steal the blinds now and then from late position.
			if sit.position >= 0.75 && sit.limpers == 0 && rng.Float64() < sty.bluff*0.35 {
				return decision{kind: raiseIt, to: open}
			}
			return decision{kind: passive}
		}
		raiseP := 0.30 + 0.65*sty.aggr
		if s > 0.93 {
			raiseP = 0.95
		}
		if rng.Float64() < raiseP {
			return decision{kind: raiseIt, to: open}
		}
		return decision{kind: callIt}
	}

	// Facing a raise.
	raiseBB := float64(sit.currentBet) / bb
	station := (1 - sty.aggr) * (1 - sty.tight)
	callRange := vpip * 0.55 * (1 + station)
	if raiseBB > 6 {
		callRange *= 0.7
	}
	if raiseBB > 15 {
		callRange *= 0.6
	}
	if float64(sit.toCall) > 0.4*float64(sit.stack) {
		callRange *= 0.6
	}
	threeBet := 0.025 + 0.06*sty.aggr
	switch {
	case s >= 1-threeBet:
		if float64(sit.toCall) >= 0.35*float64(sit.stack) {
			return decision{kind: shove}
		}
		mult := 3.0
		if sit.position < 0.5 {
			mult = 3.5 // out of position, make it bigger
		}
		return decision{kind: raiseIt, to: int(float64(sit.currentBet) * mult)}
	case s >= 1-clampRange(callRange, 0.02, 0.7):
		return decision{kind: callIt}
	case sit.opponents <= 2 && float64(sit.stack) > 20*bb && rng.Float64() < sty.bluff*0.06:
		return decision{kind: raiseIt, to: sit.currentBet * 3}
	}
	return decision{kind: passive}
}

func clampRange(x, lo, hi float64) float64 { return math.Min(hi, math.Max(lo, x)) }

func decidePostflop(sit *situation, sty style, eq float64, rng *rand.Rand) decision {
	opps := max(1, sit.opponents)
	// Equity against several random hands is roughly the product of the
	// heads-up equities, so this is the hand's strength "per opponent".
	h := math.Pow(eq, 1/float64(opps))
	pot := math.Max(float64(sit.pot), 1)
	spr := float64(sit.stack) / pot
	short := float64(sit.stack+sit.bet)/float64(sit.bb) < 12
	r := rng.Float64()

	betTo := func(frac float64) decision {
		if short || spr < 1.2 {
			return decision{kind: shove}
		}
		return decision{kind: raiseIt, to: sit.currentBet + int(frac*(pot+float64(sit.toCall)))}
	}

	if sit.toCall == 0 {
		var p, frac float64
		switch {
		case h >= 0.80: // value; sometimes slow-play
			p, frac = 0.65+0.35*sty.aggr, 0.66+0.09*rng.Float64()
		case h >= 0.62: // thin value / protection
			p, frac = 0.25+0.55*sty.aggr, 0.5+0.15*rng.Float64()
		default: // bluff, fewer the more opponents there are
			p, frac = sty.bluff*0.45*math.Max(0, 1.2-0.25*float64(opps)), 0.5+0.25*rng.Float64()
		}
		if sit.aggressor && sit.street == streetFlop {
			p += 0.15 + 0.25*sty.aggr // continuation bet
		}
		p *= 0.85 + 0.3*sit.position
		if r < p {
			return betTo(frac)
		}
		return decision{kind: passive}
	}

	odds := float64(sit.toCall) / (pot + float64(sit.toCall))
	station := (1 - sty.aggr) * (1 - sty.tight)
	margin := 0.06*sty.tight - 0.12*station
	switch {
	case h >= 0.85 && eq > odds:
		if r < 0.35+0.55*sty.aggr {
			if spr < 2 {
				return decision{kind: shove}
			}
			return betTo(0.75)
		}
		return decision{kind: callIt}
	case eq >= odds+margin:
		if h >= 0.7 && r < sty.aggr*0.25 {
			return betTo(0.75)
		}
		return decision{kind: callIt}
	case opps == 1 && sit.stack > 3*sit.toCall && r < sty.bluff*0.08:
		return betTo(0.75) // bluff-raise
	}
	return decision{kind: passive}
}

// menu is the legal move list in a form the decision can be mapped onto.
type menu struct {
	fold, check, call, allin bool
	raise                    *games.Range
}

func newMenu(specs []games.MoveSpec) menu {
	var m menu
	for i := range specs {
		switch specs[i].Type {
		case "fold":
			m.fold = true
		case "check":
			m.check = true
		case "call":
			m.call = true
		case "allin":
			m.allin = true
		case "raise":
			m.raise = specs[i].Range
		}
	}
	return m
}

// pick maps an intent to a move that is guaranteed to be in the legal list:
// sizes are clamped to the raise range, and an unavailable action degrades
// to the closest available one.
func (m menu) pick(d decision) games.Move {
	passiveMove := func() games.Move {
		switch {
		case m.check:
			return games.Move{Type: "check"}
		case m.fold:
			return games.Move{Type: "fold"}
		case m.call:
			return games.Move{Type: "call"}
		}
		return games.Move{Type: "allin"}
	}
	callMove := func() games.Move {
		switch {
		case m.check:
			return games.Move{Type: "check"}
		case m.call:
			return games.Move{Type: "call"}
		case m.allin:
			return games.Move{Type: "allin"}
		}
		return passiveMove()
	}
	switch d.kind {
	case raiseIt:
		if m.raise != nil {
			to := min(max(d.to, m.raise.Min), m.raise.Max)
			if to == m.raise.Max && m.allin {
				return games.Move{Type: "allin"}
			}
			return games.Move{Type: "raise", Args: map[string]any{"to": to}}
		}
		// A full raise is not available (short stack or closed action):
		// an all-in is the nearest aggressive move when it is legal.
		if m.allin {
			return games.Move{Type: "allin"}
		}
		return callMove()
	case shove:
		if m.allin {
			return games.Move{Type: "allin"}
		}
		if m.raise != nil {
			return games.Move{Type: "raise", Args: map[string]any{"to": m.raise.Max}}
		}
		return callMove()
	case callIt:
		return callMove()
	}
	return passiveMove()
}
