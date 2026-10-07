package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

func add(n int) games.Move { return games.Move{Type: "add", Args: map[string]any{"n": float64(n)}} }

// Two moves at the same version: exactly one is applied, the other is told
// the table moved on and gets the current view.
func TestConcurrentMovesExactlyOneWins(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour } // keep the agent out of it
	a := r.user(t, "Ann", nil)
	v := r.start(t, a, r.create(t, a, "me", "agent:ren").ID)
	if v.MySeat != 0 || !contains(v.ToMove, 0) || len(v.Legal) != 3 {
		t.Fatalf("unexpected start view: seat %d to_move %v legal %d", v.MySeat, v.ToMove, len(v.Legal))
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "m" + string(rune('a'+i)), Version: v.Version, Move: add(1 + i%3)})
		}(i)
	}
	wg.Wait()
	ok, stale := 0, 0
	for _, err := range errs {
		var se *StaleError
		switch {
		case err == nil:
			ok++
		case errors.As(err, &se):
			stale++
			if se.Table == nil || se.Table.Version != v.Version+1 {
				t.Fatalf("stale error should carry the current view, got %+v", se.Table)
			}
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || stale != len(errs)-1 {
		t.Fatalf("ok=%d stale=%d, want exactly one winner", ok, stale)
	}
	if n := r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1`, v.ID); n != 1 {
		t.Fatalf("%d moves stored, want 1", n)
	}
	if n := r.count(t, `SELECT version FROM tables WHERE id=$1`, v.ID); int64(n) != v.Version+1 {
		t.Fatalf("version %d, want %d", n, v.Version+1)
	}
}

// Retrying a client_move_id (sequentially, and concurrently) applies it once.
func TestMoveRetryIsIdempotent(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a := r.user(t, "Ann", nil)
	v := r.start(t, a, r.create(t, a, "me", "agent:lin").ID)
	ctx := context.Background()
	req := MoveRequest{ClientMoveID: "retry-1", Version: v.Version, Move: add(2)}
	var wg sync.WaitGroup
	views := make([]*TableView, 4)
	errs := make([]error, 4)
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); views[i], errs[i] = r.svc.Move(ctx, a, v.ID, req) }(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		if views[i].Version != v.Version+1 {
			t.Fatalf("retry %d saw version %d, want %d", i, views[i].Version, v.Version+1)
		}
	}
	// A late retry, long after, still answers with the current view.
	again, err := r.svc.Move(ctx, a, v.ID, req)
	if err != nil || again.Version != v.Version+1 {
		t.Fatalf("late retry: %v %+v", err, again)
	}
	if n := r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1`, v.ID); n != 1 {
		t.Fatalf("%d moves, want 1", n)
	}
	var total int
	_ = r.pool.QueryRow(ctx, `SELECT (state->>'total')::int FROM tables WHERE id=$1`, v.ID).Scan(&total)
	if total != 2 {
		t.Fatalf("total %d, want 2 (applied once)", total)
	}
}

