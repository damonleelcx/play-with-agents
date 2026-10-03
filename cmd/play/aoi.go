package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/damonleelcx/play-with-agents/internal/agent"
	"github.com/damonleelcx/play-with-agents/internal/rooms"
)

// Aoi's hands at the tables: the chat agent's Tables and Catalog interfaces,
// served by the rooms service. They live here, not in either package, so
// neither the conversation nor the tables depends on the other.

type aoiTables struct{ rooms *rooms.Service }

// CreateTable seats the player, then the agents Aoi was asked for, then any
// remaining seats open for friends. A table with no open seat starts at once:
// someone who asked Aoi to deal them in wants cards, not a lobby.
func (a aoiTables) CreateTable(ctx context.Context, userID string, req agent.TableRequest) (string, error) {
	seats := []rooms.SeatSpec{{Kind: "me"}}
	for _, id := range req.AgentIDs {
		seats = append(seats, rooms.SeatSpec{Kind: "agent", AgentID: id})
	}
	for len(seats) < req.Seats {
		seats = append(seats, rooms.SeatSpec{Kind: "open"})
	}
	tv, err := a.rooms.Create(ctx, userID, rooms.CreateRequest{GameID: req.GameID, Options: req.Options, Seats: seats})
	if err != nil {
		return "", reject(err)
	}
	for _, s := range tv.Seats {
		if s.Kind == "open" {
			return tv.ID, nil
		}
	}
	if _, err := a.rooms.Start(ctx, userID, tv.ID); err != nil {
		// The table exists and the card still links to it; the player can
		// press start themselves.
		return tv.ID, nil
	}
	return tv.ID, nil
}

type aoiCatalog struct{ rooms *rooms.Service }

func (c aoiCatalog) Games(ctx context.Context, userID string) ([]agent.GameInfo, error) {
	l, err := c.rooms.Games(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []agent.GameInfo
	add := func(cards []rooms.GameCard, mine bool) {
		for _, g := range cards {
			if g.Status == "building" {
				continue // nothing to play yet
			}
			out = append(out, agent.GameInfo{ID: g.ID, Name: g.Name, Summary: g.Summary, MinSeats: g.MinSeats, MaxSeats: g.MaxSeats, Mine: mine})
		}
	}
	add(l.Builtin, false)
	add(l.Mine, true)
	add(l.Community, false)
	return out, nil
}

func (c aoiCatalog) Rules(ctx context.Context, userID, gameID string) (string, error) {
	d, err := c.rooms.GameDetail(ctx, userID, gameID)
	if err != nil {
		return "", reject(err)
	}
	return d.RulesMD, nil
}

// reject turns the rooms service's client errors into agent.ErrRejected, whose
// message Aoi relays to the player; anything else stays an internal failure.
func reject(err error) error {
	var in *rooms.InputError
	switch {
	case errors.As(err, &in):
		return fmt.Errorf("%w: %s", agent.ErrRejected, in.Msg)
	case errors.Is(err, rooms.ErrNotFound):
		return fmt.Errorf("%w: that game is not on the shelf", agent.ErrRejected)
	case errors.Is(err, rooms.ErrForbidden):
		return fmt.Errorf("%w: that game is private", agent.ErrRejected)
	}
	return err
}
