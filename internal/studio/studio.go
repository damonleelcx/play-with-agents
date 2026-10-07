// Package studio is the game studio: a person describes a board game to Aoi,
// and a team of specialised agents designs it (Designer), builds it as a
// sandboxed JavaScript module (Engineer), plays it hundreds of times
// (Playtester, deterministic), reviews it independently (Critic) and, once
// the owner approves, publishes it (Coordinator).
//
// A build is a durable engine goal (skill build_game) linked to a games row.
// Everything here is registered with the engine, tools and skills registries
// when the package is imported; Service is the agent.Studio the chat uses.
package studio

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/agent"
	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/persona"
)

// Skill is the playbook name; Domain the goal domain.
const (
	Skill  = "build_game"
	Domain = "studio"
)

// Limits bound one build. Four revision rounds, then the owner decides; a
// big ruleset (a deck of individually written cards) earns up to
// bigRulesReplans rounds first (hook.go). The iteration, token and task
// budgets are sized for that longer case.
var Limits = engine.Limits{
	MaxIterations: 300, MaxToolCalls: 500, MaxTokens: 3_000_000, MaxCostUSD: 15,
	MaxDays: 2, MaxDepth: 6, MaxTasksPerPlan: 10, MaxReplans: 4, MaxTotalTasks: 48,
}

// Service starts builds. It implements agent.Studio.
type Service struct {
	Pool  *pgxpool.Pool
	Store *engine.Store
}

var _ agent.Studio = (*Service)(nil)

const maxPrompt = 4000

// revisePrefix starts the objective of a build that revises a game.
const revisePrefix = "Revise the game"

// StartBuild creates the game row (status building, or keeps a revised game
// as it is) and the build goal, linked both ways, in one transaction. With
// baseGameID the build revises that game: it starts from its rules and
// source and produces a new version of the same game.
func (s *Service) StartBuild(ctx context.Context, userID, convID, prompt, baseGameID, lang string) (string, string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", "", fmt.Errorf("%w: describe the game you want built", agent.ErrRejected)
	}
	if r := []rune(prompt); len(r) > maxPrompt {
		prompt = string(r[:maxPrompt])
	}
	prefs := s.prefs(ctx, userID)
	if lang == "" {
		lang, _ = prefs["language"].(string)
	}
	lang = persona.Normalize(lang)
	vis, _ := prefs["studio_visibility"].(string)
	if vis != "unlisted" && vis != "public" {
		vis = "private"
	}

	g := &engine.Goal{UserID: userID, ConversationID: convID, Domain: Domain, Skill: Skill, Language: lang,
		Limits: buildLimits(prefs), Criteria: criteria(lang), Milestones: milestones(lang)}
	var gameID string
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if baseGameID != "" {
			var owner, kind, name string
			err := tx.QueryRow(ctx, `SELECT coalesce(owner_id::text,''), kind, name FROM games WHERE id=$1 FOR UPDATE`, baseGameID).Scan(&owner, &kind, &name)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && (owner != userID || kind != "script")) {
				return fmt.Errorf("%w: you can only revise a game you made", agent.ErrRejected)
			}
			if err != nil {
				return err
			}
			var busy bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goals WHERE game_id=$1 AND status IN ('planning','active','paused','needs_attention'))`,
				baseGameID).Scan(&busy); err != nil {
				return err
			}
			if busy {
				return fmt.Errorf("%w: %s is already being built; send the change to that build instead", agent.ErrRejected, name)
			}
			gameID = baseGameID
			g.Title = buildTitle(name, lang, true)
			g.Objective = fmt.Sprintf("%s %q (id %s). The owner asks: %s", revisePrefix, name, baseGameID, prompt)
		} else {
			name := provisionalName(prompt)
			gameID = newGameID(name)
			g.Title = buildTitle(name, lang, false)
			g.Objective = prompt
			if _, err := tx.Exec(ctx, `INSERT INTO games (id, owner_id, kind, name, summary, status, visibility)
				VALUES ($1, $2, 'script', $3, '', 'building', $4)`, gameID, userID, name, vis); err != nil {
				return err
			}
		}
		g.GameID = gameID
		if err := engine.CreateGoalTx(ctx, tx, g); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE games SET goal_id=$2, updated_at=now() WHERE id=$1`, gameID, g.ID)
		return err
	})
	if err != nil {
		return "", "", err
	}
	return g.ID, gameID, nil
}

