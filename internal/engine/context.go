package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Client is the player a goal belongs to, as the worker needs it.
type Client struct {
	ID, Email, Name string
	Prefs           map[string]any
}

func (s *Store) Client(ctx context.Context, userID string) (*Client, error) {
	c := &Client{ID: userID, Prefs: map[string]any{}}
	var prefs []byte
	err := s.Pool.QueryRow(ctx, `SELECT u.email, u.name, coalesce(p.data, '{}') FROM users u
		LEFT JOIN user_preferences p ON p.user_id = u.id WHERE u.id=$1`, userID).Scan(&c.Email, &c.Name, &prefs)
	_ = json.Unmarshal(prefs, &c.Prefs)
	return c, err
}

// Memories returns the player's durable facts, newest first.
func (s *Store) Memories(ctx context.Context, userID string, limit int) []string {
	rows, err := s.Pool.Query(ctx, `SELECT content FROM memories WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			out = append(out, c)
		}
	}
	return out
}

// BuildTaskContext assembles ONLY what this step needs, from persisted state:
// the goal, the current task, what its dependencies produced, the plan at a
// glance, compressed history and the player. Nothing here comes from a
// previous model call's memory.
func (s *Store) BuildTaskContext(ctx context.Context, g *Goal, t *Task, c *Client) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# GOAL\n%s\nObjective: %s\nPlaybook: %s\n", g.Title, g.Objective, g.Skill)
	if len(g.Criteria) > 0 {
		w("Done when:\n")
		for _, c := range g.Criteria {
			w("- %s\n", c)
		}
	}

	w("\n# CURRENT TASK: %s (key %s, attempt %d of %d)\n%s\n", t.Title, t.Key, t.Attempts, t.MaxAttempts, t.Spec.Instructions)
	if t.Error != "" {
		w("\nThe previous attempt of this task ended with: %s\nAvoid repeating what caused it.\n", truncate(t.Error, 600))
	}

	all, _ := s.Tasks(ctx, g.ID)
	byKey := map[string]*Task{}
	for _, x := range all {
		byKey[x.Key] = x
	}
	if len(t.Deps) > 0 {
		w("\n# RESULTS FROM THE TASKS THIS ONE DEPENDS ON\n")
		for _, k := range t.Deps {
			d := byKey[k]
			if d == nil {
				continue
			}
			w("## %s (%s)\n%s\n", d.Title, d.Status, summariseOutput(d.Output, 2500))
		}
	}

	w("\n# PLAN AT A GLANCE\n")
	for _, x := range all {
		if x.Kind == "plan" || x.Kind == "finish" {
			continue
		}
		mark := map[string]string{"succeeded": "✓", "failed": "✗", "skipped": "–", "cancelled": "–", "waiting_approval": "⏸", "leased": "▶"}[x.Status]
		if mark == "" {
			mark = "·"
		}
		w("%s %s [%s]\n", mark, x.Title, x.Status)
	}

	if hist := s.History(ctx, g.ID, 12); hist != "" {
		w("\n# HISTORY\n%s\n", hist)
	}

	w("\n# PLAYER\nName: %s\n", c.Name)
	// A player who switched memory off asked not to be profiled: whatever
	// facts remain stay out of the prompt as well.
	if c.Prefs["memory_enabled"] != false {
		if mem := s.Memories(ctx, c.ID, 20); len(mem) > 0 {
			w("Known facts:\n")
			for _, m := range mem {
				w("- %s\n", m)
			}
		}
	}

	if g.ConversationID != "" {
		if conv := s.RecentConversation(ctx, g.ConversationID, 8); conv != "" {
			w("\n# RECENT CONVERSATION WITH THE PLAYER\n%s\n", conv)
		}
	}

	w("\n# LIMITS REMAINING\nIterations %d · tool calls %d · tokens %d\n",
		g.Limits.MaxIterations-g.Usage.Iterations, g.Limits.MaxToolCalls-g.Usage.ToolCalls, g.Limits.MaxTokens-g.Usage.Tokens())
	return b.String()
}

// History is the latest compressed summary plus the most recent raw events.
func (s *Store) History(ctx context.Context, goalID string, recent int) string {
	var b strings.Builder
	var summary string
	var upto int64
	_ = s.Pool.QueryRow(ctx, `SELECT summary, upto_event_id FROM episode_summaries WHERE goal_id=$1 ORDER BY upto_event_id DESC LIMIT 1`, goalID).Scan(&summary, &upto)
	if summary != "" {
		b.WriteString("Earlier: " + summary + "\n")
	}
	evs, _ := s.Events(ctx, goalID, upto, recent)
	for _, e := range evs {
		b.WriteString(fmt.Sprintf("%s %s %s\n", e.CreatedAt.UTC().Format("01-02 15:04"), e.Type, eventLine(e.Data)))
	}
	return strings.TrimSpace(b.String())
}

func eventLine(d map[string]any) string {
	var parts []string
	for _, k := range []string{"task", "tool", "to", "why", "error", "reason", "summary"} {
		if v, ok := d[k]; ok && fmt.Sprint(v) != "" {
			parts = append(parts, fmt.Sprintf("%s=%s", k, truncate(fmt.Sprint(v), 160)))
		}
	}
	return strings.Join(parts, " ")
}

func (s *Store) RecentConversation(ctx context.Context, convID string, n int) string {
	rows, err := s.Pool.Query(ctx, `SELECT role, content FROM (SELECT id, role, content FROM messages
		WHERE conversation_id=$1 AND role IN ('user','assistant') ORDER BY id DESC LIMIT $2) x ORDER BY id`, convID, n)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var role, content string
		if rows.Scan(&role, &content) == nil {
			b.WriteString(role + ": " + truncate(content, 700) + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func summariseOutput(o map[string]any, n int) string {
	if o == nil {
		return "(no output)"
	}
	if s, ok := o["summary"].(string); ok && s != "" {
		return truncate(s, n)
	}
	raw, _ := json.Marshal(o)
	return truncate(string(raw), n)
}
