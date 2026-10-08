package studio

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/games/script"
	"github.com/damonleelcx/play-with-agents/internal/persona"
	"github.com/damonleelcx/play-with-agents/internal/skills"
)

// Roles, as the UI shows them on tasks.
const (
	RoleDesigner    = "designer"
	RoleEngineer    = "engineer"
	RolePlaytester  = "playtester"
	RoleCritic      = "critic"
	RoleCoordinator = "coordinator"
)

// Step budgets (model turns) per role.
const (
	designSteps  = 6
	engineSteps  = 16
	criticSteps  = 6
	publishSteps = 4
)

var registerOnce sync.Once

// Register installs the studio's tools, verifiers, roles, playbook and plan
// hook. It is idempotent; the package's init calls it, so importing the
// package is enough.
func Register() {
	registerOnce.Do(func() {
		registerTools()
		registerArtTool()
		registerVerifiers()
		registerRoles()
		skills.Register(playbook())
		engine.RegisterPlanHook(Skill, planHook)
	})
}

func init() { Register() }

func playbook() *skills.Skill {
	return &skills.Skill{
		Name: Skill, Domain: Domain, Title: "Build a game", TitleZH: "制作游戏", Fixed: true,
		Description: "Designer writes the rules → Engineer writes the JavaScript module until it passes the contract check → " +
			"Playtester simulates hundreds of games → an independent Critic reviews rules vs module vs report → the owner approves publishing. " +
			"A failed playtest or a critic's 'revise' sends the module back to the Engineer (bounded by the replan limit). " +
			"In parallel, once the rules exist, the Artist paints the cover; nothing waits for it.",
		Criteria:   criteria("en"),
		Milestones: milestones("en"),
		Steps: []skills.Step{
			{Key: "design-rules", Title: "Design the rules", Role: RoleDesigner, Tools: []string{ToolSaveRules},
				Verify: []string{"called:" + ToolSaveRules}, MaxSteps: designSteps,
				Instructions: "Write the complete rules document for the player's game and save it with save_rules."},
			{Key: "write-module", Title: "Build the game module", Role: RoleEngineer, Deps: []string{"design-rules"},
				Tools: []string{ToolSaveModule, ToolReadExample}, Verify: []string{VerifyModuleChecks}, MaxSteps: engineSteps,
				Instructions: "Implement the rules as a JavaScript game module and save it with save_module until the contract check passes."},
			// The Artist runs beside the build: no step depends on it, it
			// needs no approval, and its tool always succeeds (a procedural
			// cover when the image model is unavailable).
			{Key: "illustrate-cover", Title: "Illustrate the cover", Role: RoleArtist, Deps: []string{"design-rules"},
				Tool: ToolIllustrate},
			{Key: "playtest", Title: "Playtest hundreds of games", Role: RolePlaytester, Deps: []string{"write-module"},
				Tool: ToolPlaytest, Verify: []string{VerifyPlaytestPassed}},
			{Key: "critic-review", Title: "Independent review", Role: RoleCritic, Deps: []string{"playtest"},
				Tools: []string{ToolSubmitReview}, Verify: []string{"called:" + ToolSubmitReview, VerifyCriticAccepted}, MaxSteps: criticSteps,
				Instructions: "Review the playtested module against the rules and the playtest report, then submit_review."},
			{Key: "publish", Title: "Ask the owner to publish", Role: RoleCoordinator, Deps: []string{"critic-review"},
				Tools: []string{ToolPublish}, Verify: []string{VerifyPublishDecided}, MaxSteps: publishSteps,
				Instructions: "Ask the owner to publish the reviewed version with publish_game."},
		},
	}
}