func TestIllegalAndOutOfTurnMoves(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a, b := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	v := r.create(t, a, "me", "open")
	if _, err := r.svc.Join(context.Background(), b, strings.ToLower(v.Code)); err != nil {
		t.Fatal(err)
	}
	v = r.start(t, a, v.ID)
	ctx := context.Background()
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "x1", Version: v.Version, Move: add(4)}); !errors.Is(err, games.ErrIllegal) {
		t.Fatalf("add 4: %v, want ErrIllegal", err)
	}
	if _, err := r.svc.Move(ctx, b, v.ID, MoveRequest{ClientMoveID: "x2", Version: v.Version, Move: add(1)}); !errors.Is(err, games.ErrIllegal) {
		t.Fatalf("out of turn: %v, want ErrIllegal", err)
	}
	stranger := r.user(t, "Eve", nil)
	if _, err := r.svc.Move(ctx, stranger, v.ID, MoveRequest{ClientMoveID: "x3", Version: v.Version, Move: add(1)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger: %v, want ErrForbidden", err)
	}
	if _, err := r.svc.View(ctx, v.ID, stranger); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger view: %v", err)
	}
	if n := r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1`, v.ID); n != 0 {
		t.Fatalf("rejected moves were stored: %d", n)
	}
}

// A person's clock runs out: the turn_timeout job plays DefaultMove. A job
// for a version the table has left completes without effect.
func TestTimeoutPlaysDefaultAndStaleJobIsNoop(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a := r.user(t, "Ann", map[string]any{"turn_seconds": "15"})
	v := r.start(t, a, r.create(t, a, "me", "agent:bram").ID)
	if v.Deadline == nil || v.TurnSeconds != 15 {
		t.Fatalf("expected a 15s clock, got deadline %v turn %d", v.Deadline, v.TurnSeconds)
	}
	ctx := context.Background()
	if j := r.claim(t, "w"); j != nil {
		t.Fatalf("the timeout must not be due before the deadline, claimed %+v", j)
	}
	r.dueNow(t)
	j := r.claim(t, "w")
	if j == nil || j.Kind != "turn_timeout" || j.Seat != 0 || j.StateVersion != v.Version {
		t.Fatalf("claimed %+v", j)
	}
	r.svc.run(ctx, j)
	var actor string
	var move []byte
	if err := r.pool.QueryRow(ctx, `SELECT actor, move FROM table_moves WHERE table_id=$1`, v.ID).Scan(&actor, &move); err != nil {
		t.Fatal(err)
	}
	var played games.Move
	_ = json.Unmarshal(move, &played)
	if actor != "timeout" || played.Type != "add" || played.Args["n"] != 1.0 {
		t.Fatalf("timeout move %s %s, want DefaultMove (add 1)", actor, move)
	}
	after, _ := r.svc.View(ctx, v.ID, a)
	if after.Version != v.Version+1 || after.Deadline != nil {
		t.Fatalf("after timeout: version %d deadline %v (agent's turn: no clock)", after.Version, after.Deadline)
	}

	// A stale job: an agent move for the version before the timeout.
	if _, err := r.pool.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version) VALUES ($1,'agent_move',1,$2)`, v.ID, v.Version); err != nil {
		t.Fatal(err)
	}
	stale := r.claim(t, "w")
	if stale == nil || stale.StateVersion != v.Version {
		t.Fatalf("claimed %+v, want the stale job", stale)
	}
	r.svc.run(ctx, stale)
	var status, result string
	_ = r.pool.QueryRow(ctx, `SELECT status, result FROM table_jobs WHERE id=$1`, stale.ID).Scan(&status, &result)
	if status != "done" || result != "stale" {
		t.Fatalf("stale job ended %s/%s", status, result)
	}
	if v2, _ := r.svc.View(ctx, v.ID, a); v2.Version != after.Version {
		t.Fatalf("a stale job changed the table: %d → %d", after.Version, v2.Version)
	}
}

// An all-agent table, watched by its host, plays to the end on the workers,
// using the injected Brain; plays is counted once; rematch starts a new game.
func TestAllAgentTablePlaysToCompletion(t *testing.T) {
	r := newRig(t)
	brain := &randBrain{}
	r.svc.BrainFor = func(games.Game) games.Brain { return brain }
	r.svc.Chatter = &fakeChatter{line: "Nice."}
	host := r.user(t, "Host", map[string]any{"table_talk": "off"})
	v := r.create(t, host, "agent:aoi", "agent", "agent")
	if v.MySeat != -1 || !v.IsHost {
		t.Fatalf("host should watch: %+v", v)
	}
	if v.Seats[1].AgentID == "aoi" || v.Seats[1].AgentID == v.Seats[2].AgentID {
		t.Fatalf("default agents should be distinct from each other and from picks: %+v", v.Seats)
	}
	r.start(t, host, v.ID)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.svc.RunWorkers(ctx, 3); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	var final *TableView
	waitFor(t, 30*time.Second, "the game to finish", func() bool {
		final, _ = r.svc.View(context.Background(), v.ID, host)
		return final != nil && final.Status == "finished"
	})
	if final.Outcome == nil || !strings.HasSuffix(final.Outcome.Summary, " wins") || strings.Contains(final.Outcome.Summary, "{s:") {
		t.Fatalf("outcome %+v", final.Outcome)
	}
	var total, moves int
	_ = r.pool.QueryRow(context.Background(), `SELECT (state->>'total')::int, move_seq FROM tables WHERE id=$1`, v.ID).Scan(&total, &moves)
	if total != 21 || r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1 AND actor='agent'`, v.ID) != moves {
		t.Fatalf("total %d, moves %d", total, moves)
	}
	if brain.calls.Load() < int64(moves) {
		t.Fatalf("brain called %d times for %d moves", brain.calls.Load(), moves)
	}
	if n := r.count(t, `SELECT plays FROM games WHERE id='race21'`); n != 1 {
		t.Fatalf("plays %d, want 1", n)
	}
	// Job keys are unique per (table, kind, seat, version): one per move.
	if n := r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND kind='agent_move' AND result='applied'`, v.ID); n != moves {
		t.Fatalf("%d applied jobs for %d moves", n, moves)
	}

	re, err := r.svc.Rematch(context.Background(), host, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if re.ID == v.ID || re.Status != "playing" || re.Code == v.Code {
		t.Fatalf("rematch %+v", re)
	}
	again, err := r.svc.Rematch(context.Background(), host, v.ID)
	if err != nil || again.ID != re.ID {
		t.Fatalf("second rematch should return the first: %v", err)
	}
	if old, _ := r.svc.View(context.Background(), v.ID, host); old.RematchID != re.ID {
		t.Fatalf("old table should point at the rematch")
	}
}

