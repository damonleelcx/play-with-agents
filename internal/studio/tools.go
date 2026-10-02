package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/games/playtest"
	"github.com/damonleelcx/play-with-agents/internal/games/script"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

// Tool names (docs/01-intents-tools-skills.md §2.2).
const (
	ToolSaveRules    = "save_rules"
	ToolSaveModule   = "save_module"
	ToolCheckModule  = "check_module"
	ToolReadExample  = "read_example"
	ToolPlaytest     = "playtest"
	ToolSubmitReview = "submit_review"
	ToolPublish      = "publish_game"
)

// Verifier names.
const (
	VerifyModuleChecks   = "module_checks"
	VerifyPlaytestPassed = "playtest_passed"
	VerifyCriticAccepted = "critic_accepted"
	VerifyPublishDecided = "publish_decided"
)

const maxSource = 200_000

// PlaytestGames is the default number of simulated games; the owner's
// playtest_games preference (50/200/500) overrides it.
const PlaytestGames = 200

// maxSeatCounts bounds how many seat counts one playtest covers.
const maxSeatCounts = 5

func registerTools() {
	tools.Register(&tools.Tool{
		Name: ToolSaveRules,
		Description: "Save the rules document of the game being built: its name, a one-sentence summary, the full rules in Markdown, " +
			"the seat range and whether players have hidden information. Saving again replaces the previous document.",
		Input: `{"type":"object","required":["name","summary","rules_md","min_seats","max_seats","hidden_info"],"additionalProperties":false,"properties":{
			"name":{"type":"string","minLength":1,"maxLength":60},
			"summary":{"type":"string","minLength":1,"maxLength":280},
			"rules_md":{"type":"string","minLength":200,"maxLength":20000},
			"min_seats":{"type":"integer","minimum":1,"maximum":8},
			"max_seats":{"type":"integer","minimum":1,"maximum":8},
			"hidden_info":{"type":"boolean"}}}`,
		Output: `{"type":"object","required":["saved","game_id"]}`,
		Effect: tools.Write, Gate: tools.G0, Timeout: 10 * time.Second,
		Run: runSaveRules,
	})
	tools.Register(&tools.Tool{
		Name: ToolSaveModule,
		Description: "Save the complete JavaScript module of the game being built. It is loaded in the sandbox and checked against the " +
			"module contract (setup at min and max seats, every function, random playouts, replay determinism, hidden-information leaks, " +
			"meta vs the rules' seat range). Returns the version number, ok, and the check report: fix every [error] and save again.",
		Input:  fmt.Sprintf(`{"type":"object","required":["source"],"additionalProperties":false,"properties":{"source":{"type":"string","minLength":50,"maxLength":%d}}}`, maxSource),
		Output: `{"type":"object","required":["version","ok","report"],"properties":{"version":{"type":"integer"},"ok":{"type":"boolean"},"report":{"type":"string"}}}`,
		Effect: tools.Write, Gate: tools.G0, Timeout: 90 * time.Second,
		Run: runSaveModule,
	})
	tools.Register(&tools.Tool{
		Name:        ToolCheckModule,
		Description: "Run the module contract checks on a source without saving it. Returns ok and the check report.",
		Input:       fmt.Sprintf(`{"type":"object","required":["source"],"additionalProperties":false,"properties":{"source":{"type":"string","minLength":50,"maxLength":%d}}}`, maxSource),
		Output:      `{"type":"object","required":["ok","report"]}`,
		Effect:      tools.Read, Gate: tools.G0, Timeout: 90 * time.Second,
		Run: func(ctx context.Context, env *tools.Env, a map[string]any) (map[string]any, error) {
			id := "draft"
			if gid, err := goalGame(ctx, env.Pool, env.GoalID); err == nil {
				id = gid
			}
			rep := script.Check(id, tools.Str(a, "source"))
			return map[string]any{"ok": rep.OK(), "report": rep.String()}, nil
		},
	})
	var ids []string
	for _, e := range script.Examples() {
		ids = append(ids, e.ID)
	}
	idsJSON, _ := json.Marshal(ids)
	tools.Register(&tools.Tool{
		Name:        ToolReadExample,
		Description: "Read one of the platform's reference game modules (complete, working JavaScript).",
		Input:       `{"type":"object","required":["id"],"additionalProperties":false,"properties":{"id":{"type":"string","enum":` + string(idsJSON) + `}}}`,
		Output:      `{"type":"object","required":["id","source"]}`,
		Effect:      tools.Read, Gate: tools.G0, Timeout: 5 * time.Second,
		Run: func(ctx context.Context, env *tools.Env, a map[string]any) (map[string]any, error) {
			e, ok := script.ExampleByID(tools.Str(a, "id"))
			if !ok {
				return nil, &tools.InvalidInput{Err: fmt.Errorf("no example %q", tools.Str(a, "id"))}
			}
			return map[string]any{"id": e.ID, "name": e.Name, "summary": e.Summary, "source": e.Source}, nil
		},
	})
	tools.Register(&tools.Tool{
		Name: ToolPlaytest,
		Description: "Simulate hundreds of games of the newest checked module (random, AI-vs-random and AI-vs-AI, every seat count) " +
			"and store the report on that version. Deterministic: no model involved.",
		Input:  `{"type":"object","additionalProperties":false,"properties":{"games":{"type":"integer","minimum":10,"maximum":1000}}}`,
		Output: `{"type":"object","required":["version","pass","reasons","summary"]}`,
		Effect: tools.Write, Gate: tools.G0, Timeout: 6 * time.Minute,
		Run: runPlaytest,
	})
	tools.Register(&tools.Tool{
		Name: ToolSubmitReview,
		Description: "Submit your independent review of the playtested module: verdict pass or revise, and findings " +
			"(severity high|medium|low, area rules|code|balance|fun|ui, what is wrong, and the exact fix).",
		Input: `{"type":"object","required":["verdict","summary","findings"],"additionalProperties":false,"properties":{
			"verdict":{"type":"string","enum":["pass","revise"]},
			"summary":{"type":"string","minLength":1,"maxLength":2000},
			"findings":{"type":"array","maxItems":30,"items":{"type":"object","required":["severity","area","detail"],"additionalProperties":false,"properties":{
				"severity":{"type":"string","enum":["high","medium","low"]},
				"area":{"type":"string","enum":["rules","code","balance","fun","ui"]},
				"detail":{"type":"string","minLength":1,"maxLength":1500},
				"fix":{"type":"string","maxLength":1500}}}}}}`,
		Output: `{"type":"object","required":["stored","version","verdict"]}`,
		Effect: tools.Write, Gate: tools.G0, Timeout: 10 * time.Second,
		Run: runSubmitReview,
	})
	tools.Register(&tools.Tool{
		Name: ToolPublish,
		Description: "Publish a reviewed version of the game to the catalog. The owner approves this exact call first; " +
			"until then the game stays a draft only its owner can play.",
		Input: `{"type":"object","required":["game_id","version"],"additionalProperties":false,"properties":{
			"game_id":{"type":"string","minLength":2,"maxLength":40},"version":{"type":"integer","minimum":1}}}`,
		Output: `{"type":"object","required":["published","game_id","version"]}`,
		Effect: tools.Write, Gate: tools.G1, Notify: "build_ready", Timeout: 10 * time.Second,
		Preview: func(a map[string]any) string {
			return fmt.Sprintf("Publish %s version %v to the catalog", tools.Str(a, "game_id"), a["version"])
		},
		Run:    runPublish,
		Verify: verifyPublish,
	})
}