// revisionRound is the tasks a revision adds: the Engineer fixes the module,
// then the same gates run again. The engineer step has no dependency (all
// earlier work is finished), which keeps the DAG shallow however many
// rounds there are.
//
// earlier carries the findings of previous rounds, so a fix never quietly
// undoes the one before it.
func revisionRound(n int, findings string, earlier []string) []engine.PlannedTask {
	k := func(s string) string { return fmt.Sprintf("%s-%d", s, n) }
	instr := "Fix the module so that it addresses every finding below, keep everything that already works, and save it with save_module until the contract check passes. " +
		"Fix the root cause, not just the example: if one card, piece or rule was wrong, re-check every other card, piece and rule of the same kind against the rules document before you save." +
		findingsHeader + findings
	if len(earlier) > 0 {
		instr += earlierHeader + "These were fixed in earlier rounds. They must STAY fixed; re-check each one before you save:\n"
		for i, f := range earlier {
			instr += fmt.Sprintf("\n--- round %d ---\n%s\n", i+2, f)
		}
	}
	// The Critic sees what earlier rounds asked for, so it does not reverse
	// its own rulings (one round "orthogonal only", the next "too strict").
	review := "Review the revised, playtested module against the rules and the playtest report, then submit_review."
	if all := append(append([]string{}, earlier...), findings); len(all) > 0 {
		review += "\n\nEARLIER REVIEW ROUNDS asked for these changes. Treat them as settled interpretations of the rules: do not ask to undo one unless the rules document plainly says otherwise (quote it). Report only problems that remain.\n"
		for i, f := range all {
			review += fmt.Sprintf("\n--- round %d ---\n%s\n", i+1, f)
		}
	}
	return []engine.PlannedTask{
		{Key: k("revise"), Title: fmt.Sprintf("Revise the module (round %d)", n), Role: RoleEngineer,
			Tools: []string{ToolSaveModule, ToolReadExample}, Verify: []string{VerifyModuleChecks}, MaxSteps: engineSteps,
			Instructions: instr},
		{Key: k("playtest"), Title: fmt.Sprintf("Playtest again (round %d)", n), Role: RolePlaytester, Deps: []string{k("revise")},
			Tool: ToolPlaytest, Verify: []string{VerifyPlaytestPassed}},
		{Key: k("critic"), Title: fmt.Sprintf("Review again (round %d)", n), Role: RoleCritic, Deps: []string{k("playtest")},
			Tools: []string{ToolSubmitReview}, Verify: []string{"called:" + ToolSubmitReview, VerifyCriticAccepted}, MaxSteps: criticSteps,
			Instructions: review},
		{Key: k("publish"), Title: "Ask the owner to publish", Role: RoleCoordinator, Deps: []string{k("critic")},
			Tools: []string{ToolPublish}, Verify: []string{VerifyPublishDecided}, MaxSteps: publishSteps,
			Instructions: "Ask the owner to publish the reviewed version with publish_game."},
	}
}

const (
	findingsHeader = "\n\nFINDINGS\n"
	earlierHeader  = "\n\nEARLIER FINDINGS\n"
)

// ── Roles: what each specialist sees ───────────────────────────────────────

func registerRoles() {
	engine.RegisterRole(engine.Role{Name: RoleDesigner, Temperature: 0.5,
		System:  func(g *engine.Goal, _ *engine.Task) string { return designerSystem(g.Language) },
		Context: designerContext})
	engine.RegisterRole(engine.Role{Name: RoleEngineer, Temperature: 0.2,
		System:  func(g *engine.Goal, _ *engine.Task) string { return engineerSystem + engineerLanguage(g.Language) },
		Context: engineerContext})
	engine.RegisterRole(engine.Role{Name: RoleCritic, Temperature: 0.2,
		System:  func(*engine.Goal, *engine.Task) string { return criticSystem },
		Context: criticContext})
	engine.RegisterRole(engine.Role{Name: RoleCoordinator, Temperature: 0.1,
		System:  func(*engine.Goal, *engine.Task) string { return coordinatorSystem },
		Context: coordinatorContext})
	// The playtester runs as a tool task; it has no model and no prompt.
}

