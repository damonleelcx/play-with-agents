package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/holdem"
)

// Coaching is built from the caller's own seat only: nothing another seat
// holds privately (hole cards) may appear in it, whoever asks.
func TestCoachForUsesOnlyTheCallersSeat(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour } // agents never act
	ctx := context.Background()
	ann, bob, cy := r.user(t, "Ann", nil), r.user(t, "Bob", nil), r.user(t, "Cy", nil)
	v, err := r.svc.Create(ctx, ann, CreateRequest{GameID: "holdem", Seats: []SeatSpec{{Kind: "me"}, {Kind: "open"}, {Kind: "agent", AgentID: "mika"}, {Kind: "agent", AgentID: "ren"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Join(ctx, bob, v.Code); err != nil {
		t.Fatal(err)
	}
	r.start(t, ann, v.ID)
	if _, err := r.svc.Join(ctx, cy, v.Code); err != nil { // a spectator
		t.Fatal(err)
	}

	var raw []byte
	if err := r.pool.QueryRow(ctx, `SELECT state FROM tables WHERE id=$1`, v.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	st := games.State(raw)
	// Each seat's hole cards, as that seat alone sees them.
	hole := make([][]string, 4)
	for seat := range hole {
		sv, err := holdem.New().View(st, seat)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(sv.Data)
		var d struct {
			Players []struct {
				Cards []string `json:"cards"`
			} `json:"players"`
		}
		_ = json.Unmarshal(b, &d)
		hole[seat] = d.Players[seat].Cards
		if len(hole[seat]) != 2 {
			t.Fatalf("seat %d hole cards %v", seat, hole[seat])
		}
	}

	for who, seat := range map[string]int{ann: 0, bob: 1} {
		rep, err := r.svc.CoachFor(ctx, who, v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Seat != seat || rep.Holdem == nil || rep.Holdem.Suggestion == "" || rep.Holdem.Opponents != 3 {
			t.Fatalf("seat %d report %+v", seat, rep)
		}
		js, _ := json.Marshal(rep)
		s := string(js)
		for other, cards := range hole {
			for _, c := range cards {
				has := strings.Contains(s, `"`+c+`"`)
				if other == seat && !has {
					t.Fatalf("seat %d's coaching lacks their own card %s: %s", seat, c, s)
				}
				if other != seat && has {
					t.Fatalf("seat %d's coaching reveals seat %d's card %s: %s", seat, other, c, s)
				}
			}
		}
	}

	// A spectator has no seat to coach, and a stranger may not even look.
	var in *InputError
	if _, err := r.svc.CoachFor(ctx, cy, v.ID); !errors.As(err, &in) {
		t.Fatalf("spectator: %v", err)
	}
	stranger := r.user(t, "Dee", nil)
	if _, err := r.svc.CoachFor(ctx, stranger, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger: %v", err)
	}
}

func TestPlayerTablesAndLobbySettings(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	ctx := context.Background()
	ann, bob := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	lobby := r.create(t, ann, "me", "open", "agent:ren")
	game := r.start(t, bob, r.create(t, bob, "me", "agent:mika").ID)
	if _, err := r.svc.Join(ctx, ann, game.Code); err != nil { // Ann watches Bob's game
		t.Fatal(err)
	}
	done := r.create(t, ann, "me", "agent:nova")
	if _, err := r.pool.Exec(ctx, `UPDATE tables SET status='finished', updated_at=now()-interval '1 hour' WHERE id=$1`, done.ID); err != nil {
		t.Fatal(err)
	}

	open, err := r.svc.PlayerTables(ctx, ann, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("open tables %+v", open)
	}
	byID := map[string]PlayerTable{}
	for _, p := range open {
		byID[p.ID] = p
	}
	l, g := byID[lobby.ID], byID[game.ID]
	if !l.IsHost || l.MySeat != 0 || l.SeatsTotal != 3 || l.SeatsTaken != 2 || l.Code != lobby.Code || len(l.Players) != 2 {
		t.Fatalf("lobby %+v", l)
	}
	if g.IsHost || g.MySeat != -1 || g.Status != "playing" {
		t.Fatalf("watched game %+v", g)
	}
	all, _ := r.svc.PlayerTables(ctx, ann, true)
	if len(all) != 3 {
		t.Fatalf("with finished: %d", len(all))
	}
	if other, _ := r.svc.PlayerTables(ctx, r.user(t, "Cy", nil), true); len(other) != 0 {
		t.Fatalf("a stranger sees %v", other)
	}

	sixty := 60
	if err := r.svc.ApplyLobbySettings(ctx, bob, lobby.ID, LobbySettings{Difficulty: "shark"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-host: %v", err)
	}
	if err := r.svc.ApplyLobbySettings(ctx, ann, lobby.ID, LobbySettings{Difficulty: "godlike"}); err == nil {
		t.Fatal("invalid difficulty accepted")
	}
	if err := r.svc.ApplyLobbySettings(ctx, ann, lobby.ID, LobbySettings{Difficulty: "shark", TableTalk: "quiet", TurnSeconds: &sixty}); err != nil {
		t.Fatal(err)
	}
	tr, _ := loadTable(ctx, r.pool, lobby.ID, false)
	if tr.Settings.Difficulty != "shark" || tr.Settings.TableTalk != "quiet" || tr.Settings.TurnSeconds != 60 || tr.Settings.AgentSpeed != "natural" {
		t.Fatalf("settings %+v", tr.Settings)
	}
	if err := r.svc.ApplyLobbySettings(ctx, bob, game.ID, LobbySettings{Difficulty: "casual"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a started table's settings changed: %v", err)
	}
}

func TestPlayerResults(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	ctx := context.Background()
	ann := r.user(t, "Ann", nil)
	finish := func(id, outcome string) {
		t.Helper()
		if _, err := r.pool.Exec(ctx, `UPDATE tables SET status='finished', outcome=$2 WHERE id=$1`, id, outcome); err != nil {
			t.Fatal(err)
		}
	}
	// A hold'em win (+500 chips from a 1000 stack) and a loss (-1000).
	for i, score := range []string{`[1500,500]`, `[0,2000]`} {
		v, err := r.svc.Create(ctx, ann, CreateRequest{GameID: "holdem", Options: map[string]any{"starting_stack": 1000}, Seats: []SeatSpec{{Kind: "me"}, {Kind: "agent", AgentID: "mika"}}})
		if err != nil {
			t.Fatal(err)
		}
		rank := `[1,2]`
		if i == 1 {
			rank = `[2,1]`
		}
		finish(v.ID, `{"rank":`+rank+`,"score":`+score+`,"summary":"x"}`)
	}
	race := r.create(t, ann, "me", "agent:ren")
	finish(race.ID, `{"rank":[1,2],"score":[21,15],"summary":"x"}`)
	r.create(t, ann, "me", "open") // in progress (lobby)

	st, err := r.svc.PlayerResults(ctx, ann, 2)
	if err != nil {
		t.Fatal(err)
	}
	if st.Played != 3 || st.Wins != 2 || st.ChipsWon != -500 || st.InPlay != 1 || len(st.Recent) != 2 || st.ByGame["Texas Hold'em"] != 2 {
		t.Fatalf("stats %+v", st)
	}
	for _, g := range st.Recent {
		if g.GameID == "holdem" && !g.HasChips {
			t.Fatalf("hold'em result without chips: %+v", g)
		}
		if g.GameID != "holdem" && g.HasChips {
			t.Fatalf("chips on a non-hold'em game: %+v", g)
		}
	}
	if top := st.TopGames(); top[0] != "Texas Hold'em" {
		t.Fatalf("top games %v", top)
	}
	if none, _ := r.svc.PlayerResults(ctx, r.user(t, "Bob", nil), 5); none.Played != 0 {
		t.Fatalf("someone else's results: %+v", none)
	}
}
