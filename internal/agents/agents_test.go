package agents

import "testing"

func TestRosterMatchesContract(t *testing.T) {
	want := []string{"aoi", "ren", "mika", "bram", "nova", "lin"}
	got := IDs()
	if len(got) != len(want) {
		t.Fatalf("roster %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roster %v, want %v", got, want)
		}
		a, ok := Get(want[i])
		if !ok || a.Name == "" || a.NameZH == "" || a.Title == "" || a.TitleZH == "" || a.Bio == "" || a.BioZH == "" || a.Voice == "" {
			t.Fatalf("agent %s incomplete: %+v", want[i], a)
		}
		if a.Avatar != "/play/agents/"+want[i]+".webp" {
			t.Fatalf("avatar %q", a.Avatar)
		}
		for _, v := range []float64{a.Style.Tightness, a.Style.Aggression, a.Style.Bluff, a.Style.Talk} {
			if v < 0 || v > 1 {
				t.Fatalf("%s style out of range: %+v", a.ID, a.Style)
			}
		}
	}
}

// The numbers must say what the contract's style column says.
func TestStylesMatchPokerStyles(t *testing.T) {
	g := func(id string) Style { a, _ := Get(id); return a.Style }
	ren, mika, bram, lin, nova := g("ren"), g("mika"), g("bram"), g("lin"), g("nova")
	if !(ren.Tightness > 0.6 && ren.Aggression > 0.6) {
		t.Error("ren should be tight-aggressive")
	}
	if !(mika.Tightness < 0.4 && mika.Aggression > 0.6 && mika.Bluff > 0.5 && mika.Talk > 0.7) {
		t.Error("mika should be loose-aggressive, bluffy, loud")
	}
	if !(bram.Tightness < 0.4 && bram.Aggression < 0.4) {
		t.Error("bram should be loose-passive")
	}
	if !(lin.Tightness > 0.6 && lin.Aggression < 0.4 && lin.Bluff < 0.15 && lin.Talk < 0.4) {
		t.Error("lin should be tight-passive, rarely bluffs, shy")
	}
	if nova.Tightness < 0.4 || nova.Tightness > 0.6 || nova.Aggression < 0.4 || nova.Aggression > 0.6 {
		t.Error("nova should be balanced")
	}
	if ren.Talk >= mika.Talk {
		t.Error("ren talks less than mika")
	}
}

func TestPersonaSkillAndFallback(t *testing.T) {
	for d, want := range map[string]int{"casual": 0, "regular": 1, "shark": 2, "": 1, "SHARK": 2} {
		if p := Persona("mika", d); p.Skill != want || p.ID != "mika" || p.Bluff != 0.75 {
			t.Fatalf("Persona(mika,%q) = %+v", d, p)
		}
	}
	if p := Persona("nobody", "casual"); p.ID != Host {
		t.Fatalf("unknown agent should play as the host, got %+v", p)
	}
	for _, id := range IDs() {
		for _, tr := range []string{TriggerJoin, TriggerAllIn, TriggerBigPot, TriggerWin, TriggerLoss, TriggerBust, TriggerGameOver, TriggerReply, TriggerBanter, "unknown"} {
			for _, lang := range []string{"en", "zh"} {
				l := Fallback(id, tr, lang, "salt")
				if l == "" || len([]rune(l)) > 140 {
					t.Fatalf("Fallback(%s,%s,%s) = %q", id, tr, lang, l)
				}
				if Fallback(id, tr, lang, "salt") != l {
					t.Fatal("fallback must be deterministic")
				}
			}
		}
	}
}
