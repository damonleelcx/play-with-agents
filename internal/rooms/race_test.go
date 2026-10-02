package rooms

import (
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// race21 is the test game: 2–4 seats take turns adding 1, 2 or 3 to a
// running total; whoever reaches exactly 21 wins. Each seat also holds a
// secret number (hidden information): it appears in that seat's view and in
// private events only, so the tests can prove nothing leaks.
type race21 struct{}

type raceState struct {
	Total   int   `json:"total"`
	Turn    int   `json:"turn"`
	Seats   int   `json:"seats"`
	Secrets []int `json:"secrets"`
	Winner  int   `json:"winner"`
}

func (race21) Meta() games.Meta {
	return games.Meta{ID: "race21", Name: "Race to 21", Summary: "Add 1-3; hit 21 to win.", MinSeats: 2, MaxSeats: 4,
		HiddenInfo: true, TurnSeconds: 30, Options: map[string]any{"target": 21.0}, RulesMD: "# Race to 21"}
}

func decode(st games.State) (raceState, error) {
	var s raceState
	err := json.Unmarshal(st, &s)
	return s, err
}

func encode(s raceState) games.State { b, _ := json.Marshal(s); return b }

func (race21) Setup(cfg games.Config, seed int64) (games.State, error) {
	r := rand.New(rand.NewSource(seed))
	s := raceState{Seats: cfg.Seats, Winner: -1}
	for i := 0; i < cfg.Seats; i++ {
		s.Secrets = append(s.Secrets, 1000+r.Intn(9000))
	}
	return encode(s), nil
}

func (race21) ToMove(st games.State) ([]games.Seat, error) {
	s, err := decode(st)
	if err != nil || s.Winner >= 0 {
		return []games.Seat{}, err
	}
	return []games.Seat{s.Turn}, nil
}

func (race21) Legal(st games.State, seat games.Seat) ([]games.MoveSpec, error) {
	s, err := decode(st)
	if err != nil || s.Winner >= 0 || seat != s.Turn {
		return nil, err
	}
	var out []games.MoveSpec
	for k := 1; k <= 3 && s.Total+k <= 21; k++ {
		out = append(out, games.MoveSpec{Type: "add", Label: fmt.Sprintf("{s:%d} +%d", seat, k), Args: map[string]any{"n": k}})
	}
	return out, nil
}

func argInt(m games.Move, k string) (int, bool) {
	switch v := m.Args[k].(type) {
	case int:
		return v, true
	case float64:
		return int(v), v == float64(int(v))
	}
	return 0, false
}

func (race21) Apply(st games.State, seat games.Seat, m games.Move) (games.State, []games.Event, error) {
	s, err := decode(st)
	if err != nil {
		return nil, nil, err
	}
	if s.Winner >= 0 || seat != s.Turn {
		return nil, nil, games.Illegal("not your turn")
	}
	n, ok := argInt(m, "n")
	if m.Type != "add" || !ok || n < 1 || n > 3 || s.Total+n > 21 {
		return nil, nil, games.Illegal("add 1, 2 or 3 without passing 21")
	}
	s.Total += n
	evs := []games.Event{
		{Type: "add", Seat: seat, Text: fmt.Sprintf("{s:%d} adds %d (total %d)", seat, n, s.Total)},
		{Type: "peek", Seat: seat, Text: fmt.Sprintf("secret %d", s.Secrets[seat]), Only: []int{seat}},
	}
	if s.Total == 21 {
		s.Winner = seat
		evs = append(evs, games.Event{Type: "round_end", Seat: seat, Text: fmt.Sprintf("{s:%d} reaches 21", seat)})
	} else {
		s.Turn = (s.Turn + 1) % s.Seats
	}
	return encode(s), evs, nil
}

func (race21) View(st games.State, seat games.Seat) (games.View, error) {
	s, err := decode(st)
	if err != nil {
		return games.View{}, err
	}
	data := map[string]any{"total": s.Total, "message": fmt.Sprintf("{s:%d} to move", s.Turn)}
	if seat >= 0 && seat < len(s.Secrets) {
		data["my_secret"] = s.Secrets[seat]
	}
	return games.View{Kind: "board", Data: data, Status: fmt.Sprintf("{s:%d} to move", s.Turn)}, nil
}

func (race21) Outcome(st games.State) (*games.Outcome, error) {
	s, err := decode(st)
	if err != nil || s.Winner < 0 {
		return nil, err
	}
	o := &games.Outcome{Summary: fmt.Sprintf("{s:%d} wins", s.Winner)}
	for i := 0; i < s.Seats; i++ {
		r, sc := 2, 0.0
		if i == s.Winner {
			r, sc = 1, 1
		}
		o.Rank, o.Score = append(o.Rank, r), append(o.Score, sc)
	}
	return o, nil
}

func (race21) DefaultMove(st games.State, seat games.Seat) (games.Move, error) {
	return games.Move{Type: "add", Args: map[string]any{"n": 1}}, nil
}

func init() { games.Register(race21{}) }
