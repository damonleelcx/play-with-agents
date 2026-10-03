package playtest

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/script"
)

// ErrorClass groups errors by what the module author has to fix.
type ErrorClass string

const (
	ClassModule   ErrorClass = "module error"           // a throw or crash inside the module
	ClassMismatch ErrorClass = "illegal-legal mismatch" // legal() and apply()/toMove() disagree
	ClassInvalid  ErrorClass = "invalid output"         // a return value breaks the contract
	ClassTimeout  ErrorClass = "timeout"                // a call exceeded its time budget
	ClassLeak     ErrorClass = "hidden-info leak"       // a view reveals what the seat cannot know
)

// SeatMove is one move of a replayable history.
type SeatMove struct {
	Seat games.Seat `json:"seat"`
	Move games.Move `json:"move"`
}

// Error is one failure, with everything needed to replay it: Setup with
// (Seats, Seed), then apply History in order; the failure happens at move
// MoveIndex (== len(History)) by Seat.
type Error struct {
	Game      int         `json:"game"`
	Seed      int64       `json:"seed"`
	Seats     int         `json:"seats"`
	Matchup   Matchup     `json:"matchup"`
	MoveIndex int         `json:"move_index"`
	Seat      games.Seat  `json:"seat"` // -1 when no seat is involved (setup, toMove)
	Move      *games.Move `json:"move,omitempty"`
	Class     ErrorClass  `json:"class"`
	Message   string      `json:"message"`
	History   []SeatMove  `json:"history,omitempty"`
}

// Dist summarises a sample.
type Dist struct {
	Min  float64 `json:"min"`
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	Max  float64 `json:"max"`
}

// SeatStats is the balance picture for one seat count, from
// random-vs-random games only (AI seats would skew it).
type SeatStats struct {
	Seats int `json:"seats"`
	Games int `json:"games"` // completed random-vs-random games
	// WinShare[s] is seat s's share of wins; tied winners split a win.
	WinShare []float64 `json:"win_share"`
	Draws    int       `json:"draws"` // every seat tied for first
	// FirstPlayerWinShare is the win share of whoever moved first.
	FirstPlayerWinShare float64 `json:"first_player_win_share"`
}

// AIStats compares one AI seat against random seats.
type AIStats struct {
	Games    int     `json:"games"`
	WinShare float64 `json:"win_share"` // the AI seat's share of wins
	Baseline float64 `json:"baseline"`  // expected share without skill: mean of 1/seats
}

// Latency is per-call wall time.
type Latency struct {
	Calls int           `json:"calls"`
	P50   time.Duration `json:"p50"`
	P95   time.Duration `json:"p95"`
	Max   time.Duration `json:"max"`
}

// Report is the playtest result.
type Report struct {
	Game    string    `json:"game"`
	Seed    int64     `json:"seed"`
	Options OptionsIn `json:"options"`

	Run int `json:"run"` // games played to an end, an abort or an error
	// Requested is how many games were asked for; BudgetStopped is set when
	// the time budget ended the run before all of them started.
	Requested     int  `json:"requested"`
	BudgetStopped bool `json:"budget_stopped"`
	Completed     int  `json:"completed"` // reached an outcome
	Aborted       int  `json:"aborted"`   // hit MaxMoves
	Errored       int  `json:"errored"`

	ErrorCount int            `json:"error_count"`
	Errors     []Error        `json:"errors"` // the first MaxErrors, by game
	ByClass    map[string]int `json:"by_class"`

	Length   Dist        `json:"length"` // moves per completed game
	BySeats  []SeatStats `json:"by_seats"`
	DrawRate float64     `json:"draw_rate"` // completed random-vs-random games ending in a full tie
	// FirstPlayerAdvantage is the first mover's win share minus the
	// no-advantage baseline, over random-vs-random games.
	FirstPlayerAdvantage float64 `json:"first_player_advantage"`
	AIVsRandom           AIStats `json:"ai_vs_random"`
	LeakChecks           int     `json:"leak_checks"`
	// FlakyTimeouts counts games whose timeout did not reproduce on replay.
	FlakyTimeouts int `json:"flaky_timeouts"`
	// HiddenInfo and Determinizer describe the game: a hidden-information
	// game without a determinizer cannot be checked for leaks and fails.
	HiddenInfo   bool `json:"hidden_info"`
	Determinizer bool `json:"determinizer"`

	Latency       Latency            `json:"latency"`
	LatencyByCall map[string]Latency `json:"latency_by_call"`
	Elapsed       time.Duration      `json:"elapsed"`
}

