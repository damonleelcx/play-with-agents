package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
	"github.com/damonleelcx/play-with-agents/internal/games/script"
)

// ── toy Go games ────────────────────────────────────────────────────────────

// takeAway: N stones, take 1..3 (a ranged move), whoever takes the last
// stone wins. Leaving a multiple of 4 is the winning strategy, so a search
// that works must find it.
type takeAway struct {
	hidden   bool // pretend to be a hidden-information game (no determinizer)
	brokenAt int  // Apply errors when this many stones remain (0 = never)
}

type takeState struct {
	N, Turn, Winner int
}

func (takeAway) dec(st games.State) takeState {
	var s takeState
	_ = json.Unmarshal(st, &s)
	return s
}

func (g takeAway) Meta() games.Meta {
	return games.Meta{ID: "take", Name: "Take away", MinSeats: 2, MaxSeats: 2, HiddenInfo: g.hidden}
}
func (takeAway) Setup(cfg games.Config, seed int64) (games.State, error) {
	return json.Marshal(takeState{N: 10, Winner: -1})
}
func (g takeAway) ToMove(st games.State) ([]games.Seat, error) {
	s := g.dec(st)
	if s.Winner >= 0 {
		return []games.Seat{}, nil
	}
	return []games.Seat{s.Turn}, nil
}
func (g takeAway) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	s := g.dec(st)
	if s.Winner >= 0 || seat != s.Turn {
		return nil, nil
	}
	return []games.MoveSpec{{Type: "take", Label: "Take", Range: &games.Range{Arg: "n", Min: 1, Max: min(3, s.N)}}}, nil
}
func (g takeAway) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	s := g.dec(st)
	if g.brokenAt > 0 && s.N == g.brokenAt {
		return nil, nil, fmt.Errorf("broken")
	}
	n, ok := m.Args["n"].(int)
	if !ok {
		f, isF := m.Args["n"].(float64)
		n, ok = int(f), isF
	}
	if !ok || n < 1 || n > min(3, s.N) || seat != s.Turn || s.Winner >= 0 {
		return nil, nil, games.Illegal("bad take %v", m.Args)
	}
	s.N -= n
	if s.N == 0 {
		s.Winner = seat
	}
	s.Turn = 1 - seat
	b, _ := json.Marshal(s)
	return b, nil, nil
}
func (takeAway) View(st games.State, seat games.Seat) (games.View, error) {
	return games.View{Kind: "board", Data: map[string]any{}}, nil
}
func (g takeAway) Outcome(st games.State) (*games.Outcome, error) {
	s := g.dec(st)
	if s.Winner < 0 {
		return nil, nil
	}
	o := &games.Outcome{Rank: []int{2, 2}, Score: []float64{0, 0}}
	o.Rank[s.Winner], o.Score[s.Winner] = 1, 1
	return o, nil
}
func (g takeAway) DefaultMove(st games.State, seat games.Seat) (games.Move, error) {
	return games.Move{Type: "take", Args: map[string]any{"n": 1}}, nil
}

// pennies: both seats pick heads or tails at once (toMove lists both until
// each has chosen); seat 0 wins on a match. Exercises simultaneous moves.
type pennies struct{}

type penState struct{ Pick [2]int } // -1 = not yet

