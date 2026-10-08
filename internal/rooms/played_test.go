package rooms

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// hopper offers hinted moves: add n, with a cell hint per n.
type hopper struct{ race21 }

func (hopper) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	return []games.MoveSpec{
		{Type: "add", Args: map[string]any{"n": 1}, UI: map[string]any{"from": "a", "to": "b"}},
		{Type: "add", Args: map[string]any{"n": 2}, UI: map[string]any{"cell": []any{0.0, 1.0}}},
		{Type: "pass", Args: map[string]any{}},
	}, nil
}

func TestMoveUIMatchesTheOfferedHint(t *testing.T) {
	g := hopper{}
	if ui := moveUI(g, games.State(`{}`), 0, games.Move{Type: "add", Args: map[string]any{"n": 1.0}}); ui["to"] != "b" {
		t.Fatalf("hint for add 1: %v", ui)
	}
	if ui := moveUI(g, games.State(`{}`), 0, games.Move{Type: "add", Args: map[string]any{"n": 3}}); ui != nil {
		t.Fatalf("an unoffered move got a hint: %v", ui)
	}
	if ui := moveUI(g, games.State(`{}`), 0, games.Move{Type: "pass"}); ui != nil {
		t.Fatalf("a hintless move got a hint: %v", ui)
	}
}

func TestPlayedMovesHideAnotherSeatsSecretChoices(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.user(t, "Ann", nil)
	v := r.create(t, a, "me", "agent:aoi")
	ins := func(seq, seat int, ui string) {
		if _, err := r.pool.Exec(ctx, `INSERT INTO table_moves (table_id, seq, seat, actor, move, version, ui) VALUES ($1,$2::int,$3,'agent','{}',$2::bigint,$4)`,
			v.ID, seq, seat, ui); err != nil {
			t.Fatal(err)
		}
	}
	ins(1, 1, `{"from":"a","to":"b"}`)                        // a piece now on b: public
	ins(2, 1, `{"cell":"secret-island"}`)                     // a secret pick: nothing shows there
	ins(3, 1, `{"zone":"hand-1","index":2,"cell":[0,1]}`)     // a card now face up at 0,1
	ins(4, 0, `{"zone":"hand-0","index":1,"target":"North"}`) // my own move: all of it
	view := &games.View{Kind: "board", Data: json.RawMessage(`{"board":{"rows":1,"cols":2,"cells":[[null,{"card":{"title":"Owl"}}]],"spaces":[]}}`)}
	// A grid view and a map view are alternatives; check each.
	mapView := &games.View{Kind: "board", Data: json.RawMessage(`{"board":{"spaces":[{"id":"b","x":1,"y":1,"pieces":[{"shape":"ship"}]}]}}`)}

	got, err := r.svc.playedMoves(ctx, v.ID, 0, true, view)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != 3 || got[0].UI["zone"] != nil || got[1].Seq != 4 || got[1].UI["target"] != "North" {
		t.Fatalf("grid view: %+v", got)
	}
	got, _ = r.svc.playedMoves(ctx, v.ID, 0, true, mapView)
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 4 {
		t.Fatalf("map view: %+v", got)
	}
	// Perfect information: every hint, oldest first.
	got, _ = r.svc.playedMoves(ctx, v.ID, 0, false, view)
	if len(got) != 4 || got[0].Seq != 1 || got[3].Seq != 4 {
		t.Fatalf("open game: %+v", got)
	}
}
