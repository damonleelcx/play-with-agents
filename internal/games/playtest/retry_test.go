package playtest

import "testing"

func TestOnlyTimeouts(t *testing.T) {
	if onlyTimeouts(nil) {
		t.Fatal("no errors is not a timeout")
	}
	if !onlyTimeouts([]Error{{Class: ClassTimeout}, {Class: ClassTimeout}}) {
		t.Fatal("all timeouts")
	}
	if onlyTimeouts([]Error{{Class: ClassTimeout}, {Class: ClassModule}}) {
		t.Fatal("a module error is not load")
	}
}
