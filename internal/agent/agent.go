// Package agent is Aoi's conversational front: it routes each message to an
// intent, acts on it through the platform's capabilities (tables, the game
// studio, the catalog, durable missions, settings), and answers in her voice.
// It keeps no state of its own between turns — every turn is rebuilt from the
// database.
package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/mail"
)

// Agent handles chat turns. Every capability is optional: a nil capability
// is reported to the player kindly ("the studio isn't open yet") rather than
// failing the turn, so the chat works from the first deploy and gains
// abilities as the services behind them are wired in.
type Agent struct {
	Store   *engine.Store
	Model   *engine.Model
	LLM     string // the reply model
	FastLLM string // routing and memory extraction
	Mailer  mail.Mailer
	// PublicOrigin is the site's origin ("https://play.heros-agent.space")
	// for the links Aoi hands out (invite links, unlisted game links). Empty
	// gives site-relative links.
	PublicOrigin string

	Tables  Tables
	Studio  Studio
	Catalog Catalog
	Coach   Coach
	Stats   Stats
}

// ── Capabilities Aoi acts through ──────────────────────────────────────────
//
// docs/01-intents-tools-skills.md lists which intent calls which capability.
// Every call is made as the player (userID): the services behind them check
// ownership and seats exactly as they do for the app's own buttons.

// ErrRejected marks a capability error whose message is safe to show the
// player because it is about their request, not about the system: "Connect
// Four seats exactly 2 players", "that game is not published". Wrap it:
// fmt.Errorf("%w: Connect Four seats exactly 2 players", agent.ErrRejected).
// Any other error is logged and the player hears a generic apology.
var ErrRejected = errors.New("request rejected")

// TableRequest is a table Aoi sets up for the player who asked. The player
// always takes a seat; AgentIDs fill the next seats in order; any seats beyond
// 1+len(AgentIDs) start open, for friends (the rooms service may fill them
// with agents at the start per the player's fill_empty_seats setting).
type TableRequest struct {
	GameID   string         // catalog id, e.g. "holdem"
	AgentIDs []string       // roster ids (see Roster), validated before the call
	Seats    int            // total seats including the player; always >= 1+len(AgentIDs)
	Options  map[string]any // game options as the player stated them; the game validates
	Lang     string         // en | zh | ko | ja, for the table's name and agent chatter
	Settings TableSettings  // per-table overrides of the player's table settings
}

// TableSettings override the host's preferences for one table. Empty means
// "as in my settings".
type TableSettings struct {
	Difficulty  string // casual | regular | shark
	AgentSpeed  string // fast | natural | slow
	TableTalk   string // all | quiet | off
	TurnSeconds *int   // 0 (no clock), 15, 30, 60…
}

// TableInfo is a table as Aoi talks about it.
type TableInfo struct {
	ID, Name, Code   string
	GameID, GameName string
	Status           string // lobby | playing | finished | abandoned
	Seats, OpenSeats int
	Players          []string // names of who sits there (people and agents)
	IsHost, Seated   bool     // the player hosts it / has a seat at it
	Away             bool     // the player is marked away at it
	Paused           bool
	PausedReason     string // idle | fault
	RematchID        string
	UpdatedAt        time.Time
}

// Tables is the table service as Aoi uses it (implemented over the rooms
// service). Every method acts as userID with the same rights the table API
// gives them: only the host starts a rematch, a code joins only a live table.
type Tables interface {
	CreateTable(ctx context.Context, userID string, req TableRequest) (TableInfo, error)
	// MyTables lists the tables the player hosts, sits at or watches,
	// most recently active first; finished ones only when asked.
	MyTables(ctx context.Context, userID string, withFinished bool) ([]TableInfo, error)
	JoinTable(ctx context.Context, userID, code string) (TableInfo, error)
	LeaveTable(ctx context.Context, userID, tableID string) error
	// BackToTable is "I'm back": clears the away mark and resumes a table
	// paused because nobody was attending.
	BackToTable(ctx context.Context, userID, tableID string) (TableInfo, error)
	Rematch(ctx context.Context, userID, tableID string) (TableInfo, error)
}

// CoachTip is the hold'em coach's analysis of the player's own spot.
type CoachTip struct {
	HandName   string
	Equity     float64 // 0..1 against the live opponents
	PotOdds    float64 // 0..1, the equity a call needs; 0 when nothing to call
	Opponents  int
	ToCall     int
	Suggestion string
}