func (pennies) dec(st games.State) penState {
	var s penState
	_ = json.Unmarshal(st, &s)
	return s
}
func (pennies) Meta() games.Meta {
	return games.Meta{ID: "pennies", Name: "Pennies", MinSeats: 2, MaxSeats: 2}
}
func (pennies) Setup(games.Config, int64) (games.State, error) {
	return json.Marshal(penState{Pick: [2]int{-1, -1}})
}
func (g pennies) ToMove(st games.State) ([]games.Seat, error) {
	s := g.dec(st)
	out := []games.Seat{}
	for i, p := range s.Pick {
		if p < 0 {
			out = append(out, i)
		}
	}
	return out, nil
}
func (g pennies) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	if seat < 0 || g.dec(st).Pick[seat] >= 0 {
		return nil, nil
	}
	return []games.MoveSpec{{Type: "heads", Label: "Heads"}, {Type: "tails", Label: "Tails"}}, nil
}
func (g pennies) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	s := g.dec(st)
	if s.Pick[seat] >= 0 {
		return nil, nil, games.Illegal("already picked")
	}
	s.Pick[seat] = map[string]int{"heads": 0, "tails": 1}[m.Type]
	b, _ := json.Marshal(s)
	return b, nil, nil
}
func (pennies) View(games.State, games.Seat) (games.View, error) {
	return games.View{Kind: "board"}, nil
}
func (g pennies) Outcome(st games.State) (*games.Outcome, error) {
	s := g.dec(st)
	if s.Pick[0] < 0 || s.Pick[1] < 0 {
		return nil, nil
	}
	if s.Pick[0] == s.Pick[1] {
		return &games.Outcome{Rank: []int{1, 2}, Score: []float64{1, 0}}, nil
	}
	return &games.Outcome{Rank: []int{2, 1}, Score: []float64{0, 1}}, nil
}
func (pennies) DefaultMove(games.State, games.Seat) (games.Move, error) {
	return games.Move{Type: "heads"}, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func isLegal(t *testing.T, g games.Game, st games.State, seat games.Seat, m games.Move) {
	t.Helper()
	if _, _, err := g.Apply(st, seat, m); err != nil {
		t.Fatalf("brain returned %v: %v", m, err)
	}
}

var shark = games.Persona{ID: "shark", Skill: 2}

// ── tests ───────────────────────────────────────────────────────────────────

func TestExpandAndRandomMoves(t *testing.T) {
	specs := []games.MoveSpec{
		{Type: "fold", Label: "Fold"},
		{Type: "raise", Label: "Raise", Args: map[string]any{"street": "flop"}, Range: &games.Range{Arg: "to", Min: 40, Max: 1000, Step: 20}},
	}
	ms := ai.ExpandMoves(specs, 5)
	if len(ms) != 6 || ms[1].Args["to"] != 40 || ms[5].Args["to"] != 1000 || ms[1].Args["street"] != "flop" {
		t.Fatalf("expand = %v", ms)
	}
	for _, m := range ms[1:] {
		if v := m.Args["to"].(int); (v-40)%20 != 0 {
			t.Fatalf("off-step sample %d", v)
		}
	}
	// A small range is enumerated completely.
	if ms := ai.ExpandMoves([]games.MoveSpec{{Type: "t", Range: &games.Range{Arg: "n", Min: 1, Max: 3}}}, 5); len(ms) != 3 {
		t.Fatalf("small range = %v", ms)
	}
	rng := rand.New(rand.NewSource(1))
	seen := map[int]bool{}
	for range 2000 {
		m := ai.RandomMove(specs[1:], rng)
		v := m.Args["to"].(int)
		if v < 40 || v > 1000 || (v-40)%20 != 0 {
			t.Fatalf("random value %d", v)
		}
		seen[v] = true
	}
	if len(seen) != 49 {
		t.Fatalf("random range covered %d of 49 values", len(seen))
	}
	if specs[1].Args["to"] != nil {
		t.Fatal("ExpandMoves mutated the spec's args")
	}
}

func TestMoveKeyAndOutcomeValues(t *testing.T) {
	a := games.Move{Type: "drop", Args: map[string]any{"col": 3, "x": "y"}}
	b := games.Move{Type: "drop", Args: map[string]any{"x": "y", "col": 3.0}}
	if ai.MoveKey(a) != ai.MoveKey(b) || ai.MoveKey(a) == ai.MoveKey(games.Move{Type: "drop", Args: map[string]any{"col": 4}}) {
		t.Fatal("MoveKey is not canonical")
	}
	vs := ai.OutcomeValues(&games.Outcome{Rank: []int{1, 2, 2, 4}})
	want := []float64{1, 0.5, 0.5, 0}
	for i := range want {
		if vs[i] != want[i] {
			t.Fatalf("values = %v, want %v", vs, want)
		}
	}
	if vs := ai.OutcomeValues(&games.Outcome{Rank: []int{1, 1}}); vs[0] != 0.5 || vs[1] != 0.5 {
		t.Fatalf("draw = %v", vs)
	}
}

func TestMCTSFindsWinningRangedMove(t *testing.T) {
	g := takeAway{}
	st, _ := g.Setup(games.Config{Seats: 2}, 1)
	b := ai.New(ai.Options{Iterations: [3]int{50, 300, 3000}})
	// From 10 stones the only winning move is to take 2 (leaving 8).
	for i := range 5 {
		m, err := b.Choose(context.Background(), g, st, 0, shark, rand.New(rand.NewSource(int64(i))))
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(m.Args["n"]) != "2" {
			t.Fatalf("shark took %v from 10, want 2", m.Args["n"])
		}
	}
}

func TestPersonaSkill(t *testing.T) {
	// Casual players sample moves, sharks always play the best one.
	g := takeAway{}
	st, _ := g.Setup(games.Config{Seats: 2}, 1)
	b := ai.New(ai.Options{Iterations: [3]int{400, 400, 400}})
	casualMoves := map[string]bool{}
	for i := range 30 {
		m, _ := b.Choose(context.Background(), g, st, 0, games.Persona{Skill: 0}, rand.New(rand.NewSource(int64(i))))
		casualMoves[fmt.Sprint(m.Args["n"])] = true
	}
	if len(casualMoves) < 2 {
		t.Fatalf("casual always played %v", casualMoves)
	}
}

func TestBrainAlwaysReturnsLegalMoves(t *testing.T) {
	ctx := context.Background()
	b := ai.New(ai.Options{Iterations: [3]int{60, 60, 60}})
	cases := []struct {
		name string
		g    games.Game
	}{
		{"perfect info", takeAway{}},
		{"hidden info without determinizer", takeAway{hidden: true}},
		{"module error mid-search", takeAway{brokenAt: 6}},
		{"simultaneous", pennies{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for seed := range int64(10) {
				rng := rand.New(rand.NewSource(seed))
				st, _ := c.g.Setup(games.Config{Seats: 2}, seed)
				for range 20 {
					tm, _ := c.g.ToMove(st)
					if len(tm) == 0 {
						break
					}
					seat := tm[rng.Intn(len(tm))]
					m, err := b.Choose(ctx, c.g, st, seat, games.Persona{Skill: int(seed % 3)}, rng)
					if err != nil {
						t.Fatal(err)
					}
					isLegal(t, c.g, st, seat, m)
					next, _, err := c.g.Apply(st, seat, m)
					if err != nil {
						break // the deliberately broken game
					}
					st = next
				}
			}
		})
	}
}

func TestBrainRespectsContext(t *testing.T) {
	e, _ := script.ExampleByID("reversi")
	g, err := script.Load(e.ID, e.Source, script.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := g.Setup(games.Config{Seats: 2}, 1)
	b := ai.New(ai.Options{Iterations: [3]int{1e6, 1e6, 1e6}})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	m, err := b.Choose(ctx, g, st, 0, shark, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Choose took %v with a 150ms deadline", d)
	}
	isLegal(t, g, st, 0, m)

	// An already cancelled context still yields a legal move, at once.
	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	m, err = b.Choose(cctx, g, st, 0, shark, nil)
	if err != nil {
		t.Fatal(err)
	}
	isLegal(t, g, st, 0, m)
	if _, err := b.Choose(ctx, g, st, 1, shark, nil); err == nil {
		t.Fatal("no error for a seat with no legal moves")
	}
}

func TestSearchIsDeterministic(t *testing.T) {
	e, _ := script.ExampleByID("lantern_market")
	g, err := script.Load(e.ID, e.Source, script.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := g.Setup(games.Config{Seats: 3}, 4)
	tm, _ := g.ToMove(st)
	b := ai.New(ai.Options{Iterations: [3]int{80, 80, 80}})
	var first games.Move
	for i := range 3 {
		m, err := b.Choose(context.Background(), g, st, tm[0], shark, rand.New(rand.NewSource(9)))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = m
		} else if !ai.SameMove(m, first) {
			t.Fatalf("same seed chose %v then %v", first, m)
		}
	}
}

// TestMCTSBeatsRandomAtConnectFour is the end-to-end strength check: a modest
// search budget must win more than 80% of 50 games against a random player,
// alternating who moves first.
func TestMCTSBeatsRandomAtConnectFour(t *testing.T) {
	e, _ := script.ExampleByID("connect_four")
	g, err := script.Load(e.ID, e.Source, script.Options{})
	if err != nil {
		t.Fatal(err)
	}
	iters := 100
	if raceEnabled {
		iters = 40 // the race detector slows the interpreter ~10x
	}
	brain := ai.New(ai.Options{Iterations: [3]int{iters, iters, iters}})
	const n = 50
	wins := make([]float64, n)
	sem := make(chan struct{}, 8)
	done := make(chan struct{})
	for i := range n {
		go func() {
			sem <- struct{}{}
			defer func() { <-sem; done <- struct{}{} }()
			rng := rand.New(rand.NewSource(int64(i)))
			aiSeat := i % 2
			st, _ := g.Setup(games.Config{Seats: 2}, int64(i))
			for {
				tm, err := g.ToMove(st)
				if err != nil {
					t.Error(err)
					return
				}
				if len(tm) == 0 {
					break
				}
				var m games.Move
				if tm[0] == aiSeat {
					m, err = brain.Choose(context.Background(), g, st, aiSeat, shark, rng)
				} else {
					m, err = ai.Random{}.Choose(context.Background(), g, st, tm[0], shark, rng)
				}
				if err != nil {
					t.Error(err)
					return
				}
				if st, _, err = g.Apply(st, tm[0], m); err != nil {
					t.Error(err)
					return
				}
			}
			o, _ := g.Outcome(st)
			wins[i] = ai.OutcomeValues(o)[aiSeat]
		}()
	}
	for range n {
		<-done
	}
	var total float64
	for _, w := range wins {
		total += w
	}
	rate := total / n
	t.Logf("MCTS (%d iterations) vs random at connect four: %.0f%%", iters, 100*rate)
	if rate <= 0.8 {
		t.Fatalf("MCTS won %.0f%% against random, want > 80%%", 100*rate)
	}
}

// peekGame: seat 0 guesses a hidden number 0..9 and wins on a match. The
// game is hidden-information without a determinizer, and its heuristic and
// rollouts read the true (hidden) state, so any brain that evaluated moves
// with them would always guess right.
type peekGame struct{ heuristicCalls, applyCalls *int }

type peekState struct{ Secret, Guess int }

func (peekGame) Meta() games.Meta {
	return games.Meta{ID: "peek", Name: "Peek", MinSeats: 2, MaxSeats: 2, HiddenInfo: true}
}
func (peekGame) Setup(cfg games.Config, seed int64) (games.State, error) {
	return json.Marshal(peekState{Secret: int(seed % 10), Guess: -1})
}
func (peekGame) dec(st games.State) peekState { var s peekState; _ = json.Unmarshal(st, &s); return s }
func (g peekGame) ToMove(st games.State) ([]games.Seat, error) {
	if g.dec(st).Guess >= 0 {
		return []games.Seat{}, nil
	}
	return []games.Seat{0}, nil
}
func (g peekGame) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	if seat != 0 || g.dec(st).Guess >= 0 {
		return nil, nil
	}
	return []games.MoveSpec{{Type: "guess", Label: "Guess", Range: &games.Range{Arg: "n", Min: 0, Max: 9}}}, nil
}
func (g peekGame) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	*g.applyCalls++
	s := g.dec(st)
	n, _ := m.Args["n"].(int)
	if f, ok := m.Args["n"].(float64); ok {
		n = int(f)
	}
	s.Guess = n
	b, _ := json.Marshal(s)
	return b, nil, nil
}
func (peekGame) View(games.State, games.Seat) (games.View, error) {
	return games.View{Kind: "board", Data: map[string]any{}}, nil
}
func (g peekGame) Outcome(st games.State) (*games.Outcome, error) {
	s := g.dec(st)
	if s.Guess < 0 {
		return nil, nil
	}
	if s.Guess == s.Secret {
		return &games.Outcome{Rank: []int{1, 2}, Score: []float64{1, 0}}, nil
	}
	return &games.Outcome{Rank: []int{2, 1}, Score: []float64{0, 1}}, nil
}
func (peekGame) DefaultMove(games.State, games.Seat) (games.Move, error) {
	return games.Move{Type: "guess", Args: map[string]any{"n": 0}}, nil
}
func (peekGame) HasHeuristic() bool { return true }
func (g peekGame) Heuristic(st games.State, seat games.Seat) (float64, error) {
	*g.heuristicCalls++
	s := g.dec(st)
	if s.Guess == s.Secret {
		return 1, nil
	}
	return -1, nil
}

