package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/agent"
	"github.com/damonleelcx/play-with-agents/internal/auth"
	"github.com/damonleelcx/play-with-agents/internal/engine"
)

// ── settings ───────────────────────────────────────────────────────────────
//
// GET /api/settings → { user, preferences, defaults, schema }
//   preferences: every key at its stored value or its default, plus
//                display_name (which is users.name)
//   schema:      [{ key, group, kind, values?, max_len?, min?, max?, default }] — what
//                the settings page renders and what PUT accepts
// PUT /api/settings { name?, preferences?: { key: value | null } }
//   Every key is validated before anything is written; one bad key rejects
//   the whole request. null resets a key to its default.

type prefSchema struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Kind    string   `json:"kind"`
	Values  []string `json:"values,omitempty"`
	MaxLen  int      `json:"max_len,omitempty"`
	Min     *int     `json:"min,omitempty"`
	Max     *int     `json:"max,omitempty"`
	Default any      `json:"default"`
}

var prefKindName = map[agent.PrefKind]string{agent.PrefEnum: "enum", agent.PrefInt: "int", agent.PrefBool: "bool",
	agent.PrefText: "text", agent.PrefAgents: "agents", agent.PrefRange: "range"}

func settingsSchema() []prefSchema {
	out := []prefSchema{{Key: "display_name", Group: "profile", Kind: "text", MaxLen: maxNameLen, Default: ""}}
	for _, p := range agent.PrefSpecs {
		ps := prefSchema{Key: p.Key, Group: p.Group, Kind: prefKindName[p.Kind], Values: p.Values, MaxLen: p.MaxLen, Default: p.Default}
		if p.Kind == agent.PrefRange {
			lo, hi := p.Min, p.Max
			ps.Min, ps.Max = &lo, &hi
		}
		out = append(out, ps)
	}
	return out
}

const maxNameLen = 80

func (s *Server) storedPrefs(ctx context.Context, userID string) map[string]any {
	var raw []byte
	_ = s.Pool.QueryRow(ctx, `SELECT coalesce(data,'{}') FROM user_preferences WHERE user_id=$1`, userID).Scan(&raw)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request, u *auth.User) {
	prefs := agent.WithDefaults(s.storedPrefs(r.Context(), u.ID))
	prefs["display_name"] = u.Name
	writeJSON(w, 200, map[string]any{"user": s.view(r, u), "preferences": prefs, "defaults": agent.PrefDefaults(), "schema": settingsSchema()})
}