// Leaving mid-game: an agent (the host's favourite) takes the seat and the
// person stays as a spectator; if it was their turn, the agent plays it.
func TestLeaveMidGameAgentTakesOver(t *testing.T) {
	r := newRig(t)
	a := r.user(t, "Ann", map[string]any{"favorite_agents": []string{"lin"}, "fill_empty_seats": false})
	b := r.user(t, "Bob", nil)
	v := r.create(t, a, "me", "open", "agent:lin", "open")
	ctx := context.Background()
	jv, err := r.svc.Join(ctx, b, v.Code)
	if err != nil || jv.MySeat != 1 {
		t.Fatalf("join: %v seat %d", err, jv.MySeat)
	}
	v = r.start(t, a, v.ID)
	if len(v.Seats) != 3 {
		t.Fatalf("an unfilled open seat should be removed at start: %+v", v.Seats)
	}
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	if err := r.svc.Leave(ctx, a, v.ID); err != nil {
		t.Fatal(err)
	}
	av, err := r.svc.View(ctx, v.ID, a)
	if err != nil {
		t.Fatalf("the leaver should still watch: %v", err)
	}
	s0 := av.Seats[0]
	if av.MySeat != -1 || s0.Kind != "agent" || s0.AgentID != "ren" && s0.AgentID == "lin" {
		t.Fatalf("seat 0 after leave: %+v (my seat %d)", s0, av.MySeat)
	}
	if s0.AgentID == "lin" {
		t.Fatalf("lin is already seated; the takeover should pick another agent")
	}
	found := false
	for _, e := range av.Log {
		found = found || (e.Type == "takeover" && strings.Contains(e.Text, "Ann left") && strings.Contains(e.Text, s0.Name))
	}
	if !found {
		t.Fatalf("no takeover event in %+v", av.Log)
	}
	// Seat 0 was to move: the agent now owes the move at the new version.
	if n := r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND kind='agent_move' AND seat=0 AND state_version=$2`, v.ID, av.Version); n != 1 {
		t.Fatalf("no agent_move job for the takeover")
	}
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "late", Version: av.Version, Move: add(1)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a spectator must not move: %v", err)
	}
	// Run whatever is due: Ann's superseded clock (stale), the agent's move,
	// any greetings. Bounded: each run finishes its job.
	r.dueNow(t)
	for i := 0; i < 10; i++ {
		j := r.claim(t, "w")
		if j == nil {
			break
		}
		r.svc.run(ctx, j)
	}
	if bv, _ := r.svc.View(ctx, v.ID, b); !contains(bv.ToMove, 1) || len(bv.Legal) == 0 {
		t.Fatalf("after the agent's move it should be Bob's turn: %+v", bv.ToMove)
	}
}

// Expired leases are reclaimed; the job re-runs exactly once and the stale
// worker cannot write anything (epoch fencing).
func TestExpiredLeaseReclaimedAndFenced(t *testing.T) {
	r := newRig(t)
	r.svc.Lease = time.Second
	a := r.user(t, "Ann", nil)
	v := r.start(t, a, r.create(t, a, "agent:mika", "me").ID)
	ctx := context.Background()
	staleJob := r.claim(t, "old")
	if staleJob == nil || staleJob.Kind != "agent_move" {
		t.Fatalf("claimed %+v", staleJob)
	}
	if again := r.claim(t, "other"); again != nil {
		t.Fatalf("a leased job was claimed twice: %+v", again)
	}
	time.Sleep(1100 * time.Millisecond)
	reclaimed, _, err := r.svc.Sweep(ctx)
	if err != nil || reclaimed != 1 {
		t.Fatalf("sweep reclaimed %d (%v), want 1", reclaimed, err)
	}
	fresh := r.claim(t, "new")
	if fresh == nil || fresh.ID != staleJob.ID || fresh.Epoch <= staleJob.Epoch {
		t.Fatalf("expected the same job re-claimed with a higher epoch, got %+v", fresh)
	}
	if err := r.svc.execute(ctx, staleJob); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker: %v, want ErrLeaseLost", err)
	}
	if n := r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1`, v.ID); n != 0 {
		t.Fatalf("the stale worker wrote a move")
	}
	if err := r.svc.execute(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.execute(ctx, staleJob); !errors.Is(err, ErrLeaseLost) && err != nil {
		t.Fatalf("stale worker after completion: %v", err)
	}
	if n := r.count(t, `SELECT count(*) FROM table_moves WHERE table_id=$1`, v.ID); n != 1 {
		t.Fatalf("%d moves, want exactly 1", n)
	}
	if n := r.count(t, `SELECT version FROM tables WHERE id=$1`, v.ID); int64(n) != v.Version+1 {
		t.Fatalf("version %d, want %d", n, v.Version+1)
	}
}