// ── save_rules ─────────────────────────────────────────────────────────────

func runSaveRules(ctx context.Context, env *tools.Env, a map[string]any) (map[string]any, error) {
	gameID, err := goalGame(ctx, env.Pool, env.GoalID)
	if err != nil {
		return nil, err
	}
	sp := gameSpec{MinSeats: intArg(a, "min_seats"), MaxSeats: intArg(a, "max_seats"), HiddenInfo: a["hidden_info"] == true}
	if sp.MinSeats > sp.MaxSeats {
		return nil, &tools.InvalidInput{Err: fmt.Errorf("min_seats %d is more than max_seats %d", sp.MinSeats, sp.MaxSeats)}
	}
	specJSON, _ := json.Marshal(sp)
	name := strings.TrimSpace(tools.Str(a, "name"))
	err = pgx.BeginFunc(ctx, env.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE games SET name=$2, summary=$3, rules_md=$4, spec=$5, updated_at=now() WHERE id=$1`,
			gameID, name, strings.TrimSpace(tools.Str(a, "summary")), tools.Str(a, "rules_md"), specJSON); err != nil {
			return err
		}
		var objective string
		if err := tx.QueryRow(ctx, `SELECT objective FROM goals WHERE id=$1`, env.GoalID).Scan(&objective); err != nil {
			return err
		}
		revise := strings.HasPrefix(objective, revisePrefix)
		_, err := tx.Exec(ctx, `UPDATE goals SET title=$2, updated_at=now() WHERE id=$1`, env.GoalID, buildTitle(name, env.Lang, revise))
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"saved": true, "game_id": gameID}, nil
}

// ── save_module ────────────────────────────────────────────────────────────

func runSaveModule(ctx context.Context, env *tools.Env, a map[string]any) (map[string]any, error) {
	gameID, err := goalGame(ctx, env.Pool, env.GoalID)
	if err != nil {
		return nil, err
	}
	g, err := loadGame(ctx, env.Pool, gameID)
	if err != nil {
		return nil, err
	}
	src := tools.Str(a, "source")
	rep := script.Check(gameID, src)
	rep.Issues = append(rep.Issues, specIssues(g.Spec, rep)...)
	ok := true
	var nErr, nWarn int
	for _, is := range rep.Issues {
		if is.Severity == script.SevError {
			ok = false
			nErr++
		} else {
			nWarn++
		}
	}
	text := rep.String()
	report := map[string]any{
		"goal_id": env.GoalID, "task_id": env.TaskID, "saved_at": time.Now().UTC(),
		"check": map[string]any{"ok": ok, "errors": nErr, "warnings": nWarn, "issues": rep.Issues,
			"playouts": rep.Playouts, "finished": rep.Finished, "text": text},
	}
	meta := map[string]any{}
	if rep.Meta != nil {
		raw, _ := json.Marshal(rep.Meta)
		_ = json.Unmarshal(raw, &meta)
	}
	ver, err := storeDraft(ctx, env, gameID, src, meta, report)
	if err != nil {
		return nil, err
	}
	head := fmt.Sprintf("Saved as version %d. ", ver)
	if ok {
		head += "The module passes the contract check; it goes to the playtest next.\n"
	} else {
		head += "The module FAILS the contract check: fix every [error] below and call save_module again with the complete source.\n"
	}
	return map[string]any{"version": ver, "ok": ok, "report": head + text}, nil
}

// specIssues compares the module's meta with the Designer's decisions.
func specIssues(sp gameSpec, rep script.CheckReport) []script.Issue {
	if rep.Meta == nil || sp.MaxSeats == 0 {
		return nil
	}
	m := rep.Meta
	var out []script.Issue
	if m.MinSeats != sp.MinSeats || m.MaxSeats != sp.MaxSeats {
		out = append(out, script.Issue{Severity: script.SevError, Where: "meta", Message: fmt.Sprintf(
			"meta.minSeats/maxSeats are %d/%d but the rules document says %d to %d players; make them match the rules",
			m.MinSeats, m.MaxSeats, sp.MinSeats, sp.MaxSeats)})
	}
	if m.HiddenInfo != sp.HiddenInfo {
		out = append(out, script.Issue{Severity: script.SevError, Where: "meta", Message: fmt.Sprintf(
			"meta.hiddenInfo is %v but the rules document says hidden information is %v", m.HiddenInfo, sp.HiddenInfo)})
	}
	return out
}

// storeDraft writes a module version. The goal's working draft (not yet
// playtested, not published, no table started on it) is replaced in place,
// so an engineer's many saves become one version; anything else gets a new
// version number.
func storeDraft(ctx context.Context, env *tools.Env, gameID, src string, meta, report map[string]any) (int, error) {
	metaJSON, _ := json.Marshal(meta)
	repJSON, _ := json.Marshal(report)
	var ver int
	err := pgx.BeginFunc(ctx, env.Pool, func(tx pgx.Tx) error {
		// Serialise savers of one game.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM games WHERE id=$1 FOR UPDATE`, gameID); err != nil {
			return err
		}
		var last int
		var lastGoal string
		var tested bool
		var current int
		err := tx.QueryRow(ctx, `SELECT v.version, coalesce(v.report->>'goal_id',''), v.report ? 'playtest', g.current_version
			FROM game_versions v JOIN games g ON g.id=v.game_id WHERE v.game_id=$1 ORDER BY v.version DESC LIMIT 1`, gameID).
			Scan(&last, &lastGoal, &tested, &current)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if last > 0 && lastGoal == env.GoalID && !tested && last != current {
			var used bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tables WHERE game_id=$1 AND game_version=$2)`, gameID, last).Scan(&used); err != nil {
				return err
			}
			if !used {
				ver = last
				_, err := tx.Exec(ctx, `UPDATE game_versions SET source=$3, meta=$4, report=$5, created_at=now() WHERE game_id=$1 AND version=$2`,
					gameID, ver, src, metaJSON, repJSON)
				return err
			}
		}
		ver = last + 1
		_, err = tx.Exec(ctx, `INSERT INTO game_versions (game_id, version, source, meta, report) VALUES ($1,$2,$3,$4,$5)`,
			gameID, ver, src, metaJSON, repJSON)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE games SET updated_at=now() WHERE id=$1`, gameID)
		}
		return err
	})
	return ver, err
}