// TestHiddenInfoWithoutDeterminizerDoesNotPeek: without a determinizer the
// brain must not evaluate moves on the true state (heuristic or playouts).
func TestHiddenInfoWithoutDeterminizerDoesNotPeek(t *testing.T) {
	var hCalls, aCalls int
	g := peekGame{&hCalls, &aCalls}
	b := ai.New(ai.Options{Iterations: [3]int{200, 200, 200}, RangeSamples: 10})
	right, simulated := 0, 0
	const n = 100
	for seed := range int64(n) {
		st, _ := g.Setup(games.Config{Seats: 2}, seed)
		before := aCalls
		m, err := b.Choose(context.Background(), g, st, 0, shark, rand.New(rand.NewSource(seed)))
		if err != nil {
			t.Fatal(err)
		}
		simulated += aCalls - before
		isLegal(t, g, st, 0, m)
		if next, _, _ := g.Apply(st, 0, m); g.dec(next).Guess == g.dec(st).Secret {
			right++
		}
	}
	if hCalls != 0 {
		t.Errorf("heuristic called %d times on the true hidden state", hCalls)
	}
	if simulated != 0 {
		t.Errorf("the brain simulated %d moves on the true hidden state", simulated)
	}
	if right > n/3 {
		t.Errorf("guessed the hidden number %d/%d times: the brain is peeking", right, n)
	}
}

