package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// deleteAccount does what auth.DeleteAccount does around its hook: hand
// everything over, then delete the user row (the foreign keys of 0013 do
// the rest).
func (r *rig) deleteAccount(t *testing.T, userID string) {
	t.Helper()
	ctx := context.Background()
	if err := r.svc.ReleaseUser(ctx, userID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
}

func hasEvent(v *TableView, typ, contains string) bool {
	for _, e := range v.Log {
		if e.Type == typ && strings.Contains(e.Text, contains) {
			return true
		}
	}
	return false
}

func raceTotal(t *testing.T, r *rig, tableID string) int {
	t.Helper()
	var total int
	if err := r.pool.QueryRow(context.Background(), `SELECT (state->>'total')::int FROM tables WHERE id=$1`, tableID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

// A seated player deletes their account mid-game, at a table with no
// clock: an agent takes the seat over (the game state is untouched) and
// the table keeps moving instead of waiting forever for a ghost.
func TestDeletedSeatedPlayerIsTakenOver(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	ctx := context.Background()
	a, b := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	zero := 0
	v, err := r.svc.Create(ctx, a, CreateRequest{GameID: "race21", TurnSeconds: &zero,
		Seats: []SeatSpec{{Kind: "me"}, {Kind: "open"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Join(ctx, b, v.Code); err != nil {
		t.Fatal(err)
	}
	v = r.start(t, a, v.ID)
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "a1", Version: v.Version, Move: add(3)}); err != nil {
		t.Fatal(err)
	}
	// Bob is to move now, with no clock: before the fix this froze.
	r.deleteAccount(t, b)

	av, err := r.svc.View(ctx, v.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if av.Status != "playing" || av.Seats[1].Kind != "agent" || !hasEvent(av, "takeover", "Bob left") {
		t.Fatalf("after Bob's deletion: status %s seat %+v log %+v", av.Status, av.Seats[1], av.Log)
	}
	if av.HostID != a || !av.IsHost {
		t.Fatalf("the host should not change: %q", av.HostID)
	}
	if got := raceTotal(t, r, v.ID); got != 3 {
		t.Fatalf("the game state changed on takeover: total %d, want 3", got)
	}
	if n := r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND kind='agent_move' AND seat=1 AND status='ready'`, v.ID); n != 1 {
		t.Fatalf("no agent move scheduled for the taken-over seat (%d)", n)
	}
	if n := r.count(t, `SELECT count(*) FROM table_seats WHERE table_id=$1 AND kind='human' AND user_id IS NULL`, v.ID); n != 0 {
		t.Fatalf("%d ghost seats left", n)
	}
	// The agent plays Bob's turn.
	r.dueNow(t)
	for i := 0; i < 5; i++ {
		j := r.claim(t, "w")
		if j == nil {
			break
		}
		r.svc.run(ctx, j)
	}
	if got := raceTotal(t, r, v.ID); got <= 3 {
		t.Fatalf("the agent did not move for the deleted player (total %d)", got)
	}
}

// The host deletes their account: the table survives, host rights pass to
// the next person seated, and an all-agent table nobody else can see closes.
func TestDeletedHostHandsTheTableOver(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	ctx := context.Background()
	a, b := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	v := r.create(t, a, "me", "open", "agent:lin")
	if _, err := r.svc.Join(ctx, b, v.Code); err != nil {
		t.Fatal(err)
	}
	v = r.start(t, a, v.ID)
	// A finished table of Ann's that Bob was at, for the rematch check, and
	// a lobby only Ann was in, and an all-agent table only Ann watched.
	fin := r.create(t, a, "me", "open")
	if _, err := r.svc.Join(ctx, b, fin.Code); err != nil {
		t.Fatal(err)
	}
	fin = r.start(t, a, fin.ID)
	if _, err := r.pool.Exec(ctx, `UPDATE tables SET status='finished' WHERE id=$1`, fin.ID); err != nil {
		t.Fatal(err)
	}
	lobby := r.create(t, a, "me", "agent:ren")
	solo := r.start(t, a, r.create(t, a, "agent:ren", "agent:mika").ID)

	r.deleteAccount(t, a)

	bv, err := r.svc.View(ctx, v.ID, b)
	if err != nil {
		t.Fatalf("Bob lost his table: %v", err)
	}
	if bv.Status != "playing" || bv.HostID != b || !bv.IsHost || bv.Seats[0].Kind != "agent" {
		t.Fatalf("after the host left: status %s host %q is_host %v seat0 %+v", bv.Status, bv.HostID, bv.IsHost, bv.Seats[0])
	}
	if !hasEvent(bv, "host", "is the host now") || !hasEvent(bv, "takeover", "Ann left") {
		t.Fatalf("missing events: %+v", bv.Log)
	}
	fv, err := r.svc.Rematch(ctx, b, fin.ID)
	if err != nil {
		t.Fatalf("the new host cannot rematch: %v", err)
	}
	if fv.HostID != b {
		t.Fatalf("rematch host %q", fv.HostID)
	}
	var status string
	_ = r.pool.QueryRow(ctx, `SELECT status FROM tables WHERE id=$1`, lobby.ID).Scan(&status)
	if status != "abandoned" {
		t.Fatalf("a lobby nobody else is in should close, got %s", status)
	}
	_ = r.pool.QueryRow(ctx, `SELECT status FROM tables WHERE id=$1`, solo.ID).Scan(&status)
	if status != "abandoned" {
		t.Fatalf("an all-agent table nobody can see should close, got %s", status)
	}
	if n := r.count(t, `SELECT count(*) FROM tables WHERE id IN ($1, $2)`, v.ID, fin.ID); n != 2 {
		t.Fatalf("the host's deletion removed tables (%d of 2 left)", n)
	}
}

// A table with no host at all (nobody else seated to take it): any person
// seated may do the host's job.
func TestHostlessTableLetsSeatedPeopleRematch(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a, b := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	v := r.create(t, a, "me", "open")
	if _, err := r.svc.Join(ctx, b, v.Code); err != nil {
		t.Fatal(err)
	}
	v = r.start(t, a, v.ID)
	if _, err := r.pool.Exec(ctx, `UPDATE tables SET status='finished', host_id=NULL WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	stranger := r.user(t, "Eve", nil)
	if _, err := r.svc.Rematch(ctx, stranger, v.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a stranger rematched a hostless table: %v", err)
	}
	nv, err := r.svc.Rematch(ctx, b, v.ID)
	if err != nil || nv.HostID != b {
		t.Fatalf("seated person's rematch: %v %+v", err, nv)
	}
}

// A game's owner deletes their account: published games and every table of
// them stay (credited to "a former player"); drafts go, unless someone
// else's table used them, which is closed with a visible event instead.
func TestDeletedGameOwnerKeepsOthersTables(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	r.svc.Loader = func(ctx context.Context, id string, version int) (games.Game, error) { return race21{}, nil }
	ctx := context.Background()
	a, b := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	meta, _ := json.Marshal(race21{}.Meta())
	if _, err := r.pool.Exec(ctx, `INSERT INTO games (id, owner_id, kind, name, status, visibility, current_version) VALUES
		('pub', $1, 'script', 'Pub', 'published', 'public', 1),
		('shared-draft', $1, 'script', 'Shared', 'draft', 'private', 1),
		('lone-draft', $1, 'script', 'Lone', 'draft', 'private', 1)`, a); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pub", "shared-draft", "lone-draft"} {
		if _, err := r.pool.Exec(ctx, `INSERT INTO game_versions (game_id, version, source, meta) VALUES ($1, 1, 'src', $2)`, id, meta); err != nil {
			t.Fatal(err)
		}
	}
	// Bob hosts a table of Ann's published game.
	pubT, err := r.svc.Create(ctx, b, CreateRequest{GameID: "pub", Seats: []SeatSpec{{Kind: "me"}, {Kind: "agent"}}})
	if err != nil {
		t.Fatal(err)
	}
	r.start(t, b, pubT.ID)
	// Ann's draft, with Bob at the table.
	shared, err := r.svc.Create(ctx, a, CreateRequest{GameID: "shared-draft", Seats: []SeatSpec{{Kind: "me"}, {Kind: "open"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Join(ctx, b, shared.Code); err != nil {
		t.Fatal(err)
	}
	r.start(t, a, shared.ID)
	// Ann's other draft, only ever played by Ann.
	lone, err := r.svc.Create(ctx, a, CreateRequest{GameID: "lone-draft", Seats: []SeatSpec{{Kind: "me"}, {Kind: "agent"}}})
	if err != nil {
		t.Fatal(err)
	}

	r.deleteAccount(t, a)

	pv, err := r.svc.View(ctx, pubT.ID, b)
	if err != nil || pv.Status != "playing" {
		t.Fatalf("Bob's table of the published game: %v %+v", err, pv)
	}
	gl, err := r.svc.Games(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range gl.Community {
		if c.ID == "pub" {
			found = true
			if c.OwnerName != formerOwner {
				t.Fatalf("published game credited to %q", c.OwnerName)
			}
		}
		if c.ID == "shared-draft" || c.ID == "lone-draft" {
			t.Fatalf("a draft became visible: %+v", c)
		}
	}
	if !found {
		t.Fatalf("the published game left the shelf: %+v", gl.Community)
	}
	if _, err := r.svc.Create(ctx, b, CreateRequest{GameID: "pub", Seats: []SeatSpec{{Kind: "me"}, {Kind: "agent"}}}); err != nil {
		t.Fatalf("the published game is no longer playable: %v", err)
	}

	sv, err := r.svc.View(ctx, shared.ID, b)
	if err != nil {
		t.Fatalf("Bob's record of the shared draft: %v", err)
	}
	if sv.Status != "abandoned" || !hasEvent(sv, "closed", "deleted their account") {
		t.Fatalf("the shared draft's table: status %s log %+v", sv.Status, sv.Log)
	}
	if n := r.count(t, `SELECT count(*) FROM games WHERE id='shared-draft' AND owner_id IS NULL AND visibility='private'`); n != 1 {
		t.Fatal("the shared draft should be kept, hidden and ownerless")
	}
	if n := r.count(t, `SELECT count(*) FROM games WHERE id='lone-draft'`); n != 0 {
		t.Fatal("the lone draft should be deleted")
	}
	if n := r.count(t, `SELECT count(*) FROM game_versions WHERE game_id='lone-draft'`); n != 0 {
		t.Fatal("the lone draft's versions should be deleted")
	}
	if n := r.count(t, `SELECT count(*) FROM tables WHERE id=$1`, lone.ID); n != 0 {
		t.Fatal("the lone draft's own table should go with it")
	}
}

// ── Faults ──────────────────────────────────────────────────────────────────

// flaky is race21 whose Apply fails the next `fails` calls.
type flaky struct {
	race21
	fails *atomic.Int64
}

func (f flaky) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	if f.fails.Add(-1) >= 0 {
		return nil, nil, errors.New("boom")
	}
	return f.race21.Apply(st, seat, m)
}

func flakyRig(t *testing.T, fails int64) (*rig, *atomic.Int64, string, string, *TableView) {
	t.Helper()
	r := newRig(t)
	counter := &atomic.Int64{}
	r.svc.Loader = func(ctx context.Context, id string, version int) (games.Game, error) {
		return flaky{fails: counter}, nil
	}
	ctx := context.Background()
	a, b := r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	meta, _ := json.Marshal(race21{}.Meta())
	if _, err := r.pool.Exec(ctx, `INSERT INTO games (id, owner_id, kind, name, status, visibility, current_version)
		VALUES ('flaky', $1, 'script', 'Flaky', 'published', 'public', 1)`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO game_versions (game_id, version, source, meta) VALUES ('flaky', 1, 'src', $1)`, meta); err != nil {
		t.Fatal(err)
	}
	// The agent moves first; Ann hosts, Bob sits too.
	v, err := r.svc.Create(ctx, a, CreateRequest{GameID: "flaky", Seats: []SeatSpec{{Kind: "agent", AgentID: "ren"}, {Kind: "me"}, {Kind: "open"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Join(ctx, b, v.Code); err != nil {
		t.Fatal(err)
	}
	v = r.start(t, a, v.ID)
	counter.Store(fails)
	return r, counter, a, b, v
}

// runJobs runs every due move/clock job, failing ones included, at most n.
func (r *rig) runJobs(t *testing.T, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := r.pool.Exec(ctx, `UPDATE table_jobs SET run_after=now() WHERE status='ready' AND kind <> 'agent_chat'`); err != nil {
			t.Fatal(err)
		}
		if _, err := r.pool.Exec(ctx, `UPDATE table_jobs SET status='done' WHERE status='ready' AND kind = 'agent_chat'`); err != nil {
			t.Fatal(err)
		}
		j := r.claim(t, "w")
		if j == nil {
			return
		}
		r.svc.run(ctx, j)
	}
}

// A job that fails for good (3 attempts) is followed by the default move;
// when that works, the game simply goes on.
func TestFailedJobFallsBackToDefaultMove(t *testing.T) {
	// 3 attempts × (agent move + default retry) = 6 failures, then it works.
	r, _, a, _, v := flakyRig(t, 6)
	r.runJobs(t, 3)
	av, err := r.svc.View(context.Background(), v.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if av.Paused || av.Version <= v.Version || !contains(av.ToMove, 1) {
		t.Fatalf("the default move should have been played: paused %v version %d to_move %v", av.Paused, av.Version, av.ToMove)
	}
	if n := r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND status='failed'`, v.ID); n != 1 {
		t.Fatalf("%d failed jobs, want 1", n)
	}
}

// When even the default move fails, the table pauses with a visible event;
// only the host can resume it, and a second fault closes it.
func TestFailedJobPausesThenAbandons(t *testing.T) {
	r, _, a, b, v := flakyRig(t, 1<<40)
	ctx := context.Background()
	r.runJobs(t, 3)
	av, err := r.svc.View(ctx, v.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if !av.Paused || av.PausedReason != "fault" || av.Deadline != nil || !hasEvent(av, "fault", "hit a problem and was paused") {
		t.Fatalf("expected a fault pause: paused %v reason %q log %+v", av.Paused, av.PausedReason, av.Log)
	}
	if n := r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND status='ready' AND kind <> 'agent_chat'`, v.ID); n != 0 {
		t.Fatalf("a paused table still has %d jobs queued", n)
	}
	// Bob is seated but not the host.
	if _, err := r.svc.Back(ctx, b, v.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("a non-host resumed a faulted table: %v", err)
	}
	if _, err := r.svc.Move(ctx, b, v.ID, MoveRequest{ClientMoveID: "b1", Version: av.Version, Move: add(1)}); err == nil {
		t.Fatal("a move went through on a faulted table")
	}
	rv, err := r.svc.Back(ctx, a, v.ID)
	if err != nil {
		t.Fatalf("host resume: %v", err)
	}
	if rv.Paused || rv.PausedReason != "" {
		t.Fatalf("still paused after the host resumed: %+v", rv)
	}
	if n := r.count(t, `SELECT count(*) FROM table_jobs WHERE table_id=$1 AND status='ready' AND kind='agent_move'`, v.ID); n != 1 {
		t.Fatalf("resume did not retry the agent's move (%d jobs)", n)
	}
	// Still broken: the second fault abandons the table.
	r.runJobs(t, 3)
	fv, err := r.svc.View(ctx, v.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if fv.Status != "abandoned" || !hasEvent(fv, "fault_closed", "closed") {
		t.Fatalf("second fault: status %s log %+v", fv.Status, fv.Log)
	}
}

// A job whose worker keeps dying (leases expiring) fails for good through
// the sweep and gets the same handling.
func TestSweptFailedJobIsRecovered(t *testing.T) {
	r, _, a, _, v := flakyRig(t, 0)
	r.svc.Lease = time.Second
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `UPDATE table_jobs SET attempts=max_attempts-1, run_after=now() WHERE table_id=$1 AND kind='agent_move'`, v.ID); err != nil {
		t.Fatal(err)
	}
	j := r.claim(t, "dies")
	if j == nil || j.Kind != "agent_move" {
		t.Fatalf("claimed %+v", j)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE table_jobs SET lease_expires_at=now() - interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	av, err := r.svc.View(ctx, v.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if av.Version <= v.Version || !contains(av.ToMove, 1) {
		t.Fatalf("the swept job's default move was not played: version %d to_move %v", av.Version, av.ToMove)
	}
}

// ── Move retries ────────────────────────────────────────────────────────────

func TestClientMoveIDReusedForADifferentMoveConflicts(t *testing.T) {
	r := newRig(t)
	r.svc.Think = func(string, bool) time.Duration { return time.Hour }
	ctx := context.Background()
	a := r.user(t, "Ann", nil)
	v := r.start(t, a, r.create(t, a, "me", "agent:lin").ID)
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "m1", Version: v.Version, Move: add(2)}); err != nil {
		t.Fatal(err)
	}
	// Same id, same move: a retry, answered with the view.
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "m1", Version: v.Version, Move: add(2)}); err != nil {
		t.Fatalf("true retry: %v", err)
	}
	// Same id, different move: a conflict, not a silent 200.
	if _, err := r.svc.Move(ctx, a, v.ID, MoveRequest{ClientMoveID: "m1", Version: v.Version, Move: add(3)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused id with another move: %v, want ErrConflict", err)
	}
	// Someone who cannot see the table learns nothing from a replay.
	eve := r.user(t, "Eve", nil)
	if _, err := r.svc.Move(ctx, eve, v.ID, MoveRequest{ClientMoveID: "m1", Version: v.Version, Move: add(2)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger replay: %v, want ErrForbidden", err)
	}
	if got := raceTotal(t, r, v.ID); got != 2 {
		t.Fatalf("total %d, want 2", got)
	}
}