// ── playtest ───────────────────────────────────────────────────────────────

func runPlaytest(ctx context.Context, env *tools.Env, a map[string]any) (map[string]any, error) {
	gameID, err := goalGame(ctx, env.Pool, env.GoalID)
	if err != nil {
		return nil, err
	}
	v, err := latestVersion(ctx, env.Pool, gameID, env.GoalID, checkedOK)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, &tools.InvalidInput{Err: errors.New("no module of this build passes the contract check yet")}
	}
	g, err := script.Load(gameID, v.Source, script.Options{})
	if err != nil {
		return nil, fmt.Errorf("version %d no longer loads: %w", v.Version, err)
	}
	n := intArg(a, "games")
	if n == 0 {
		n = playtestGames(ctx, env)
	}
	m := g.Meta()
	rep := playtest.Run(ctx, g, playtest.Options{Games: n, Seats: seatCounts(m.MinSeats, m.MaxSeats), Seed: int64(v.Version), MaxErrors: 20})
	if ctx.Err() != nil {
		return nil, &tools.Transient{Err: fmt.Errorf("playtest interrupted: %w", ctx.Err())}
	}
	pass, reasons := rep.Verdict()
	if reasons == nil {
		reasons = []string{}
	}
	md := rep.Markdown()
	err = pgx.BeginFunc(ctx, env.Pool, func(tx pgx.Tx) error {
		if err := mergeReport(ctx, tx, gameID, v.Version, map[string]any{
			"playtest": rep, "verdict": map[string]any{"pass": pass, "reasons": reasons}, "markdown": md}); err != nil {
			return err
		}
		if pass {
			// Playable by its owner from now on, before anyone approves
			// publishing.
			_, err := tx.Exec(ctx, `UPDATE games SET status='draft', updated_at=now() WHERE id=$1 AND status='building'`, gameID)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"version": v.Version, "pass": pass, "reasons": reasons, "games": rep.Run,
		"completed": rep.Completed, "errors": rep.ErrorCount, "summary": truncate(md, 6000)}, nil
}

// playtestGames is the owner's playtest_games preference (50/200/500).
func playtestGames(ctx context.Context, env *tools.Env) int {
	var raw []byte
	_ = env.Pool.QueryRow(ctx, `SELECT data->'playtest_games' FROM user_preferences WHERE user_id=$1`, env.UserID).Scan(&raw)
	var n int
	if json.Unmarshal(raw, &n) != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			fmt.Sscan(s, &n)
		}
	}
	switch n {
	case 50, 200, 500:
		return n
	}
	return PlaytestGames
}