// OptionsIn records the options a report was produced with.
type OptionsIn struct {
	Games        int   `json:"games"`
	Seats        []int `json:"seats"`
	Mix          Mix   `json:"mix"`
	MaxMoves     int   `json:"max_moves"`
	AIIterations int   `json:"ai_iterations"`
}

func buildReport(meta games.Meta, determinizer bool, opt Options, results []gameResult, elapsed time.Duration) Report {
	r := Report{
		Game: meta.ID, Seed: opt.Seed, Elapsed: elapsed, ByClass: map[string]int{}, Errors: []Error{},
		Options: OptionsIn{Games: opt.Games, Seats: opt.Seats, Mix: opt.Mix, MaxMoves: opt.MaxMoves, AIIterations: opt.AIIterations},
	}
	r.HiddenInfo, r.Determinizer = meta.HiddenInfo, determinizer
	for _, g := range results {
		if g.flakyTimeout {
			r.FlakyTimeouts++
		}
	}
	var lengths []float64
	lat := map[string][]time.Duration{}
	bySeats := map[int]*SeatStats{}
	var firstShare, firstBase float64
	var rrGames, rrDraws int
	var aiShare, aiBase float64

	for _, g := range results {
		if !g.played {
			continue
		}
		r.Run++
		r.LeakChecks += g.leaks
		for k, v := range g.latency {
			lat[k] = append(lat[k], v...)
		}
		switch {
		case len(g.errs) > 0:
			r.Errored++
			r.ErrorCount += len(g.errs)
			for _, e := range g.errs {
				r.ByClass[string(e.Class)]++
				if len(r.Errors) < opt.MaxErrors {
					r.Errors = append(r.Errors, e)
				}
			}
			continue
		case g.aborted:
			r.Aborted++
			continue
		case !g.completed:
			continue
		}
		r.Completed++
		lengths = append(lengths, float64(g.moves))
		share := winShares(g.outcome)

		switch g.matchup {
		case RandomVsRandom:
			ss := bySeats[g.seats]
			if ss == nil {
				ss = &SeatStats{Seats: g.seats, WinShare: make([]float64, g.seats)}
				bySeats[g.seats] = ss
			}
			ss.Games++
			for s, w := range share {
				ss.WinShare[s] += w
			}
			if g.seats > 1 && isDraw(g.outcome) {
				ss.Draws++
				rrDraws++
			}
			rrGames++
			if g.first >= 0 && g.seats > 1 {
				ss.FirstPlayerWinShare += share[g.first]
				firstShare += share[g.first]
				firstBase += 1 / float64(g.seats)
			}
		case AIVsRandom:
			if g.seats > 1 && g.aiSeat >= 0 {
				r.AIVsRandom.Games++
				aiShare += share[g.aiSeat]
				aiBase += 1 / float64(g.seats)
			}
		}
	}

	r.Length = dist(lengths)
	for _, ss := range bySeats {
		if ss.Games > 0 {
			for s := range ss.WinShare {
				ss.WinShare[s] /= float64(ss.Games)
			}
			ss.FirstPlayerWinShare /= float64(ss.Games)
		}
		r.BySeats = append(r.BySeats, *ss)
	}
	sort.Slice(r.BySeats, func(i, j int) bool { return r.BySeats[i].Seats < r.BySeats[j].Seats })
	if rrGames > 0 {
		r.DrawRate = float64(rrDraws) / float64(rrGames)
		r.FirstPlayerAdvantage = (firstShare - firstBase) / float64(rrGames)
	}
	if n := r.AIVsRandom.Games; n > 0 {
		r.AIVsRandom.WinShare = aiShare / float64(n)
		r.AIVsRandom.Baseline = aiBase / float64(n)
	}

	var all []time.Duration
	r.LatencyByCall = map[string]Latency{}
	for k, v := range lat {
		r.LatencyByCall[k] = latency(v)
		all = append(all, v...)
	}
	r.Latency = latency(all)
	return r
}

