package agent

import (
	"reflect"
	"testing"
)

func TestSlotsFoundInTheMessageWhenTheRouterMissesThem(t *testing.T) {
	games := []GameInfo{{ID: "holdem", Name: "Texas Hold'em"}, {ID: "moon", Name: "月沉之后：最后一页"}, {ID: "gem", Name: "Gem Rush"}, {ID: "gem2", Name: "Gem Rush Deluxe"}}
	cases := []struct {
		text   string
		game   string
		agents []string
	}{
		{"来玩《月沉之后：最后一页》吧！诺娃，轮到你上场了 📖", "moon", []string{"nova"}},
		{"Let's play gem rush deluxe with Mika and Captain Bram", "gem2", []string{"mika", "bram"}},
		{"Aoi, deal me in", "", nil},
	}
	for _, c := range cases {
		g, _ := gameInText(games, c.text)
		if g.ID != c.game {
			t.Errorf("%q: game %q, want %q", c.text, g.ID, c.game)
		}
		if a := agentsInText(c.text); !reflect.DeepEqual(a, c.agents) {
			t.Errorf("%q: agents %v, want %v", c.text, a, c.agents)
		}
	}
}
