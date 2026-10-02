package rooms

import (
	"context"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/agents"
	"github.com/damonleelcx/play-with-agents/internal/games"
)

// step makes every ready job due, claims the next one, checks its kind and
// seat, and runs it.
func (r *rig) step(t *testing.T, kind string, seat int) {
	t.Helper()
	r.dueNow(t)
	j := r.claim(t, "w")
	// Jobs for a version the table has left complete as no-ops; skip them.
	for j != nil && j.StateVersion > 0 && j.StateVersion != int64(r.count(t, `SELECT version FROM tables WHERE id=$1`, j.TableID)) {
		r.svc.run(context.Background(), j)
		j = r.claim(t, "w")
	}
	if j == nil || j.Kind != kind || j.Seat != seat {
		t.Fatalf("claimed %+v, want %s for seat %d", j, kind, seat)
	}
	r.svc.run(context.Background(), j)
}

func (r *rig) view(t *testing.T, id, user string) *TableView {
	t.Helper()
	v, err := r.svc.View(context.Background(), id, user)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// timeoutDueIn is how far in the future the seat's pending clock job is.
func (r *rig) timeoutDueIn(t *testing.T, tableID string, seat int) time.Duration {
	t.Helper()
	var secs float64
	if err := r.pool.QueryRow(context.Background(), `SELECT extract(epoch FROM run_after - now())::float8 FROM table_jobs
		WHERE table_id=$1 AND kind='turn_timeout' AND seat=$2 AND status='ready'`, tableID, seat).Scan(&secs); err != nil {
		t.Fatalf("no pending turn_timeout for seat %d: %v", seat, err)
	}
	return time.Duration(secs * float64(time.Second))
}

// Two clock run-outs in a row mark a person away; their next turn then
// resolves after the short grace instead of the full clock, and the seat
// shows as away. Their own move clears it and restores the full clock.
func TestConsecutiveTimeoutsMarkAwayAndMoveClearsIt(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a := r.user(t, "Ann", map[string]any{"turn_seconds": "30", "table_talk": "off"})
	v := r.start(t, a, r.create(t, a, "me", "agent:bram").ID)
	ctx := context.Background()

	r.step(t, "turn_timeout", 0) // 1st run-out
	if s := r.view(t, v.ID, a).Seats[0]; s.Away {
		t.Fatal("one timeout must not mark a person away")
	}
	r.step(t, "agent_move", 1)
	if d := r.timeoutDueIn(t, v.ID, 0); d < 25*time.Second {
		t.Fatalf("after one timeout the full clock still applies, due in %s", d)
	}
	r.step(t, "turn_timeout", 0) // 2nd run-out: away
	r.step(t, "agent_move", 1)
	av := r.view(t, v.ID, a)
	if !av.Seats[0].Away || av.Seats[1].Away {
		t.Fatalf("seats after two timeouts: %+v", av.Seats)
	}
	if d := r.timeoutDueIn(t, v.ID, 0); d > 2*time.Second {
		t.Fatalf("an away seat's turn should resolve after the grace, due in %s", d)
	}
	if av.Deadline == nil || time.Until(*av.Deadline) > 2*time.Second {
		t.Fatalf("the shown deadline should be the grace: %v", av.Deadline)
	}

	// The away turn resolves with the default move; agents keep their pace.
	r.step(t, "turn_timeout", 0)
	if n := r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1 AND actor='timeout'`, v.ID); n != 3 {
		t.Fatalf("%d timeout moves, want 3", n)
	}
	r.step(t, "agent_move", 1)

	// A move by the person clears away and the next turn has the full clock.
	cur := r.view(t, v.ID, a)
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "back-1", Version: cur.Version, Move: add(1)}); err != nil {
		t.Fatal(err)
	}
	if s := r.view(t, v.ID, a).Seats[0]; s.Away {
		t.Fatal("a move should clear away")
	}
	if n := r.count(t, `SELECT timeouts FROM table_seats WHERE table_id=$1 AND seat=0`, v.ID); n != 0 {
		t.Fatalf("timeouts %d after a move", n)
	}
	r.step(t, "agent_move", 1)
	if d := r.timeoutDueIn(t, v.ID, 0); d < 25*time.Second {
		t.Fatalf("after coming back the full clock applies, due in %s", d)
	}
}

// A table where every person has been away long enough is paused: still
// "playing", nothing scheduled, no table talk. "I'm back" resumes it.
func TestAllAwayPausesAndBackResumes(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	fc := &fakeChatter{line: "Arr!"}
	r.svc.Chatter = fc
	a := r.user(t, "Ann", map[string]any{"turn_seconds": "30"})
	v := r.start(t, a, r.create(t, a, "me", "agent:bram").ID)
	ctx := context.Background()

	if _, _, err := r.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if r.view(t, v.ID, a).Paused {
		t.Fatal("a fresh table must not pause")
	}
	_, _ = r.pool.Exec(ctx, `UPDATE table_seats SET timeouts=5, away_since=now() - interval '20 minutes' WHERE table_id=$1 AND seat=0`, v.ID)
	if _, _, err := r.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	pv := r.view(t, v.ID, a)
	if !pv.Paused || pv.Status != "playing" || pv.Deadline != nil || !pv.Seats[0].Away || pv.Version != v.Version+1 {
		t.Fatalf("after sweep: paused %v status %s deadline %v seats %+v v%d", pv.Paused, pv.Status, pv.Deadline, pv.Seats, pv.Version)
	}
	if n := r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND status='ready'`, v.ID); n != 0 {
		t.Fatalf("%d jobs still pending on a paused table", n)
	}
	// Table talk is not written while paused.
	_, _ = r.pool.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version, payload) VALUES ($1,'agent_chat',1,-5,'{"trigger":"banter"}')`, v.ID)
	j := r.claim(t, "w")
	r.svc.run(ctx, j)
	var result string
	_ = r.pool.QueryRow(ctx, `SELECT result FROM table_jobs WHERE id=$1`, j.ID).Scan(&result)
	if result != "nobody listening" || len(fc.reqs) != 0 {
		t.Fatalf("talk on a paused table: %q, chatter calls %d", result, len(fc.reqs))
	}

	bv, err := r.svc.Back(ctx, a, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bv.Paused || bv.Seats[0].Away || bv.Deadline == nil {
		t.Fatalf("after back: paused %v seats %+v deadline %v", bv.Paused, bv.Seats, bv.Deadline)
	}
	if d := r.timeoutDueIn(t, v.ID, 0); d < 25*time.Second {
		t.Fatalf("after back the full clock applies, due in %s", d)
	}
	// Back again is a harmless no-op.
	if again, err := r.svc.Back(ctx, a, v.ID); err != nil || again.Version != bv.Version {
		t.Fatalf("second back: %v v%d→v%d", err, bv.Version, again.Version)
	}
	// Strangers cannot resume someone's table.
	if _, err := r.svc.Back(ctx, r.user(t, "Eve", nil), v.ID); err == nil {
		t.Fatal("a stranger resumed the table")
	}
}

// No human activity at all also pauses (an all-agent table nobody touches),
// and a person's move on a paused table resumes it.
func TestIdleTablePausesAndMoveResumes(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a := r.user(t, "Ann", map[string]any{"turn_seconds": "30", "table_talk": "off"})
	v := r.start(t, a, r.create(t, a, "me", "agent:bram").ID)
	ctx := context.Background()
	_, _ = r.pool.Exec(ctx, `UPDATE tables SET last_human_at = now() - interval '20 minutes' WHERE id=$1`, v.ID)
	if _, abandoned, err := r.svc.Sweep(ctx); err != nil || abandoned != 0 {
		t.Fatalf("sweep: %v abandoned %d", err, abandoned)
	}
	pv := r.view(t, v.ID, a)
	if !pv.Paused || len(pv.Legal) == 0 {
		t.Fatalf("idle table: paused %v legal %v", pv.Paused, pv.Legal)
	}
	// A job queued before the pause is stale now.
	if _, err := r.pool.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version) VALUES ($1,'turn_timeout',0,$2)`, v.ID, pv.Version); err != nil {
		t.Fatal(err)
	}
	r.step(t, "turn_timeout", 0)
	if n := r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1`, v.ID); n != 0 {
		t.Fatalf("a job ran on a paused table: %d moves", n)
	}
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "m1", Version: pv.Version, Move: add(2)}); err != nil {
		t.Fatal(err)
	}
	mv := r.view(t, v.ID, a)
	if mv.Paused || r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND kind='agent_move' AND status='ready'`, v.ID) != 1 {
		t.Fatalf("after a move: paused %v", mv.Paused)
	}
}

