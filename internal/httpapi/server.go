// Package httpapi is the web tier: JSON API, streamed chat with Aoi, live
// updates, and the single-page app. Tables and games are in tables_api.go.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/agent"
	"github.com/damonleelcx/play-with-agents/internal/auth"
	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/persona"
	"github.com/damonleelcx/play-with-agents/internal/rooms"
	"github.com/damonleelcx/play-with-agents/internal/tools"
	"github.com/damonleelcx/play-with-agents/internal/tts"
)

type Server struct {
	Pool         *pgxpool.Pool
	Auth         *auth.Service
	Store        *engine.Store
	Agent        *agent.Agent
	Static       fs.FS
	CookieSecure bool
	MailEnabled  bool
	Hub          *Hub
	Rooms        *rooms.Service // tables, games, agents (tables_api.go)
	Speech       *tts.Service   // Aoi's voice; nil when no TTS key is configured
	// SpeechDailyChars is each account's daily allowance of synthesised
	// characters (0: DefaultSpeechDailyChars).
	SpeechDailyChars int

	limiter *limiter
}

const cookieName = "play_session"

// csrfHeader must be present (value "1") on every state-changing API call; see
// middleware.
const csrfHeader = "X-Play"

type ctxKey int

const (
	userKey ctxKey = iota
	sessKey
)

func (s *Server) Handler() http.Handler {
	s.limiter = newLimiter()
	m := http.NewServeMux()

	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	m.HandleFunc("GET /readyz", s.ready)

	// Auth
	m.HandleFunc("POST /api/auth/signup", s.limit("signup", 5, s.signUp))
	m.HandleFunc("POST /api/auth/signin", s.limit("signin", 10, s.signIn))
	m.HandleFunc("POST /api/auth/signout", s.signOut)
	m.HandleFunc("GET /api/auth/me", s.me)
	m.HandleFunc("POST /api/auth/verify", s.limit("verify", 20, s.verify))
	m.HandleFunc("POST /api/auth/resend", s.limit("resend", 3, s.authed(s.resend)))
	m.HandleFunc("POST /api/auth/forgot", s.limit("forgot", 5, s.forgot))
	m.HandleFunc("POST /api/auth/reset", s.limit("reset", 10, s.reset))

	// Settings & account
	m.HandleFunc("GET /api/settings", s.authed(s.getSettings))
	m.HandleFunc("PUT /api/settings", s.authed(s.putSettings))
	m.HandleFunc("POST /api/account/password", s.limit("password", 10, s.authed(s.changePassword)))
	m.HandleFunc("GET /api/account/sessions", s.authed(s.sessions))
	m.HandleFunc("DELETE /api/account/sessions/{id}", s.authed(s.revokeSession))
	m.HandleFunc("POST /api/account/sessions/revoke-others", s.authed(s.revokeOthers))
	m.HandleFunc("GET /api/account/export", s.authed(s.export))
	m.HandleFunc("POST /api/account/delete", s.limit("delete", 5, s.authed(s.deleteAccount)))
	m.HandleFunc("GET /api/account/usage", s.authed(s.usage))
	m.HandleFunc("GET /api/memories", s.authed(s.memories))
	m.HandleFunc("DELETE /api/memories/{id}", s.authed(s.deleteMemory))
	m.HandleFunc("DELETE /api/memories", s.authed(s.clearMemories))

	// Conversation
	m.HandleFunc("GET /api/conversations", s.verified(s.conversations))
	m.HandleFunc("POST /api/conversations", s.verified(s.newConversation))
	m.HandleFunc("PATCH /api/conversations/{id}", s.verified(s.patchConversation))
	m.HandleFunc("DELETE /api/conversations/{id}", s.verified(s.deleteConversation))
	m.HandleFunc("GET /api/conversations/{id}/messages", s.verified(s.messages))
	m.HandleFunc("POST /api/conversations/{id}/messages", s.limit("chat", 30, s.verified(s.postMessage)))

	// Missions (goals), timeline, approvals
	m.HandleFunc("GET /api/goals", s.verified(s.goals))
	m.HandleFunc("GET /api/goals/{id}", s.verified(s.goal))
	m.HandleFunc("GET /api/goals/{id}/timeline", s.verified(s.timeline))
	m.HandleFunc("POST /api/goals/{id}/{action}", s.verified(s.goalAction))
	m.HandleFunc("GET /api/approvals", s.verified(s.approvals))
	m.HandleFunc("POST /api/approvals/{id}", s.verified(s.decide))
	m.HandleFunc("POST /api/studio/build", s.limit("studio-build", studioBuildsPerMinute, s.verified(s.studioBuild)))

	// Aoi's voice: GET says whether it exists; POST returns MP3.
	m.HandleFunc("GET /api/speech", s.authed(s.speechStatus))
	m.HandleFunc("POST /api/speech", s.verified(s.speak))

	m.HandleFunc("GET /api/stream", s.authed(s.stream))

	// ── Tables & games (rooms) ──
	// Registered in tables_api.go; absent when no rooms service is wired.
	if s.Rooms != nil {
		s.tableRoutes(m)
	}

	m.Handle("/", s.spa())
	return s.middleware(m)
}

