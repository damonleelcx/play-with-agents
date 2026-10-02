package tools

import (
	"reflect"
	"testing"
	"time"
)

func d(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

func TestDeadlineRollsOverWeekendsAndHolidays(t *testing.T) {
	cases := []struct {
		trigger string
		days    int
		court   bool
		want    string
	}{
		{"2026-06-01", 30, false, "2026-07-01"}, // plain
		{"2026-06-04", 30, false, "2026-07-06"}, // lands Sat Jul 4 → Mon Jul 6 (Jul 3 observed holiday is Fri, not counted as landing)
		{"2026-12-04", 21, false, "2026-12-28"}, // Fri Dec 25 holiday → lands Dec 25 → next court day Mon Dec 28
		{"2026-09-01", 5, true, "2026-09-09"},   // court days skip Labor Day Mon Sep 7
		{"2026-01-15", 3, false, "2026-01-20"},  // lands Sun Jan 18 → Mon Jan 19 is MLK Day → Tue Jan 20
	}
	for _, c := range cases {
		got, _ := Deadline(d(c.trigger), c.days, c.court)
		if got.Format("2006-01-02") != c.want {
			t.Errorf("%s +%d (court=%v) = %s, want %s", c.trigger, c.days, c.court, got.Format("2006-01-02"), c.want)
		}
	}
}

func TestFederalHolidays(t *testing.T) {
	for _, h := range []string{"2026-01-01", "2026-01-19", "2026-02-16", "2026-05-25", "2026-06-19", "2026-07-03", "2026-09-07",
		"2026-10-12", "2026-11-11", "2026-11-26", "2026-12-25", "2027-12-31"} {
		if !FederalHoliday(d(h)) {
			t.Errorf("%s should be a holiday", h)
		}
	}
	for _, n := range []string{"2026-07-04" /* Saturday: observed Friday */, "2026-03-10", "2026-11-27"} {
		if FederalHoliday(d(n)) {
			t.Errorf("%s should not be a holiday", n)
		}
	}
}

func TestExtractCitations(t *testing.T) {
	text := `See Marbury v. Madison, 5 U.S. 137 (1803); Granberry v. Islay Invs., 9 Cal. 4th 738 (1995);
	Smith v. X, 999 F.3d 12; also 123 Cal. App. 4th 456 and 45 N.E.2d 789. Filed on 10 Jan. 2024, see 42 U.S.C. § 1983.`
	got := ExtractCitations(text)
	want := []string{"5 U.S. 137", "9 Cal. 4th 738", "999 F.3d 12", "123 Cal. App. 4th 456", "45 N.E.2d 789"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestRedFlags(t *testing.T) {
	hit := []string{
		"I have crushing chest pain going down my left arm and I'm sweating",
		"my dad's face is drooping and he has slurred speech",
		"I can't breathe",
		"I want to kill myself",
		"我胸口剧痛，左手臂发麻还出汗",
		"我不想活了",
		"孩子叫不醒",
	}
	for _, s := range hit {
		if len(CheckRedFlags(s)) == 0 {
			t.Errorf("missed: %s", s)
		}
	}
	miss := []string{"I have a mild headache since yesterday", "my landlord kept my deposit", "我的房东不退押金", "chest x-ray results look normal"}
	for _, s := range miss {
		if f := CheckRedFlags(s); len(f) > 0 {
			t.Errorf("false alarm on %q: %v", s, f[0].Code)
		}
	}
}

func TestControlledDenyList(t *testing.T) {
	for _, s := range []string{"Oxycodone 5 mg", "XANAX", "adderall XR", "zolpidem"} {
		if !IsControlled(s) {
			t.Errorf("%s not caught", s)
		}
	}
	if IsControlled("lisinopril") {
		t.Error("lisinopril is not controlled")
	}
	tl, _ := Get("rx_submit")
	if g, _ := tl.EffectiveGate(map[string]any{"drug": "Vicodin", "controlled_schedule": "none"}); g != G3 {
		t.Errorf("gate %s for vicodin", g)
	}
	if g, r := tl.EffectiveGate(map[string]any{"drug": "amoxicillin"}); g != G2 || r != "physician" {
		t.Errorf("gate %s/%s for amoxicillin", g, r)
	}
}

func TestEveryExternalToolIsGatedOrIdempotent(t *testing.T) {
	for _, n := range Names() {
		tl, _ := Get(n)
		if tl.Effect == External && tl.IdemKey == nil {
			t.Errorf("%s has external effects and no idempotency key", n)
		}
		if tl.Description == "" {
			t.Errorf("%s has no description", n)
		}
	}
}

func TestChunk(t *testing.T) {
	s := "aaaa\n\nbbbb\n\ncccc"
	if got := Chunk(s, 10); len(got) != 2 {
		t.Fatalf("got %d chunks: %q", len(got), got)
	}
}
