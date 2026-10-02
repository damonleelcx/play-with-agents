package rooms

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/holdem"
)

// Real Hold'em with the real Hold'em Brain on the workers: the table plays a
// few hands to the end, agent talk is triggered by its events, and nothing
// the Chatter is shown contains a card that is not public.
func TestHoldemAllAgentsIntegration(t *testing.T) {
	r := newRig(t)
	brain := holdem.Brain{}
	r.svc.BrainFor = func(games.Game) games.Brain { return brain }
	fc := &fakeChatter{line: "Nice hand!"}
	r.svc.Chatter = fc
	host := r.user(t, "Host", map[string]any{"table_talk": "all"})
	ctx := context.Background()
	v, err := r.svc.Create(ctx, host, CreateRequest{GameID: "holdem", Options: map[string]any{"max_hands": 3},
		Seats: []SeatSpec{{Kind: "agent", AgentID: "mika"}, {Kind: "agent", AgentID: "bram"}, {Kind: "agent", AgentID: "nova"}}})
	if err != nil {
		t.Fatal(err)
	}
	r.start(t, host, v.ID)

	// Spectator view mid-hand: nobody's hole cards.
	sv, err := r.svc.View(ctx, v.ID, host)
	if err != nil || sv.View == nil || sv.View.Kind != "holdem" {
		t.Fatalf("view: %v %+v", err, sv)
	}
	raw, _ := json.Marshal(sv.View.Data)
	var data struct {
		Players []struct {
			Cards []string `json:"cards"`
		} `json:"players"`
	}
	_ = json.Unmarshal(raw, &data)
	for i, p := range data.Players {
		if len(p.Cards) > 0 {
			t.Fatalf("the host (spectator) sees seat %d's cards %v", i, p.Cards)
		}
	}
	for _, e := range sv.Log {
		if e.Type == "deal" && e.Seat >= 0 {
			t.Fatalf("the spectator sees a private deal event: %+v", e)
		}
	}

	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { r.svc.RunWorkers(wctx, 4); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	var final *TableView
	waitFor(t, 90*time.Second, "three hands of hold'em", func() bool {
		final, _ = r.svc.View(ctx, v.ID, host)
		return final != nil && final.Status == "finished"
	})
	if final.Outcome == nil || strings.Contains(final.Outcome.Summary, "{s:") {
		t.Fatalf("outcome %+v", final.Outcome)
	}
	if n := r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1 AND actor <> 'agent'`, v.ID); n != 0 {
		t.Fatalf("%d moves fell back to timeouts", n)
	}
	cardRe := regexp.MustCompile(`\b[2-9TJQKA][shdc]\b`)
	fc.mu.Lock()
	defer fc.mu.Unlock()
	for _, req := range fc.reqs {
		if strings.Contains(req.Table, "{s:") {
			t.Fatalf("unsubstituted prompt: %s", req.Table)
		}
		// Every card in a prompt must be on the public table (board or shown).
		for _, c := range cardRe.FindAllString(req.Table, -1) {
			if !strings.Contains(req.Table, `"board":`) && !strings.Contains(req.Table, "shown") {
				t.Fatalf("card %s in a prompt with no public cards: %s", c, req.Table)
			}
		}
	}
	t.Logf("hold'em: %d moves, %d chatter calls, outcome %q", r.count(t, `SELECT move_seq FROM tables WHERE id=$1`, v.ID), len(fc.reqs), final.Outcome.Summary)
}