// winShares splits one win among the seats ranked first.
func winShares(o *games.Outcome) []float64 {
	share := make([]float64, len(o.Rank))
	winners := 0
	for _, rk := range o.Rank {
		if rk == 1 {
			winners++
		}
	}
	for s, rk := range o.Rank {
		if rk == 1 {
			share[s] = 1 / float64(winners)
		}
	}
	return share
}

func isDraw(o *games.Outcome) bool {
	for _, rk := range o.Rank {
		if rk != 1 {
			return false
		}
	}
	return true
}

func dist(xs []float64) Dist {
	if len(xs) == 0 {
		return Dist{}
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	return Dist{Min: s[0], Mean: sum / float64(len(s)), P50: pct(s, 0.5), P95: pct(s, 0.95), Max: s[len(s)-1]}
}

func pct[T ~float64 | ~int64](sorted []T, p float64) T {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

func latency(ds []time.Duration) Latency {
	if len(ds) == 0 {
		return Latency{}
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return Latency{Calls: len(s), P50: pct(s, 0.5), P95: pct(s, 0.95), Max: s[len(s)-1]}
}

// ── verdict ─────────────────────────────────────────────────────────────────

// Thresholds of the verdict.
const (
	minCompletion   = 0.95
	maxSeatShare    = 0.80
	minAIEdge       = 0.10 // AI win share must beat the baseline by this much
	minSampleGames  = 20   // balance warnings need this many games
	minBudgetGames  = 30   // a budget-stopped run needs at least this many
	minAISampleGame = 10
)

// Verdict decides whether the game may ship. pass is false on any error, on
// completion below 95% or on invalid outputs. Reasons lists every failure
// ("FAIL: ...") and warning ("WARN: ...").
func (r Report) Verdict() (pass bool, reasons []string) {
	pass = true
	failf := func(f string, a ...any) {
		pass = false
		reasons = append(reasons, "FAIL: "+fmt.Sprintf(f, a...))
	}
	warnf := func(f string, a ...any) { reasons = append(reasons, "WARN: "+fmt.Sprintf(f, a...)) }

	if r.Run == 0 {
		failf("no games were played")
		return pass, reasons
	}
	if r.BudgetStopped {
		if need := max(minBudgetGames, r.Requested/4); r.Run < need {
			failf("only %d of %d games finished within the time budget (at least %d needed): legal/apply/view or the game's length make it too slow to play", r.Run, r.Requested, need)
		} else {
			warnf("the time budget stopped the run after %d of %d games", r.Run, r.Requested)
		}
	}
	if r.FlakyTimeouts > 0 {
		warnf("%d game(s) timed out once but not on replay (a busy machine, not the module); keep legal/apply/view cheap", r.FlakyTimeouts)
	}
	if r.HiddenInfo && !r.Determinizer {
		failf("%s", script.MsgNoDeterminize)
	}
	if r.ErrorCount > 0 {
		classes := make([]string, 0, len(r.ByClass))
		for c, n := range r.ByClass {
			classes = append(classes, fmt.Sprintf("%d %s", n, c))
		}
		sort.Strings(classes)
		failf("%d error(s) (%s); first: %s", r.ErrorCount, strings.Join(classes, ", "), r.Errors[0].Message)
	}
	if n := r.ByClass[string(ClassInvalid)]; n > 0 {
		failf("%d invalid output(s): the module returned values that break the contract", n)
	}
	if c := float64(r.Completed) / float64(r.Run); c < minCompletion {
		failf("only %.0f%% of games completed (%d aborted at %d moves, %d errored); every game must end", 100*c, r.Aborted, r.Options.MaxMoves, r.Errored)
	}

	for _, ss := range r.BySeats {
		if ss.Games < minSampleGames || ss.Seats < 2 {
			continue
		}
		for s, w := range ss.WinShare {
			if w > maxSeatShare {
				warnf("with %d seats, seat %d wins %.0f%% of random games: the game may be unbalanced", ss.Seats, s, 100*w)
			}
		}
		if ss.Draws == ss.Games {
			warnf("with %d seats, every random game ended in a draw", ss.Seats)
		}
	}
	if a := r.AIVsRandom; a.Games >= minAISampleGame && a.WinShare < a.Baseline+minAIEdge {
		warnf("the AI won %.0f%% against random players (%.0f%% expected by chance): skill may not matter, the game may be pure luck", 100*a.WinShare, 100*a.Baseline)
	}
	return pass, reasons
}

// ── markdown ────────────────────────────────────────────────────────────────

// Markdown is a concise summary for the Critic agent and the user.
func (r Report) Markdown() string {
	var b strings.Builder
	pass, reasons := r.Verdict()
	verdict := "PASS"
	if !pass {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "## Playtest: %s — %s\n\n", r.Game, verdict)
	fmt.Fprintf(&b, "%d games (seed %d, seats %v, AI %d iterations): %d completed, %d aborted at %d moves, %d with errors. %s.\n\n",
		r.Run, r.Seed, r.Options.Seats, r.Options.AIIterations, r.Completed, r.Aborted, r.Options.MaxMoves, r.Errored, r.Elapsed.Round(time.Millisecond))
	for _, s := range reasons {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	if len(reasons) > 0 {
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Game length (moves) | median %.0f, p95 %.0f, range %.0f–%.0f |\n", r.Length.P50, r.Length.P95, r.Length.Min, r.Length.Max)
	fmt.Fprintf(&b, "| Draw rate (random play) | %.0f%% |\n", 100*r.DrawRate)
	fmt.Fprintf(&b, "| First-player advantage | %+.0f pts |\n", 100*r.FirstPlayerAdvantage)
	if a := r.AIVsRandom; a.Games > 0 {
		fmt.Fprintf(&b, "| AI vs random | AI wins %.0f%% (chance %.0f%%, %d games) |\n", 100*a.WinShare, 100*a.Baseline, a.Games)
	}
	for _, ss := range r.BySeats {
		parts := make([]string, len(ss.WinShare))
		for s, w := range ss.WinShare {
			parts[s] = fmt.Sprintf("{s:%d} %.0f%%", s, 100*w)
		}
		fmt.Fprintf(&b, "| Wins, %d seats (%d random games) | %s |\n", ss.Seats, ss.Games, strings.Join(parts, ", "))
	}
	if r.LeakChecks > 0 {
		fmt.Fprintf(&b, "| Hidden-info leak checks | %d |\n", r.LeakChecks)
	}
	fmt.Fprintf(&b, "| Call latency | p50 %v, p95 %v, max %v (%d calls) |\n",
		r.Latency.P50.Round(time.Microsecond), r.Latency.P95.Round(time.Microsecond), r.Latency.Max.Round(time.Microsecond), r.Latency.Calls)

	if len(r.Errors) > 0 {
		b.WriteString("\n### Errors (replayable)\n\n")
		for i, e := range r.Errors {
			if i == 5 {
				fmt.Fprintf(&b, "- … %d more\n", r.ErrorCount-5)
				break
			}
			mv := ""
			if e.Move != nil {
				j, _ := json.Marshal(e.Move)
				mv = " move " + string(j)
			}
			fmt.Fprintf(&b, "- **%s** game %d (seed %d, %d seats), move %d, seat %d%s: %s\n",
				e.Class, e.Game, e.Seed, e.Seats, e.MoveIndex, e.Seat, mv, e.Message)
		}
	}
	return b.String()
}
