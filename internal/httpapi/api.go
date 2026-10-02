package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/damonleelcx/play-with-agents/internal/agent"
	"github.com/damonleelcx/play-with-agents/internal/auth"
	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/persona"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

// ── settings ───────────────────────────────────────────────────────────────

// Preference keys the client may set, with a validator each. Anything else is
// rejected rather than stored, so the preferences blob stays meaningful.
var prefKeys = map[string]func(any) bool{
	"language": func(v any) bool { s, ok := v.(string); return ok && (s == "en" || s == "zh") },
	"verbosity": func(v any) bool {
		s, ok := v.(string)
		return ok && (s == "brief" || s == "balanced" || s == "detailed")
	},
	"tone":              func(v any) bool { s, ok := v.(string); return ok && (s == "warm" || s == "neutral" || s == "formal") },
	"timezone":          func(v any) bool { s, ok := v.(string); _, err := time.LoadLocation(s); return ok && err == nil },
	"notify_email":      func(v any) bool { _, ok := v.(bool); return ok },
	"notify_approvals":  func(v any) bool { _, ok := v.(bool); return ok },
	"memory_enabled":    func(v any) bool { _, ok := v.(bool); return ok },
	"theme":             func(v any) bool { s, ok := v.(string); return ok && (s == "light" || s == "dark" || s == "system") },
	"font_size":         func(v any) bool { s, ok := v.(string); return ok && (s == "small" || s == "medium" || s == "large") },
	"enter_to_send":     func(v any) bool { _, ok := v.(bool); return ok },
	"reduce_motion":     func(v any) bool { _, ok := v.(bool); return ok },
	"jurisdiction":      func(v any) bool { s, ok := v.(string); return ok && utf8.RuneCountInString(s) <= 80 },
	"goal_max_cost_usd": func(v any) bool { f, ok := v.(float64); return ok && f >= 1 && f <= 200 },
	"goal_max_days":     func(v any) bool { f, ok := v.(float64); return ok && f >= 1 && f <= 180 },
	"approval_mode":     func(v any) bool { s, ok := v.(string); return ok && (s == "ask" || s == "ask_always") },
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var prefs []byte
	_ = s.Pool.QueryRow(r.Context(), `SELECT coalesce(data,'{}') FROM user_preferences WHERE user_id=$1`, u.ID).Scan(&prefs)
	if prefs == nil {
		prefs = []byte("{}")
	}
	var lic []map[string]any
	rows, err := s.Pool.Query(r.Context(), `SELECT id, kind, number, jurisdiction, status, created_at FROM professional_licenses WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err == nil {
		for rows.Next() {
			var id, kind, num, jur, st string
			var at time.Time
			if rows.Scan(&id, &kind, &num, &jur, &st, &at) == nil {
				lic = append(lic, map[string]any{"id": id, "kind": kind, "number": num, "jurisdiction": jur, "status": st, "created_at": at})
			}
		}
		rows.Close()
	}
	writeJSON(w, 200, map[string]any{"user": s.view(r, u), "preferences": json.RawMessage(prefs), "licenses": lic})
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
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if utf8.RuneCountInString(n) > 80 {
			writeErr(w, 400, "name is too long")
			return
		}
		_, _ = s.Pool.Exec(r.Context(), `UPDATE users SET name=$2, updated_at=now() WHERE id=$1`, u.ID, n)
	}
	if len(in.Preferences) > 0 {
		for k, v := range in.Preferences {
			ok, known := prefKeys[k]
			if !known || !ok(v) {
				writeErr(w, 400, "invalid preference: "+k)
				return
			}
		}
		patch, _ := json.Marshal(in.Preferences)
		if _, err := s.Pool.Exec(r.Context(), `INSERT INTO user_preferences (user_id, data) VALUES ($1,$2)
			ON CONFLICT (user_id) DO UPDATE SET data = user_preferences.data || EXCLUDED.data, updated_at=now()`, u.ID, patch); err != nil {
			writeErr(w, 500, "could not save")
			return
		}
	}
	nu, _ := s.Auth.UserByID(r.Context(), u.ID)
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
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeErr(w, 400, "current password is incorrect")
			return
		}
		writeErr(w, 400, err.Error())
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
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) revokeOthers(w http.ResponseWriter, r *http.Request, u *auth.User) {
	sess, _ := r.Context().Value(sessKey).(string)
	_ = s.Auth.RevokeAllSessions(r.Context(), u.ID, sess)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// export returns everything held about the account, as JSON.
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
		"cases":         q(`SELECT id, title, objective, skill, status, created_at FROM goals WHERE user_id=$1`),
		"documents":     q(`SELECT id, title, kind, version, content, created_at FROM documents WHERE user_id=$1`),
		"reminders":     q(`SELECT due_at, text, sent_at FROM reminders WHERE user_id=$1`),
	}
	w.Header().Set("Content-Disposition", `attachment; filename="act-export.json"`)
	writeJSON(w, 200, data)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct{ Password string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	if err := s.Auth.DeleteAccount(r.Context(), u.ID, in.Password); err != nil {
		writeErr(w, 400, "password is incorrect")
		return
	}
	clearCookie(w, s.CookieSecure)
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

// requestLicense records a professional licence for an admin to verify. The
// role changes now; the ability to approve G2 actions waits for verification.
func (s *Server) requestLicense(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct{ Kind, Number, Jurisdiction string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	role := map[string]string{"bar": "attorney", "medical": "physician"}[in.Kind]
	if role == "" || strings.TrimSpace(in.Number) == "" || strings.TrimSpace(in.Jurisdiction) == "" {
		writeErr(w, 400, "kind (bar|medical), number and jurisdiction are required")
		return
	}
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO professional_licenses (user_id, kind, number, jurisdiction) VALUES ($1,$2,$3,$4)`,
		u.ID, in.Kind, strings.TrimSpace(in.Number), strings.TrimSpace(in.Jurisdiction)); err != nil {
		writeErr(w, 500, "could not save")
		return
	}
	_, _ = s.Pool.Exec(r.Context(), `UPDATE users SET role=$2 WHERE id=$1 AND role <> 'admin'`, u.ID, role)
	writeJSON(w, 201, map[string]string{"status": "pending"})
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
		writeErr(w, 400, "message is too long (12,000 characters max) — upload long text as a document")
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
	return map[string]any{"id": g.ID, "title": g.Title, "objective": g.Objective, "domain": g.Domain, "skill": g.Skill,
		"status": g.Status, "criteria": g.Criteria, "milestones": g.Milestones, "usage": g.Usage, "limits": g.Limits,
		"attention_reason": g.AttentionReason, "conversation_id": g.ConversationID, "created_at": g.CreatedAt, "updated_at": g.UpdatedAt}
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
			FROM tasks WHERE goal_id=$1 AND kind IN ('llm','wait')`, g.ID).Scan(&done, &total)
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
			"finished_at": t.FinishedAt, "summary": outputSummary(t.Output), "mode": t.Spec.Mode, "activity": activity[t.ID]})
	}
	docs := []map[string]any{}
	rows, err := s.Pool.Query(r.Context(), `SELECT id, title, kind, version, created_at FROM documents WHERE goal_id=$1 ORDER BY created_at DESC`, g.ID)
	if err == nil {
		for rows.Next() {
			var id, title, kind string
			var v int
			var at time.Time
			if rows.Scan(&id, &title, &kind, &v, &at) == nil {
				docs = append(docs, map[string]any{"id": id, "title": title, "kind": kind, "version": v, "created_at": at})
			}
		}
		rows.Close()
	}
	out := goalView(g)
	out["tasks"], out["documents"], out["approvals"] = tasks, docs, s.approvalList(r, u, g.ID)
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
	var rems []map[string]any
	rows, err = s.Pool.Query(r.Context(), `SELECT due_at, text FROM reminders WHERE goal_id=$1 AND sent_at IS NULL ORDER BY due_at LIMIT 10`, g.ID)
	if err == nil {
		for rows.Next() {
			var at time.Time
			var t string
			if rows.Scan(&at, &t) == nil {
				rems = append(rems, map[string]any{"due_at": at, "text": t})
			}
		}
		rows.Close()
	}
	writeJSON(w, 200, map[string]any{"events": evs, "summaries": summaries, "next": next, "reminders": rems})
}

func (s *Server) goalAction(w http.ResponseWriter, r *http.Request, u *auth.User) {
	g, err := s.Store.GoalForUser(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	switch r.PathValue("action") {
	case "pause":
		if g.Status == "active" || g.Status == "planning" {
			err = s.Store.SetGoalStatus(r.Context(), g.ID, "paused", "paused by the client")
		}
	case "resume":
		if g.Status == "paused" || g.Status == "needs_attention" {
			err = s.Store.SetGoalStatus(r.Context(), g.ID, "active", "resumed by the client")
			if err == nil {
				_, err = s.Store.EnqueuePlan(r.Context(), g.ID, fmt.Sprintf("resume-%d", time.Now().Unix()), "review", "the client resumed the case; check the plan still fits")
			}
		}
	case "cancel":
		if g.Status != "completed" && g.Status != "cancelled" {
			err = s.Store.SetGoalStatus(r.Context(), g.ID, "cancelled", "cancelled by the client")
		}
	default:
		writeErr(w, 404, "unknown action")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	g, _ = s.Store.Goal(r.Context(), g.ID)
	writeJSON(w, 200, goalView(g))
}

// ── approvals ──────────────────────────────────────────────────────────────

// approvalList: the client sees their own; a licensed professional also sees
// the queue of G2 items for their role.
func (s *Server) approvalList(r *http.Request, u *auth.User, goalID string) []map[string]any {
	q := `SELECT a.id, a.goal_id, g.title, a.tool, a.args, a.preview, a.gate, a.required_role, a.status, a.note, a.created_at, a.decided_at,
		u.name, u.email, a.user_id = $1 AS own
		FROM approvals a JOIN goals g ON g.id=a.goal_id JOIN users u ON u.id=a.user_id
		WHERE (a.user_id=$1 OR (a.gate='G2' AND a.required_role=$2 AND $3))`
	args := []any{u.ID, u.Role, u.Licensed}
	if goalID != "" {
		q += ` AND a.goal_id=$4`
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
		var id, gid, title, tool, preview, gate, role, st, note, cname, cemail string
		var argsRaw json.RawMessage
		var at time.Time
		var dec *time.Time
		var own bool
		if rows.Scan(&id, &gid, &title, &tool, &argsRaw, &preview, &gate, &role, &st, &note, &at, &dec, &cname, &cemail, &own) != nil {
			continue
		}
		canDecide := st == "pending" && ((gate == "G1" && own) || (gate == "G2" && u.Role == role && u.Licensed))
		v := map[string]any{"id": id, "goal_id": gid, "goal_title": title, "tool": tool, "args": argsRaw, "preview": preview,
			"gate": gate, "required_role": role, "status": st, "note": note, "created_at": at, "decided_at": dec, "can_decide": canDecide, "own": own}
		if !own {
			v["client"] = map[string]string{"name": cname, "email": cemail}
		}
		out = append(out, v)
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
	var owner, gate, role, status string
	err := s.Pool.QueryRow(r.Context(), `SELECT user_id, gate, required_role, status FROM approvals WHERE id=$1::uuid`, id).Scan(&owner, &gate, &role, &status)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	// The gate decides who may decide — checked here, on the server, against
	// the stored approval, never against anything the client sent.
	allowed := (gate == "G1" && owner == u.ID) || (gate == "G2" && u.Role == role && u.Licensed)
	if !allowed {
		if gate == "G2" {
			writeErr(w, 403, "only a verified licensed "+role+" can approve this")
			return
		}
		writeErr(w, 403, "not yours to approve")
		return
	}
	if err := s.Store.Decide(r.Context(), id, u.ID, in.Approve, strings.TrimSpace(in.Note)); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ── documents ──────────────────────────────────────────────────────────────

func (s *Server) documents(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rows, err := s.Pool.Query(r.Context(), `SELECT d.id, d.title, d.kind, d.version, d.created_at, coalesce(d.goal_id::text,''), coalesce(g.title,''), length(d.content)
		FROM documents d LEFT JOIN goals g ON g.id=d.goal_id WHERE d.user_id=$1 ORDER BY d.created_at DESC LIMIT 300`, u.ID)
	if err != nil {
		writeErr(w, 500, "error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, kind, gid, gt string
		var v, n int
		var at time.Time
		if rows.Scan(&id, &title, &kind, &v, &at, &gid, &gt, &n) == nil {
			out = append(out, map[string]any{"id": id, "title": title, "kind": kind, "version": v, "created_at": at, "goal_id": gid, "goal_title": gt, "chars": n})
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) document(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var id, title, kind, content, sum string
	var v int
	var at time.Time
	err := s.Pool.QueryRow(r.Context(), `SELECT id, title, kind, content, sha256, version, created_at FROM documents WHERE id=$1::uuid AND user_id=$2`,
		r.PathValue("id"), u.ID).Scan(&id, &title, &kind, &content, &sum, &v, &at)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "title": title, "kind": kind, "content": content, "sha256": sum, "version": v, "created_at": at})
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request, u *auth.User) {
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		writeErr(w, 400, "file too large (12 MB max)")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "no file")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeErr(w, 400, "could not read the file")
		return
	}
	text, err := s.Agent.ExtractText(r.Context(), u.ID, hdr.Filename, data)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(text) == "" {
		writeErr(w, 400, "no readable text found in this file")
		return
	}
	env := &tools.Env{Pool: s.Pool, UserID: u.ID}
	id, ver, _, err := tools.SaveDocument(r.Context(), env, hdr.Filename, "upload", text)
	if err != nil {
		writeErr(w, 500, "could not save")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "title": hdr.Filename, "version": ver, "chars": utf8.RuneCountInString(text),
		"language": persona.Normalize(agent.DetectLang(text, ""))})
}

// ── admin: licence verification ────────────────────────────────────────────

func (s *Server) licenses(w http.ResponseWriter, r *http.Request, u *auth.User) {
	rows, err := s.Pool.Query(r.Context(), `SELECT l.id, l.kind, l.number, l.jurisdiction, l.status, l.created_at, us.name, us.email
		FROM professional_licenses l JOIN users us ON us.id=l.user_id ORDER BY (l.status='pending') DESC, l.created_at DESC LIMIT 200`)
	if err != nil {
		writeErr(w, 500, "error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, kind, num, jur, st, name, email string
		var at time.Time
		if rows.Scan(&id, &kind, &num, &jur, &st, &at, &name, &email) == nil {
			out = append(out, map[string]any{"id": id, "kind": kind, "number": num, "jurisdiction": jur, "status": st, "created_at": at, "name": name, "email": email})
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) decideLicense(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct{ Verify bool }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	st := map[bool]string{true: "verified", false: "rejected"}[in.Verify]
	tag, err := s.Pool.Exec(r.Context(), `UPDATE professional_licenses SET status=$2, verified_by=$3, verified_at=now() WHERE id=$1::uuid`, r.PathValue("id"), st, u.ID)
	if err != nil || tag.RowsAffected() == 0 {
		writeErr(w, 404, "not found")
		return
	}
	writeJSON(w, 200, map[string]string{"status": st})
}
