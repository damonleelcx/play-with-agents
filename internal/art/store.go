package art

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is what both a pool and a transaction offer.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Stored is a cover row without its bytes.
type Stored struct {
	GameID      string
	Version     int
	ContentType string
	Source      string
	Model       string
	Name        string // the game's name when the cover was made
	Prompt      string
	CreatedAt   time.Time
}

// Save stores (or replaces) a game's cover; the version goes up by one each
// time, so cover URLs carrying ?v= change with the picture.
func Save(ctx context.Context, q Querier, gameID, name string, r *Result) (int, error) {
	var v int
	err := q.QueryRow(ctx, `INSERT INTO game_covers (game_id, version, content_type, bytes, prompt, source, name, model)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (game_id) DO UPDATE SET version = game_covers.version + 1, content_type = EXCLUDED.content_type,
			bytes = EXCLUDED.bytes, prompt = EXCLUDED.prompt, source = EXCLUDED.source, name = EXCLUDED.name,
			model = EXCLUDED.model, created_at = now()
		RETURNING version`, gameID, r.ContentType, r.Bytes, r.Prompt, r.Source, name, r.Model).Scan(&v)
	return v, err
}

// Info returns a cover's row without the bytes, or nil when the game has
// none.
func Info(ctx context.Context, q Querier, gameID string) (*Stored, error) {
	var s Stored
	err := q.QueryRow(ctx, `SELECT game_id, version, content_type, source, model, name, prompt, created_at FROM game_covers WHERE game_id=$1`, gameID).
		Scan(&s.GameID, &s.Version, &s.ContentType, &s.Source, &s.Model, &s.Name, &s.Prompt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Load returns a cover's bytes, content type and version; ok is false when
// the game has none.
func Load(ctx context.Context, q Querier, gameID string) (b []byte, contentType string, version int, ok bool, err error) {
	err = q.QueryRow(ctx, `SELECT bytes, content_type, version FROM game_covers WHERE game_id=$1`, gameID).Scan(&b, &contentType, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", 0, false, nil
	}
	if err != nil {
		return nil, "", 0, false, err
	}
	return b, contentType, version, true, nil
}

// StaticCovers are the covers shipped with the web app
// (web/public/play/covers/{id}.webp) for the built-in game and the seeded
// community examples. A stored cover takes precedence.
var StaticCovers = map[string]bool{"holdem": true, "tictactoe": true, "connect-four": true, "reversi": true, "lantern-market": true}

// StaticURL is the shipped cover of id, or "".
func StaticURL(id string) string {
	if StaticCovers[id] {
		return "/play/covers/" + id + ".webp"
	}
	return ""
}
