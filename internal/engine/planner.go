package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/persona"
	"github.com/damonleelcx/play-with-agents/internal/skills"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

// PlannedTask is the planner's contract with the model. Everything the model
// proposes is validated against it before a single row is written.
type PlannedTask struct {
	Key          string  `json:"key"`
	Title        string  `json:"title"`
	Role         string  `json:"role,omitempty"`
	Instructions string  `json:"instructions"`
	Tools        strList `json:"tools"`
	Deps         strList `json:"deps"`
	Verify       strList `json:"verify"`
	WaitDays     flexNum `json:"wait_days"`
	MaxSteps     int     `json:"max_steps,omitempty"`
	// Tool + Args make a deterministic tool task (no model).
	Tool string         `json:"tool,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

// strList accepts ["a","b"], "a", "a, b" or null — models are inconsistent
// about list fields, and a shape quibble should not cost a whole replan.
type strList []string

func (l *strList) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*l = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		*l = nil
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	*l = out
	return nil
}

// flexNum accepts 7, 7.0 or "7".
type flexNum float64

func (n *flexNum) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		*n = flexNum(f)
		return nil
	}
	var s string
	_ = json.Unmarshal(b, &s)
	fmt.Sscan(s, &f)
	*n = flexNum(f)
	return nil
}

type Planner struct {
	Store *Store
	Model *Model
	LLM   string // model id
}

var reKey = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,40}$`)

// fromSkill is the deterministic fallback plan: the template as written.
func fromSkill(sk *skills.Skill) []PlannedTask {
	out := make([]PlannedTask, 0, len(sk.Steps))
	for _, s := range sk.Steps {
		out = append(out, PlannedTask{Key: s.Key, Title: s.Title, Role: s.Role, Instructions: s.Instructions, Tools: s.Tools,
			Deps: s.Deps, Verify: s.Verify, WaitDays: flexNum(s.WaitDays), MaxSteps: s.MaxSteps, Tool: s.Tool, Args: s.Args})
	}
	return out
}

// validate checks a proposed set of tasks against existing ones. It returns
// the depth of each new task.
func validate(proposed []PlannedTask, existing map[string]int, lim Limits) (map[string]int, error) {
	if len(proposed) == 0 {
		return nil, fmt.Errorf("no tasks")
	}
	if len(proposed) > lim.MaxTasksPerPlan {
		return nil, fmt.Errorf("%d tasks exceeds the per-plan limit of %d", len(proposed), lim.MaxTasksPerPlan)
	}
	if len(proposed)+len(existing) > lim.MaxTotalTasks {
		return nil, fmt.Errorf("goal would have %d tasks; limit is %d", len(proposed)+len(existing), lim.MaxTotalTasks)
	}
	byKey := map[string]PlannedTask{}
	for _, p := range proposed {
		if !reKey.MatchString(p.Key) {
			return nil, fmt.Errorf("bad task key %q", p.Key)
		}
		if _, dup := byKey[p.Key]; dup {
			return nil, fmt.Errorf("duplicate key %q", p.Key)
		}
		if _, dup := existing[p.Key]; dup {
			return nil, fmt.Errorf("key %q already exists", p.Key)
		}
		if strings.TrimSpace(p.Title) == "" {
			return nil, fmt.Errorf("task %q has no title", p.Key)
		}
		if p.WaitDays < 0 || p.WaitDays > 60 {
			return nil, fmt.Errorf("task %q waits %v days", p.Key, p.WaitDays)
		}
		if p.Tool != "" {
			// A tool task runs unattended, so it may only use a tool that
			// needs nobody's approval.
			tl, ok := tools.Get(p.Tool)
			if !ok {
				return nil, fmt.Errorf("task %q runs unknown tool %q", p.Key, p.Tool)
			}
			if tl.Gate != tools.G0 || tl.GateFor != nil {
				return nil, fmt.Errorf("task %q: tool %q needs approval and cannot run as a tool task", p.Key, p.Tool)
			}
			if p.WaitDays != 0 {
				return nil, fmt.Errorf("task %q cannot both run a tool and wait", p.Key)
			}
		} else if p.WaitDays == 0 && strings.TrimSpace(p.Instructions) == "" {
			return nil, fmt.Errorf("task %q has no instructions", p.Key)
		}
		for _, t := range p.Tools {
			if _, ok := tools.Get(t); !ok {
				return nil, fmt.Errorf("task %q uses unknown tool %q", p.Key, t)
			}
		}
		for _, v := range p.Verify {
			if _, ok := lookupVerifier(v); !ok {
				return nil, fmt.Errorf("task %q has unknown verifier %q", p.Key, v)
			}
		}
		byKey[p.Key] = p
	}
	depth := map[string]int{}
	var visit func(k string, stack map[string]bool) (int, error)
	visit = func(k string, stack map[string]bool) (int, error) {
		if d, ok := existing[k]; ok {
			return d, nil
		}
		if d, ok := depth[k]; ok {
			return d, nil
		}
		p, ok := byKey[k]
		if !ok {
			return 0, fmt.Errorf("dependency %q does not exist", k)
		}
		if stack[k] {
			return 0, fmt.Errorf("dependency cycle through %q", k)
		}
		stack[k] = true
		d := 0
		for _, dep := range p.Deps {
			dd, err := visit(dep, stack)
			if err != nil {
				return 0, err
			}
			if dd+1 > d {
				d = dd + 1
			}
		}
		delete(stack, k)
		if d > lim.MaxDepth {
			return 0, fmt.Errorf("task %q is %d deep; limit is %d", k, d, lim.MaxDepth)
		}
		depth[k] = d
		return d, nil
	}
	for k := range byKey {
		if _, err := visit(k, map[string]bool{}); err != nil {
			return nil, err
		}
	}
	return depth, nil
}

