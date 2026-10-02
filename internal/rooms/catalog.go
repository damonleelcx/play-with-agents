package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// GameCard is a game as the lobby lists it.
type GameCard struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Summary    string `json:"summary"`
	MinSeats   int    `json:"min_seats"`
	MaxSeats   int    `json:"max_seats"`
	HiddenInfo bool   `json:"hidden_info"`
	Status     string `json:"status"`
	Visibility string `json:"visibility"`
	OwnerName  string `json:"owner_name,omitempty"`
	Version    int    `json:"version"`
	Plays      int    `json:"plays"`
	Cover      string `json:"cover,omitempty"`
}

type GameVersion struct {
	Version   int             `json:"version"`
	CreatedAt time.Time       `json:"created_at"`
	Report    json.RawMessage `json:"report"`
}

type GameDetail struct {
	GameCard
	RulesMD  string        `json:"rules_md"`
	Versions []GameVersion `json:"versions"`
	GoalID   string        `json:"goal_id,omitempty"`
}

type GameList struct {
	Builtin   []GameCard `json:"builtin"`
	Mine      []GameCard `json:"mine"`
	Community []GameCard `json:"community"`
}

func builtinCard(m games.Meta, plays int) GameCard {
	return GameCard{ID: m.ID, Kind: "builtin", Name: m.Name, Summary: m.Summary, MinSeats: m.MinSeats, MaxSeats: m.MaxSeats,
		HiddenInfo: m.HiddenInfo, Status: "published", Visibility: "public", Plays: plays}
}

const cardCols = `g.id, g.kind, g.name, g.summary, g.status, g.visibility, coalesce(u.name,''), g.current_version, g.plays,
	coalesce(v.meta, '{}'), g.rules_md, coalesce(g.goal_id::text,''), coalesce(g.owner_id::text,'')`

const cardFrom = ` FROM games g LEFT JOIN users u ON u.id = g.owner_id
	LEFT JOIN game_versions v ON v.game_id = g.id AND v.version = g.current_version`

type cardRow struct {
	GameCard
	rules, goal, owner string
}

func scanCard(row pgx.Row) (*cardRow, error) {
	var c cardRow
	var meta []byte
	if err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.Summary, &c.Status, &c.Visibility, &c.OwnerName, &c.Version, &c.Plays,
		&meta, &c.rules, &c.goal, &c.owner); err != nil {
		return nil, err
	}
	var m games.Meta
	_ = json.Unmarshal(meta, &m)
	c.MinSeats, c.MaxSeats, c.HiddenInfo = m.MinSeats, m.MaxSeats, m.HiddenInfo
	return &c, nil
}

// Games lists the built-ins, the user's own games and the public ones.
// Unlisted games are reachable only by id (GameDetail).
func (s *Service) Games(ctx context.Context, userID string) (*GameList, error) {
	out := &GameList{Builtin: []GameCard{}, Mine: []GameCard{}, Community: []GameCard{}}
	plays := map[string]int{}
	rows, err := s.Pool.Query(ctx, `SELECT id, plays FROM games WHERE kind='builtin'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int
		if rows.Scan(&id, &n) == nil {
			plays[id] = n
		}
	}
	rows.Close()
	for _, m := range games.Builtins() {
		out.Builtin = append(out.Builtin, builtinCard(m, plays[m.ID]))
	}
	rows, err = s.Pool.Query(ctx, `SELECT `+cardCols+cardFrom+`
		WHERE g.kind='script' AND (g.owner_id=$1 OR (g.status='published' AND g.visibility='public'))
		ORDER BY (g.owner_id=$1) DESC, g.plays DESC, g.updated_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		if c.owner == userID {
			out.Mine = append(out.Mine, c.GameCard)
		} else {
			out.Community = append(out.Community, c.GameCard)
		}
	}
	return out, rows.Err()
}

