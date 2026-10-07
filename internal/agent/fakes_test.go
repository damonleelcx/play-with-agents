package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Fakes for the capabilities. They record what Aoi asked for, so the turn
// tests check what she DOES with a route.

type fakeTables struct {
	mu     sync.Mutex
	got    []TableRequest
	err    error
	tables []TableInfo // what MyTables returns (open ones unless finished)
	calls  []string    // "join:CODE", "leave:ID", "back:ID", "rematch:ID", "advice:USER:ID"
	advice CoachAdvice
	stats  PlayerStats
}

func (f *fakeTables) log(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }

func (f *fakeTables) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeTables) CreateTable(_ context.Context, _ string, req TableRequest) (TableInfo, error) {
	f.mu.Lock()
	f.got = append(f.got, req)
	f.mu.Unlock()
	if f.err != nil {
		return TableInfo{}, f.err
	}
	open := req.Seats - 1 - len(req.AgentIDs)
	st := "playing"
	if open > 0 {
		st = "lobby"
	}
	return TableInfo{ID: "tbl-1", Code: "K7M2QX", Name: "Sam's table", GameID: req.GameID, Status: st, Seats: req.Seats, OpenSeats: open, IsHost: true, Seated: true}, nil
}

func (f *fakeTables) MyTables(_ context.Context, _ string, withFinished bool) ([]TableInfo, error) {
	var out []TableInfo
	for _, t := range f.tables {
		if withFinished || t.Status == "lobby" || t.Status == "playing" {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeTables) find(id string) (TableInfo, error) {
	for _, t := range f.tables {
		if t.ID == id {
			return t, nil
		}
	}
	return TableInfo{}, fmt.Errorf("%w: that table was not found", ErrRejected)
}

func (f *fakeTables) JoinTable(_ context.Context, _ string, code string) (TableInfo, error) {
	f.log("join:" + code)
	for _, t := range f.tables {
		if t.Code == code {
			t.Seated = true
			return t, nil
		}
	}
	return TableInfo{}, fmt.Errorf("%w: that table was not found", ErrRejected)
}

func (f *fakeTables) LeaveTable(_ context.Context, _ string, id string) error {
	f.log("leave:" + id)
	_, err := f.find(id)
	return err
}

func (f *fakeTables) BackToTable(_ context.Context, _ string, id string) (TableInfo, error) {
	f.log("back:" + id)
	t, err := f.find(id)
	t.Paused, t.Away = false, false
	return t, err
}

func (f *fakeTables) Rematch(_ context.Context, _ string, id string) (TableInfo, error) {
	f.log("rematch:" + id)
	if _, err := f.find(id); err != nil {
		return TableInfo{}, err
	}
	return TableInfo{ID: "tbl-rematch", Code: "RMT234", Name: "rematch", Status: "playing", Seats: 3, Seated: true, IsHost: true}, nil
}

func (f *fakeTables) Advice(_ context.Context, userID, tableID string) (CoachAdvice, error) {
	f.log("advice:" + userID + ":" + tableID)
	return f.advice, nil
}

func (f *fakeTables) PlayerStats(context.Context, string) (PlayerStats, error) { return f.stats, nil }

type fakeStudio struct {
	prompt, base string
	err          error
}

func (f *fakeStudio) StartBuild(_ context.Context, _, _, prompt, base, _ string) (string, string, error) {
	f.prompt, f.base = prompt, base
	return "11111111-1111-1111-1111-111111111111", "my-game", f.err
}

// catalogLog records the owner-only catalog changes.
type catalogLog struct {
	mu    sync.Mutex
	calls []string // "visibility:ID:VIS", "rename:ID:NAME", "delete:ID"
}

func (l *catalogLog) add(s string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.calls = append(l.calls, s)
	l.mu.Unlock()
}

func (l *catalogLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

type fakeCatalog struct{ log *catalogLog }

var fakeGames = []GameInfo{
	builtinGames[0],
	{ID: "dragon-chess", Name: "Dragon Chess", Summary: "Chess where dragons fly over pieces.", MinSeats: 2, MaxSeats: 2, Mine: true, Kind: "script", Status: "draft", Visibility: "private"},
	{ID: "star-race", Name: "Star Race", Summary: "Race ships across a starfield, 2-4 players.", MinSeats: 2, MaxSeats: 4, Kind: "script", Status: "published", Visibility: "public", Owner: "Kim", Plays: 40},
	{ID: "moon-tiles", Name: "Moon Tiles", Summary: "Place tiles to build a moon base.", MinSeats: 1, MaxSeats: 4, Mine: true, Kind: "script", Status: "published", Visibility: "public"},
}

func (fakeCatalog) Games(context.Context, string) ([]GameInfo, error) {
	return append([]GameInfo(nil), fakeGames...), nil
}

func (fakeCatalog) Rules(_ context.Context, _, id string) (string, error) {
	if id == "holdem" {
		return "Side pots: when a player is all-in, later bets go to a side pot they cannot win.", nil
	}
	return "", errors.New("no rules")
}

func (fakeCatalog) Game(_ context.Context, _, id string) (GameInfo, error) {
	for _, g := range fakeGames {
		if g.ID == id {
			return g, nil
		}
	}
	return GameInfo{}, fmt.Errorf("%w: not found", ErrRejected)
}

func (c fakeCatalog) SetVisibility(_ context.Context, _, id, vis string) error {
	c.log.add("visibility:" + id + ":" + vis)
	return nil
}

func (c fakeCatalog) Rename(_ context.Context, _, id, name string) error {
	c.log.add("rename:" + id + ":" + name)
	return nil
}

func (c fakeCatalog) DeleteDraft(_ context.Context, _, id string) error {
	c.log.add("delete:" + id)
	return nil
}