// One LISTEN per process: a commit anywhere reaches this process's
// subscribers as "table" {version}, and chat lines arrive as "chat".
func TestListenFansOutNotifications(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a := r.user(t, "Ann", map[string]any{"table_talk": "off"})
	v := r.start(t, a, r.create(t, a, "me", "agent:nova").ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.svc.Listen(ctx)
	sub, done := r.svc.Subscribe(v.ID)
	defer done()

	// Wait (bounded) until the LISTEN is live, by nudging until a signal lands.
	waitFor(t, 5*time.Second, "the listener", func() bool {
		_, _ = r.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, v.ID)
		select {
		case <-sub:
			return true
		case <-time.After(100 * time.Millisecond):
			return false
		}
	})
	for len(sub) > 0 {
		<-sub
	}
	// A second service instance stands in for another pod.
	other := New(r.pool)
	other.Think = r.svc.Think
	if _, err := other.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "n1", Version: v.Version, Move: add(3)}); err != nil {
		t.Fatal(err)
	}
	if err := other.Chat(ctx, a, v.ID, "hello table", "c1"); err != nil {
		t.Fatal(err)
	}
	gotTable, gotChat := false, false
	timeout := time.After(5 * time.Second)
	for !gotTable || !gotChat {
		select {
		case sig := <-sub:
			switch sig.Kind {
			case "table":
				gotTable = gotTable || sig.Version == v.Version+1
			case "chat":
				gotChat = sig.Chat != nil && sig.Chat.Text == "hello table" && sig.Chat.Name == "Ann" && !sig.Chat.Agent
			}
		case <-timeout:
			t.Fatalf("table=%v chat=%v after 5s", gotTable, gotChat)
		}
	}
}