// seatCounts covers every seat count from min to max, thinned evenly to at
// most maxSeatCounts (always keeping both ends).
func seatCounts(lo, hi int) []int {
	if hi < lo {
		hi = lo
	}
	var all []int
	for n := lo; n <= hi; n++ {
		all = append(all, n)
	}
	if len(all) <= maxSeatCounts {
		return all
	}
	out := make([]int, 0, maxSeatCounts)
	for i := 0; i < maxSeatCounts; i++ {
		out = append(out, all[i*(len(all)-1)/(maxSeatCounts-1)])
	}
	return out
}

// ── submit_review ──────────────────────────────────────────────────────────

type finding struct {
	Severity string `json:"severity"`
	Area     string `json:"area"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"`
}

func runSubmitReview(ctx context.Context, env *tools.Env, a map[string]any) (map[string]any, error) {
	gameID, err := goalGame(ctx, env.Pool, env.GoalID)
	if err != nil {
		return nil, err
	}
	v, err := latestVersion(ctx, env.Pool, gameID, env.GoalID, playtested)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, &tools.InvalidInput{Err: errors.New("there is no playtested version to review")}
	}
	var fs []finding
	raw, _ := json.Marshal(a["findings"])
	_ = json.Unmarshal(raw, &fs)
	if fs == nil {
		fs = []finding{}
	}
	verdict := tools.Str(a, "verdict")
	review := map[string]any{"verdict": verdict, "summary": tools.Str(a, "summary"), "findings": fs,
		"markdown": reviewMarkdown(verdict, tools.Str(a, "summary"), fs), "task_id": env.TaskID, "at": time.Now().UTC()}
	if err := mergeReport(ctx, env.Pool, gameID, v.Version, map[string]any{"review": review}); err != nil {
		return nil, err
	}
	return map[string]any{"stored": true, "version": v.Version, "verdict": verdict}, nil
}

