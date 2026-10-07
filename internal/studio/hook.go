package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// planHook makes the studio's planning moments deterministic where a rule
// is more reliable than a model:
//
//   - a failed playtest or a critic's "revise" adds a revision round
//     (revise → playtest → critic → publish), up to the replan limit; past
//     it, Aoi asks the owner for a decision and the goal waits;
//   - the owner's change past the limit grants one more round;
//   - once the owner decided on publishing, the build completes and Aoi
//     posts the game card.
//
// Anything else (an engineer that ran out of steps, a daily review, a change
// mid-build) goes to the generic replanner.
func planHook(ctx context.Context, p *engine.Planner, g *engine.Goal, t *engine.Task) (bool, error) {
	if t.Kind == "finish" {
		return finish(ctx, p, g, t)
	}
	switch t.Spec.Mode {
	case "replan":
		return reviseOnFailure(ctx, p, g, t)
	case "change":
		// The owner's own word is the decision the limit waits for.
		if g.Replans >= g.Limits.MaxReplans {
			g.Limits.MaxReplans = g.Replans + 1
			if err := p.Store.SetLimits(ctx, g.ID, g.Limits); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

// trigger is the gate that failed and why.
type trigger struct {
	task     *engine.Task
	findings string
	short    string
}

func reviseOnFailure(ctx context.Context, p *engine.Planner, g *engine.Goal, t *engine.Task) (bool, error) {
	all, err := p.Store.Tasks(ctx, g.ID)
	if err != nil {
		return false, err
	}
	tr, err := failedGate(ctx, p.Store, g, all)
	if err != nil || tr == nil {
		return false, err
	}
	if g.Replans >= g.Limits.MaxReplans {
		extended, err := extendForBigRules(ctx, p, g)
		if err != nil {
			return false, err
		}
		if !extended {
			return true, escalate(ctx, p, g, t, tr)
		}
	}
	round := 2
	var supersede, earlier []string
	for _, x := range all {
		if strings.HasPrefix(x.Key, "revise-") {
			round++
			if f := findingsOf(x.Spec.Instructions); f != "" {
				earlier = append(earlier, f)
			}
		}
		// Everything downstream of the failed gate is still blocked on it.
		if x.Status == "blocked" && x.Kind != "plan" && x.Kind != "finish" {
			supersede = append(supersede, x.Key)
		}
	}
	supersede = append(supersede, tr.task.Key)
	return true, p.ApplyEdit(ctx, g, t, engine.PlanEdit{
		Supersede: supersede,
		Add:       revisionRound(round, tr.findings, earlier),
		Why:       fmt.Sprintf("%s; revision round %d", tr.short, round),
		Data:      map[string]any{"round": round, "trigger": tr.task.Key},
	})
}

// bigRulesReplans is the revision budget of a big ruleset: a deck of
// individually written cards fails review one card at a time, and each
// round fixes a few.
const (
	bigRulesReplans = 8
	bigRulesRunes   = 2500
)

// extendForBigRules raises the revision limit, once, when the rules
// document is long enough that four rounds are not a fair test.
func extendForBigRules(ctx context.Context, p *engine.Planner, g *engine.Goal) (bool, error) {
	if g.Limits.MaxReplans >= bigRulesReplans {
		return false, nil
	}
	game, err := loadGame(ctx, p.Store.Pool, g.GameID)
	if err != nil || utf8.RuneCountInString(game.RulesMD) < bigRulesRunes {
		return false, nil
	}
	g.Limits.MaxReplans = bigRulesReplans
	if need := 6 + 4*bigRulesReplans; g.Limits.MaxTotalTasks < need {
		g.Limits.MaxTotalTasks = need
	}
	return true, p.Store.SetLimits(ctx, g.ID, g.Limits)
}

// findingsOf is the findings block of an earlier revise task's instructions.
func findingsOf(instructions string) string {
	i := strings.Index(instructions, findingsHeader)
	if i < 0 {
		return ""
	}
	f := instructions[i+len(findingsHeader):]
	if j := strings.Index(f, earlierHeader); j >= 0 {
		f = f[:j]
	}
	return strings.TrimSpace(f)
}

// failedGate finds the newest failed playtest or critic task that failed
// because of its verdict (not because of an outage), with its findings.
func failedGate(ctx context.Context, s *engine.Store, g *engine.Goal, all []*engine.Task) (*trigger, error) {
	for i := len(all) - 1; i >= 0; i-- {
		x := all[i]
		if x.Status != "failed" {
			continue
		}
		switch x.Spec.Role {
		case RoleArtist:
			// The cover never gates anything; look past it.
			continue
		case RolePlaytester:
			var raw []byte
			err := s.Pool.QueryRow(ctx, `SELECT output FROM tool_calls WHERE task_id=$1 AND tool=$2 AND status='succeeded' ORDER BY id DESC LIMIT 1`,
				x.ID, ToolPlaytest).Scan(&raw)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil // the playtest never ran: an outage, not a verdict
			}
			if err != nil {
				return nil, err
			}
			var out struct {
				Pass    bool     `json:"pass"`
				Reasons []string `json:"reasons"`
				Version int      `json:"version"`
			}
			_ = json.Unmarshal(raw, &out)
			if out.Pass {
				return nil, nil
			}
			return &trigger{task: x, findings: playtestFindings(ctx, s, g, out.Version, out.Reasons),
				short: fmt.Sprintf("the playtest of version %d failed", out.Version)}, nil
		case RoleCritic:
			var raw []byte
			err := s.Pool.QueryRow(ctx, `SELECT input FROM tool_calls WHERE task_id=$1 AND tool=$2 AND status='succeeded' ORDER BY id DESC LIMIT 1`,
				x.ID, ToolSubmitReview).Scan(&raw)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			var in struct {
				Verdict  string    `json:"verdict"`
				Summary  string    `json:"summary"`
				Findings []finding `json:"findings"`
			}
			_ = json.Unmarshal(raw, &in)
			if in.Verdict != "revise" {
				return nil, nil
			}
			return &trigger{task: x, findings: reviewMarkdown(in.Verdict, in.Summary, in.Findings), short: "the critic asked for a revision"}, nil
		default:
			// The newest failure is something else: not ours to decide.
			return nil, nil
		}
	}
	return nil, nil
}

// playtestFindings turns a failed playtest into instructions: the verdict's
// reasons and the first replayable errors with the moves that led there.
func playtestFindings(ctx context.Context, s *engine.Store, g *engine.Goal, ver int, reasons []string) string {
	var b strings.Builder
	b.WriteString("The playtest FAILED:\n")
	for _, r := range reasons {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	var raw []byte
	_ = s.Pool.QueryRow(ctx, `SELECT coalesce(report->'playtest'->'errors','[]') FROM game_versions WHERE game_id=$1 AND version=$2`, g.GameID, ver).Scan(&raw)
	var errs []struct {
		Game      int             `json:"game"`
		Seats     int             `json:"seats"`
		MoveIndex int             `json:"move_index"`
		Seat      int             `json:"seat"`
		Class     string          `json:"class"`
		Message   string          `json:"message"`
		Move      json.RawMessage `json:"move"`
		History   json.RawMessage `json:"history"`
	}
	_ = json.Unmarshal(raw, &errs)
	for i, e := range errs {
		if i == 3 {
			break
		}
		fmt.Fprintf(&b, "\nFailing game %d (%d seats), move %d by seat %d — %s: %s\n", e.Game, e.Seats, e.MoveIndex, e.Seat, e.Class, e.Message)
		if len(e.Move) > 0 && string(e.Move) != "null" {
			fmt.Fprintf(&b, "Move: %s\n", truncate(string(e.Move), 300))
		}
		if len(e.History) > 0 && string(e.History) != "null" {
			fmt.Fprintf(&b, "Moves before it (seat, move): %s\n", truncate(string(e.History), 1500))
		}
	}
	return b.String()
}

// escalate stops the loop: Aoi tells the owner she needs a decision and the
// goal waits in needs_attention. A change from the owner (chat) grants the
// next round; resuming re-checks the limit.
func escalate(ctx context.Context, p *engine.Planner, g *engine.Goal, t *engine.Task, tr *trigger) error {
	if err := p.Store.Complete(ctx, t, map[string]any{"summary": "revision limit reached: " + tr.short}); err != nil {
		return err
	}
	name := g.Title
	if game, err := loadGame(ctx, p.Store.Pool, g.GameID); err == nil {
		name = game.Name
	}
	var msg, reason string
	why := localShort(tr, g.Language)
	switch g.Language {
	case "zh":
		msg = fmt.Sprintf("《%s》需要你来拿主意：工作室已经修改了 %d 轮，可还是没过关（%s）。告诉我想怎么改，比如简化某条规则，我就让大家再来一轮；也可以先取消这次制作。",
			name, g.Replans, why)
		reason = fmt.Sprintf("修改了 %d 轮仍未通过（%s），需要你的决定", g.Replans, why)
	case "ja":
		msg = fmt.Sprintf("「%s」について、あなたの判断が必要なの。スタジオで %d 回直したけど、まだ通らなくて（%s）。どこを変えたいか教えてね——ルールを一つシンプルにするだけで通ることも多いよ。もう一回チームで挑戦するか、今回の制作をキャンセルしてもOK。",
			name, g.Replans, why)
		reason = fmt.Sprintf("%d 回修正しても未通過（%s）。オーナーの判断が必要です", g.Replans, why)
	case "ko":
		msg = fmt.Sprintf("“%s”에 대해 결정이 필요해요. 스튜디오에서 %d번이나 고쳤는데 아직 통과하지 못했어요(%s). 어떻게 바꾸고 싶은지 알려 주세요 — 규칙 하나만 단순하게 바꿔도 통과하는 경우가 많아요. 팀이 한 번 더 도전하거나, 이번 제작을 취소할 수도 있어요.",
			name, g.Replans, why)
		reason = fmt.Sprintf("%d번 수정했지만 아직 통과하지 못함(%s). 주인의 결정이 필요해요", g.Replans, why)
	default:
		msg = fmt.Sprintf("I need your call on “%s”: the studio has been through %d revision rounds and it still doesn't pass (%s). Tell me what to change — simplifying a rule often does it — and the team will go another round, or cancel the build.",
			name, g.Replans, why)
		reason = fmt.Sprintf("still failing after %d revision rounds (%s); the owner needs to decide", g.Replans, why)
	}
	p.Store.PostMessage(ctx, g.ConversationID, g.UserID, msg, map[string]any{"intent": Skill, "mood": "sad", "goal_id": g.ID,
		"kind": "attention", "cards": []map[string]any{{"kind": "mission", "goal_id": g.ID}}}, "studio-escalate-"+t.ID)
	return p.Store.SetGoalStatus(ctx, g.ID, "needs_attention", reason)
}

// localShort says, in the owner's language, which gate keeps failing.
func localShort(tr *trigger, lang string) string {
	critic := tr.task.Spec.Role == RoleCritic
	switch lang {
	case "zh":
		if critic {
			return "评审要求修改"
		}
		return "试玩没有通过"
	case "ja":
		if critic {
			return "クリティックが修正を求めています"
		}
		return "テストプレイに通りませんでした"
	case "ko":
		if critic {
			return "크리틱이 수정을 요청했어요"
		}
		return "플레이테스트를 통과하지 못했어요"
	}
	return tr.short
}

// finish completes a build once the owner decided on publishing: published,
// or kept as a draft. Aoi posts the game card with a warm line in the
// owner's language. Undecided builds go to the generic judge.
func finish(ctx context.Context, p *engine.Planner, g *engine.Goal, t *engine.Task) (bool, error) {
	var published, declined bool
	if err := p.Store.Pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM tool_calls WHERE goal_id=$1 AND tool=$2 AND status='succeeded'),
		EXISTS(SELECT 1 FROM approvals WHERE goal_id=$1 AND tool=$2 AND status='rejected')`, g.ID, ToolPublish).Scan(&published, &declined); err != nil {
		return false, err
	}
	if !published && !declined {
		return false, nil
	}
	game, err := loadGame(ctx, p.Store.Pool, g.GameID)
	if err != nil {
		return false, err
	}
	outcome := "published"
	if !published {
		outcome = "kept as a draft"
	}
	if err := p.Store.Complete(ctx, t, map[string]any{"summary": fmt.Sprintf("%s: %s", game.Name, outcome)}); err != nil {
		return true, err
	}
	line := announce(ctx, p, g, game.Name, published)
	p.Store.PostMessage(ctx, g.ConversationID, g.UserID, line, map[string]any{"intent": Skill, "mood": "smile", "goal_id": g.ID,
		"kind": "complete", "cards": []map[string]any{{"kind": "game", "game_id": game.ID}}}, "studio-finish-"+t.ID)
	why := "the owner approved publishing"
	if !published {
		why = "the owner kept the game as a draft"
	}
	return true, p.Store.SetGoalStatus(ctx, g.ID, "completed", why)
}

// announce is Aoi's one-liner for the game card, in the owner's language.
// The model writes it; a fixed line stands in if the model is unavailable.
func announce(ctx context.Context, p *engine.Planner, g *engine.Goal, name string, published bool) string {
	what := "is now published on the shelf: anyone can sit down and play it"
	if !published {
		what = "is finished and kept as a private draft: only the owner can play it for now"
	}
	resp, err := p.Model.Chat(ctx, engine.CallMeta{Purpose: "announce", UserID: g.UserID, GoalID: g.ID},
		llm.Request{Model: p.LLM, Temperature: 0.8, MaxTokens: 160, Messages: []llm.Message{
			{Role: "system", Content: persona.Soul},
			{Role: "user", Content: fmt.Sprintf("The player's game %q %s. Write ONE warm, playful sentence (at most 30 words) in %s telling them, and invite them to a table. Plain text, no Markdown, no quotes around it.",
				name, what, persona.LangName(g.Language))}}})
	if err == nil {
		if s := strings.Trim(strings.TrimSpace(resp.Message.Content), `"“”`); s != "" && len([]rune(s)) < 400 {
			return s
		}
	}
	switch {
	case g.Language == "zh" && published:
		return "《" + name + "》上架啦！快来开一桌，我第一个坐下～"
	case g.Language == "zh":
		return "《" + name + "》做好了，先作为草稿留着——随时可以开一桌试试！"
	case g.Language == "ja" && published:
		return "「" + name + "」、棚に並んだよ！さっそく一卓どう？最初の一手はわたしがもらうね～"
	case g.Language == "ja":
		return "「" + name + "」ができたよ。下書きとして保存してあるから、いつでも試しに一卓どうぞ！"
	case g.Language == "ko" && published:
		return "“" + name + "”이(가) 진열대에 올라갔어요! 한 판 해요, 첫 수는 제가 할게요~"
	case g.Language == "ko":
		return "“" + name + "” 완성! 초안으로 저장해 뒀어요 — 언제든 한 판 시험해 봐요!"
	case published:
		return "“" + name + "” is on the shelf! Pull up a chair — I call first move."
	default:
		return "“" + name + "” is ready and saved as your draft — deal me in whenever you like!"
	}
}