// validateSettings checks a PUT body and splits it into the users.name change
// (if any), the keys to set and the keys to reset.
func validateSettings(name *string, prefs map[string]any) (newName *string, set map[string]any, reset []string, err error) {
	if name != nil {
		n := strings.TrimSpace(*name)
		newName = &n
	}
	set = map[string]any{}
	for k, v := range prefs {
		if k == "display_name" {
			n, ok := v.(string)
			if !ok {
				return nil, nil, nil, fmt.Errorf("display_name must be text")
			}
			n = strings.TrimSpace(n)
			newName = &n
			continue
		}
		if v == nil {
			if _, known := agent.PrefSpecFor(k); !known {
				return nil, nil, nil, fmt.Errorf("unknown setting %q", k)
			}
			reset = append(reset, k)
			continue
		}
		if err := agent.ValidatePref(k, v); err != nil {
			return nil, nil, nil, err
		}
		set[k] = agent.NormalizePref(k, v)
	}
	if newName != nil && (utf8.RuneCountInString(*newName) > maxNameLen || strings.ContainsAny(*newName, "\n\r\t")) {
		return nil, nil, nil, fmt.Errorf("name must be one line of at most %d characters", maxNameLen)
	}
	return newName, set, reset, nil
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Name        *string        `json:"name"`
		Preferences map[string]any `json:"preferences"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	name, set, reset, err := validateSettings(in.Name, in.Preferences)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	patch, _ := json.Marshal(set)
	err = pgx.BeginFunc(r.Context(), s.Pool, func(tx pgx.Tx) error {
		if name != nil {
			if _, err := tx.Exec(r.Context(), `UPDATE users SET name=$2, updated_at=now() WHERE id=$1`, u.ID, *name); err != nil {
				return err
			}
		}
		if len(set) > 0 || len(reset) > 0 {
			// jsonb - text[] removes the reset keys; || merges the rest. A nil
			// slice arrives as SQL NULL, and jsonb - NULL is NULL: the whole
			// row would be wiped (and refused by NOT NULL), so it is coalesced.
			if reset == nil {
				reset = []string{}
			}
			_, err := tx.Exec(r.Context(), `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2)
				ON CONFLICT (user_id) DO UPDATE SET data = (user_preferences.data - COALESCE($3::text[], '{}')) || EXCLUDED.data, updated_at=now()`,
				u.ID, patch, reset)
			return err
		}
		return nil
	})
	if err != nil {
		slog.Error("put settings", "err", err)
		writeErr(w, 500, "could not save")
		return
	}
	nu, err := s.Auth.UserByID(r.Context(), u.ID)
	if err != nil {
		writeErr(w, 500, "could not reload the account")
		return
	}
	s.getSettings(w, r, nu)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct{ Current, Next string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	sess, _ := r.Context().Value(sessKey).(string)
	if err := s.Auth.ChangePassword(r.Context(), u.ID, in.Current, in.Next, sess); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeErr(w, 400, "current password is incorrect")
		case auth.UserFacing(err):
			writeErr(w, 400, err.Error())
		default:
			slog.Error("change password", "user", u.ID, "err", err)
			writeErr(w, 500, "could not change the password")
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request, u *auth.User) {
	sess, _ := r.Context().Value(sessKey).(string)
	list, err := s.Auth.Sessions(r.Context(), u.ID, sess)
	if err != nil {
		writeErr(w, 500, "could not list sessions")
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if err := s.Auth.RevokeSessionPrefix(r.Context(), u.ID, r.PathValue("id")); err != nil {
		if auth.UserFacing(err) {
			writeErr(w, 400, err.Error())
			return
		}
		slog.Error("revoke session", "user", u.ID, "err", err)
		writeErr(w, 500, "could not sign that session out")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) revokeOthers(w http.ResponseWriter, r *http.Request, u *auth.User) {
	sess, _ := r.Context().Value(sessKey).(string)
	_ = s.Auth.RevokeAllSessions(r.Context(), u.ID, sess)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// export returns everything held about the account, as JSON. A query whose
// table does not exist in this deployment contributes nothing rather than
// failing the export.
func (s *Server) export(w http.ResponseWriter, r *http.Request, u *auth.User) {
	ctx := r.Context()
	q := func(sql string) []map[string]any {
		rows, err := s.Pool.Query(ctx, sql, u.ID)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out []map[string]any
		fields := rows.FieldDescriptions()
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				continue
			}
			m := map[string]any{}
			for i, f := range fields {
				m[f.Name] = vals[i]
			}
			out = append(out, m)
		}
		return out
	}
	data := map[string]any{
		"exported_at":   time.Now().UTC(),
		"account":       u,
		"preferences":   q(`SELECT data FROM user_preferences WHERE user_id=$1`),
		"memories":      q(`SELECT content, kind, created_at FROM memories WHERE user_id=$1`),
		"conversations": q(`SELECT c.id, c.title, c.created_at, (SELECT json_agg(json_build_object('role',m.role,'content',m.content,'at',m.created_at) ORDER BY m.id) FROM messages m WHERE m.conversation_id=c.id) AS messages FROM conversations c WHERE c.user_id=$1`),
		"missions":      q(`SELECT id, title, objective, skill, status, created_at FROM goals WHERE user_id=$1`),
		"approvals":     q(`SELECT tool, preview, status, created_at, decided_at FROM approvals WHERE user_id=$1`),
		"games":         q(`SELECT id, name, summary, rules_md, status, visibility, created_at FROM games WHERE owner_id=$1`),
	}
	w.Header().Set("Content-Disposition", `attachment; filename="play-with-agents-export.json"`)
	writeJSON(w, 200, data)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct{ Password string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	if err := s.Auth.DeleteAccount(r.Context(), u.ID, in.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeErr(w, 400, "password is incorrect")
			return
		}
		slog.Error("delete account", "user", u.ID, "err", err)
		writeErr(w, 500, "could not delete the account right now; please try again")
		return
	}
	clearCookie(w, s.CookieSecure)
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var today, month int64
	_ = s.Pool.QueryRow(r.Context(), `SELECT
		coalesce(sum(prompt_tokens+completion_tokens) FILTER (WHERE created_at >= date_trunc('day', now())),0),
		coalesce(sum(prompt_tokens+completion_tokens) FILTER (WHERE created_at >= date_trunc('month', now())),0)
		FROM llm_calls WHERE user_id=$1`, u.ID).Scan(&today, &month)
	var goals []map[string]any
	rows, err := s.Pool.Query(r.Context(), `SELECT id, title, status, usage, limits FROM goals WHERE user_id=$1 ORDER BY updated_at DESC LIMIT 20`, u.ID)
	if err == nil {
		for rows.Next() {
			var id, title, st string
			var use, lim json.RawMessage
			if rows.Scan(&id, &title, &st, &use, &lim) == nil {
				goals = append(goals, map[string]any{"id": id, "title": title, "status": st, "usage": use, "limits": lim})
			}
		}
		rows.Close()
	}
	writeJSON(w, 200, map[string]any{"tokens_today": today, "tokens_month": month, "goals": goals})
}

func (s *Server) memories(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rows, err := s.Pool.Query(r.Context(), `SELECT id, kind, content, source, created_at FROM memories WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		writeErr(w, 500, "error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, kind, content, src string
		var at time.Time
		if rows.Scan(&id, &kind, &content, &src, &at) == nil {
			out = append(out, map[string]any{"id": id, "kind": kind, "content": content, "source": src, "created_at": at})
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request, u *auth.User) {
	_, _ = s.Pool.Exec(r.Context(), `DELETE FROM memories WHERE id=$1::uuid AND user_id=$2`, r.PathValue("id"), u.ID)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) clearMemories(w http.ResponseWriter, r *http.Request, u *auth.User) {
	_, _ = s.Pool.Exec(r.Context(), `DELETE FROM memories WHERE user_id=$1`, u.ID)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ── conversations ──────────────────────────────────────────────────────────

func (s *Server) conversations(w http.ResponseWriter, r *http.Request, u *auth.User) {
	archived := r.URL.Query().Get("archived") == "1"
	rows, err := s.Pool.Query(r.Context(), `SELECT c.id, c.title, c.archived, c.updated_at,
		(SELECT left(content, 120) FROM messages m WHERE m.conversation_id=c.id ORDER BY id DESC LIMIT 1)
		FROM conversations c WHERE c.user_id=$1 AND c.archived=$2 ORDER BY c.updated_at DESC LIMIT 200`, u.ID, archived)
	if err != nil {
		writeErr(w, 500, "error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title string
		var arch bool
		var at time.Time
		var last *string
		if rows.Scan(&id, &title, &arch, &at, &last) == nil {
			out = append(out, map[string]any{"id": id, "title": title, "archived": arch, "updated_at": at, "last": last})
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) newConversation(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var id string
	if err := s.Pool.QueryRow(r.Context(), `INSERT INTO conversations (user_id) VALUES ($1) RETURNING id`, u.ID).Scan(&id); err != nil {
		writeErr(w, 500, "error")
		return
	}
	writeJSON(w, 201, map[string]string{"id": id})
}

func (s *Server) ownConversation(r *http.Request, u *auth.User) (string, bool) {
	id := r.PathValue("id")
	var ok bool
	_ = s.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1::uuid AND user_id=$2)`, id, u.ID).Scan(&ok)
	return id, ok
}

func (s *Server) patchConversation(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := s.ownConversation(r, u)
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	var in struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	if in.Title != nil {
		_, _ = s.Pool.Exec(r.Context(), `UPDATE conversations SET title=left($2,120) WHERE id=$1`, id, strings.TrimSpace(*in.Title))
	}
	if in.Archived != nil {
		_, _ = s.Pool.Exec(r.Context(), `UPDATE conversations SET archived=$2 WHERE id=$1`, id, *in.Archived)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := s.ownConversation(r, u)
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	_, _ = s.Pool.Exec(r.Context(), `DELETE FROM conversations WHERE id=$1`, id)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, ok := s.ownConversation(r, u)
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	rows, err := s.Pool.Query(r.Context(), `SELECT id, role, content, meta, created_at FROM messages WHERE conversation_id=$1 ORDER BY id`, id)
	if err != nil {
		writeErr(w, 500, "error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var mid int64
		var role, content string
		var meta json.RawMessage
		var at time.Time
		if rows.Scan(&mid, &role, &content, &meta, &at) == nil {
			out = append(out, map[string]any{"id": mid, "role": role, "content": content, "meta": meta, "created_at": at})
		}
	}
	writeJSON(w, 200, out)
}

type sseSink struct {
	w  http.ResponseWriter
	fl http.Flusher
}

func (s *sseSink) send(ev string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", ev, b)
	s.fl.Flush()
}
func (s *sseSink) Meta(v map[string]any) { s.send("meta", v) }
func (s *sseSink) Delta(d string)        { s.send("delta", map[string]string{"t": d}) }

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	convID, ok := s.ownConversation(r, u)
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	var in struct {
		Content     string `json:"content"`
		ClientMsgID string `json:"client_msg_id"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Content) == "" {
		writeErr(w, 400, "message is empty")
		return
	}
	if utf8.RuneCountInString(in.Content) > 12000 {
		writeErr(w, 400, "message is too long (12,000 characters max)")
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	sink := &sseSink{w: w, fl: fl}
	au := agent.User{ID: u.ID, Email: u.Email, Name: u.Name, Lang: s.lang(r, u)}
	// The turn is not tied to the request: if the browser disconnects mid-
	// stream, the reply is still produced and saved, and appears on refresh.
	ctx, cancel := contextDetached(r, 3*time.Minute)
	defer cancel()
	id, err := s.Agent.Turn(ctx, au, convID, strings.TrimSpace(in.Content), in.ClientMsgID, sink)
	if err != nil {
		slog.Error("turn", "err", err)
		sink.send("error", map[string]string{"error": "something went wrong; your message is saved"})
		return
	}
	sink.send("done", map[string]int64{"id": id})
}

// ── goals ──────────────────────────────────────────────────────────────────

func goalView(g *engine.Goal) map[string]any {
	v := map[string]any{"id": g.ID, "title": g.Title, "objective": g.Objective, "domain": g.Domain, "skill": g.Skill, "kind": g.Skill,
		"status": g.Status, "criteria": g.Criteria, "milestones": g.Milestones, "usage": g.Usage, "limits": g.Limits,
		"attention_reason": g.AttentionReason, "conversation_id": g.ConversationID, "created_at": g.CreatedAt, "updated_at": g.UpdatedAt}
	if g.GameID != "" {
		v["game_id"] = g.GameID // the game a studio build produces
	}
	return v
}

func (s *Server) goals(w http.ResponseWriter, r *http.Request, u *auth.User) {
	gs, err := s.Store.Goals(r.Context(), u.ID, 100)
	if err != nil {
		writeErr(w, 500, "error")
		return
	}
	out := []map[string]any{}
	for _, g := range gs {
		v := goalView(g)
		var done, total int
		_ = s.Pool.QueryRow(r.Context(), `SELECT count(*) FILTER (WHERE status IN ('succeeded','skipped')), count(*) FILTER (WHERE status NOT IN ('cancelled'))
			FROM tasks WHERE goal_id=$1 AND kind IN ('llm','wait','tool')`, g.ID).Scan(&done, &total)
		v["progress"] = map[string]int{"done": done, "total": total}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
}

func (s *Server) goal(w http.ResponseWriter, r *http.Request, u *auth.User) {
	g, err := s.Store.GoalForUser(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	ts, _ := s.Store.Tasks(r.Context(), g.ID)
	// Latest meaningful event per task: what it is doing right now, or that it
	// is being recovered after an interruption.
	activity := map[string]map[string]any{}
	arows, err := s.Pool.Query(r.Context(), `SELECT DISTINCT ON (task_id) task_id::text, type, coalesce(data->>'tool',''), created_at
		FROM events WHERE goal_id=$1 AND task_id IS NOT NULL
		AND type IN ('task.started','task.resumed','tool.called','task.verify_failed','lease.reclaimed','task.lease_lost','task.retry','approval.requested')
		ORDER BY task_id, id DESC`, g.ID)
	if err == nil {
		for arows.Next() {
			var tid, typ, tool string
			var at time.Time
			if arows.Scan(&tid, &typ, &tool, &at) == nil {
				activity[tid] = map[string]any{"type": typ, "tool": tool, "at": at}
			}
		}
		arows.Close()
	}
	tasks := []map[string]any{}
	for _, t := range ts {
		tasks = append(tasks, map[string]any{"id": t.ID, "key": t.Key, "title": t.Title, "kind": t.Kind, "status": t.Status,
			"attempts": t.Attempts, "deps": t.Deps, "error": t.Error, "run_after": t.RunAfter, "started_at": t.StartedAt,
			"finished_at": t.FinishedAt, "summary": outputSummary(t.Output), "mode": t.Spec.Mode, "role": t.Spec.Role, "activity": activity[t.ID]})
	}
	out := goalView(g)
	out["tasks"], out["approvals"] = tasks, s.approvalList(r, u, g.ID)
	writeJSON(w, 200, out)
}

func outputSummary(o map[string]any) string {
	if o == nil {
		return ""
	}
	if s, ok := o["summary"].(string); ok {
		return s
	}
	return ""
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request, u *auth.User) {
	g, err := s.Store.GoalForUser(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	evs, _ := s.Store.Events(r.Context(), g.ID, 0, 500)
	var summaries []map[string]any
	rows, err := s.Pool.Query(r.Context(), `SELECT summary, upto_event_id, created_at FROM episode_summaries WHERE goal_id=$1 ORDER BY upto_event_id`, g.ID)
	if err == nil {
		for rows.Next() {
			var sm string
			var upto int64
			var at time.Time
			if rows.Scan(&sm, &upto, &at) == nil {
				summaries = append(summaries, map[string]any{"summary": sm, "upto_event_id": upto, "created_at": at})
			}
		}
		rows.Close()
	}
	var next []map[string]any
	rows, err = s.Pool.Query(r.Context(), `SELECT title, status, run_after FROM tasks WHERE goal_id=$1 AND status IN ('ready','blocked','waiting_approval','leased')
		ORDER BY run_after LIMIT 10`, g.ID)
	if err == nil {
		for rows.Next() {
			var title, st string
			var at time.Time
			if rows.Scan(&title, &st, &at) == nil {
				next = append(next, map[string]any{"title": title, "status": st, "at": at})
			}
		}
		rows.Close()
	}
	writeJSON(w, 200, map[string]any{"events": evs, "summaries": summaries, "next": next})
}

func (s *Server) goalAction(w http.ResponseWriter, r *http.Request, u *auth.User) {
	g, err := s.Store.GoalForUser(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	action := r.PathValue("action")
	if action != "pause" && action != "resume" && action != "cancel" {
		writeErr(w, 404, "unknown action")
		return
	}
	// The same control Aoi uses when the owner asks in chat.
	err = s.Store.GoalControl(r.Context(), g, action, map[string]string{"pause": "paused by the owner", "resume": "resumed by the owner", "cancel": "cancelled by the owner"}[action])
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	g, _ = s.Store.Goal(r.Context(), g.ID)
	writeJSON(w, 200, goalView(g))
}

// ── approvals ──────────────────────────────────────────────────────────────

// approvalList: G1 approvals belong to the goal's owner, who alone sees and
// decides them.
func (s *Server) approvalList(r *http.Request, u *auth.User, goalID string) []map[string]any {
	q := `SELECT a.id, a.goal_id, g.title, a.tool, a.args, a.preview, a.gate, a.status, a.note, a.created_at, a.decided_at
		FROM approvals a JOIN goals g ON g.id=a.goal_id WHERE a.user_id=$1`
	args := []any{u.ID}
	if goalID != "" {
		q += ` AND a.goal_id=$2`
		args = append(args, goalID)
	} else {
		q += ` AND (a.status='pending' OR a.decided_at > now()-interval '14 days')`
	}
	q += ` ORDER BY (a.status='pending') DESC, a.created_at DESC LIMIT 100`
	rows, err := s.Pool.Query(r.Context(), q, args...)
	out := []map[string]any{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, gid, title, tool, preview, gate, st, note string
		var argsRaw json.RawMessage
		var at time.Time
		var dec *time.Time
		if rows.Scan(&id, &gid, &title, &tool, &argsRaw, &preview, &gate, &st, &note, &at, &dec) != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "goal_id": gid, "goal_title": title, "tool": tool, "args": argsRaw, "preview": preview,
			"gate": gate, "status": st, "note": note, "created_at": at, "decided_at": dec, "can_decide": st == "pending"})
	}
	return out
}

func (s *Server) approvals(w http.ResponseWriter, r *http.Request, u *auth.User) {
	writeJSON(w, 200, s.approvalList(r, u, ""))
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	id := r.PathValue("id")
	// Checked on the server, against the stored approval — never against
	// anything the client sent (engine.DecideAsOwner, the path Aoi's chat
	// uses too). Someone else's approval is reported as missing, not as
	// forbidden, so ids reveal nothing.
	switch err := s.Store.DecideAsOwner(r.Context(), id, u.ID, in.Approve, strings.TrimSpace(in.Note)); {
	case errors.Is(err, engine.ErrApprovalNotFound):
		writeErr(w, 404, "not found")
		return
	case errors.Is(err, engine.ErrNotApprovable):
		writeErr(w, 403, "this action cannot be approved")
		return
	case err != nil:
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
