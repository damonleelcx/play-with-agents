package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/llm"
)

// Limits bound one goal. Hitting any of them moves the goal to
// needs_attention — it never runs on, and it never fails silently.
type Limits struct {
	MaxIterations   int     `json:"max_iterations"` // LLM steps across all tasks
	MaxToolCalls    int     `json:"max_tool_calls"`
	MaxTokens       int     `json:"max_tokens"`
	MaxCostUSD      float64 `json:"max_cost_usd"`
	MaxDays         int     `json:"max_days"`  // wall clock since creation
	MaxDepth        int     `json:"max_depth"` // DAG depth
	MaxTasksPerPlan int     `json:"max_tasks_per_plan"`
	MaxReplans      int     `json:"max_replans"`
	MaxTotalTasks   int     `json:"max_total_tasks"` // recursion guard across replans
}

func (l Limits) WithDefaults() Limits {
	d := Limits{MaxIterations: 200, MaxToolCalls: 500, MaxTokens: 2_000_000, MaxCostUSD: 20, MaxDays: 30,
		MaxDepth: 6, MaxTasksPerPlan: 20, MaxReplans: 10, MaxTotalTasks: 80}
	if l.MaxIterations == 0 {
		l.MaxIterations = d.MaxIterations
	}
	if l.MaxToolCalls == 0 {
		l.MaxToolCalls = d.MaxToolCalls
	}
	if l.MaxTokens == 0 {
		l.MaxTokens = d.MaxTokens
	}
	if l.MaxCostUSD == 0 {
		l.MaxCostUSD = d.MaxCostUSD
	}
	if l.MaxDays == 0 {
		l.MaxDays = d.MaxDays
	}
	if l.MaxDepth == 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxTasksPerPlan == 0 {
		l.MaxTasksPerPlan = d.MaxTasksPerPlan
	}
	if l.MaxReplans == 0 {
		l.MaxReplans = d.MaxReplans
	}
	if l.MaxTotalTasks == 0 {
		l.MaxTotalTasks = d.MaxTotalTasks
	}
	return l
}

type Usage struct {
	Iterations       int     `json:"iterations"`
	ToolCalls        int     `json:"tool_calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	// Images counts image-model calls (the studio's cover art); their cost
	// is in CostUSD.
	Images int `json:"images,omitempty"`
}

func (u Usage) Tokens() int { return u.PromptTokens + u.CompletionTokens }

// Exceeded names the first limit this usage breaks, or "".
func (g *Goal) Exceeded() string {
	l, u := g.Limits, g.Usage
	switch {
	case u.Iterations >= l.MaxIterations:
		return fmt.Sprintf("iteration limit reached (%d)", l.MaxIterations)
	case u.ToolCalls >= l.MaxToolCalls:
		return fmt.Sprintf("tool-call limit reached (%d)", l.MaxToolCalls)
	case u.Tokens() >= l.MaxTokens:
		return fmt.Sprintf("token limit reached (%d)", l.MaxTokens)
	case u.CostUSD >= l.MaxCostUSD:
		return fmt.Sprintf("cost limit reached ($%.2f)", l.MaxCostUSD)
	case time.Since(g.CreatedAt) > time.Duration(l.MaxDays)*24*time.Hour:
		return fmt.Sprintf("time limit reached (%d days)", l.MaxDays)
	}
	return ""
}

// ErrBudget stops work on a goal; the worker parks the goal for a person.
type ErrBudget struct{ Reason string }

func (e *ErrBudget) Error() string { return "budget exhausted: " + e.Reason }

// Rates for the cost estimate, USD per million tokens. The token plan is
// prepaid, so this is a spending signal, not an invoice.
var (
	CostPerMIn  = 0.8
	CostPerMOut = 3.2
)

// Model wraps the LLM client with accounting: every call is budget-checked
// before it happens and recorded after, against the goal, the account and the
// deployment.
type Model struct {
	Client                *llm.Client
	Store                 *Store
	AccountDailyTokens    int
	DeploymentDailyTokens int
}

type CallMeta struct {
	Purpose, UserID, GoalID, TaskID string
	CountIteration                  bool
}

func (m *Model) Chat(ctx context.Context, meta CallMeta, req llm.Request) (*llm.Response, error) {
	if err := m.preflight(ctx, meta); err != nil {
		return nil, err
	}
	resp, err := m.Client.Chat(ctx, req)
	m.record(ctx, meta, req.Model, resp, err)
	return resp, err
}

func (m *Model) Stream(ctx context.Context, meta CallMeta, req llm.Request, onDelta func(string)) (*llm.Response, error) {
	if err := m.preflight(ctx, meta); err != nil {
		return nil, err
	}
	resp, err := m.Client.Stream(ctx, req, onDelta)
	m.record(ctx, meta, req.Model, resp, err)
	return resp, err
}

func (m *Model) preflight(ctx context.Context, meta CallMeta) error {
	if meta.GoalID != "" {
		g, err := m.Store.Goal(ctx, meta.GoalID)
		if err != nil {
			return err
		}
		if r := g.Exceeded(); r != "" {
			return &ErrBudget{r}
		}
	}
	var acct, all int64
	err := m.Store.Pool.QueryRow(ctx, `SELECT
		coalesce(sum(prompt_tokens+completion_tokens) FILTER (WHERE user_id = nullif($1,'')::uuid), 0),
		coalesce(sum(prompt_tokens+completion_tokens), 0)
		FROM llm_calls WHERE created_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, meta.UserID).Scan(&acct, &all)
	if err != nil {
		return err
	}
	if m.AccountDailyTokens > 0 && meta.UserID != "" && acct >= int64(m.AccountDailyTokens) {
		return &ErrBudget{"this account's daily allowance is used up; it resets at 00:00 UTC"}
	}
	if m.DeploymentDailyTokens > 0 && all >= int64(m.DeploymentDailyTokens) {
		return &ErrBudget{"the service's daily capacity is used up; it resets at 00:00 UTC"}
	}
	return nil
}