// ── middleware ─────────────────────────────────────────────────────────────

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "path", r.URL.Path, "err", v)
				writeErr(w, 500, "internal error")
			}
		}()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
			"font-src 'self' https://fonts.gstatic.com; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if s.CookieSecure {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		// CSRF: state-changing API calls must carry a custom header. A
		// cross-site form cannot set one, and a cross-site fetch that sets one
		// is preflighted — and this server answers no preflight.
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get(csrfHeader) != "1" {
				writeErr(w, 403, "missing request header")
				return
			}
		}
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/stream" {
			slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }

// Unwrap lets http.ResponseController reach the connection (deadlines).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) currentUser(r *http.Request) (*auth.User, string) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, ""
	}
	u, sess, err := s.Auth.SessionUser(r.Context(), c.Value)
	if err != nil {
		return nil, ""
	}
	return u, sess
}

type handler func(w http.ResponseWriter, r *http.Request, u *auth.User)

// sessionAlive re-reads the request's session: a long-lived stream must stop
// once its session is revoked or the account is gone.
func (s *Server) sessionAlive(r *http.Request) bool {
	u, _ := s.currentUser(r)
	return u != nil
}

func (s *Server) authed(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, sess := s.currentUser(r)
		if u == nil {
			writeErr(w, 401, "sign in required")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), sessKey, sess))
		h(w, r, u)
	}
}

// verified gates everything that can spend model tokens behind a confirmed
// email: an unverified account costs the operator nothing.
func (s *Server) verified(h handler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, u *auth.User) {
		if !u.EmailVerified {
			writeErr(w, 403, "email_unverified")
			return
		}
		h(w, r, u)
	})
}

// limiter is a per-IP token bucket per route. In-process, so it bounds each
// pod; the ingress rate limit bounds the whole.
type limiter struct {
	mu sync.Mutex
	b  map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter() *limiter { return &limiter{b: map[string]*bucket{}} }

func (l *limiter) allow(key string, perMin int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.b[key]
	if !ok {
		if len(l.b) > 50000 {
			l.b = map[string]*bucket{}
		}
		b = &bucket{tokens: float64(perMin), last: now}
		l.b[key] = b
	}
	b.tokens += now.Sub(b.last).Minutes() * float64(perMin)
	if b.tokens > float64(perMin) {
		b.tokens = float64(perMin)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (s *Server) limit(name string, perMin int, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(name+"|"+clientIP(r), perMin) {
			w.Header().Set("Retry-After", "60")
			writeErr(w, 429, "too many requests, try again in a minute")
			return
		}
		h(w, r)
	}
}

func clientIP(r *http.Request) string {
	// Traefik is the only thing in front of this pod and it sets X-Real-Ip.
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		return strings.TrimSpace(strings.Split(xf, ",")[0])
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

// ── helpers ────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, userID string) error {
	raw, exp, err := s.Auth.CreateSession(r.Context(), userID, r.UserAgent(), clientIP(r))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: raw, Path: "/", Expires: exp, HttpOnly: true,
		Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode})
	return nil
}

func clearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) lang(r *http.Request, u *auth.User) string {
	if u != nil {
		var l string
		_ = s.Pool.QueryRow(r.Context(), `SELECT coalesce(data->>'language','') FROM user_preferences WHERE user_id=$1`, u.ID).Scan(&l)
		if l != "" {
			return persona.Normalize(l)
		}
	}
	if q := r.URL.Query().Get("lang"); q != "" {
		return persona.Normalize(q)
	}
	return persona.Normalize(r.Header.Get("Accept-Language"))
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Pool.Ping(ctx); err != nil {
		writeErr(w, 503, "database unreachable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "mail": s.MailEnabled, "tools": len(tools.Names())})
}

// ── auth handlers ──────────────────────────────────────────────────────────

type userView struct {
	*auth.User
	Language    string `json:"language"`
	MailEnabled bool   `json:"mail_enabled"`
}

func (s *Server) view(r *http.Request, u *auth.User) userView {
	return userView{User: u, Language: s.lang(r, u), MailEnabled: s.MailEnabled}
}

func (s *Server) signUp(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, Name, Language string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	lang := persona.Normalize(in.Language)
	u, err := s.Auth.SignUp(r.Context(), in.Email, in.Password, in.Name, lang)
	if err != nil {
		if auth.UserFacing(err) {
			writeErr(w, 400, err.Error())
			return
		}
		slog.Error("signup", "err", err)
		writeErr(w, 500, "could not create the account")
		return
	}
	if err := s.setSession(w, r, u.ID); err != nil {
		writeErr(w, 500, "could not sign in")
		return
	}
	writeJSON(w, 201, s.view(r, u))
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	u, err := s.Auth.SignIn(r.Context(), in.Email, in.Password)
	if err != nil {
		writeErr(w, 401, auth.ErrInvalidCredentials.Error())
		return
	}
	if err := s.setSession(w, r, u.ID); err != nil {
		writeErr(w, 500, "could not sign in")
		return
	}
	writeJSON(w, 200, s.view(r, u))
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	if u, sess := s.currentUser(r); u != nil {
		_ = s.Auth.RevokeSession(r.Context(), u.ID, sess)
	}
	clearCookie(w, s.CookieSecure)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, _ := s.currentUser(r)
	if u == nil {
		writeErr(w, 401, "signed out")
		return
	}
	writeJSON(w, 200, s.view(r, u))
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token string }
	if err := readJSON(r, &in); err != nil || in.Token == "" {
		writeErr(w, 400, "bad request")
		return
	}
	u, err := s.Auth.VerifyEmail(r.Context(), in.Token)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// Verifying never creates a session. The link proves control of the
	// mailbox, not who registered the account: someone could sign up with a
	// victim's address, and signing the clicker in would land the victim in
	// the attacker's account. A browser already signed in to this very
	// account gets its refreshed user; anyone else is asked to sign in.
	if cur, _ := s.currentUser(r); cur != nil && cur.ID == u.ID {
		writeJSON(w, 200, map[string]any{"verified": true, "signed_in": true, "user": s.view(r, u)})
		return
	}
	writeJSON(w, 200, map[string]any{"verified": true, "signed_in": false})
}

func (s *Server) resend(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if u.EmailVerified {
		writeJSON(w, 200, map[string]bool{"already_verified": true})
		return
	}
	if err := s.Auth.SendVerification(r.Context(), u.ID, s.lang(r, u)); err != nil {
		slog.Error("resend verification", "err", err)
		writeErr(w, 502, "could not send the email right now")
		return
	}
	writeJSON(w, 200, map[string]bool{"sent": true})
}

func (s *Server) forgot(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Language string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	if err := s.Auth.ForgotPassword(r.Context(), in.Email, persona.Normalize(in.Language)); err != nil {
		slog.Error("forgot", "err", err)
	}
	// Same answer whether or not the account exists.
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	u, err := s.Auth.ResetPassword(r.Context(), in.Token, in.Password)
	if err != nil {
		if auth.UserFacing(err) {
			writeErr(w, 400, err.Error())
			return
		}
		slog.Error("reset password", "err", err)
		writeErr(w, 500, "could not reset the password")
		return
	}
	_ = s.setSession(w, r, u.ID)
	writeJSON(w, 200, s.view(r, u))
}

// ── SPA ────────────────────────────────────────────────────────────────────

func (s *Server) spa() http.Handler {
	files := http.FileServer(http.FS(s.Static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, 404, "not found")
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := s.Static.Open(p); err == nil {
				st, _ := f.Stat()
				f.Close()
				if st != nil && !st.IsDir() {
					if strings.HasPrefix(p, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		// Client-side route: serve the shell.
		b, err := fs.ReadFile(s.Static, "index.html")
		if err != nil {
			http.Error(w, "web app not built", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
}

// ── live updates ───────────────────────────────────────────────────────────

// Hub fans NOTIFY play_user out to that user's open streams.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]bool
}

func NewHub() *Hub { return &Hub{subs: map[string]map[chan struct{}]bool{}} }

func (h *Hub) Notify(userID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs[userID] {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

func (h *Hub) sub(userID string) (chan struct{}, func()) {
	c := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[userID] == nil {
		h.subs[userID] = map[chan struct{}]bool{}
	}
	h.subs[userID][c] = true
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		delete(h.subs[userID], c)
		h.mu.Unlock()
	}
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, u *auth.User) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	c, done := s.Hub.sub(u.ID)
	defer done()
	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c:
			// Coalesce bursts: a plan writes many events at once.
			time.Sleep(250 * time.Millisecond)
			select {
			case <-c:
			default:
			}
			fmt.Fprint(w, "event: refresh\ndata: {}\n\n")
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