// CoachAdvice is everything Aoi may use to coach the player at a table. It
// comes from the player's OWN seat only: their seat's view, their legal moves
// and (hold'em) the coach's numbers, which are computed from that view.
type CoachAdvice struct {
	TableID, GameID, GameName string
	YourTurn                  bool
	Legal                     []string
	YourView                  string    // JSON of the player's own seat view
	Holdem                    *CoachTip // hold'em only
}

// Coach analyses the player's own seat at a playing table.
type Coach interface {
	Advice(ctx context.Context, userID, tableID string) (CoachAdvice, error)
}

// GameResult is one finished game the player sat in.
type GameResult struct {
	GameName   string
	Place      int // 1 = won; 0 unknown
	Players    int
	Chips      int // hold'em: play-money chips won (negative: lost)
	HasChips   bool
	FinishedAt time.Time
}

// PlayerStats is the player's record.
type PlayerStats struct {
	Played, Wins, ChipsWon, InPlay int
	ByGame                         map[string]int
	Recent                         []GameResult
}

// Stats reads the player's own results.
type Stats interface {
	PlayerStats(ctx context.Context, userID string) (PlayerStats, error)
}

// Studio starts game builds (implemented by the studio). A build is a durable
// mission (an engine goal) that produces a game row; both ids come back so
// Aoi can attach the mission card at once. baseGameID is "" for a new game,
// or the game to revise. Pausing, resuming, cancelling and approving go
// through the engine (Store.GoalControl, Store.DecideAsOwner), the same path
// as the mission page's buttons.
type Studio interface {
	StartBuild(ctx context.Context, userID, convID, prompt, baseGameID, lang string) (goalID, gameID string, err error)
}

// GameInfo is one entry of the catalog as Aoi needs it.
type GameInfo struct {
	ID         string
	Name       string
	Summary    string
	MinSeats   int
	MaxSeats   int
	Mine       bool   // the player owns it (so they may revise it)
	Kind       string // builtin | script
	Status     string // building | draft | published
	Visibility string // private | unlisted | public
	Plays      int
	Owner      string // owner's display name, for community games
}

// Catalog lists the games a user may play: the built-ins, their own (any
// status) and published community games. Rules returns a game's rules text
// (Markdown) if the user may see it. The owner-only changes (visibility,
// rename, delete a draft) are refused for anyone else's game.
type Catalog interface {
	Games(ctx context.Context, userID string) ([]GameInfo, error)
	Rules(ctx context.Context, userID, gameID string) (string, error)
	Game(ctx context.Context, userID, gameID string) (GameInfo, error)
	SetVisibility(ctx context.Context, userID, gameID, visibility string) error
	Rename(ctx context.Context, userID, gameID, name string) error
	DeleteDraft(ctx context.Context, userID, gameID string) error
}

// builtinGames is what Aoi knows without a catalog: the one game that ships
// with the platform.
var builtinGames = []GameInfo{{ID: "holdem", Name: "Texas Hold'em", Summary: "No-limit hold'em with play-money chips.", MinSeats: 2, MaxSeats: 9, Kind: "builtin", Status: "published", Visibility: "public"}}

// ── The AI roster ──────────────────────────────────────────────────────────

// RosterAgent is one AI player as the router and the settings need it. The
// full profiles (bios, avatars, play styles) live with the rooms service; this
// list must name the same ids (docs/00-architecture.md, "AI player roster").
type RosterAgent struct {
	ID, Name, NameZH, NameKO, NameJA, Style string
}

var Roster = []RosterAgent{
	{"aoi", "Aoi", "葵", "아오이", "葵", "balanced, adaptive; the host"},
	{"ren", "Ren", "蓮", "렌", "レン", "tight-aggressive; calm strategist"},
	{"mika", "Mika", "美香", "미카", "ミカ", "loose-aggressive; loves to bluff"},
	{"bram", "Captain Bram", "布拉姆船长", "브램 선장", "ブラム船長", "loose-passive; calls a lot, tells stories"},
	{"nova", "Nova", "诺娃", "노바", "ノヴァ", "balanced, math-driven; cheerful robot"},
	{"lin", "Lin", "琳", "린", "リン", "tight-passive; shy prodigy"},
}

// IsAgentID reports whether id names a roster agent.
func IsAgentID(id string) bool {
	for _, a := range Roster {
		if a.ID == id {
			return true
		}
	}
	return false
}

func agentName(id, lang string) string {
	for _, a := range Roster {
		if a.ID == id {
			switch lang {
			case "zh":
				return a.NameZH
			case "ko":
				return a.NameKO
			case "ja":
				return a.NameJA
			}
			return a.Name
		}
	}
	return id
}

// cleanAgents keeps roster ids only, lower-cased, de-duplicated, in order.
func cleanAgents(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if IsAgentID(id) && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