// buildLimits is Limits tightened by the owner's own ceilings
// (goal_max_cost_usd, goal_max_days). A preference can only lower the
// studio's maxima, never raise them; a missing or malformed one is ignored.
func buildLimits(prefs map[string]any) engine.Limits {
	l := Limits
	if v, ok := prefNumber(prefs, "goal_max_cost_usd"); ok && v >= 1 {
		l.MaxCostUSD = min(l.MaxCostUSD, v)
	}
	if v, ok := prefNumber(prefs, "goal_max_days"); ok && v >= 1 {
		l.MaxDays = min(l.MaxDays, int(v))
	}
	return l
}

// prefNumber reads a numeric preference stored as a JSON number or a
// numeric string.
func prefNumber(prefs map[string]any, key string) (float64, bool) {
	switch v := prefs[key].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

func (s *Service) prefs(ctx context.Context, userID string) map[string]any {
	var raw []byte
	_ = s.Pool.QueryRow(ctx, `SELECT data FROM user_preferences WHERE user_id=$1`, userID).Scan(&raw)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func criteria(lang string) []string {
	switch lang {
	case "ja":
		return []string{
			"ルール文書が保存されている：準備、手番、合法手、得点、終了とタイブレーク、例外ケース",
			"ゲームモジュールが契約チェックをエラーなしで通過している",
			"モジュールがテストプレイ関門を通過：全人数で数百局のシミュレーション、エラーなし、全局が終了する",
			"独立したクリティックがモジュールを承認している（判定：合格）",
			"オーナーが公開を承認した（または下書きとして残すことを選んだ）",
		}
	case "ko":
		return []string{
			"규칙 문서 저장 완료: 준비, 턴, 가능한 수, 점수, 종료와 동점 처리, 예외 상황",
			"게임 모듈이 계약 검사를 오류 없이 통과",
			"모듈이 플레이테스트 관문 통과: 모든 인원수에서 수백 판 시뮬레이션, 오류 없음, 모든 판이 끝남",
			"독립 크리틱이 모듈을 승인(판정: 통과)",
			"주인이 공개를 승인함(또는 초안으로 두기로 함)",
		}
	}
	if lang == "zh" {
		return []string{
			"规则文档已保存：配置、回合、合法走法、计分、结束与平局判定、边界情况",
			"游戏模块通过契约检查，没有错误",
			"模块通过试玩关卡：数百局模拟、每种人数、无错误、每局都能结束",
			"独立评审认可该模块（结论为通过）",
			"主人批准发布（或选择保留为草稿）",
		}
	}
	return []string{
		"The rules document is saved: setup, turns, legal moves, scoring, end and tie-breaks, edge cases",
		"The game module passes the contract check with no errors",
		"The module passes the playtest gate: hundreds of simulated games at every seat count, no errors, every game ends",
		"The independent critic accepts the module (verdict pass)",
		"The owner approved publishing (or chose to keep it as a draft)",
	}
}

func milestones(lang string) []string {
	switch lang {
	case "ja":
		return []string{"ルール", "モジュール", "テストプレイ", "レビュー", "公開"}
	case "ko":
		return []string{"규칙", "모듈", "플레이테스트", "리뷰", "공개"}
	}
	if lang == "zh" {
		return []string{"规则", "模块", "试玩", "评审", "发布"}
	}
	return []string{"Rules", "Module", "Playtest", "Review", "Publish"}
}

// buildTitle names the mission after the game. (The owner's email quotes
// it, so it carries no quotes of its own.)
func buildTitle(name, lang string, revise bool) string {
	switch {
	case lang == "zh" && revise:
		return name + "（修改）"
	case lang == "ja" && revise:
		return name + "（修正）"
	case lang == "ko" && revise:
		return name + " (수정)"
	case revise:
		return name + " (revision)"
	default:
		return name
	}
}

// provisionalName is the game's name until the Designer names it: the first
// words of the idea.
func provisionalName(prompt string) string {
	line := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	words := strings.Fields(line)
	var b strings.Builder
	for _, w := range words {
		if b.Len() > 0 && len([]rune(b.String()+" "+w)) > 36 {
			b.WriteString("…")
			break
		}
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(w)
	}
	name := b.String()
	if r := []rune(name); len(r) > 40 {
		name = string(r[:36]) + "…"
	}
	if name == "" {
		name = "New game"
	}
	r := []rune(name)
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}

var reGameID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)

// newGameID makes a catalog id from the name plus a random suffix.
func newGameID(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 24 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "game"
	}
	id := slug + "-" + randSuffix(5)
	if !reGameID.MatchString(id) {
		id = "game-" + randSuffix(8)
	}
	return id
}

func randSuffix(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	out := make([]byte, n)
	for i := range out {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		out[i] = alphabet[k.Int64()]
	}
	return string(out)
}