// GameDetail returns one game the user may see: built-ins, their own, and
// published public or unlisted ones.
func (s *Service) GameDetail(ctx context.Context, userID, id string) (*GameDetail, error) {
	if g, ok := games.Builtin(id); ok {
		m := g.Meta()
		var plays int
		_ = s.Pool.QueryRow(ctx, `SELECT plays FROM games WHERE id=$1`, id).Scan(&plays)
		return &GameDetail{GameCard: builtinCard(m, plays), RulesMD: m.RulesMD, Versions: []GameVersion{}}, nil
	}
	c, err := scanCard(s.Pool.QueryRow(ctx, `SELECT `+cardCols+cardFrom+` WHERE g.id=$1 AND g.kind='script'`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if c.owner != userID && (c.Status != "published" || c.Visibility == "private") {
		return nil, ErrNotFound // not 403: do not confirm a private game exists
	}
	d := &GameDetail{GameCard: c.GameCard, RulesMD: c.rules, Versions: []GameVersion{}}
	if c.owner == userID {
		d.GoalID = c.goal
	}
	rows, err := s.Pool.Query(ctx, `SELECT version, created_at, report FROM game_versions WHERE game_id=$1 ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v GameVersion
		if err := rows.Scan(&v.Version, &v.CreatedAt, &v.Report); err != nil {
			return nil, err
		}
		d.Versions = append(d.Versions, v)
	}
	return d, rows.Err()
}

type GamePatch struct {
	Name       *string `json:"name,omitempty"`
	Summary    *string `json:"summary,omitempty"`
	Visibility *string `json:"visibility,omitempty"`
}

// PatchGame lets the owner rename, re-describe or change who can see a game.
func (s *Service) PatchGame(ctx context.Context, userID, id string, p GamePatch) (*GameDetail, error) {
	if p.Name != nil && (clip(*p.Name, 60) == "" || len([]rune(*p.Name)) > 60) {
		return nil, badInput("name must be 1 to 60 characters")
	}
	if p.Summary != nil && len([]rune(*p.Summary)) > 280 {
		return nil, badInput("summary must be at most 280 characters")
	}
	if p.Visibility != nil && oneOf(*p.Visibility, "", "private", "unlisted", "public") == "" {
		return nil, badInput("visibility must be private, unlisted or public")
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE games SET name=coalesce($3, name), summary=coalesce($4, summary),
		visibility=coalesce($5, visibility), updated_at=now() WHERE id=$1 AND owner_id=$2 AND kind='script'`,
		id, userID, trimPtr(p.Name), trimPtr(p.Summary), p.Visibility)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GameDetail(ctx, userID, id)
}

// DeleteGame removes the owner's draft. Published games stay: other
// people's tables and records refer to them.
func (s *Service) DeleteGame(ctx context.Context, userID, id string) error {
	var status string
	err := s.Pool.QueryRow(ctx, `SELECT status FROM games WHERE id=$1 AND owner_id=$2 AND kind='script'`, id, userID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "draft" {
		return fmt.Errorf("%w: only drafts can be deleted", ErrConflict)
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM games WHERE id=$1 AND owner_id=$2 AND status='draft'`, id, userID)
	return err
}

func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := clip(*p, 280)
	return &v
}

// SourceLoader is a Loader that reads a version's module source from
// game_versions and hands it to compile (script.Load in production). The
// Service caches the result per (id, version), so each version compiles
// once per process.
func SourceLoader(pool *pgxpool.Pool, compile func(id, src string) (games.Game, error)) func(ctx context.Context, id string, version int) (games.Game, error) {
	return func(ctx context.Context, id string, version int) (games.Game, error) {
		var src string
		err := pool.QueryRow(ctx, `SELECT source FROM game_versions WHERE game_id=$1 AND version=$2`, id, version).Scan(&src)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: game %s v%d", ErrNotFound, id, version)
		}
		if err != nil {
			return nil, err
		}
		return compile(id, src)
	}
}
