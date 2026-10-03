package games

import (
	"context"
	"math/rand"
)

// Persona is how an AI player plays. Every value is 0..1.
//
//	Tightness  how few starting hands / risky lines it plays
//	Aggression bets and raises versus checks and calls
//	Bluff      how often it represents strength it does not have
//	Talk       how much table talk it produces (used by the rooms service)
//
// Skill scales search effort: 0 casual, 1 regular, 2 shark.
type Persona struct {
	ID         string  `json:"id"`
	Tightness  float64 `json:"tightness"`
	Aggression float64 `json:"aggression"`
	Bluff      float64 `json:"bluff"`
	Talk       float64 `json:"talk"`
	Skill      int     `json:"skill"`
}

// Brain picks a move for an AI-controlled seat. It may only use what that
// seat is allowed to know: a hidden-information Brain works from
// g.View(st, seat) (or the game's own determinisation), never from the
// opponents' hidden state. It must return within the context deadline and
// must return a legal move; callers fall back to DefaultMove on error.
type Brain interface {
	Choose(ctx context.Context, g Game, st State, seat Seat, p Persona, rng *rand.Rand) (Move, error)
}