func (m *Model) record(ctx context.Context, meta CallMeta, model string, resp *llm.Response, err error) {
	c := context.WithoutCancel(ctx)
	var pt, ct, lat int
	msg := ""
	if resp != nil {
		pt, ct, lat = resp.Usage.PromptTokens, resp.Usage.CompletionTokens, int(resp.Latency.Milliseconds())
		if resp.Model != "" {
			model = resp.Model
		}
	}
	if err != nil {
		msg = err.Error()
	}
	_, _ = m.Store.Pool.Exec(c, `INSERT INTO llm_calls (user_id, goal_id, task_id, purpose, model, prompt_tokens, completion_tokens, latency_ms, error)
		VALUES (nullif($1,'')::uuid, nullif($2,'')::uuid, nullif($3,'')::uuid, $4, $5, $6, $7, $8, $9)`,
		meta.UserID, meta.GoalID, meta.TaskID, meta.Purpose, model, pt, ct, lat, msg)
	if meta.GoalID != "" {
		cost := float64(pt)/1e6*CostPerMIn + float64(ct)/1e6*CostPerMOut
		it := 0
		if meta.CountIteration {
			it = 1
		}
		_ = m.Store.AddUsage(c, meta.GoalID, Usage{Iterations: it, PromptTokens: pt, CompletionTokens: ct, CostUSD: cost})
	}
}

// AddUsage increments counters atomically in SQL, so concurrent workers on
// one goal never lose each other's increments.
func (s *Store) AddUsage(ctx context.Context, goalID string, d Usage) error {
	_, err := s.Pool.Exec(ctx, `UPDATE goals SET usage = jsonb_build_object(
		'iterations',        coalesce((usage->>'iterations')::int,0) + $2,
		'tool_calls',        coalesce((usage->>'tool_calls')::int,0) + $3,
		'prompt_tokens',     coalesce((usage->>'prompt_tokens')::int,0) + $4,
		'completion_tokens', coalesce((usage->>'completion_tokens')::int,0) + $5,
		'cost_usd',          coalesce((usage->>'cost_usd')::float,0) + $6,
		'images',            coalesce((usage->>'images')::int,0) + $7) WHERE id=$1`,
		goalID, d.Iterations, d.ToolCalls, d.PromptTokens, d.CompletionTokens, d.CostUSD, d.Images)
	return err
}
