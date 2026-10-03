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

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/mail"
)

// Agent handles chat turns. Tables, Studio and Catalog are optional: a nil
// capability is reported to the player kindly ("the studio isn't open yet")
// rather than failing the turn, so the chat works from the first deploy and
// gains abilities as the services behind them are wired in.
type Agent struct {
	Store   *engine.Store
	Model   *engine.Model
	LLM     string // the reply model
	FastLLM string // routing and memory extraction
	Mailer  mail.Mailer

	Tables  Tables
	Studio  Studio
	Catalog Catalog
}

// ── Capabilities Aoi acts through ──────────────────────────────────────────

// ErrRejected marks a capability error whose message is safe to show the
// player because it is about their request, not about the system: "Connect
// Four seats exactly 2 players", "that game is not published". Wrap it:
// fmt.Errorf("%w: Connect Four seats exactly 2 players", agent.ErrRejected).
// Any other error is logged and the player hears a generic apology.
var ErrRejected = errors.New("request rejected")

// TableRequest is a table Aoi sets up for the player who asked. The player
// always takes a seat; AgentIDs fill the next seats in order; any seats beyond
// 1+len(AgentIDs) start open (the rooms service may fill them per the player's
// fill_empty_seats setting).
type TableRequest struct {
	GameID   string         // catalog id, e.g. "holdem"
	AgentIDs []string       // roster ids (see Roster), validated before the call
	Seats    int            // total seats including the player; always >= 1+len(AgentIDs)
	Options  map[string]any // game options as the player stated them; the game validates
	Lang     string         // en | zh, for the table's name and agent chatter
}

// Tables creates tables (implemented by the rooms service).
type Tables interface {
	CreateTable(ctx context.Context, userID string, req TableRequest) (tableID string, err error)
}

// Studio starts game builds (implemented by the studio). A build is a durable
// mission (an engine goal) that produces a game row; both ids come back so
// Aoi can attach the mission card at once. baseGameID is "" for a new game,
// or the game to revise.
type Studio interface {
	StartBuild(ctx context.Context, userID, convID, prompt, baseGameID, lang string) (goalID, gameID string, err error)
}

// GameInfo is one entry of the catalog as Aoi needs it.
type GameInfo struct {
	ID       string
	Name     string
	Summary  string
	MinSeats int
	MaxSeats int
	Mine     bool // the player owns it (so they may revise it)
}

// Catalog lists the games a user may play: the built-ins, their own (any
// status) and published community games. Rules returns a game's rules text
// (Markdown) if the user may see it.
type Catalog interface {
	Games(ctx context.Context, userID string) ([]GameInfo, error)
	Rules(ctx context.Context, userID, gameID string) (string, error)
}

// builtinGames is what Aoi knows without a catalog: the one game that ships
// with the platform.
var builtinGames = []GameInfo{{ID: "holdem", Name: "Texas Hold'em", Summary: "No-limit hold'em with play-money chips.", MinSeats: 2, MaxSeats: 9}}

// ── The AI roster ──────────────────────────────────────────────────────────

// RosterAgent is one AI player as the router and the settings need it. The
// full profiles (bios, avatars, play styles) live with the rooms service; this
// list must name the same ids (docs/00-architecture.md, "AI player roster").
type RosterAgent struct {
	ID, Name, NameZH, Style string
}

var Roster = []RosterAgent{
	{"aoi", "Aoi", "葵", "balanced, adaptive; the host"},
	{"ren", "Ren", "蓮", "tight-aggressive; calm strategist"},
	{"mika", "Mika", "美香", "loose-aggressive; loves to bluff"},
	{"bram", "Captain Bram", "布拉姆船长", "loose-passive; calls a lot, tells stories"},
	{"nova", "Nova", "诺瓦", "balanced, math-driven; cheerful robot"},
	{"lin", "Lin", "琳", "tight-passive; shy prodigy"},
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
			if lang == "zh" {
				return a.NameZH
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
