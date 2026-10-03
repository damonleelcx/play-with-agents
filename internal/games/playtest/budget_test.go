package playtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games/script"
)

// A run whose time budget is already spent stops starting games, and the
// verdict says why instead of the tool timing out.
func TestBudgetStopsTheRun(t *testing.T) {
	ex, ok := script.ExampleByID("connect_four")
	if !ok {
		t.Skip("connect_four example missing")
	}
	g, err := script.Load(ex.ID, ex.Source, script.Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := Run(context.Background(), g, Options{Games: 200, Budget: time.Nanosecond, Parallelism: 1})
	if !r.BudgetStopped || r.Requested != 200 || r.Run >= 200 {
		t.Fatalf("stopped=%v requested=%d run=%d", r.BudgetStopped, r.Requested, r.Run)
	}
	pass, reasons := r.Verdict()
	if pass || !strings.Contains(strings.Join(reasons, " "), "time budget") {
		t.Fatalf("pass=%v reasons=%v", pass, reasons)
	}

	// Enough games inside the budget passes, with a warning.
	r = Run(context.Background(), g, Options{Games: 40, Budget: time.Hour})
	if r.BudgetStopped {
		t.Fatal("an ample budget must not stop the run")
	}
}
