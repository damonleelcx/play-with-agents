package studio

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/art"
	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

// The Artist: a deterministic tool task (illustrate_cover) that runs beside
// the playtest once the rules exist. The fast model writes the picture's
// subject from the rules document; the image model paints it; anything that
// goes wrong ends in the procedural cover, and the task still succeeds. No
// step depends on it, so it can never hold up publishing.

const (
	RoleArtist     = "artist"
	ToolIllustrate = "illustrate_cover"
)

// Artist is what illustrate_cover needs from the process. Unset (tests, or
// a process that never called ConfigureArt) means no model and no images:
// every cover is the procedural one.
type Artist struct {
	Images  *art.Client
	Model   *engine.Model
	FastLLM string
}

var (
	artistMu sync.RWMutex
	artist   Artist
)

// ConfigureArt installs the image client and the model that writes the
// art prompts. Call it once at startup, before the workers run.
func ConfigureArt(a Artist) {
	artistMu.Lock()
	artist = a
	artistMu.Unlock()
}

func currentArtist() Artist {
	artistMu.RLock()
	defer artistMu.RUnlock()
	return artist
}

func registerArtTool() {
	tools.Register(&tools.Tool{
		Name: ToolIllustrate,
		Description: "Make the cover of the game being built: a text-free 16:9 illustration of its theme, board and pieces " +
			"(or a procedural cover when image generation is unavailable). Never fails the build.",
		Input:  `{"type":"object","additionalProperties":false,"properties":{}}`,
		Output: `{"type":"object","required":["stored","source"]}`,
		Effect: tools.Write, Gate: tools.G0, Timeout: 9 * time.Minute,
		Run: runIllustrate,
	})
}

func runIllustrate(ctx context.Context, env *tools.Env, _ map[string]any) (map[string]any, error) {
	gameID, err := goalGame(ctx, env.Pool, env.GoalID)
	if err != nil {
		return nil, err
	}
	g, err := loadGame(ctx, env.Pool, gameID)
	if err != nil {
		return nil, err
	}
	prev, err := art.Info(ctx, env.Pool, gameID)
	if err != nil {
		return nil, &tools.Transient{Err: err}
	}
	a := currentArtist()
	// A revision keeps the cover unless the game was renamed (or the old one
	// is only the procedural stand-in and real art is now possible).
	if prev != nil && prev.Name == g.Name && (prev.Source == "generated" || !a.Images.Enabled()) {
		return map[string]any{"stored": false, "source": prev.Source, "version": prev.Version,
			"summary": "kept the existing cover: the game's name did not change"}, nil
	}
	store := &engine.Store{Pool: env.Pool}
	// One image per build, at most two attempts — across task retries too.
	allowed := art.MaxAttempts - goalImages(ctx, env.Pool, env.GoalID)
	subject, how := "", "template"
	if a.Images.Enabled() && allowed > 0 {
		subject, how = writeSubject(ctx, a, engine.CallMeta{Purpose: "cover_prompt", UserID: env.UserID, GoalID: env.GoalID, TaskID: env.TaskID}, g)
	}
	if subject == "" {
		subject = art.TemplateSubject(g.Name, g.Summary)
	}
	res, err := art.Make(ctx, a.Images, gameID, g.Name, art.Prompt(subject), allowed)
	if err != nil {
		return nil, &tools.Transient{Err: err}
	}
	if res.Attempts > 0 {
		_ = store.AddUsage(ctx, env.GoalID, engine.Usage{Images: res.Attempts, CostUSD: float64(res.Attempts) * art.CostPerImageUSD})
		store.Event(ctx, env.UserID, env.GoalID, env.TaskID, "usage.image", map[string]any{
			"images": res.Attempts, "model": res.Model, "source": res.Source, "notes": res.Notes})
	}
	ver, err := art.Save(ctx, env.Pool, gameID, g.Name, res)
	if err != nil {
		return nil, &tools.Transient{Err: err}
	}
	summary := "painted the cover"
	if res.Source == "procedural" {
		summary = "made a procedural cover"
		if len(res.Notes) > 0 {
			summary += " (" + strings.Join(res.Notes, "; ") + ")"
		}
		if res.Attempts > 0 {
			slog.Warn("cover generation fell back to procedural", "game", gameID, "attempts", res.Attempts, "notes", res.Notes)
		}
	}
	return map[string]any{"stored": true, "source": res.Source, "model": res.Model, "version": ver,
		"images": res.Attempts, "prompt_by": how, "summary": truncate(summary, 600)}, nil
}

