package script

import (
	"strings"
	"testing"
)

// Rich cards, cell.card, story and prompt in the board view; the
// {zone,index,cell} and {zone,index,target} ui hints.

func TestValidateViewStoryCards(t *testing.T) {
	long := strings.Repeat("x", 301)
	manyLines := "[" + strings.TrimSuffix(strings.Repeat(`{"text":"a"},`, 201), ",") + "]"
	rich := `{"title":"The Lamplighter","text":"Every dusk he climbed the hill.","effect":"+1 per adjacent place.","kind":"character","value":1,"cost":"2","accent":"#e7b75f","seat":1}`
	cases := []struct{ name, view, want string }{
		{"rich hand card", `{"zones":[{"id":"hand-0","owner":0,"layout":"fan","cards":[` + rich + `]}]}`, ""},
		{"card on a cell", `{"board":{"rows":1,"cols":2,"style":"tiles","cells":[[{"card":` + rich + `},{"blocked":true}]]}}`, ""},
		{"story and prompt", `{"story":[{"text":"Once upon a time.","seat":0,"title":"Opening"},{"text":"{s:1} smiled."}],"prompt":"Choose a card"}`, ""},
		{"piece and card", `{"board":{"rows":1,"cols":1,"cells":[[{"piece":{},"card":{"title":"x"}}]]}}`, "has both piece and card"},
		{"card typo", `{"zones":[{"id":"z","cards":[{"titel":"x"}]}]}`, "unknown key(s) titel"},
		{"text too long", `{"zones":[{"id":"z","cards":[{"text":"` + long + `"}]}]}`, "over the limit of 300"},
		{"hidden leaks title", `{"zones":[{"id":"z","cards":[{"hidden":true,"title":"Fox"}]}]}`, "is hidden but has title"},
		{"value type", `{"zones":[{"id":"z","cards":[{"value":true}]}]}`, "number or a short string"},
		{"card seat", `{"zones":[{"id":"z","cards":[{"seat":5}]}]}`, "seat number 0..1"},
		{"blocked type", `{"board":{"rows":1,"cols":1,"cells":[[{"blocked":"yes"}]]}}`, "must be true or false"},
		{"story not array", `{"story":"once"}`, "view.story must be an array"},
		{"story without text", `{"story":[{"seat":0}]}`, "view.story[0].text must be a non-empty string"},
		{"story too long", `{"story":` + manyLines + `}`, "has 201 lines, over the limit of 200"},
		{"story bad seat", `{"story":[{"text":"a","seat":2}]}`, "view.story[0].seat must be a seat number"},
		{"story placeholder", `{"story":[{"text":"{s:7} ran"}]}`, "placeholder {s:7}"},
		{"prompt type", `{"prompt":3}`, "view.prompt must be a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateView(tc.view, 2, 64<<10)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestParseUICardHints(t *testing.T) {
	cases := []struct{ name, legal, want string }{
		{"card to cell", `[{"type":"play","label":"a","ui":{"zone":"hand-0","index":1,"cell":[2,3]}}]`, ""},
		{"card with target", `[{"type":"play","label":"a","ui":{"zone":"hand-0","index":1,"target":"Swap"}}]`, ""},
		{"target without zone", `[{"type":"play","label":"a","ui":{"target":"Swap"}}]`, "has target without zone"},
		{"target and cell", `[{"type":"play","label":"a","ui":{"zone":"h","index":0,"cell":[0,0],"target":"x"}}]`, "has both cell and target"},
		{"from with zone", `[{"type":"play","label":"a","ui":{"zone":"h","index":0,"from":[0,0],"to":[0,1]}}]`, "mixes from/to"},
		{"cell shape", `[{"type":"play","label":"a","ui":{"zone":"h","index":0,"cell":[1]}}]`, "must be [row, col]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseLegal(tc.legal, 512)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// Check reports hints the mover's view cannot show: the move would be
// unclickable.
func TestCheckCatchesDanglingHints(t *testing.T) {
	cases := []struct{ name, legal, view, want string }{
		{"missing zone", `{ type: "inc", label: "+1", ui: { zone: "hand", index: 0, cell: [0, 0] } }`,
			`{ board: { rows: 1, cols: 1, cells: [[null]] }, message: "go" }`, `has ui.zone "hand" but the mover's view has no zone`},
		{"index past the zone", `{ type: "inc", label: "+1", ui: { zone: "hand", index: 2 } }`,
			`{ zones: [{ id: "hand", owner: s.turn, cards: [{ title: "a" }] }], message: "go" }`, "that zone shows 1 cards"},
		{"cell off the board", `{ type: "inc", label: "+1", ui: { cell: [3, 0] } }`,
			`{ board: { rows: 2, cols: 2, cells: [[null, null], [null, null]] }, message: "go" }`, "outside the 2x2 board"},
		{"no board", `{ type: "inc", label: "+1", ui: { cell: [0, 0] } }`,
			`{ message: "go" }`, "the view has no board"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := module(`legal(s, seat) { return s.n >= 5 || seat !== s.turn ? [] : [` + tc.legal + `]; },
				view(s, seat) { return ` + tc.view + `; },`)
			r := Check("t", src)
			if !strings.Contains(r.String(), tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, r)
			}
		})
	}
}