// TestBrainStopsMidCallAtDeadline: a decision abandoned at its deadline must
// not keep running the call (or whole playout) it was in. The module below
// hangs inside every simulated playout; its call budget is 10s, so only the
// context binding (ai.ContextBinder) can stop it in time.
func TestBrainStopsMidCallAtDeadline(t *testing.T) {
	src := `const game = {
	  meta: { name: "Hang", summary: "Hangs in search.", minSeats: 2, maxSeats: 2, hiddenInfo: false, turnSeconds: 10 },
	  setup(ctx) { return { n: 0, turn: 0 }; },
	  toMove(s) { return s.n >= 10 ? [] : [s.turn]; },
	  legal(s, seat) { return s.n >= 10 || seat !== s.turn ? [] : [{ type: "a", label: "A" }, { type: "b", label: "B" }]; },
	  apply(s, seat, m) { if (s.n >= 2) { while (true) {} } return { n: s.n + 1, turn: 1 - seat }; },
	  view(s) { return { message: "x" }; },
	  outcome(s) { return s.n >= 10 ? { rank: [1, 1], score: [0, 0], summary: "Draw" } : null; },
	};`
	g, err := script.Load("hang", src, script.Options{CallBudget: 10 * time.Second, RolloutBudget: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := g.Setup(games.Config{Seats: 2}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	m, err := ai.New(ai.Options{}).Choose(ctx, g, st, 0, shark, rand.New(rand.NewSource(1)))
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Choose took %v with a 200ms deadline", d)
	}
	isLegal(t, g, st, 0, m)
}