func designerContext(ctx context.Context, s *engine.Store, g *engine.Goal, t *engine.Task, c *engine.Client) (string, error) {
	gameID, err := goalGame(ctx, s.Pool, g.ID)
	if err != nil {
		return "", err
	}
	game, err := loadGame(ctx, s.Pool, gameID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	w("# TASK\n%s\n", t.Spec.Instructions)
	w("\n# THE PLAYER'S IDEA\n%s\n", g.Objective)
	if conv := s.RecentConversation(ctx, g.ConversationID, 8); conv != "" {
		w("\n# CONVERSATION WITH AOI (for details the player gave)\n%s\n", conv)
	}
	if strings.TrimSpace(game.RulesMD) != "" {
		w("\n# CURRENT RULES OF %q (revise these; keep what the player did not ask to change)\n%s\n", game.Name, game.RulesMD)
	}
	w(`
# THE RULES DOCUMENT (rules_md, Markdown) MUST HAVE THESE SECTIONS
1. Overview — the idea and how you win, in two or three sentences.
2. Components — board (exact size), pieces, cards, counters.
3. Setup — the exact starting position for every allowed player count, and who moves first.
4. Turn structure — what one turn is, in order.
5. Legal moves — every kind of move, exactly when it is legal, and what happens if a player has no legal move.
6. Scoring — if any.
7. End of the game and tie-breaks — every way the game ends, how the winner is decided, and what a draw is.
8. Edge cases — anything a program could get wrong (full board, simultaneous events, last move).
9. Players and information — the seat range, and whether any information is hidden from some players (hidden_info).
10. Strategy hint for AI players — one or two sentences on what good play looks like.

Choose min_seats/max_seats to match the idea (the player said how many, use exactly that). Keep the name short (max 60 characters).
`)
	return b.String(), nil
}

func engineerContext(ctx context.Context, s *engine.Store, g *engine.Goal, t *engine.Task, c *engine.Client) (string, error) {
	gameID, err := goalGame(ctx, s.Pool, g.ID)
	if err != nil {
		return "", err
	}
	game, err := loadGame(ctx, s.Pool, gameID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	w("# TASK\n%s\n", t.Spec.Instructions)
	w("\n# RULES DOCUMENT: %s\n%s\n\nSeat range: %d to %d players. Hidden information: %v. meta.minSeats, meta.maxSeats and meta.hiddenInfo must match exactly. meta.name: %q.\n",
		game.Name, game.RulesMD, game.Spec.MinSeats, game.Spec.MaxSeats, game.Spec.HiddenInfo, game.Name)

	// The module to start from: this build's newest, else (revisions) the
	// game's published one.
	cur, err := latestVersion(ctx, s.Pool, gameID, g.ID, anyOfGoal)
	if err != nil {
		return "", err
	}
	if cur == nil {
		if cur, err = baseVersion(ctx, s.Pool, game); err != nil {
			return "", err
		}
	}
	if cur != nil {
		w("\n# CURRENT MODULE (version %d) — start from this source and change only what is needed\n```js\n%s\n```\n", cur.Version, cur.Source)
		if txt := str(cur.Report, "check", "text"); txt != "" {
			w("\nIts last contract check:\n%s\n", truncate(txt, 3000))
		}
		if md := str(cur.Report, "markdown"); md != "" {
			w("\nIts last playtest:\n%s\n", truncate(md, 4000))
		}
		if rv := str(cur.Report, "review", "markdown"); rv != "" {
			w("\nIts last review:\n%s\n", truncate(rv, 4000))
		}
	}

	w("\n# MODULE CONTRACT (follow exactly)\n%s\n", moduleContract)
	w("\n# REFERENCE MODULES\n")
	for _, id := range referenceModules(game.Spec, game.RulesMD) {
		if e, ok := script.ExampleByID(id); ok {
			w("\n## %s (%s)\n```js\n%s\n```\n", e.ID, e.Summary, e.Source)
		}
	}
	w("\nOther references (read_example): ")
	for _, e := range script.Examples() {
		w("%s — %s; ", e.ID, e.Summary)
	}
	w("\n")
	return b.String(), nil
}

// criticContext is deliberately narrow: the rules, the source and the
// playtest report — nothing the Engineer said about its own work.
func criticContext(ctx context.Context, s *engine.Store, g *engine.Goal, t *engine.Task, c *engine.Client) (string, error) {
	gameID, err := goalGame(ctx, s.Pool, g.ID)
	if err != nil {
		return "", err
	}
	game, err := loadGame(ctx, s.Pool, gameID)
	if err != nil {
		return "", err
	}
	v, err := latestVersion(ctx, s.Pool, gameID, g.ID, playtested)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("no playtested version to review")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# TASK\n%s\n\nWrite summary and findings in %s.\n", t.Spec.Instructions, langName(g.Language))
	fmt.Fprintf(&b, "\n# 1. RULES DOCUMENT: %s\nSeat range %d–%d, hidden information: %v.\n\n%s\n",
		game.Name, game.Spec.MinSeats, game.Spec.MaxSeats, game.Spec.HiddenInfo, game.RulesMD)
	fmt.Fprintf(&b, "\n# 2. MODULE SOURCE (version %d)\n```js\n%s\n```\n", v.Version, v.Source)
	fmt.Fprintf(&b, "\n# 3. PLAYTEST REPORT\n%s\n", str(v.Report, "markdown"))
	if txt := str(v.Report, "check", "text"); txt != "" {
		fmt.Fprintf(&b, "\nContract check: %s\n", truncate(txt, 1500))
	}
	return b.String(), nil
}

func coordinatorContext(ctx context.Context, s *engine.Store, g *engine.Goal, t *engine.Task, c *engine.Client) (string, error) {
	gameID, err := goalGame(ctx, s.Pool, g.ID)
	if err != nil {
		return "", err
	}
	game, err := loadGame(ctx, s.Pool, gameID)
	if err != nil {
		return "", err
	}
	v, err := latestVersion(ctx, s.Pool, gameID, g.ID, reviewedOK)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("no version has passed the playtest and the critic")
	}
	return fmt.Sprintf("# TASK\n%s\n\nGame: %q (game_id %q). Version to publish: %d (playtest passed, critic verdict pass).\nCall publish_game with {\"game_id\": %q, \"version\": %d}. Write your final sentence in %s.\n\nCritic's review:\n%s\n",
		t.Spec.Instructions, game.Name, gameID, v.Version, gameID, v.Version, langName(g.Language), truncate(str(v.Report, "review", "markdown"), 2000)), nil
}

func langName(lang string) string { return persona.LangName(lang) }

// storyWords mark a game whose cards go onto the board or that tells a
// story: its few-shot template is story_tiles (rich cards, card → cell
// moves, the story panel). Checked in the rules document, any language.
var storyWords = []string{"story", "stories", "tale", "narrat", "故事", "叙事", "物語", "ストーリー", "이야기", "스토리"}

// cardWords mark a card game in any of the four languages.
var cardWords = []string{"card", "deck", "hand", "牌", "卡", "手札", "カード", "デッキ", "카드", "덱"}

// referenceModules picks the two bundled modules the Engineer sees in full.
func referenceModules(spec gameSpec, rules string) []string {
	low := strings.ToLower(rules)
	has := func(words []string) bool {
		for _, w := range words {
			if strings.Contains(low, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has(storyWords):
		return []string{"tictactoe", "story_tiles"}
	case spec.HiddenInfo && has(cardWords) && strings.Contains(low, "board"):
		return []string{"lantern_market", "story_tiles"}
	case spec.HiddenInfo || spec.MaxSeats > 2:
		return []string{"tictactoe", "lantern_market"}
	}
	return []string{"tictactoe", "connect_four"}
}