func insertTasks(ctx context.Context, tx pgx.Tx, goalID string, planVersion int, ts []PlannedTask, depth map[string]int) error {
	for _, p := range ts {
		kind := "llm"
		switch {
		case p.Tool != "":
			kind = "tool"
		case p.WaitDays > 0 && p.Instructions == "":
			kind = "wait"
		}
		spec, _ := json.Marshal(TaskSpec{Role: p.Role, Instructions: p.Instructions, Tools: p.Tools, Verify: p.Verify,
			WaitDays: float64(p.WaitDays), MaxSteps: p.MaxSteps, Tool: p.Tool, Args: p.Args})
		if _, err := tx.Exec(ctx, `INSERT INTO tasks (goal_id, key, title, kind, spec, status, depth, plan_version)
			VALUES ($1,$2,$3,$4,$5,'blocked',$6,$7) ON CONFLICT (goal_id, key) DO NOTHING`,
			goalID, p.Key, p.Title, kind, spec, depth[p.Key], planVersion); err != nil {
			return err
		}
	}
	for _, p := range ts {
		for _, d := range p.Deps {
			if _, err := tx.Exec(ctx, `INSERT INTO task_deps (task_id, depends_on)
				SELECT a.id, b.id FROM tasks a, tasks b WHERE a.goal_id=$1 AND a.key=$2 AND b.goal_id=$1 AND b.key=$3
				ON CONFLICT DO NOTHING`, goalID, p.Key, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// completePlanTx finishes the plan task inside the planner's transaction, so
// "the plan was written" and "the plan task succeeded" are one fact.
func completePlanTx(ctx context.Context, tx pgx.Tx, t *Task, out map[string]any) error {
	raw, _ := json.Marshal(out)
	tag, err := tx.Exec(ctx, `UPDATE tasks SET status='succeeded', output=$3, error='', lease_owner=NULL, lease_expires_at=NULL,
		finished_at=now(), updated_at=now() WHERE id=$1 AND lease_epoch=$2 AND status='leased'`, t.ID, t.LeaseEpoch, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (p *Planner) system(g *Goal) string {
	return persona.WorkerSystem(g.Language, time.Now()) + `

YOU ARE PLANNING, not executing. Reply with JSON only.`
}

// Initial turns the goal's playbook into a tailored DAG.
func (p *Planner) Initial(ctx context.Context, g *Goal, t *Task) error {
	sk, ok := skills.Get(g.Skill)
	if !ok {
		sk, _ = skills.Get(skills.Fallback)
	}
	template := fromSkill(sk)
	if sk.Fixed {
		return p.instantiate(ctx, g, t, sk, template, "playbook used as written (a fixed playbook)")
	}
	tmplJSON, _ := json.MarshalIndent(template, "", " ")
	var memories []string
	if c, _ := p.Store.Client(ctx, g.UserID); c != nil && c.Prefs["memory_enabled"] != false {
		memories = p.Store.Memories(ctx, g.UserID, 10)
	}
	prompt := fmt.Sprintf(`Tailor this playbook into a plan for THIS player's request.

PLAYBOOK %q — %s
%s

SITUATION
%s

Return JSON: {"title": short mission title in the player's language, "criteria": [measurable completion criteria], "milestones": [3-5 short names], "tasks": [ {key,title,instructions,tools,deps,verify,wait_days} ]}.
Rules: keep every verify check and approval step the playbook has — never remove them (these guards in particular: %v). You may drop steps that clearly do not apply, add at most 4 steps, and make instructions specific to the request. Keys are lowercase-with-dashes. Tools must come from the playbook. At most %d tasks, depth at most %d. Task titles in the player's language (%s).`,
		sk.Name, sk.Description, tmplJSON, p.Store.RecentConversation(ctx, g.ConversationID, 10)+"\nObjective: "+g.Objective+memoLine(memories),
		guardNames(), g.Limits.MaxTasksPerPlan, g.Limits.MaxDepth, persona.LangName(g.Language))

	var plan struct {
		Title      string        `json:"title"`
		Criteria   []string      `json:"criteria"`
		Milestones []string      `json:"milestones"`
		Tasks      []PlannedTask `json:"tasks"`
	}
	why := "tailored by the planner"
	resp, err := p.Model.Chat(ctx, CallMeta{Purpose: "plan", UserID: g.UserID, GoalID: g.ID, TaskID: t.ID, CountIteration: true},
		llm.Request{Model: p.LLM, JSON: true, Temperature: 0.2, Messages: []llm.Message{
			{Role: "system", Content: p.system(g)}, {Role: "user", Content: prompt}}})
	var depth map[string]int
	if err == nil {
		err = json.Unmarshal([]byte(llm.ExtractJSON(resp.Message.Content)), &plan)
	}
	if err == nil {
		var reverted bool
		plan.Tasks = keepTemplateFields(plan.Tasks, template)
		if plan.Tasks, reverted = keepSafetySteps(plan.Tasks, template); reverted {
			why = "playbook used as written (the tailored plan dropped a guard check)"
		}
		depth, err = validate(plan.Tasks, map[string]int{}, g.Limits)
	}
	if err != nil {
		if _, isBudget := err.(*ErrBudget); isBudget {
			return err
		}
		// The model's plan is rejected, not the goal: fall back to the
		// playbook as written, which is valid by construction.
		why = "playbook used as written (planner output rejected: " + truncate(err.Error(), 200) + ")"
		plan.Tasks = template
		plan.Criteria, plan.Milestones, plan.Title = sk.Criteria, sk.Milestones, ""
		if depth, err = validate(plan.Tasks, map[string]int{}, g.Limits); err != nil {
			return fmt.Errorf("playbook %s is invalid: %w", sk.Name, err)
		}
	}
	if len(plan.Criteria) == 0 {
		plan.Criteria = sk.Criteria
	}
	if len(plan.Milestones) == 0 {
		plan.Milestones = sk.Milestones
	}
	return pgx.BeginFunc(ctx, p.Store.Pool, func(tx pgx.Tx) error {
		if err := insertTasks(ctx, tx, g.ID, 1, plan.Tasks, depth); err != nil {
			return err
		}
		crit, _ := json.Marshal(plan.Criteria)
		ms, _ := json.Marshal(plan.Milestones)
		title := g.Title
		if strings.TrimSpace(plan.Title) != "" {
			title = truncate(plan.Title, 120)
		}
		if _, err := tx.Exec(ctx, `UPDATE goals SET status='active', plan_version=1, title=$2, completion_criteria=$3, milestones=$4, updated_at=now()
			WHERE id=$1 AND status='planning'`, g.ID, title, crit, ms); err != nil {
			return err
		}
		if err := completePlanTx(ctx, tx, t, map[string]any{"summary": fmt.Sprintf("Planned %d tasks. %s", len(plan.Tasks), why)}); err != nil {
			return err
		}
		if err := promoteTx(ctx, tx, g.ID); err != nil {
			return err
		}
		keys := make([]string, len(plan.Tasks))
		for i, x := range plan.Tasks {
			keys[i] = x.Title
		}
		return eventTx(ctx, tx, g.UserID, g.ID, t.ID, "goal.planned", map[string]any{"tasks": keys, "why": why})
	})
}

// instantiate writes a playbook as written: the goal keeps the title and
// criteria it was created with (the skill's own when it has none).
func (p *Planner) instantiate(ctx context.Context, g *Goal, t *Task, sk *skills.Skill, plan []PlannedTask, why string) error {
	depth, err := validate(plan, map[string]int{}, g.Limits)
	if err != nil {
		return fmt.Errorf("playbook %s is invalid: %w", sk.Name, err)
	}
	crit := g.Criteria
	if len(crit) == 0 {
		crit = sk.Criteria
	}
	ms := g.Milestones
	if len(ms) == 0 {
		ms = sk.Milestones
	}
	return pgx.BeginFunc(ctx, p.Store.Pool, func(tx pgx.Tx) error {
		if err := insertTasks(ctx, tx, g.ID, 1, plan, depth); err != nil {
			return err
		}
		cj, _ := json.Marshal(crit)
		mj, _ := json.Marshal(ms)
		if _, err := tx.Exec(ctx, `UPDATE goals SET status='active', plan_version=1, completion_criteria=$2, milestones=$3, updated_at=now()
			WHERE id=$1 AND status='planning'`, g.ID, cj, mj); err != nil {
			return err
		}
		if err := completePlanTx(ctx, tx, t, map[string]any{"summary": fmt.Sprintf("Planned %d tasks. %s", len(plan), why)}); err != nil {
			return err
		}
		if err := promoteTx(ctx, tx, g.ID); err != nil {
			return err
		}
		titles := make([]string, len(plan))
		for i, x := range plan {
			titles[i] = x.Title
		}
		return eventTx(ctx, tx, g.UserID, g.ID, t.ID, "goal.planned", map[string]any{"tasks": titles, "why": why})
	})
}

// keepTemplateFields carries what a model tends to drop when it rewrites a
// step — its role, tool, arguments and step budget — over from the playbook
// step with the same key.
func keepTemplateFields(proposed, template []PlannedTask) []PlannedTask {
	byKey := map[string]PlannedTask{}
	for _, x := range template {
		byKey[x.Key] = x
	}
	for i, x := range proposed {
		tm, ok := byKey[x.Key]
		if !ok {
			continue
		}
		if x.Role == "" {
			proposed[i].Role = tm.Role
		}
		if x.Tool == "" && tm.Tool != "" {
			proposed[i].Tool, proposed[i].Args = tm.Tool, tm.Args
		}
		if x.MaxSteps == 0 {
			proposed[i].MaxSteps = tm.MaxSteps
		}
	}
	return proposed
}

// keepSafetySteps restores the guard verifiers the model dropped. A plan may
// be reshaped; its safety properties may not.
func keepSafetySteps(proposed, template []PlannedTask) ([]PlannedTask, bool) {
	need := map[string]bool{}
	for _, t := range template {
		for _, v := range t.Verify {
			if isGuard(v) {
				need[v] = true
			}
		}
	}
	has := map[string]bool{}
	for _, t := range proposed {
		for _, v := range t.Verify {
			has[v] = true
		}
	}
	for v := range need {
		if !has[v] {
			// Put the requirement back by reverting to the template entirely:
			// patching a model's DAG is how subtle holes get made.
			return template, true
		}
	}
	return proposed, false
}

func memoLine(m []string) string {
	if len(m) == 0 {
		return ""
	}
	return "\nKnown facts: " + strings.Join(m, "; ")
}

type replanOp struct {
	Op           string      `json:"op"` // retry | skip | cancel | update | add
	Key          string      `json:"key"`
	Instructions string      `json:"instructions"`
	Why          string      `json:"why"`
	Task         PlannedTask `json:"task"`
}

// Replan compares state to the goal and adjusts the plan. Modes: replan (a task
// failed), review (periodic), change (the player changed something), cycle
// (a recurring plan finished a cycle).
func (p *Planner) Replan(ctx context.Context, g *Goal, t *Task) error {
	if g.Replans >= g.Limits.MaxReplans {
		if err := p.Store.Complete(ctx, t, map[string]any{"summary": "replan limit reached"}); err != nil {
			return err
		}
		return p.Store.SetGoalStatus(ctx, g.ID, "needs_attention", fmt.Sprintf("re-planned %d times without finishing; a person should look", g.Replans))
	}
	all, err := p.Store.Tasks(ctx, g.ID)
	if err != nil {
		return err
	}
	var state strings.Builder
	existing := map[string]int{}
	for _, x := range all {
		existing[x.Key] = x.Depth
		if x.Kind == "plan" || x.Kind == "finish" {
			continue
		}
		fmt.Fprintf(&state, "- key=%s status=%s title=%q deps=%v", x.Key, x.Status, x.Title, x.Deps)
		if x.Spec.Role != "" {
			fmt.Fprintf(&state, " role=%s", x.Spec.Role)
		}
		if x.Spec.Tool != "" {
			fmt.Fprintf(&state, " tool_step=%s", x.Spec.Tool)
		}
		if len(x.Spec.Verify) > 0 {
			fmt.Fprintf(&state, " verify=%v", x.Spec.Verify)
		}
		if x.Error != "" {
			fmt.Fprintf(&state, " error=%q", truncate(x.Error, 300))
		}
		if x.Status == "succeeded" {
			fmt.Fprintf(&state, " result=%q", truncate(summariseOutput(x.Output, 300), 300))
		}
		state.WriteString("\n")
	}
	sk, _ := skills.Get(g.Skill)
	allowed := []string{}
	if sk != nil {
		seen := map[string]bool{}
		for _, s := range sk.Steps {
			for _, tl := range s.Tools {
				if !seen[tl] {
					seen[tl] = true
					allowed = append(allowed, tl)
				}
			}
		}
	}
	prompt := fmt.Sprintf(`Compare the current state with the goal and decide how the plan should change.

WHY YOU ARE RE-PLANNING (%s): %s

GOAL: %s
Objective: %s
Done when: %v

TASKS
%s
HISTORY
%s

RECENT CONVERSATION
%s

Return JSON: {"assessment": one paragraph, "ops": [...], "goal_status": "continue" | "needs_attention", "attention_reason": "...", "message_to_player": optional short update in %s}.
ops: {"op":"retry","key","instructions","why"} re-run a failed/cancelled task with better instructions;
     {"op":"skip","key","why"} mark a blocked/failed task not needed so its dependents can proceed;
     {"op":"cancel","key","why"} drop a task that no longer serves the goal;
     {"op":"update","key","instructions","why"} change a task that has not started;
     {"op":"add","task":{key,title,role,instructions,tools,deps,verify,wait_days,max_steps,tool,args},"why"} add a task (deps may name existing keys; role is who does it, e.g. one of the roles above; "tool"+"args" instead of instructions make a deterministic step that runs that one tool, like an existing tool step).
Never skip or cancel a task that carries one of these guard checks while the goal still needs it: %v. Use goal_status "needs_attention" when only a person can unblock it (e.g. missing information from the player — then ask for it in message_to_player). Allowed tools: %v. At most %d new tasks.`,
		t.Spec.Mode, t.Spec.Reason, g.Title, g.Objective, g.Criteria, state.String(), p.Store.History(ctx, g.ID, 15),
		p.Store.RecentConversation(ctx, g.ConversationID, 6), persona.LangName(g.Language), guardNames(), allowed, g.Limits.MaxTasksPerPlan)

	resp, err := p.Model.Chat(ctx, CallMeta{Purpose: "replan", UserID: g.UserID, GoalID: g.ID, TaskID: t.ID, CountIteration: true},
		llm.Request{Model: p.LLM, JSON: true, Temperature: 0.2, Messages: []llm.Message{
			{Role: "system", Content: p.system(g)}, {Role: "user", Content: prompt}}})
	if err != nil {
		return err
	}
	var out struct {
		Assessment      string     `json:"assessment"`
		Ops             []replanOp `json:"ops"`
		GoalStatus      string     `json:"goal_status"`
		AttentionReason string     `json:"attention_reason"`
		Message         string     `json:"message_to_player"`
	}
	if err := json.Unmarshal([]byte(llm.ExtractJSON(resp.Message.Content)), &out); err != nil {
		return fmt.Errorf("replan output unreadable: %w", err)
	}

	// Validate every op before applying any.
	byKey := map[string]*Task{}
	for _, x := range all {
		byKey[x.Key] = x
	}
	var adds []PlannedTask
	pv := g.PlanVersion + 1
	rename := map[string]string{}
	for _, op := range out.Ops {
		if op.Op == "add" {
			nk := fmt.Sprintf("v%d-%s", pv, op.Task.Key)
			rename[op.Task.Key] = nk
		}
	}
	// Each op is checked on its own. An invalid op is dropped and recorded,
	// not allowed to sink the valid ones; a request to redo finished work
	// becomes a new follow-up task, which is what it means.
	var ops []replanOp
	var dropped []string
	for i, op := range out.Ops {
		switch op.Op {
		case "retry", "skip", "cancel", "update":
			x := byKey[op.Key]
			switch {
			case x == nil || x.Kind == "plan" || x.Kind == "finish":
				dropped = append(dropped, fmt.Sprintf("op %d: no task %q", i, op.Key))
				continue
			case x.Status == "succeeded" && (op.Op == "retry" || op.Op == "update") && op.Instructions != "":
				nk := fmt.Sprintf("v%d-%s-redo", pv, x.Key)
				adds = append(adds, PlannedTask{Key: nk, Title: x.Title, Role: x.Spec.Role, Instructions: op.Instructions,
					Tools: x.Spec.Tools, Verify: x.Spec.Verify, MaxSteps: x.Spec.MaxSteps, Tool: x.Spec.Tool, Args: x.Spec.Args})
				continue
			case x.Status == "succeeded":
				dropped = append(dropped, fmt.Sprintf("op %d: %q already succeeded", i, op.Key))
				continue
			case (op.Op == "skip" || op.Op == "cancel") && hasGuard(x.Spec.Verify):
				dropped = append(dropped, fmt.Sprintf("op %d: refused to drop guarded step %q", i, op.Key))
				continue
			case op.Op == "update" && x.Status != "blocked" && x.Status != "ready":
				dropped = append(dropped, fmt.Sprintf("op %d: %q is %s and cannot be updated", i, op.Key, x.Status))
				continue
			}
			ops = append(ops, op)
		case "add":
			nt := op.Task
			nt.Key = rename[nt.Key]
			for j, d := range nt.Deps {
				if r, ok := rename[d]; ok {
					nt.Deps[j] = r
				}
			}
			adds = append(adds, nt)
		default:
			dropped = append(dropped, fmt.Sprintf("op %d: unknown op %q", i, op.Op))
		}
	}
	out.Ops = ops
	var depth map[string]int
	if len(adds) > 0 {
		if depth, err = validate(adds, existing, g.Limits); err != nil {
			return fmt.Errorf("replan rejected: %w", err)
		}
	}

	err = pgx.BeginFunc(ctx, p.Store.Pool, func(tx pgx.Tx) error {
		for _, op := range out.Ops {
			x := byKey[op.Key]
			switch op.Op {
			case "retry":
				spec := x.Spec
				if op.Instructions != "" {
					spec.Instructions = op.Instructions
				}
				raw, _ := json.Marshal(spec)
				if _, err := tx.Exec(ctx, `UPDATE tasks SET status='blocked', attempts=0, error='', spec=$2, run_after=now(), finished_at=NULL, updated_at=now()
					WHERE id=$1`, x.ID, raw); err != nil {
					return err
				}
				// Resuming a retried task from its old checkpoint would replay
				// the approach that failed.
				if _, err := tx.Exec(ctx, `DELETE FROM checkpoints WHERE task_id=$1`, x.ID); err != nil {
					return err
				}
			case "skip", "cancel":
				st := map[string]string{"skip": "skipped", "cancel": "cancelled"}[op.Op]
				if _, err := tx.Exec(ctx, `UPDATE tasks SET status=$2, error=$3, finished_at=now(), updated_at=now() WHERE id=$1`, x.ID, st, "replan: "+op.Why); err != nil {
					return err
				}
				if op.Op == "cancel" {
					// Dependents of a cancelled task can never run; cancel them too
					// rather than leave them blocked forever.
					if _, err := tx.Exec(ctx, `WITH RECURSIVE dn AS (
						SELECT task_id FROM task_deps WHERE depends_on=$1
						UNION SELECT d.task_id FROM task_deps d JOIN dn ON d.depends_on = dn.task_id)
						UPDATE tasks SET status='cancelled', error='dependency cancelled by replan', finished_at=now()
						WHERE id IN (SELECT task_id FROM dn) AND status IN ('blocked','ready')`, x.ID); err != nil {
						return err
					}
				}
			case "update":
				spec := x.Spec
				spec.Instructions = op.Instructions
				raw, _ := json.Marshal(spec)
				if _, err := tx.Exec(ctx, `UPDATE tasks SET spec=$2, updated_at=now() WHERE id=$1`, x.ID, raw); err != nil {
					return err
				}
			}
		}
		if len(adds) > 0 {
			if err := insertTasks(ctx, tx, g.ID, pv, adds, depth); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE goals SET plan_version=$2, replans=replans+1, next_review_at=now()+interval '1 day', updated_at=now() WHERE id=$1`, g.ID, pv); err != nil {
			return err
		}
		if err := completePlanTx(ctx, tx, t, map[string]any{"summary": out.Assessment, "ops": len(out.Ops)}); err != nil {
			return err
		}
		if err := promoteTx(ctx, tx, g.ID); err != nil {
			return err
		}
		return eventTx(ctx, tx, g.UserID, g.ID, t.ID, "goal.replanned", map[string]any{
			"mode": t.Spec.Mode, "reason": t.Spec.Reason, "why": out.Assessment, "ops": out.Ops, "added": len(adds), "dropped": dropped})
	})
	if err != nil {
		return err
	}
	if out.Message != "" && g.ConversationID != "" {
		p.Store.PostMessage(ctx, g.ConversationID, g.UserID, out.Message, map[string]any{"goal_id": g.ID, "kind": "update"}, "replan-"+t.ID)
	}
	if out.GoalStatus == "needs_attention" {
		return p.Store.SetGoalStatus(ctx, g.ID, "needs_attention", out.AttentionReason)
	}
	return nil
}

// hasGuard reports whether any of a task's verifiers is a guard.
func hasGuard(verify []string) bool {
	for _, v := range verify {
		if isGuard(v) {
			return true
		}
	}
	return false
}

// Finish checks the completion criteria against what was actually produced.
func (p *Planner) Finish(ctx context.Context, g *Goal, t *Task) error {
	all, _ := p.Store.Tasks(ctx, g.ID)
	var work strings.Builder
	succeeded := 0
	for _, x := range all {
		if x.Kind == "plan" || x.Kind == "finish" {
			continue
		}
		if x.Status == "succeeded" {
			succeeded++
		}
		fmt.Fprintf(&work, "- %s [%s]: %s\n", x.Title, x.Status, truncate(summariseOutput(x.Output, 500), 500))
	}
	prompt := fmt.Sprintf(`Decide whether this goal is complete.

GOAL: %s
Objective: %s
COMPLETION CRITERIA:
%s
WORK DONE:
%s
Return JSON {"complete": bool, "unmet": [criteria not met, with why], "message_to_player": a warm closing (if complete) or short status (if not), in %s, naming what was produced and what happens next}.
Be strict: a criterion is met only if the work above shows it.`,
		g.Title, g.Objective, "- "+strings.Join(g.Criteria, "\n- "), work.String(), persona.LangName(g.Language))
	resp, err := p.Model.Chat(ctx, CallMeta{Purpose: "judge", UserID: g.UserID, GoalID: g.ID, TaskID: t.ID},
		llm.Request{Model: p.LLM, JSON: true, Temperature: 0, Messages: []llm.Message{
			{Role: "system", Content: p.system(g)}, {Role: "user", Content: prompt}}})
	if err != nil {
		return err
	}
	var v struct {
		Complete bool     `json:"complete"`
		Unmet    []string `json:"unmet"`
		Message  string   `json:"message_to_player"`
	}
	if err := json.Unmarshal([]byte(llm.ExtractJSON(resp.Message.Content)), &v); err != nil {
		return fmt.Errorf("judge output unreadable: %w", err)
	}
	// Deterministic floor under the judge: nothing succeeded, nothing is done.
	if succeeded == 0 {
		v.Complete = false
		v.Unmet = append(v.Unmet, "no task succeeded")
	}
	if err := p.Store.Complete(ctx, t, map[string]any{"summary": fmt.Sprintf("complete=%v unmet=%v", v.Complete, v.Unmet)}); err != nil {
		return err
	}
	sk, _ := skills.Get(g.Skill)
	switch {
	case v.Complete && sk != nil && sk.Recurring:
		if v.Message != "" {
			p.Store.PostMessage(ctx, g.ConversationID, g.UserID, v.Message, map[string]any{"goal_id": g.ID, "kind": "update"}, "finish-"+t.ID)
		}
		_, err := p.Store.EnqueuePlan(ctx, g.ID, fmt.Sprintf("cycle-%d", g.PlanVersion), "cycle",
			"a cycle of this recurring plan finished; add the next cycle as the playbook describes, unless the owner has closed it")
		return err
	case v.Complete:
		p.Store.PostMessage(ctx, g.ConversationID, g.UserID, v.Message, map[string]any{"goal_id": g.ID, "kind": "complete"}, "finish-"+t.ID)
		return p.Store.SetGoalStatus(ctx, g.ID, "completed", "all completion criteria met")
	default:
		_, err := p.Store.EnqueuePlan(ctx, g.ID, fmt.Sprintf("replan-unmet-%d", g.PlanVersion), "replan",
			"all tasks finished but criteria are unmet: "+strings.Join(v.Unmet, "; "))
		return err
	}
}

// PostMessage adds an assistant message to a conversation, idempotently by key.
func (s *Store) PostMessage(ctx context.Context, convID, userID, content string, meta map[string]any, key string) {
	if convID == "" || strings.TrimSpace(content) == "" {
		return
	}
	raw, _ := json.Marshal(meta)
	_, _ = s.Pool.Exec(context.WithoutCancel(ctx), `INSERT INTO messages (conversation_id, role, content, meta, client_msg_id)
		VALUES ($1, 'assistant', $2, $3, $4) ON CONFLICT DO NOTHING`, convID, content, raw, key)
	_, _ = s.Pool.Exec(context.WithoutCancel(ctx), `UPDATE conversations SET updated_at=now() WHERE id=$1`, convID)
	_, _ = s.Pool.Exec(context.WithoutCancel(ctx), `SELECT pg_notify('play_user', $1)`, userID)
}
