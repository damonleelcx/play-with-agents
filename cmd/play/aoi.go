package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/damonleelcx/play-with-agents/internal/agent"
	"github.com/damonleelcx/play-with-agents/internal/rooms"
)

// Aoi's hands at the tables: the chat agent's Tables, Catalog, Coach and
// Stats interfaces, served by the rooms service. They live here, not in
// either package, so neither the conversation nor the tables depends on the
// other. Every call acts as the player, with the same checks as the table
// API's own endpoints.

type aoiTables struct{ rooms *rooms.Service }

// CreateTable seats the player, then the agents Aoi was asked for, then any
// remaining seats open for friends, and applies the per-table settings. A
// table with no open seat starts at once: someone who asked Aoi to deal them
// in wants cards, not a lobby.
func (a aoiTables) CreateTable(ctx context.Context, userID string, req agent.TableRequest) (agent.TableInfo, error) {
	seats := []rooms.SeatSpec{{Kind: "me"}}
	for _, id := range req.AgentIDs {
		seats = append(seats, rooms.SeatSpec{Kind: "agent", AgentID: id})
	}
	for len(seats) < req.Seats {
		seats = append(seats, rooms.SeatSpec{Kind: "open"})
	}
	tv, err := a.rooms.Create(ctx, userID, rooms.CreateRequest{GameID: req.GameID, Options: req.Options, Seats: seats, TurnSeconds: req.Settings.TurnSeconds})
	if err != nil {
		return agent.TableInfo{}, reject(err)
	}
	s := req.Settings
	if s.Difficulty != "" || s.AgentSpeed != "" || s.TableTalk != "" {
		if err := a.rooms.ApplyLobbySettings(ctx, userID, tv.ID, rooms.LobbySettings{Difficulty: s.Difficulty, AgentSpeed: s.AgentSpeed, TableTalk: s.TableTalk}); err != nil {
			// The table exists with the player's usual settings; say so
			// rather than pretend the override applied.
			return infoFromView(tv), rejectTable(err)
		}
	}
	for _, st := range tv.Seats {
		if st.Kind == "open" {
			return infoFromView(tv), nil
		}
	}
	if v, err := a.rooms.Start(ctx, userID, tv.ID); err == nil {
		return infoFromView(v), nil
	}
	// The table exists and the card still links to it; the player can press
	// start themselves.
	return infoFromView(tv), nil
}

func (a aoiTables) MyTables(ctx context.Context, userID string, withFinished bool) ([]agent.TableInfo, error) {
	ts, err := a.rooms.PlayerTables(ctx, userID, withFinished)
	if err != nil {
		return nil, err
	}
	out := make([]agent.TableInfo, 0, len(ts))
	for _, t := range ts {
		out = append(out, agent.TableInfo{ID: t.ID, Name: t.Name, Code: t.Code, GameID: t.GameID, GameName: t.GameName, Status: t.Status,
			Seats: t.SeatsTotal, OpenSeats: t.SeatsTotal - t.SeatsTaken, Players: t.Players, IsHost: t.IsHost, Seated: t.MySeat >= 0,
			Away: t.Away, Paused: t.Paused, PausedReason: t.PausedReason, RematchID: t.RematchID, UpdatedAt: t.UpdatedAt})
	}
	return out, nil
}

func (a aoiTables) JoinTable(ctx context.Context, userID, code string) (agent.TableInfo, error) {
	v, err := a.rooms.Join(ctx, userID, code)
	if err != nil {
		return agent.TableInfo{}, rejectTable(err)
	}
	return infoFromView(v), nil
}

func (a aoiTables) LeaveTable(ctx context.Context, userID, tableID string) error {
	return rejectTable(a.rooms.Leave(ctx, userID, tableID))
}

func (a aoiTables) BackToTable(ctx context.Context, userID, tableID string) (agent.TableInfo, error) {
	v, err := a.rooms.Back(ctx, userID, tableID)
	if err != nil {
		return agent.TableInfo{}, rejectTable(err)
	}
	return infoFromView(v), nil
}

func (a aoiTables) Rematch(ctx context.Context, userID, tableID string) (agent.TableInfo, error) {
	v, err := a.rooms.Rematch(ctx, userID, tableID)
	if err != nil {
		return agent.TableInfo{}, rejectTable(err)
	}
	return infoFromView(v), nil
}

// Advice coaches the player from their own seat (rooms.CoachFor builds it
// from that seat's view only).
func (a aoiTables) Advice(ctx context.Context, userID, tableID string) (agent.CoachAdvice, error) {
	rep, err := a.rooms.CoachFor(ctx, userID, tableID)
	if err != nil {
		return agent.CoachAdvice{}, rejectTable(err)
	}
	adv := agent.CoachAdvice{TableID: rep.TableID, GameID: rep.GameID, GameName: rep.GameName, YourTurn: rep.YourTurn, Legal: rep.Legal, YourView: string(rep.View)}
	if h := rep.Holdem; h != nil {
		adv.Holdem = &agent.CoachTip{HandName: h.HandName, Equity: h.Equity, PotOdds: h.PotOdds, Opponents: h.Opponents, ToCall: h.ToCall, Suggestion: h.Suggestion}
	}
	return adv, nil
}

