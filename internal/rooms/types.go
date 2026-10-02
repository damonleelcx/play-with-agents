package rooms

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// ── Errors (the HTTP layer maps these to status codes) ──────────────────────

var (
	ErrNotFound  = errors.New("not found")                           // 404
	ErrForbidden = errors.New("forbidden")                           // 403
	ErrConflict  = errors.New("conflict")                            // 409
	ErrRateLimit = errors.New("slow down")                           // 429
	ErrNotTurn   = fmt.Errorf("%w: not your turn", games.ErrIllegal) // 422
	// errStale is internal: the version-guarded UPDATE matched no row.
	errStale = errors.New("table version moved on")
	// ErrLeaseLost: a job's lease was taken over; the worker must not write.
	ErrLeaseLost = errors.New("lease lost: another worker owns this job now")
)

// InputError is a client mistake (400).
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func badInput(format string, a ...any) error { return &InputError{fmt.Sprintf(format, a...)} }

// StaleError is returned by Move when the client's version is not current
// (409). It carries the current view so the client can redraw without a
// second request.
type StaleError struct{ Table *TableView }

func (e *StaleError) Error() string { return "the table has moved on; refresh and try again" }

// ── Requests ────────────────────────────────────────────────────────────────

type SeatSpec struct {
	Kind    string `json:"kind"` // me | agent | open (create); agent | open (set seat)
	AgentID string `json:"agent_id,omitempty"`
}

type CreateRequest struct {
	GameID      string         `json:"game_id"`
	Name        string         `json:"name,omitempty"`
	Options     map[string]any `json:"options,omitempty"`
	TurnSeconds *int           `json:"turn_seconds,omitempty"`
	Seats       []SeatSpec     `json:"seats"`
}

type MoveRequest struct {
	ClientMoveID string     `json:"client_move_id"`
	Version      int64      `json:"version"`
	Move         games.Move `json:"move"`
}

// ── Views (docs/00-architecture.md, "Tables") ───────────────────────────────

type GameRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type SeatView struct {
	Seat    int    `json:"seat"`
	Kind    string `json:"kind"` // human | agent | open
	Name    string `json:"name"`
	Avatar  string `json:"avatar"`
	AgentID string `json:"agent_id,omitempty"`
	IsMe    bool   `json:"is_me"`
	// Away: the person missed their clock repeatedly; their turns now
	// resolve quickly with the default move until they act or come back.
	Away bool `json:"away,omitempty"`
}

type LogEntry struct {
	Seq  int       `json:"seq"`
	Type string    `json:"type"`
	Seat int       `json:"seat"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type ChatLine struct {
	ID     int64     `json:"id"`
	Seat   int       `json:"seat"`
	Name   string    `json:"name"`
	Avatar string    `json:"avatar"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
	Agent  bool      `json:"agent"`
}

type TableView struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Code        string           `json:"code"`
	Game        GameRef          `json:"game"`
	Status      string           `json:"status"`
	HostID      string           `json:"host_id"`
	IsHost      bool             `json:"is_host"`
	MySeat      int              `json:"my_seat"`
	Version     int64            `json:"version"`
	Seats       []SeatView       `json:"seats"`
	ToMove      []int            `json:"to_move"`
	Deadline    *time.Time       `json:"deadline"`
	TurnSeconds int              `json:"turn_seconds"`
	Legal       []games.MoveSpec `json:"legal"`
	View        *games.View      `json:"view"`
	Log         []LogEntry       `json:"log"`
	Chat        []ChatLine       `json:"chat"`
	Outcome     *games.Outcome   `json:"outcome"`
	// RematchID is set once the host started a rematch, so everyone at the
	// old table can follow. An additive field to the contract.
	RematchID string `json:"rematch_id,omitempty"`
	// Paused: nobody is attending the table (everyone away, or no activity
	// for a while). Status stays "playing"; nothing happens until a person
	// moves or posts /back.
	Paused bool `json:"paused"`
}

type TableSummary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Code       string    `json:"code"`
	GameName   string    `json:"game_name"`
	Status     string    `json:"status"`
	SeatsTaken int       `json:"seats_taken"`
	SeatsTotal int       `json:"seats_total"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ── Settings ────────────────────────────────────────────────────────────────

// Settings is the snapshot of the host's preferences a table is created with.
type Settings struct {
	TurnSeconds int      `json:"turn_seconds"`
	AgentSpeed  string   `json:"agent_speed"`      // fast | natural | slow
	TableTalk   string   `json:"table_talk"`       // all | quiet | off
	Difficulty  string   `json:"agent_difficulty"` // casual | regular | shark
	Language    string   `json:"language"`         // en | zh
	FillEmpty   bool     `json:"fill_empty_seats"`
	Favorites   []string `json:"favorite_agents,omitempty"`
}

// ── Table talk ──────────────────────────────────────────────────────────────

// ChatRequest is everything a Chatter may use to write one line. It is built
// from the spectator view only, so an agent can never leak hidden cards.
type ChatRequest struct {
	HostID    string // whose budget pays for the call
	TableID   string
	AgentID   string
	AgentName string
	Voice     string
	Persona   games.Persona
	Language  string // en | zh
	Trigger   string // agents.Trigger*
	About     string // the triggering event or message, names substituted
	Table     string // public summary of the table
	Recent    []ChatLine
	// Own is this agent's own last few lines at the table, oldest first, so
	// the Chatter can avoid repeating itself.
	Own []string
}

// Chatter writes one line of table talk in an agent's voice.
type Chatter interface {
	Line(ctx context.Context, req ChatRequest) (string, error)
}