// TableView never shows another seat's legal moves, hidden view data or
// private events; spectators see public information only; {s:N} is never
// sent raw.
func TestViewHidesOtherSeats(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	a, b, c := r.user(t, "Ann", nil), r.user(t, "Bob", nil), r.user(t, "Cy", nil)
	v := r.create(t, a, "me", "open")
	ctx := context.Background()
	if _, err := r.svc.Join(ctx, b, v.Code); err != nil {
		t.Fatal(err)
	}
	v = r.start(t, a, v.ID)
	cv, err := r.svc.Join(ctx, c, v.Code)
	if err != nil || cv.MySeat != -1 {
		t.Fatalf("a full, started table should take Cy as a spectator: %v %d", err, cv.MySeat)
	}
	av, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "p1", Version: v.Version, Move: add(2)})
	if err != nil {
		t.Fatal(err)
	}
	bv, _ := r.svc.View(ctx, v.ID, b)
	cv, _ = r.svc.View(ctx, v.ID, c)
	av, _ = r.svc.View(ctx, v.ID, a)

	if len(av.Legal) != 0 || len(cv.Legal) != 0 || len(bv.Legal) == 0 {
		t.Fatalf("legal moves: ann %d (not her turn), cy %d (spectator), bob %d (his turn)", len(av.Legal), len(cv.Legal), len(bv.Legal))
	}
	for _, m := range bv.Legal {
		if !strings.HasPrefix(m.Label, "Bob +") {
			t.Fatalf("label not substituted: %q", m.Label)
		}
	}
	var secrets [2]int
	var raw []byte
	_ = r.pool.QueryRow(ctx, `SELECT state FROM tables WHERE id=$1`, v.ID).Scan(&raw)
	var st raceState
	_ = json.Unmarshal(raw, &st)
	copy(secrets[:], st.Secrets)

	check := func(who string, tv *TableView, own int) {
		t.Helper()
		js, _ := json.Marshal(tv)
		s := string(js)
		for seat, sec := range secrets {
			has := strings.Contains(s, itoa(sec))
			if seat == own && !has {
				t.Fatalf("%s should see their own secret", who)
			}
			if seat != own && has {
				t.Fatalf("%s sees seat %d's secret: %s", who, seat, s)
			}
		}
		if strings.Contains(s, "{s:") {
			t.Fatalf("%s got an unsubstituted seat reference: %s", who, s)
		}
		for _, e := range tv.Log {
			if e.Type == "peek" && own != 0 {
				t.Fatalf("%s sees Ann's private event", who)
			}
		}
	}
	check("ann", av, 0)
	check("bob", bv, 1)
	check("cy", cv, -1)
	if av.View.Status != "Bob to move" {
		t.Fatalf("status %q", av.View.Status)
	}
	sawAdd := false
	for _, e := range cv.Log {
		sawAdd = sawAdd || e.Text == "Ann adds 2 (total 2)"
	}
	if !sawAdd {
		t.Fatalf("public event missing or unsubstituted: %+v", cv.Log)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// People's chat is rate limited per table; agents answer @mentions through
// the Chatter, which sees only public information, under their own limit.
func TestChatLimitsAndAgentReplies(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	fc := &fakeChatter{line: "{s:0}, you will need more than that. I hold As Kd!"}
	r.svc.Chatter = fc
	a := r.user(t, "Ann", nil)
	v := r.start(t, a, r.create(t, a, "me", "agent:aoi").ID)
	ctx := context.Background()
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "q", Version: v.Version, Move: add(1)}); err != nil {
		t.Fatal(err)
	}
	_, _ = r.pool.Exec(ctx, `DELETE FROM table_jobs`) // only chat jobs from here on

	if err := r.svc.Chat(ctx, a, v.ID, "hey @Aoi, ready to lose?", "c0"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Chat(ctx, a, v.ID, "hey @Aoi, ready to lose?", "c0"); err != nil {
		t.Fatalf("a retried send should be a no-op: %v", err)
	}
	for i := 1; i < humanPerWindow; i++ {
		if err := r.svc.Chat(ctx, a, v.ID, "spam", "s"+itoa(i)); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
	}
	if err := r.svc.Chat(ctx, a, v.ID, "one too many", "over"); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("burst limit: %v", err)
	}
	if n := r.count(t, `SELECT count(*) FROM table_chat WHERE table_id=$1 AND user_id=$2`, v.ID, a); n != humanPerWindow {
		t.Fatalf("%d lines stored, want %d", n, humanPerWindow)
	}

	// Only the reply to the mention: the spam may have made Aoi chime in.
	_, _ = r.pool.Exec(ctx, `DELETE FROM table_jobs WHERE NOT coalesce((payload->>'mention')::bool, false)`)
	r.dueNow(t)
	j := r.claim(t, "w")
	if j == nil || j.Kind != "agent_chat" || j.Seat != 1 || j.StateVersion >= 0 {
		t.Fatalf("mention should enqueue a reply from Aoi keyed by the chat id, got %+v", j)
	}
	r.svc.run(ctx, j)
	if len(fc.reqs) != 1 {
		t.Fatalf("chatter called %d times", len(fc.reqs))
	}
	req := fc.reqs[0]
	var raw []byte
	_ = r.pool.QueryRow(ctx, `SELECT state FROM tables WHERE id=$1`, v.ID).Scan(&raw)
	var st raceState
	_ = json.Unmarshal(raw, &st)
	for _, sec := range st.Secrets {
		if strings.Contains(req.Table, itoa(sec)) {
			t.Fatalf("the chatter saw a hidden secret: %s", req.Table)
		}
	}
	if req.Trigger != "reply" || !strings.Contains(req.About, "ready to lose") || strings.Contains(req.Table, "{s:") || req.HostID != a {
		t.Fatalf("chat request %+v", req)
	}
	var said string
	_ = r.pool.QueryRow(ctx, `SELECT text FROM table_chat WHERE table_id=$1 AND agent_id='aoi'`, v.ID).Scan(&said)
	if said == "" || strings.Contains(said, "As Kd") || strings.Contains(said, "{s:") {
		t.Fatalf("agent line %q: a card reveal must be replaced", said)
	}

	// The agent limit: a second line within agentGap is dropped.
	fc.line = "Hehe, {s:0}!"
	if _, err := r.pool.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version, payload) VALUES ($1,'agent_chat',1,-999,'{"trigger":"banter"}')`, v.ID); err != nil {
		t.Fatal(err)
	}
	j2 := r.claim(t, "w")
	r.svc.run(ctx, j2)
	var result string
	_ = r.pool.QueryRow(ctx, `SELECT result FROM table_jobs WHERE id=$1`, j2.ID).Scan(&result)
	if result != "rate limited" || len(fc.reqs) != 1 {
		t.Fatalf("second agent line: %q (chatter calls %d)", result, len(fc.reqs))
	}
	// Once the gap has passed, the agent may talk again, with names substituted.
	_, _ = r.pool.Exec(ctx, `UPDATE table_chat SET at = at - interval '5 minutes' WHERE table_id=$1`, v.ID)
	_, _ = r.pool.Exec(ctx, `INSERT INTO table_jobs (table_id, kind, seat, state_version, payload) VALUES ($1,'agent_chat',1,-1000,'{"trigger":"banter"}')`, v.ID)
	r.svc.run(ctx, r.claim(t, "w"))
	var last string
	_ = r.pool.QueryRow(ctx, `SELECT text FROM table_chat WHERE table_id=$1 AND agent_id='aoi' ORDER BY id DESC LIMIT 1`, v.ID).Scan(&last)
	if last != "Hehe, Ann!" {
		t.Fatalf("agent line %q", last)
	}
	tv, _ := r.svc.View(ctx, v.ID, a)
	if n := len(tv.Chat); n != humanPerWindow+2 || !tv.Chat[n-1].Agent || tv.Chat[n-1].Avatar == "" {
		t.Fatalf("view chat: %+v", tv.Chat)
	}
}

func TestLobbySeatsAndValidation(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a, b := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	if _, err := r.svc.Create(ctx, a, CreateRequest{GameID: "race21", Seats: []SeatSpec{{Kind: "me"}}}); err == nil {
		t.Fatal("one seat is below the game's minimum")
	}
	if _, err := r.svc.Create(ctx, a, CreateRequest{GameID: "race21", Seats: []SeatSpec{{Kind: "me"}, {Kind: "me"}}}); err == nil {
		t.Fatal("two 'me' seats accepted")
	}
	if _, err := r.svc.Create(ctx, a, CreateRequest{GameID: "nope", Seats: []SeatSpec{{Kind: "me"}, {Kind: "open"}}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown game: %v", err)
	}
	v := r.create(t, a, "me", "open", "open")
	for _, ch := range v.Code {
		if strings.ContainsRune("IL O01", ch) {
			t.Fatalf("ambiguous character in code %q", v.Code)
		}
	}
	if _, err := r.svc.SetSeat(ctx, b, v.ID, 1, SeatSpec{Kind: "agent", AgentID: "ren"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-host set seat: %v", err)
	}
	sv, err := r.svc.SetSeat(ctx, a, v.ID, 2, SeatSpec{Kind: "agent", AgentID: "ren"})
	if err != nil || sv.Seats[2].AgentID != "ren" || sv.Seats[2].Avatar != "/play/agents/ren.webp" {
		t.Fatalf("set seat: %v %+v", err, sv.Seats)
	}
	if _, err := r.svc.SetSeat(ctx, a, v.ID, 0, SeatSpec{Kind: "open"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a person's seat must not be taken: %v", err)
	}
	// Fill empty seats defaults to on: the open seat gets an agent at start.
	st := r.start(t, a, v.ID)
	if len(st.Seats) != 3 || st.Seats[1].Kind != "agent" || st.Seats[1].AgentID == "ren" {
		t.Fatalf("seats after start: %+v", st.Seats)
	}
	if _, err := r.svc.Join(ctx, b, "ZZZZZZ"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad code: %v", err)
	}
	list, err := r.svc.List(ctx, a, "open")
	if err != nil || len(list) != 1 || list[0].SeatsTaken != 3 || list[0].GameName != "Race to 21" {
		t.Fatalf("list: %v %+v", err, list)
	}
	if l, _ := r.svc.List(ctx, b, "mine"); len(l) != 0 {
		t.Fatalf("Bob is not at any table: %+v", l)
	}
}

func TestSweepAbandonsIdleTables(t *testing.T) {
	r := newRig(t)
	a := r.user(t, "Ann", nil)
	v := r.start(t, a, r.create(t, a, "me", "agent:aoi").ID)
	ctx := context.Background()
	_, _ = r.pool.Exec(ctx, `UPDATE tables SET last_human_at = now() - interval '3 hours' WHERE id=$1`, v.ID)
	_, abandoned, err := r.svc.Sweep(ctx)
	if err != nil || abandoned != 1 {
		t.Fatalf("abandoned %d (%v)", abandoned, err)
	}
	av, _ := r.svc.View(ctx, v.ID, a)
	if av.Status != "abandoned" || av.Version != v.Version+1 {
		t.Fatalf("after sweep: %s v%d", av.Status, av.Version)
	}
	if n := r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND status='ready'`, v.ID); n != 0 {
		t.Fatalf("%d jobs still pending on an abandoned table", n)
	}
}