func (a aoiTables) PlayerStats(ctx context.Context, userID string) (agent.PlayerStats, error) {
	st, err := a.rooms.PlayerResults(ctx, userID, 5)
	if err != nil {
		return agent.PlayerStats{}, err
	}
	out := agent.PlayerStats{Played: st.Played, Wins: st.Wins, ChipsWon: st.ChipsWon, InPlay: st.InPlay, ByGame: st.ByGame}
	for _, r := range st.Recent {
		out.Recent = append(out.Recent, agent.GameResult{GameName: r.GameName, Place: r.Place, Players: r.Players, Chips: r.Chips, HasChips: r.HasChips, FinishedAt: r.FinishedAt})
	}
	return out, nil
}

func infoFromView(v *rooms.TableView) agent.TableInfo {
	info := agent.TableInfo{ID: v.ID, Name: v.Name, Code: v.Code, GameID: v.Game.ID, GameName: v.Game.Name, Status: v.Status,
		Seats: len(v.Seats), IsHost: v.IsHost, Seated: v.MySeat >= 0, Paused: v.Paused, PausedReason: v.PausedReason, RematchID: v.RematchID}
	for _, s := range v.Seats {
		switch {
		case s.Kind == "open":
			info.OpenSeats++
		default:
			info.Players = append(info.Players, s.Name)
		}
		if s.IsMe && s.Away {
			info.Away = true
		}
	}
	return info
}

type aoiCatalog struct{ rooms *rooms.Service }

func gameInfo(g rooms.GameCard, mine bool) agent.GameInfo {
	return agent.GameInfo{ID: g.ID, Name: g.Name, Summary: g.Summary, MinSeats: g.MinSeats, MaxSeats: g.MaxSeats, Mine: mine,
		Kind: g.Kind, Status: g.Status, Visibility: g.Visibility, Plays: g.Plays, Owner: g.OwnerName}
}

func (c aoiCatalog) Games(ctx context.Context, userID string) ([]agent.GameInfo, error) {
	l, err := c.rooms.Games(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []agent.GameInfo
	for _, g := range l.Builtin {
		out = append(out, gameInfo(g, false))
	}
	// The player's own games are listed in every status (a build in
	// progress is something they talk about); play refuses one still
	// building.
	for _, g := range l.Mine {
		out = append(out, gameInfo(g, true))
	}
	for _, g := range l.Community {
		out = append(out, gameInfo(g, false))
	}
	return out, nil
}

func (c aoiCatalog) Rules(ctx context.Context, userID, gameID string) (string, error) {
	d, err := c.rooms.GameDetail(ctx, userID, gameID)
	if err != nil {
		return "", reject(err)
	}
	return d.RulesMD, nil
}

func (c aoiCatalog) Game(ctx context.Context, userID, gameID string) (agent.GameInfo, error) {
	d, err := c.rooms.GameDetail(ctx, userID, gameID)
	if err != nil {
		return agent.GameInfo{}, reject(err)
	}
	// GoalID is only filled for the owner.
	mine := d.GoalID != "" || ownedBy(ctx, c.rooms, userID, gameID)
	return gameInfo(d.GameCard, mine), nil
}

func ownedBy(ctx context.Context, s *rooms.Service, userID, gameID string) bool {
	var ok bool
	_ = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM games WHERE id=$1 AND owner_id::text=$2)`, gameID, userID).Scan(&ok)
	return ok
}

func (c aoiCatalog) SetVisibility(ctx context.Context, userID, gameID, visibility string) error {
	_, err := c.rooms.PatchGame(ctx, userID, gameID, rooms.GamePatch{Visibility: &visibility})
	return reject(err)
}

func (c aoiCatalog) Rename(ctx context.Context, userID, gameID, name string) error {
	_, err := c.rooms.PatchGame(ctx, userID, gameID, rooms.GamePatch{Name: &name})
	return reject(err)
}

func (c aoiCatalog) DeleteDraft(ctx context.Context, userID, gameID string) error {
	return reject(c.rooms.DeleteGame(ctx, userID, gameID))
}

// reject turns the rooms service's client errors about games into
// agent.ErrRejected, whose message Aoi relays to the player; anything else
// stays an internal failure.
func reject(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, rooms.ErrNotFound):
		return fmt.Errorf("%w: that game is not on the shelf (or is not yours)", agent.ErrRejected)
	case errors.Is(err, rooms.ErrForbidden):
		return fmt.Errorf("%w: that game is private", agent.ErrRejected)
	}
	if msg, ok := rooms.IsClientError(err); ok {
		return fmt.Errorf("%w: %s", agent.ErrRejected, msg)
	}
	return err
}

// rejectTable is reject for table operations.
func rejectTable(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, rooms.ErrNotFound):
		return fmt.Errorf("%w: that table was not found", agent.ErrRejected)
	case errors.Is(err, rooms.ErrForbidden):
		return fmt.Errorf("%w: only the host can do that at this table", agent.ErrRejected)
	}
	if msg, ok := rooms.IsClientError(err); ok {
		return fmt.Errorf("%w: %s", agent.ErrRejected, msg)
	}
	return err
}
