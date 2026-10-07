package script

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strings"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
)

// Severity of a Check issue. Errors make the module unusable; warnings are
// things a careful author would fix.
type Severity string

const (
	SevError Severity = "error"
	SevWarn  Severity = "warn"
)

// Issue is one finding, written so an LLM Engineer can act on it directly.
type Issue struct {
	Severity Severity `json:"severity"`
	Where    string   `json:"where"`
	Message  string   `json:"message"`
}

// CheckReport is the result of Check.
type CheckReport struct {
	ID      string      `json:"id"`
	Meta    *games.Meta `json:"meta,omitempty"`
	Issues  []Issue     `json:"issues"`
	Console []string    `json:"console,omitempty"` // the module's recent console output
	// Playouts is how many random games were played; Finished how many ended.
	Playouts int `json:"playouts"`
	Finished int `json:"finished"`
}

// MsgNoDeterminize is the Check error for a hidden-information module without
// determinize. The playtester reports the same.
const MsgNoDeterminize = "hidden-information games must define determinize(state, seat, ctx) so fairness can be verified and agents can play without peeking: " +
	"return a copy of the state with everything seat cannot see (opponents' hands, the deck order) resampled with ctx.random()"

// OK reports whether the module has no errors (warnings allowed).
func (r CheckReport) OK() bool { return r.count(SevError) == 0 }

func (r CheckReport) count(s Severity) int {
	n := 0
	for _, i := range r.Issues {
		if i.Severity == s {
			n++
		}
	}
	return n
}