// goalImages is how many image calls the goal has made so far.
func goalImages(ctx context.Context, q querier, goalID string) int {
	var n int
	_ = q.QueryRow(ctx, `SELECT coalesce((usage->>'images')::int, 0) FROM goals WHERE id=$1`, goalID).Scan(&n)
	return n
}

const artistSystem = `You are the Artist of the game studio of "Play with Agents". You write the subject of ONE cover illustration for a board game, for a text-to-image model.

Write 2 to 4 sentences in English describing exactly what the picture shows: the game's theme and world, its board and pieces (or cards, tokens) arranged mid-play, the mood, and the camera angle. Make it vivid, cinematic and family-friendly. The palette leans on deep navy, electric blue and warm ember accents where it fits the theme.
Never ask for text, letters, numbers, a title, logos or real brands in the picture, and no real people. Output only the description: no preamble, no quotes, no Markdown.`

// writeSubject asks the fast model for the picture's subject. "" means use
// the template.
func writeSubject(ctx context.Context, a Artist, meta engine.CallMeta, g *gameRow) (string, string) {
	if a.Model == nil || a.FastLLM == "" {
		return "", "template"
	}
	rules := truncate(g.RulesMD, 3500)
	resp, err := a.Model.Chat(ctx, meta, llm.Request{Model: a.FastLLM, Temperature: 0.7, MaxTokens: 300, Messages: []llm.Message{
		{Role: "system", Content: artistSystem},
		{Role: "user", Content: fmt.Sprintf("GAME: %s\nSUMMARY: %s\nPLAYERS: %d to %d\n\nRULES (for theme, board and components):\n%s",
			g.Name, g.Summary, g.Spec.MinSeats, g.Spec.MaxSeats, rules)},
	}})
	if err != nil || resp == nil {
		return "", "template"
	}
	s := strings.Trim(strings.TrimSpace(resp.Message.Content), "\"“”`")
	if len([]rune(s)) < 30 {
		return "", "template"
	}
	return s, "model"
}

// ── backfill ───────────────────────────────────────────────────────────────

// BackfillCovers makes covers for every draft or published script game
// that has none (`play covers backfill`). The seeded examples ship static
// covers and are skipped. With dryRun it only lists them. Each game gets at
// most art.MaxAttempts image calls; failures store the procedural cover.
func BackfillCovers(ctx context.Context, pool *pgxpool.Pool, a Artist, dryRun bool, out io.Writer) error {
	rows, err := pool.Query(ctx, `SELECT g.id FROM games g LEFT JOIN game_covers c ON c.game_id = g.id
		WHERE g.kind='script' AND g.status IN ('draft','published') AND c.game_id IS NULL
		ORDER BY g.status DESC, g.plays DESC, g.created_at`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	images := 0
	n := 0
	for _, id := range ids {
		g, err := loadGame(ctx, pool, id)
		if err != nil {
			return err
		}
		if g.Owner == "" && art.StaticCovers[id] {
			continue // a seeded example: its cover ships with the web app
		}
		n++
		if dryRun {
			fmt.Fprintf(out, "would cover %-32s %q (%s)\n", id, g.Name, g.Status)
			continue
		}
		subject, how := "", "template"
		if a.Images.Enabled() {
			subject, how = writeSubject(ctx, a, engine.CallMeta{Purpose: "cover_prompt", UserID: g.Owner}, g)
		}
		if subject == "" {
			subject = art.TemplateSubject(g.Name, g.Summary)
		}
		res, err := art.Make(ctx, a.Images, id, g.Name, art.Prompt(subject), art.MaxAttempts)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		images += res.Attempts
		ver, err := art.Save(ctx, pool, id, g.Name, res)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		note := ""
		if len(res.Notes) > 0 {
			note = " — " + strings.Join(res.Notes, "; ")
		}
		fmt.Fprintf(out, "covered %-32s %q: %s %s (prompt by %s, %d image calls, %d KB, v%d)%s\n",
			id, g.Name, res.Source, res.Model, how, res.Attempts, len(res.Bytes)/1024, ver, note)
	}
	verb := "covered"
	if dryRun {
		verb = "would cover"
	}
	fmt.Fprintf(out, "%s %d game(s); %d image call(s)\n", verb, n, images)
	return nil
}
