package talk

import "testing"

func TestEchoes(t *testing.T) {
	about := "Mika bets 696 and is all-in"
	if !echoes("Mika bets 696 and is all-in", about) {
		t.Fatal("a restated action must count as an echo")
	}
	if echoes("Go big or go home, sailors! Who's brave enough to call?", about) {
		t.Fatal("a reaction is not an echo")
	}
	if echoes("anything", "") {
		t.Fatal("nothing to echo")
	}
}