// String renders the report as plain text for the Engineer agent.
func (r CheckReport) String() string {
	var b strings.Builder
	status := "OK"
	if !r.OK() {
		status = "FAILED"
	}
	fmt.Fprintf(&b, "check %s: %s (%d errors, %d warnings; %d/%d random playouts finished)\n",
		r.ID, status, r.count(SevError), r.count(SevWarn), r.Finished, r.Playouts)
	for _, i := range r.Issues {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", i.Severity, i.Where, i.Message)
	}
	if len(r.Console) > 0 {
		b.WriteString("console (most recent last):\n")
		for _, l := range r.Console[max(0, len(r.Console)-10):] {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}
	return b.String()
}

const (
	checkPlayouts  = 3
	checkMaxMoves  = 300
	checkMaxIssues = 25
)

// Check loads a module and exercises it the way the platform will: meta
// sanity, setup at the minimum and maximum seat counts, every function on
// the opening position, every opening move, a few random playouts, replay
// determinism and (for hidden-information games) a view leak check. It
// never panics on a bad module; everything becomes an Issue.
func Check(id, src string) CheckReport {
	c := &checker{rep: CheckReport{ID: id, Issues: []Issue{}}}
	g, err := Load(id, src, Options{PoolSize: 2})
	if err != nil {
		c.add(SevError, "load", err.Error())
		return c.rep
	}
	// A second, independent instance: replaying on fresh runtimes is what
	// exposes module-level state, which would otherwise look deterministic.
	g2, err := Load(id, src, Options{PoolSize: 1})
	if err != nil {
		c.add(SevError, "load", err.Error())
		return c.rep
	}
	m := g.Meta()
	c.rep.Meta = &m
	c.checkMeta(g, m)

	seatCounts := []int{m.MinSeats}
	if m.MaxSeats != m.MinSeats {
		seatCounts = append(seatCounts, m.MaxSeats)
	}
	for _, n := range seatCounts {
		c.checkSeats(g, g2, n)
	}
	c.rep.Console = g.Logs()
	return c.rep
}

type checker struct {
	rep  CheckReport
	seen map[string]bool
}

func (c *checker) add(sev Severity, where, msg string) {
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	key := string(sev) + "|" + msg
	if c.seen[key] || len(c.rep.Issues) >= checkMaxIssues {
		return
	}
	c.seen[key] = true
	c.rep.Issues = append(c.rep.Issues, Issue{Severity: sev, Where: where, Message: msg})
}

func (c *checker) errf(where string, err error) {
	c.add(SevError, where, err.Error())
}

func (c *checker) checkMeta(g *Game, m games.Meta) {
	if strings.TrimSpace(m.Summary) == "" {
		c.add(SevWarn, "meta", "meta.summary is empty; write one sentence for the lobby card")
	}
	if len(m.Name) > 60 {
		c.add(SevWarn, "meta", "meta.name is longer than 60 characters")
	}
	if m.TurnSeconds != 0 && (m.TurnSeconds < 5 || m.TurnSeconds > 600) {
		c.add(SevWarn, "meta", fmt.Sprintf("meta.turnSeconds is %d; use 5..600 (or 0 for no clock)", m.TurnSeconds))
	}
	if m.HiddenInfo && !g.HasDeterminizer() {
		c.add(SevError, "meta", MsgNoDeterminize)
	}
	for _, path := range g.mutable {
		c.add(SevError, "module", fmt.Sprintf("module-level value %s cannot be frozen: it would be mutable state shared by every call and every table. Build it inside the function that needs it", path))
	}
	if !m.HiddenInfo && g.HasDeterminizer() {
		c.add(SevWarn, "meta", "determinize is defined but meta.hiddenInfo is false; it is only used for hidden-information games")
	}
}

func (c *checker) checkSeats(g, g2 *Game, n int) {
	where := fmt.Sprintf("%d seats", n)
	cfg := games.Config{Seats: n}
	st, err := g.Setup(cfg, 1)
	if err != nil {
		c.errf(where+", setup", err)
		return
	}
	// Same seed on a fresh instance, and again on this one: module-level
	// variables show up in one comparison or the other.
	again, err1 := g.Setup(cfg, 1)
	fresh, err2 := g2.Setup(cfg, 1)
	if err1 == nil && err2 == nil && (!bytes.Equal(st, fresh) || !bytes.Equal(st, again)) {
		c.add(SevError, where+", setup", "setup is not deterministic: the same seed gave different states. Use ctx.random() for randomness and never keep data in module-level variables")
	}

	tm, ok := c.position(g, st, n, where+", opening position", true)
	if !ok {
		return
	}
	if len(tm) == 0 {
		c.add(SevError, where+", opening position", "the game is over before the first move (toMove returned [])")
		return
	}
	if g.HasDeterminizer() {
		c.leakCheck(g, st, n, where+", opening position")
	}

	// Every opening move must apply cleanly.
	for _, seat := range tm {
		specs, err := g.Legal(st, seat)
		if err != nil {
			continue // already reported
		}
		for i, m := range ai.ExpandMoves(specs, 3) {
			if i >= 40 {
				break
			}
			if _, _, err := g.Apply(st, seat, m); err != nil {
				c.errf(fmt.Sprintf("%s, opening move %s by seat %d", where, describeMove(m), seat), err)
			}
		}
	}

	for k := 0; k < checkPlayouts; k++ {
		c.playout(g, g2, n, int64(k+1), where)
	}
}

// position checks every function on one state and returns toMove.
func (c *checker) position(g *Game, st games.State, n int, where string, full bool) ([]games.Seat, bool) {
	tm, err := g.ToMove(st)
	if err != nil {
		c.errf(where, err)
		return nil, false
	}
	o, err := g.Outcome(st)
	if err != nil {
		c.errf(where, err)
		return nil, false
	}
	if len(tm) == 0 && o == nil {
		c.add(SevError, where, "toMove returned [] (the game is over) but outcome returned null; return { rank, score, summary } once the game ends")
		return nil, false
	}
	if len(tm) > 0 && o != nil {
		c.add(SevError, where, fmt.Sprintf("outcome is set but toMove still lists seats %v; toMove must return [] once the game is over", tm))
		return nil, false
	}
	if o != nil {
		c.text(where+", outcome.summary", o.Summary)
	}
	legalOf := make(map[int][]games.MoveSpec, n)
	for seat := 0; seat < n; seat++ {
		specs, err := g.Legal(st, seat)
		if err != nil {
			c.errf(where, err)
			return nil, false
		}
		legalOf[seat] = specs
		mover := slices.Contains(tm, seat)
		if mover && len(specs) == 0 {
			c.add(SevError, where, fmt.Sprintf("toMove lists seat %d but legal(state, %d) returned no moves; offer at least a pass", seat, seat))
		}
		if !mover && len(specs) > 0 {
			c.add(SevError, where, fmt.Sprintf("legal(state, %d) returned %d moves but seat %d is not in toMove; return [] when it is not the seat's turn", seat, len(specs), seat))
		}
		if mover && full && len(specs) > 0 {
			if _, err := g.DefaultMove(st, seat); err != nil {
				c.errf(where+", defaultMove", err)
			}
		}
	}
	seats := []games.Seat{}
	if full {
		for s := games.Spectator; s < n; s++ {
			seats = append(seats, s)
		}
	} else if len(tm) > 0 {
		seats = append(seats, tm[0])
	}
	for _, s := range seats {
		v, err := g.View(st, s)
		if err != nil {
			c.errf(fmt.Sprintf("%s, view for seat %d", where, s), err)
			return nil, false
		}
		c.text(where+", view.message", v.Status)
		if s >= 0 {
			c.hints(fmt.Sprintf("%s, legal(state, %d)", where, s), legalOf[s], v)
		}
	}
	return tm, true
}

// hints checks that every move's ui hint points at something the mover's
// own view shows: a hint to a missing zone, card or cell leaves the move
// unclickable on the board.
func (c *checker) hints(where string, specs []games.MoveSpec, v games.View) {
	if len(specs) == 0 {
		return
	}
	raw, ok := v.Data.(json.RawMessage)
	if !ok {
		return
	}
	var view struct {
		Board *struct {
			Rows, Cols int
			Cells      [][]*struct {
				Blocked bool `json:"blocked"`
			}
		} `json:"board"`
		Zones []struct {
			ID    string `json:"id"`
			Cards []struct {
				Hidden bool `json:"hidden"`
			} `json:"cards"`
		} `json:"zones"`
	}
	if json.Unmarshal(raw, &view) != nil {
		return
	}
	cellOK := func(k string, x any) bool {
		a, _ := x.([]any)
		if len(a) != 2 {
			return true // parseUI already reported the shape
		}
		r, _ := asInt(a[0])
		col, _ := asInt(a[1])
		if view.Board == nil {
			c.add(SevError, where, fmt.Sprintf("a move has ui.%s %v but the view has no board; add view.board or drop the hint", k, x))
			return false
		}
		if r >= view.Board.Rows || col >= view.Board.Cols {
			c.add(SevError, where, fmt.Sprintf("a move has ui.%s [%d,%d], outside the %dx%d board; rows and cols are 0-based", k, r, col, view.Board.Rows, view.Board.Cols))
			return false
		}
		if k != "from" && r < len(view.Board.Cells) && col < len(view.Board.Cells[r]) && view.Board.Cells[r][col] != nil && view.Board.Cells[r][col].Blocked {
			c.add(SevWarn, where, fmt.Sprintf("a move targets ui.%s [%d,%d], which the view marks blocked; players will not expect to play there", k, r, col))
		}
		return true
	}
	for _, m := range specs {
		for _, k := range []string{"cell", "from", "to"} {
			if x, ok := m.UI[k]; ok && !cellOK(k, x) {
				return
			}
		}
		zid, ok := m.UI["zone"].(string)
		if !ok {
			continue
		}
		idx, _ := asInt(m.UI["index"])
		found := false
		for _, z := range view.Zones {
			if z.ID != zid {
				continue
			}
			found = true
			if idx >= len(z.Cards) {
				c.add(SevError, where, fmt.Sprintf("move %q has ui {zone:%q, index:%d} but that zone shows %d cards in the mover's view; index the cards as the view lists them", m.Label, zid, idx, len(z.Cards)))
				return
			}
			if z.Cards[idx].Hidden {
				c.add(SevWarn, where, fmt.Sprintf("move %q points at card %d of zone %q, which is hidden in the mover's own view; show the mover their own cards", m.Label, idx, zid))
			}
		}
		if !found {
			c.add(SevError, where, fmt.Sprintf("move %q has ui.zone %q but the mover's view has no zone with that id", m.Label, zid))
			return
		}
	}
}

// seatWords catches text that names seats instead of using {s:N}; the
// platform cannot substitute player names into "Player 2".
var seatWords = regexp.MustCompile(`(?i)\b(player|seat)\s*#?\d+\b`)

func (c *checker) text(where, s string) {
	if seatWords.MatchString(s) {
		c.add(SevWarn, where, fmt.Sprintf("text %q refers to a seat by number; write {s:N} so the platform can show the player's name", s))
	}
}

func (c *checker) playout(g, g2 *Game, n int, seed int64, where string) {
	c.rep.Playouts++
	where = fmt.Sprintf("%s, random playout %d", where, seed)
	rng := rand.New(rand.NewSource(seed))
	st, err := g.Setup(games.Config{Seats: n}, seed)
	if err != nil {
		c.errf(where, err)
		return
	}
	var steps []step
	for i := 0; i < checkMaxMoves; i++ {
		at := fmt.Sprintf("%s, move %d", where, i)
		tm, ok := c.position(g, st, n, at, i%10 == 0)
		if !ok {
			return
		}
		if len(tm) == 0 {
			c.rep.Finished++
			c.replay(g2, n, seed, steps, st, where)
			return
		}
		if g.HasDeterminizer() && i%5 == 0 {
			c.leakCheck(g, st, n, at)
		}
		seat := tm[rng.Intn(len(tm))]
		specs, err := g.Legal(st, seat)
		if err != nil || len(specs) == 0 {
			return // reported by position
		}
		m := ai.RandomMove(specs, rng)
		next, events, err := g.Apply(st, seat, m)
		if err != nil {
			c.errf(fmt.Sprintf("%s, apply %s by seat %d", at, describeMove(m), seat), err)
			return
		}
		for _, e := range events {
			c.text(at+", event", e.Text)
		}
		steps = append(steps, step{seat, m})
		st = next
	}
	c.add(SevWarn, where, fmt.Sprintf("the game did not end within %d random moves; make sure every game ends (toMove returns [] and outcome is set)", checkMaxMoves))
}

type step struct {
	seat games.Seat
	move games.Move
}

func (c *checker) replay(g2 *Game, n int, seed int64, steps []step, want games.State, where string) {
	st, err := g2.Setup(games.Config{Seats: n}, seed)
	if err != nil {
		return
	}
	for _, s := range steps {
		if st, _, err = g2.Apply(st, s.seat, s.move); err != nil {
			c.add(SevError, where, "replaying the same moves failed on a fresh runtime: "+err.Error()+". Never keep data in module-level variables")
			return
		}
	}
	if !bytes.Equal(st, want) {
		c.add(SevError, where, "replaying the same moves from the same seed gave a different state. Use only ctx.random() for randomness and never keep data in module-level variables")
	}
}

// leakCheck tests the fairness invariant: the view for viewer V must be the
// same in any two states that differ only in information hidden from V.
// determinize(state, seat) produces exactly such a state for V = seat, so:
//
//   - seat's own view, the seats to move and seat's legal moves must not
//     change (else the view shows what seat cannot know, or determinize
//     resampled something seat does know);
//   - the spectator view must not change either. A spectator knows only
//     public information, so everything any seat cannot see is hidden from
//     it too. This matters most: the spectator view is shown to anyone with
//     the invite code and feeds the AI players' table talk.
//
// Determinizing from every seat in turn reaches states that differ in
// everything any seat hides (the union), which the spectator view must not
// reveal either. Other seats' views are not compared against seat's
// determinization: it may legitimately resample their own hands. Each of
// them is checked against its own determinization instead.
func (c *checker) leakCheck(g *Game, st games.State, n int, where string) {
	for k := uint64(1); k <= 2; k++ {
		chained := st
		for seat := 0; seat < n; seat++ {
			det, err := g.Determinize(st, seat, k)
			if err != nil {
				c.errf(where+", determinize", err)
				return
			}
			msg, err := LeakDiff(g, st, det, seat)
			if err != nil {
				c.errf(where+", hidden information check", err)
				return
			}
			if msg != "" {
				c.add(SevError, where+", hidden information", msg)
				return
			}
			if chained, err = g.Determinize(chained, seat, k+uint64(seat)<<8); err != nil {
				c.errf(where+", determinize", err)
				return
			}
		}
		msg, err := SpectatorDiff(g, st, chained, fmt.Sprintf("determinizing from every seat in turn (0..%d)", n-1))
		if err != nil {
			c.errf(where+", hidden information check", err)
			return
		}
		if msg != "" {
			c.add(SevError, where+", hidden information", msg)
			return
		}
	}
}

// LeakDiff checks det, a determinization of st from seat's point of view,
// against st: seat's own view, toMove and legal moves (SameInformation) and
// the spectator view (SpectatorDiff). It describes the first difference, or
// returns "". The playtester uses it for its leak check.
func LeakDiff(g games.Game, st, det games.State, seat games.Seat) (string, error) {
	if msg, err := SameInformation(g, st, det, seat); msg != "" || err != nil {
		return msg, err
	}
	return SpectatorDiff(g, st, det, fmt.Sprintf("determinize(state, %d)", seat))
}

// SpectatorDiff compares the spectator view (seat -1) of st and of other, a
// state that differs from st only in information some seat cannot see
// (produced by how). Any difference means spectators see hidden information.
func SpectatorDiff(g games.Game, st, other games.State, how string) (string, error) {
	va, err := g.View(st, games.Spectator)
	if err != nil {
		return "", err
	}
	vb, err := g.View(other, games.Spectator)
	if err != nil {
		return "", err
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	if bytes.Equal(ja, jb) {
		return "", nil
	}
	return fmt.Sprintf("the spectator view, view(state, -1), changed after %s: it shows information a seat cannot see (a hand, the deck order), "+
		"or determinize changed public information. Spectators (anyone with the invite code, and the AI players' table talk) may only see public information: "+
		"when seat < 0, hide every private zone. Before: %s After: %s",
		how, truncate(string(ja), 300), truncate(string(jb), 300)), nil
}

// SameInformation compares what seat sees in two states (view, toMove and
// legal moves) and describes the first difference, or returns "". A failing
// call is returned as err, so callers can tell a broken module from a leak.
// The playtester uses it for its hidden-information leak check.
func SameInformation(g games.Game, a, b games.State, seat games.Seat) (diff string, err error) {
	va, err := g.View(a, seat)
	if err != nil {
		return "", err
	}
	vb, err := g.View(b, seat)
	if err != nil {
		return "", err
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	if !bytes.Equal(ja, jb) {
		return fmt.Sprintf("view(state, %d) changed after determinize(state, %d): the view shows information seat %d should not have, or determinize changed something seat %d knows. Before: %s After: %s",
			seat, seat, seat, seat, truncate(string(ja), 300), truncate(string(jb), 300)), nil
	}
	ta, err := g.ToMove(a)
	if err != nil {
		return "", err
	}
	tb, err := g.ToMove(b)
	if err != nil {
		return "", err
	}
	if !slices.Equal(ta, tb) {
		return fmt.Sprintf("toMove changed after determinize(state, %d): %v became %v", seat, ta, tb), nil
	}
	la, err := g.Legal(a, seat)
	if err != nil {
		return "", err
	}
	lb, err := g.Legal(b, seat)
	if err != nil {
		return "", err
	}
	ka, _ := json.Marshal(la)
	kb, _ := json.Marshal(lb)
	if !bytes.Equal(ka, kb) {
		return fmt.Sprintf("legal(state, %d) changed after determinize(state, %d): the seat's options must depend only on what it knows", seat, seat), nil
	}
	return "", nil
}
