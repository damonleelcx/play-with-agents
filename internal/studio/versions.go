package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is what both a pool and a transaction offer.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// gameSpec is the Designer's structural decision, stored on games.spec, so
// the Engineer's meta can be checked against it.
type gameSpec struct {
	MinSeats   int  `json:"min_seats"`
	MaxSeats   int  `json:"max_seats"`
	HiddenInfo bool `json:"hidden_info"`
}

type gameRow struct {
	ID, Owner, Name, Summary, RulesMD, Status, Visibility string
	Current                                               int
	Spec                                                  gameSpec
}

var errNoGame = errors.New("this build has no game")

// goalGame is the game a build goal produces.
func goalGame(ctx context.Context, q querier, goalID string) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT coalesce(game_id,'') FROM goals WHERE id=$1`, goalID).Scan(&id)
	if err == nil && id == "" {
		err = errNoGame
	}
	return id, err
}

func loadGame(ctx context.Context, q querier, id string) (*gameRow, error) {
	var g gameRow
	var spec []byte
	err := q.QueryRow(ctx, `SELECT id, coalesce(owner_id::text,''), name, summary, rules_md, status, visibility, current_version, spec
		FROM games WHERE id=$1`, id).Scan(&g.ID, &g.Owner, &g.Name, &g.Summary, &g.RulesMD, &g.Status, &g.Visibility, &g.Current, &spec)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(spec, &g.Spec)
	return &g, nil
}

type version struct {
	Version int
	Source  string
	Report  map[string]any
}

// Version filters. Each names the newest version of a game that this goal
// produced and reached a stage.
const (
	anyOfGoal  = `report->>'goal_id' = $2`
	checkedOK  = `report->>'goal_id' = $2 AND (report->'check'->>'ok')::boolean`
	playtested = `report->>'goal_id' = $2 AND (report->'verdict'->>'pass')::boolean`
	reviewedOK = `report->>'goal_id' = $2 AND (report->'verdict'->>'pass')::boolean AND report->'review'->>'verdict' = 'pass'`
)

// latestVersion returns the newest version of gameID matching filter (with
// goalID as $2), or nil.
func latestVersion(ctx context.Context, q querier, gameID, goalID, filter string) (*version, error) {
	var v version
	var raw []byte
	err := q.QueryRow(ctx, `SELECT version, source, report FROM game_versions WHERE game_id=$1 AND `+filter+`
		ORDER BY version DESC LIMIT 1`, gameID, goalID).Scan(&v.Version, &v.Source, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &v.Report)
	return &v, nil
}

// baseVersion is what a revision starts from: the published version, or the
// newest one when nothing is published yet.
func baseVersion(ctx context.Context, q querier, g *gameRow) (*version, error) {
	var v version
	var raw []byte
	err := q.QueryRow(ctx, `SELECT version, source, report FROM game_versions WHERE game_id=$1
		ORDER BY (version = $2) DESC, version DESC LIMIT 1`, g.ID, g.Current).Scan(&v.Version, &v.Source, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &v.Report)
	return &v, nil
}

// mergeReport merges keys into a version's report.
func mergeReport(ctx context.Context, q querier, gameID string, ver int, patch map[string]any) error {
	raw, _ := json.Marshal(patch)
	tag, err := q.Exec(ctx, `UPDATE game_versions SET report = report || $3::jsonb WHERE game_id=$1 AND version=$2`, gameID, ver, raw)
	if err == nil && tag.RowsAffected() == 0 {
		err = fmt.Errorf("game %s has no version %d", gameID, ver)
	}
	return err
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}
