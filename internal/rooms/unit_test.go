package rooms

import (
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

func TestSubst(t *testing.T) {
	names := []string{"Ann", "Captain Bram"}
	cases := map[string]string{
		"{s:0} raises to 120":     "Ann raises to 120",
		"{s:1} calls {s:0}":       "Captain Bram calls Ann",
		"{s:7} is not here":       "Seat 8 is not here",
		"no refs":                 "no refs",
		"{s:x} and {s:} stay put": "{s:x} and {s:} stay put",
	}
	for in, want := range cases {
		if got := Subst(in, names); got != want {
			t.Errorf("Subst(%q) = %q, want %q", in, got, want)
		}
	}
	v, err := substView(games.View{Kind: "board", Status: "{s:0} to move",
		Data: map[string]any{"message": "{s:1} won", "players": []any{map[string]any{"info": "{s:0}"}}, "n": 3}}, names)
	if err != nil {
		t.Fatal(err)
	}
	d := v.Data.(map[string]any)
	if v.Status != "Ann to move" || d["message"] != "Captain Bram won" || d["players"].([]any)[0].(map[string]any)["info"] != "Ann" || d["n"] != 3.0 {
		t.Fatalf("substView: %+v", v)
	}
}

func TestRevealsHidden(t *testing.T) {
	public := map[string]bool{"As": true, "Kd": true, "Th": true}
	for text, want := range map[string]bool{
		"Ah, nice hand!":                false, // a word, not a card
		"As expected.":                  false,
		"That Kd on the board, wow":     false, // public
		"10h? no, Th is out there":      false, // public, both spellings
		"I have Qs Qh":                  true,
		"pocket A♣ here":                true,
		"holding the queen of hearts":   true,
		"the ace of spades is out":      false,
		"我有黑桃A":                         false, // As is public
		"我有红桃Q":                         true,
		"スペードのAは見えてるね":                  false, // As is public
		"ハートのクイーンを持ってるよ":                true,
		"클로버 에이스가 내 손에":                 true,
		"다이아 K 나왔네":                     false, // Kd is public
		"As Ah, sweet":                  true,  // two codes: Ah counts here
		"Bet 20 on 3 to 1 odds, 9s ago": true,  // conservative: 9s reads as a card
	} {
		if got := revealsHidden(text, public); got != want {
			t.Errorf("revealsHidden(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestSanitizeLine(t *testing.T) {
	names := []string{"Ann", "Mika"}
	if got := sanitizeLine(`  "Mika: {s:0}, bring it!"  `, "Mika", names, nil); got != "Ann, bring it!" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeLine("line one\nline two", "Mika", names, nil); got != "line one line two" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("word ", 60)
	got := sanitizeLine(long, "Mika", names, nil)
	if n := len([]rune(got)); n > maxAgentRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("long line %d runes: %q", n, got)
	}
	if got := sanitizeLine("I hold 7c 2d lol", "Mika", names, map[string]bool{}); got != "" {
		t.Fatalf("a hidden card must reject the line, got %q", got)
	}
}

func TestThinkDelayRanges(t *testing.T) {
	s := &Service{}
	for speed, r := range map[string][2]float64{"fast": {0.4, 0.9}, "natural": {1.2, 3}, "slow": {2.5, 5}, "": {1.2, 3}} {
		for i := 0; i < 200; i++ {
			d := s.thinkDelay(speed, false).Seconds()
			if d < r[0] || d > r[1] {
				t.Fatalf("%s delay %.2f outside %v", speed, d, r)
			}
			if a := s.thinkDelay(speed, true).Seconds(); a < r[0]+3.5 || a > r[1]+3.5 {
				t.Fatalf("%s after-result delay %.2f", speed, a)
			}
		}
	}
	if !hasResult([]games.Event{{Type: "showdown"}}) || !hasResult([]games.Event{{Type: "round_end"}}) || hasResult([]games.Event{{Type: "raise"}}) {
		t.Fatal("result detection")
	}
	_ = time.Second
}

func TestSettingsFromPrefs(t *testing.T) {
	meta := games.Meta{TurnSeconds: 20}
	st := settingsFrom(map[string]any{"turn_seconds": "0", "agent_speed": "warp", "table_talk": "quiet",
		"agent_difficulty": "shark", "language": "zh-CN", "fill_empty_seats": false, "favorite_agents": []any{"lin", 3}}, meta)
	if st.TurnSeconds != 0 || st.AgentSpeed != "natural" || st.TableTalk != "quiet" || st.Difficulty != "shark" ||
		st.Language != "zh" || st.FillEmpty || len(st.Favorites) != 1 {
		t.Fatalf("%+v", st)
	}
	for in, want := range map[string]string{"ko": "ko", "ko-KR": "ko", "ja": "ja", "JA-jp": "ja", "fr": "en"} {
		if got := settingsFrom(map[string]any{"language": in}, meta).Language; got != want {
			t.Fatalf("language %q → %q, want %q", in, got, want)
		}
	}
	if d := settingsFrom(nil, meta); d.TurnSeconds != 20 || !d.FillEmpty || d.TableTalk != "all" || d.Language != "en" {
		t.Fatalf("defaults %+v", d)
	}
}

func TestIsListed(t *testing.T) {
	legal := []games.MoveSpec{{Type: "fold"}, {Type: "raise", Range: &games.Range{Arg: "to", Min: 40, Max: 1000}}}
	ok := map[*games.Move]bool{
		{Type: "fold"}: true,
		{Type: "raise", Args: map[string]any{"to": 40.0}}:   true,
		{Type: "raise", Args: map[string]any{"to": 1000}}:   true,
		{Type: "raise", Args: map[string]any{"to": 1001.0}}: false,
		{Type: "raise"}: false,
		{Type: "call"}:  false,
	}
	for m, want := range ok {
		if isListed(legal, *m) != want {
			t.Errorf("isListed(%+v) != %v", *m, want)
		}
	}
}