// ── Table talk cadence (no database) ────────────────────────────────────────

func TestTooSimilar(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Into the vault of my considerable savings it goes.", "Another one for the vault of my considerable savings!", true},
		{"Noted.", "noted", true},
		{"Bold move, sailor.", "Well played.", false},
		{"Hehe, Ann!", "Hehe, I heard that~", false},
		{"The odds were 73% in my favour, beep.", "Odds 73% in my favour again, beep!", true},
		{"Nice river.", "Nice turn.", false},
		{"我的筹码金库又满了", "筹码金库又满了一点", true},
	}
	for _, c := range cases {
		if got := tooSimilar(c.a, c.b); got != c.want {
			t.Errorf("tooSimilar(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if !repeatsOwn("for the vault of my considerable savings", []string{"Hi.", "Straight into the vault of my considerable savings."}) {
		t.Error("a reused catchphrase must be caught")
	}
}

func TestTalkCadenceScalesWithPersona(t *testing.T) {
	ren, mika := agents.Persona("ren", "").Talk, agents.Persona("mika", "").Talk
	if talkiness(ren)*10 > talkiness(mika) {
		t.Fatalf("Ren (%v) should be far rarer than Mika (%v)", talkiness(ren), talkiness(mika))
	}
	if agentCooldown(mika) < 45*time.Second || agentCooldown(ren) <= agentCooldown(mika) {
		t.Fatalf("cooldowns: mika %s ren %s", agentCooldown(mika), agentCooldown(ren))
	}
	win := func(amt int) games.Event {
		return games.Event{Type: "win", Seat: 1, Data: map[string]any{"amount": amt}}
	}
	if got := classify(win(180), 20); got != triggerSmallPot {
		t.Fatalf("9bb pot: %q", got)
	}
	if got := classify(win(200), 20); got != agents.TriggerWin {
		t.Fatalf("10bb pot: %q", got)
	}
	if got := classify(win(300), 20); got != agents.TriggerBigPot {
		t.Fatalf("15bb pot: %q", got)
	}
	if got := classify(win(5), 0); got != agents.TriggerWin {
		t.Fatalf("no blinds: %q", got)
	}
}

// Small pots almost never prompt talk, and quiet agents rarely talk at all:
// a statistical check of talkAfter's rates over many simulated hands.
func TestTalkAfterRates(t *testing.T) {
	svc := &Service{}
	g, _ := games.Builtin("holdem")
	rate := func(agentID string, amt int) float64 {
		tr := &tableRow{Settings: Settings{TableTalk: "all"}, Options: map[string]any{"big_blind": 20.0},
			Seats: []seatRow{{Seat: 0, Kind: "human", Name: "Ann"}, {Seat: 1, Kind: "agent", AgentID: agentID, Name: agentID}}}
		evs := []games.Event{{Type: "win", Seat: 1, Text: "{s:1} wins", Data: map[string]any{"amount": amt}}}
		n := 0
		const trials = 20000
		for i := 0; i < trials; i++ {
			if len(svc.talkAfter(tr, evs, nil, noOutcome{g})) > 0 {
				n++
			}
		}
		return float64(n) / trials
	}
	smallMika, bigMika, bigRen := rate("mika", 100), rate("mika", 400), rate("ren", 400)
	if smallMika > 0.08 || bigMika < 0.3 || bigRen > 0.06 {
		t.Fatalf("rates: small pot (Mika) %.3f, decent pot Mika %.3f, Ren %.3f", smallMika, bigMika, bigRen)
	}
	// Nobody listening: no talk at all.
	tr := &tableRow{Settings: Settings{TableTalk: "all"}, Seats: []seatRow{{Seat: 0, Kind: "human", AwaySince: &time.Time{}},
		{Seat: 1, Kind: "agent", AgentID: "mika"}}}
	for i := 0; i < 200; i++ {
		if len(svc.talkAfter(tr, []games.Event{{Type: "bust", Seat: 1}}, nil, noOutcome{g})) > 0 {
			t.Fatal("talk enqueued while everyone is away")
		}
	}
}

// noOutcome wraps a game so Outcome(nil) is nil (talkAfter checks it).
type noOutcome struct{ games.Game }

func (noOutcome) Outcome(games.State) (*games.Outcome, error) { return nil, nil }
