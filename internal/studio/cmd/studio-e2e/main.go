// Command studio-e2e runs one real game build against the live model, with
// the engine's workers and scheduler in this process, and prints the
// timeline: tasks, revision rounds, tokens, wall time and the playtest and
// critic verdicts. It stops when the build awaits the owner's publish
// approval (or needs a person, or fails), bounded by -timeout.
//
//	set -a; . ./.env; set +a
//	go run ./internal/studio/cmd/studio-e2e -prompt "a 2-player game on a 5x5 grid ..."
//
// It uses its own database (play_studio_e2e on the configured server) so the
// dev server's workers never pick its tasks up. -approve also approves the
// publish and runs the build to completion.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/config"
	"github.com/damonleelcx/play-with-agents/internal/db"
	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/studio"
)

var outPath *string

func main() {
	prompt := flag.String("prompt", "a 2-player game on a 5x5 grid where players take turns placing stones; you win by making a line of 4; if the board fills it's a draw", "the game idea")
	dbName := flag.String("db", "play_studio_e2e", "database on the configured server")
	timeout := flag.Duration("timeout", 45*time.Minute, "give up after this long")
	workers := flag.Int("workers", 2, "mission workers")
	approve := flag.Bool("approve", false, "approve publishing and run to completion")
	lang := flag.String("lang", "en", "owner language")
	outPath = flag.String("out", "", "save the newest playtested module here")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err := run(*prompt, *dbName, *lang, *timeout, *workers, *approve); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(prompt, dbName, lang string, timeout time.Duration, nWorkers int, approve bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.LLMAPIKey == "" {
		return fmt.Errorf("PLAY_LLM_API_KEY is not set (source .env first)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	url, err := db.EnsureDatabase(ctx, cfg.DatabaseURL, dbName)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	client := llm.New(cfg.LLMBaseURL, cfg.LLMAPIKey)
	client.Fallback = map[string]string{cfg.LLMModel: cfg.LLMFastModel}
	store := &engine.Store{Pool: pool}
	model := &engine.Model{Client: client, Store: store}
	planner := &engine.Planner{Store: store, Model: model, LLM: cfg.LLMModel}

	// The owner.
	var uid, conv string
	email := fmt.Sprintf("e2e-%d@example.com", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name, email_verified_at) VALUES ($1,'x','E2E Owner', now()) RETURNING id`, email).Scan(&uid); err != nil {
		return err
	}
	_, _ = pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2)`, uid, fmt.Sprintf(`{"language":%q,"playtest_games":200}`, lang))
	if err := pool.QueryRow(ctx, `INSERT INTO conversations (user_id, title) VALUES ($1, 'Studio E2E') RETURNING id`, uid).Scan(&conv); err != nil {
		return err
	}
	_, _ = pool.Exec(ctx, `INSERT INTO messages (conversation_id, role, content) VALUES ($1, 'user', $2)`, conv, "Let's make a game: "+prompt)

	svc := &studio.Service{Pool: pool, Store: store}
	start := time.Now()
	goalID, gameID, err := svc.StartBuild(ctx, uid, conv, prompt, "", lang)
	if err != nil {
		return err
	}
	fmt.Printf("goal %s  game %s  model %s\n", goalID, gameID, cfg.LLMModel)

	wctx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	wake := make(chan struct{}, 16)
	go engine.Listen(wctx, pool, wake, nil)
	for i := 0; i < nWorkers; i++ {
		w := &engine.Worker{ID: fmt.Sprintf("e2e-%d", i), Store: store, Model: model, Planner: planner, LLM: cfg.LLMModel,
			Mailer: mail.Log{}, Lease: 2 * time.Minute, Wake: wake}
		go w.Run(wctx)
	}
	sched := &engine.Scheduler{Store: store, Model: model, FastLLM: cfg.LLMFastModel, Mailer: mail.Log{}, Interval: 3 * time.Second}
	go sched.Run(wctx)

	// Bounded wait: the context's deadline ends it whatever happens.
	lastEvent := int64(0)
	approved := false
	for {
		select {
		case <-ctx.Done():
			report(ctx, store, pool, goalID, gameID, start)
			return fmt.Errorf("timed out after %s", timeout)
		case <-time.After(5 * time.Second):
		}
		lastEvent = printEvents(context.Background(), store, goalID, lastEvent, start)
		g, err := store.Goal(ctx, goalID)
		if err != nil {
			return err
		}
		var pending string
		_ = pool.QueryRow(ctx, `SELECT id::text FROM approvals WHERE goal_id=$1 AND status='pending'`, goalID).Scan(&pending)
		switch {
		case pending != "" && !approve:
			fmt.Printf("\n== AWAITING PUBLISH APPROVAL after %s ==\n", time.Since(start).Round(time.Second))
			report(context.Background(), store, pool, goalID, gameID, start)
			return nil
		case pending != "" && !approved:
			fmt.Printf("\n== awaiting publish approval after %s: approving ==\n", time.Since(start).Round(time.Second))
			if err := store.Decide(ctx, pending, uid, true, "e2e"); err != nil {
				return err
			}
			approved = true
		case g.Status == "completed" || g.Status == "needs_attention" || g.Status == "failed" || g.Status == "cancelled":
			fmt.Printf("\n== GOAL %s after %s: %s ==\n", strings.ToUpper(g.Status), time.Since(start).Round(time.Second), g.AttentionReason)
			report(context.Background(), store, pool, goalID, gameID, start)
			if g.Status != "completed" {
				return fmt.Errorf("the build stopped: %s", g.Status)
			}
			return nil
		}
	}
}

func printEvents(ctx context.Context, s *engine.Store, goalID string, after int64, start time.Time) int64 {
	evs, _ := s.Events(ctx, goalID, after, 200)
	for _, e := range evs {
		switch e.Type {
		case "task.started", "task.succeeded", "task.failed", "task.retry", "task.verify_failed", "goal.replanned",
			"goal.planned", "approval.requested", "approval.decided", "goal.status", "budget.exceeded":
			line := ""
			for _, k := range []string{"task", "tool", "why", "error", "summary", "to", "decision"} {
				if v, ok := e.Data[k]; ok && fmt.Sprint(v) != "" {
					line += fmt.Sprintf(" %s=%q", k, clip(fmt.Sprint(v), 160))
				}
			}
			fmt.Printf("[%6s] %-20s%s\n", e.CreatedAt.Sub(start).Round(time.Second), e.Type, line)
		}
		after = e.ID
	}
	return after
}

// report prints the build: usage, every task, every version's gates, and
// the newest playtest report. With -out the newest module is saved there.
func report(ctx context.Context, s *engine.Store, pool *pgxpool.Pool, goalID, gameID string, start time.Time) {
	g, err := s.Goal(ctx, goalID)
	if err != nil {
		fmt.Println("report:", err)
		return
	}
	u := g.Usage
	fmt.Printf("\nGOAL %q status=%s replans=%d/%d wall=%s\n", g.Title, g.Status, g.Replans, g.Limits.MaxReplans, time.Since(start).Round(time.Second))
	fmt.Printf("usage: %d model steps, %d tool calls, %d prompt + %d completion = %d tokens, ~$%.2f\n",
		u.Iterations, u.ToolCalls, u.PromptTokens, u.CompletionTokens, u.Tokens(), u.CostUSD)

	fmt.Println("\nTASKS")
	ts, _ := s.Tasks(ctx, goalID)
	for _, t := range ts {
		d := ""
		if t.StartedAt != nil && t.FinishedAt != nil {
			d = t.FinishedAt.Sub(*t.StartedAt).Round(time.Second).String()
		}
		fmt.Printf("  %-16s %-11s %-6s %-16s attempts=%d %8s %s\n", t.Key, t.Spec.Role, t.Kind, t.Status, t.Attempts, d, clip(t.Error, 140))
	}

	fmt.Println("\nTOKENS BY ROLE")
	rows, err := pool.Query(ctx, `SELECT coalesce(nullif(t.spec->>'role',''), l.purpose), count(*), sum(l.prompt_tokens), sum(l.completion_tokens), max(l.model)
		FROM llm_calls l LEFT JOIN tasks t ON t.id=l.task_id WHERE l.goal_id=$1 GROUP BY 1 ORDER BY 3 DESC`, goalID)
	if err == nil {
		for rows.Next() {
			var role, m string
			var n, pt, ct int
			if rows.Scan(&role, &n, &pt, &ct, &m) == nil {
				fmt.Printf("  %-12s %3d calls  %7d in  %6d out  (%s)\n", role, n, pt, ct, m)
			}
		}
		rows.Close()
	}

	fmt.Println("\nVERSIONS")
	rows, err = pool.Query(ctx, `SELECT version, coalesce(report->'check'->>'ok',''), coalesce(report->'check'->>'errors',''),
		coalesce(report->'verdict'->>'pass',''), coalesce(report->'verdict'->'reasons','[]')::text,
		coalesce(report->'review'->>'verdict',''), coalesce(jsonb_array_length(report->'review'->'findings'),0)
		FROM game_versions WHERE game_id=$1 ORDER BY version`, gameID)
	if err == nil {
		for rows.Next() {
			var v, nf int
			var ok, nerr, pass, reasons, review string
			if rows.Scan(&v, &ok, &nerr, &pass, &reasons, &review, &nf) == nil {
				fmt.Printf("  v%d check_ok=%s errors=%s playtest_pass=%s critic=%s (%d findings) %s\n", v, ok, nerr, pass, review, nf, clip(reasons, 300))
			}
		}
		rows.Close()
	}
	var status, name string
	var cur int
	_ = pool.QueryRow(ctx, `SELECT status, name, current_version FROM games WHERE id=$1`, gameID).Scan(&status, &name, &cur)
	fmt.Printf("\nGAME %s %q status=%s current_version=%d\n", gameID, name, status, cur)

	var md, review, src string
	_ = pool.QueryRow(ctx, `SELECT coalesce(report->>'markdown',''), coalesce(report->'review'->>'markdown',''), source FROM game_versions
		WHERE game_id=$1 AND report ? 'playtest' ORDER BY version DESC LIMIT 1`, gameID).Scan(&md, &review, &src)
	fmt.Printf("\nNEWEST PLAYTEST\n%s\n\nNEWEST REVIEW\n%s\n", md, review)
	if *outPath != "" && src != "" {
		if err := os.WriteFile(*outPath, []byte(src), 0o644); err == nil {
			fmt.Println("module saved to", *outPath)
		}
	}
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