// The Loader serves non-built-in games and is cached per (id, version).
func TestLoaderAndCatalog(t *testing.T) {
	r := newRig(t)
	calls := 0
	r.svc.Loader = func(ctx context.Context, id string, version int) (games.Game, error) {
		calls++
		return race21{}, nil
	}
	ctx := context.Background()
	a, b := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	meta, _ := json.Marshal(race21{}.Meta())
	_, err := r.pool.Exec(ctx, `INSERT INTO games (id, owner_id, kind, name, status, visibility, current_version) VALUES
		('my-draft', $1, 'script', 'Draft', 'draft', 'private', 1), ('pub', $1, 'script', 'Pub', 'published', 'public', 1),
		('unl', $1, 'script', 'Unl', 'published', 'unlisted', 1)`, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"my-draft", "pub", "unl"} {
		_, _ = r.pool.Exec(ctx, `INSERT INTO game_versions (game_id, version, source, meta) VALUES ($1, 1, 'src', $2)`, id, meta)
	}
	if _, err := r.svc.Create(ctx, b, CreateRequest{GameID: "my-draft", Seats: []SeatSpec{{Kind: "me"}, {Kind: "agent"}}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else's draft: %v", err)
	}
	v, err := r.svc.Create(ctx, a, CreateRequest{GameID: "my-draft", Seats: []SeatSpec{{Kind: "me"}, {Kind: "agent"}}})
	if err != nil || v.Game.Kind != "script" {
		t.Fatalf("own draft: %v", err)
	}
	r.start(t, a, v.ID)
	_, _ = r.svc.View(ctx, v.ID, a)
	if calls != 1 {
		t.Fatalf("loader called %d times, want 1 (cached)", calls)
	}
	gl, err := r.svc.Games(ctx, b)
	if err != nil || len(gl.Mine) != 0 || len(gl.Community) != 1 || gl.Community[0].ID != "pub" || gl.Community[0].MaxSeats != 4 || gl.Community[0].OwnerName != "Ann" {
		t.Fatalf("bob's games: %v %+v", err, gl)
	}
	if len(gl.Builtin) == 0 {
		t.Fatal("built-ins missing")
	}
	if _, err := r.svc.GameDetail(ctx, b, "unl"); err != nil {
		t.Fatalf("unlisted by id: %v", err)
	}
	if _, err := r.svc.GameDetail(ctx, b, "my-draft"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private draft by id: %v", err)
	}
	vis := "public"
	if _, err := r.svc.PatchGame(ctx, b, "my-draft", GamePatch{Visibility: &vis}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner patch: %v", err)
	}
	if d, err := r.svc.PatchGame(ctx, a, "my-draft", GamePatch{Visibility: &vis}); err != nil || d.Visibility != "public" {
		t.Fatalf("owner patch: %v", err)
	}
	if err := r.svc.DeleteGame(ctx, a, "pub"); !errors.Is(err, ErrConflict) {
		t.Fatalf("published games are not deletable: %v", err)
	}
	if err := r.svc.DeleteGame(ctx, a, "my-draft"); err != nil {
		t.Fatal(err)
	}
}