func reviewMarkdown(verdict, summary string, fs []finding) string {
	var b strings.Builder
	head := "**Verdict: pass** — ready to publish"
	if verdict != "pass" {
		head = "**Verdict: revise** — back to the engineer"
	}
	fmt.Fprintf(&b, "%s\n\n%s\n", head, strings.TrimSpace(summary))
	for _, sev := range []string{"high", "medium", "low"} {
		for _, f := range fs {
			if f.Severity != sev {
				continue
			}
			fmt.Fprintf(&b, "\n- **%s · %s**: %s", f.Severity, f.Area, f.Detail)
			if f.Fix != "" {
				fmt.Fprintf(&b, " — *fix:* %s", f.Fix)
			}
		}
	}
	return b.String()
}

// ── publish_game ───────────────────────────────────────────────────────────

func runPublish(ctx context.Context, env *tools.Env, a map[string]any) (map[string]any, error) {
	gameID, err := goalGame(ctx, env.Pool, env.GoalID)
	if err != nil {
		return nil, err
	}
	if tools.Str(a, "game_id") != gameID {
		return nil, &tools.InvalidInput{Err: fmt.Errorf("this build makes game %s", gameID)}
	}
	ver := intArg(a, "version")
	var pass bool
	var review string
	err = env.Pool.QueryRow(ctx, `SELECT coalesce((report->'verdict'->>'pass')::boolean,false), coalesce(report->'review'->>'verdict','')
		FROM game_versions WHERE game_id=$1 AND version=$2 AND report->>'goal_id'=$3`, gameID, ver, env.GoalID).Scan(&pass, &review)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &tools.InvalidInput{Err: fmt.Errorf("version %d was not built by this mission", ver)}
	}
	if err != nil {
		return nil, err
	}
	// The gates hold here too, not only in the plan: an unplaytested or
	// unreviewed version is never published, whoever asks.
	if !pass || review != "pass" {
		return nil, &tools.InvalidInput{Err: fmt.Errorf("version %d has not passed both the playtest and the critic (playtest %v, critic %q)", ver, pass, review)}
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE games SET status='published', current_version=$2, updated_at=now() WHERE id=$1`, gameID, ver); err != nil {
		return nil, err
	}
	return map[string]any{"published": true, "game_id": gameID, "version": ver}, nil
}

func verifyPublish(ctx context.Context, env *tools.Env, a, out map[string]any) error {
	var status string
	var cur int
	if err := env.Pool.QueryRow(ctx, `SELECT status, current_version FROM games WHERE id=$1`, tools.Str(a, "game_id")).Scan(&status, &cur); err != nil {
		return err
	}
	if status != "published" || cur != intArg(a, "version") {
		return fmt.Errorf("the game is %s at version %d after publishing", status, cur)
	}
	return nil
}

// ── verifiers ──────────────────────────────────────────────────────────────

func registerVerifiers() {
	engine.RegisterVerifier(engine.Verifier{Name: VerifyModuleChecks, Guard: true, Check: func(ctx context.Context, s *engine.Store, t *engine.Task) []string {
		gameID, err := goalGame(ctx, s.Pool, t.GoalID)
		if err != nil {
			return []string{"this build has no game row: " + err.Error()}
		}
		var ok bool
		var text string
		err = s.Pool.QueryRow(ctx, `SELECT coalesce((report->'check'->>'ok')::boolean,false), coalesce(report->'check'->>'text','')
			FROM game_versions WHERE game_id=$1 AND report->>'task_id'=$2 ORDER BY version DESC LIMIT 1`, gameID, t.ID).Scan(&ok, &text)
		if errors.Is(err, pgx.ErrNoRows) {
			return []string{"no module was saved in this task: call save_module with the complete source"}
		}
		if err != nil {
			return []string{err.Error()}
		}
		if !ok {
			return []string{"the latest saved module still fails the contract check; fix the errors and call save_module again:\n" + truncate(text, 2500)}
		}
		return nil
	}})
	engine.RegisterVerifier(engine.Verifier{Name: VerifyPlaytestPassed, Guard: true, Check: func(ctx context.Context, s *engine.Store, t *engine.Task) []string {
		var raw []byte
		err := s.Pool.QueryRow(ctx, `SELECT output FROM tool_calls WHERE task_id=$1 AND tool=$2 AND status='succeeded' ORDER BY id DESC LIMIT 1`,
			t.ID, ToolPlaytest).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return []string{"the playtest has not run in this task"}
		}
		if err != nil {
			return []string{err.Error()}
		}
		var out struct {
			Pass    bool     `json:"pass"`
			Reasons []string `json:"reasons"`
			Version int      `json:"version"`
		}
		_ = json.Unmarshal(raw, &out)
		if out.Pass {
			return nil
		}
		var fails []string
		for _, r := range out.Reasons {
			if strings.HasPrefix(r, "FAIL") {
				fails = append(fails, r)
			}
		}
		return []string{fmt.Sprintf("the playtest of version %d failed: %s", out.Version, strings.Join(fails, "; "))}
	}})
	engine.RegisterVerifier(engine.Verifier{Name: VerifyCriticAccepted, Guard: true, Final: true, Check: func(ctx context.Context, s *engine.Store, t *engine.Task) []string {
		var raw []byte
		err := s.Pool.QueryRow(ctx, `SELECT input FROM tool_calls WHERE task_id=$1 AND tool=$2 AND status='succeeded' ORDER BY id DESC LIMIT 1`,
			t.ID, ToolSubmitReview).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // called:submit_review asks for it
		}
		if err != nil {
			return []string{err.Error()}
		}
		var in struct {
			Verdict  string    `json:"verdict"`
			Summary  string    `json:"summary"`
			Findings []finding `json:"findings"`
		}
		_ = json.Unmarshal(raw, &in)
		if in.Verdict == "pass" {
			return nil
		}
		return []string{"the critic asked for a revision: " + truncate(in.Summary, 600)}
	}})
	engine.RegisterVerifier(engine.Verifier{Name: VerifyPublishDecided, Check: func(ctx context.Context, s *engine.Store, t *engine.Task) []string {
		var published, declined bool
		_ = s.Pool.QueryRow(ctx, `SELECT
			EXISTS(SELECT 1 FROM tool_calls WHERE task_id=$1 AND tool=$2 AND status='succeeded'),
			EXISTS(SELECT 1 FROM approvals WHERE task_id=$1 AND tool=$2 AND status='rejected')`, t.ID, ToolPublish).Scan(&published, &declined)
		if published || declined {
			return nil
		}
		return []string{"publishing was not decided: call publish_game with the game_id and version you were given; the owner is asked to approve"}
	}})
}

func intArg(a map[string]any, k string) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
