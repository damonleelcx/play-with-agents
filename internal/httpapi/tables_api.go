package httpapi

// Tables, games and agents: the HTTP face of internal/rooms
// (docs/00-architecture.md, "HTTP API"). Handlers stay thin: they decode,
// call the service and map its errors to status codes.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/agents"
	"github.com/damonleelcx/play-with-agents/internal/auth"
	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/rooms"
)

// tableRoutes registers every games/agents/tables endpoint. Moves and chat
// are rate limited per IP here, on top of the service's own per-person
// chat limit and per-table agent limit.
func (s *Server) tableRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/agents", s.listAgents)

	m.HandleFunc("GET /api/games", s.authed(s.listGames))
	m.HandleFunc("GET /api/games/{id}", s.authed(s.getGame))
	m.HandleFunc("GET /api/games/{id}/cover", s.authed(s.gameCover))
	m.HandleFunc("PATCH /api/games/{id}", s.verified(s.patchGame))
	m.HandleFunc("DELETE /api/games/{id}", s.verified(s.deleteGame))
	m.HandleFunc("GET /api/games/{id}/comments", s.authed(s.gameComments))
	m.HandleFunc("POST /api/games/{id}/comments", s.limit("game-comment", 20, s.verified(s.postGameComment)))
	m.HandleFunc("DELETE /api/games/{id}/comments/{cid}", s.verified(s.deleteGameComment))

	m.HandleFunc("POST /api/tables", s.limit("table-create", 20, s.verified(s.createTable)))
	m.HandleFunc("GET /api/tables", s.authed(s.listTables))
	m.HandleFunc("POST /api/tables/join", s.limit("table-join", 30, s.verified(s.joinTable)))
	m.HandleFunc("GET /api/tables/{id}", s.authed(s.getTable))
	m.HandleFunc("PUT /api/tables/{id}/seats/{seat}", s.verified(s.setSeat))
	m.HandleFunc("POST /api/tables/{id}/start", s.verified(s.startTable))
	m.HandleFunc("POST /api/tables/{id}/moves", s.limit("table-move", 240, s.verified(s.postMove)))
	m.HandleFunc("POST /api/tables/{id}/chat", s.limit("table-chat", 120, s.verified(s.postTableChat)))
	m.HandleFunc("POST /api/tables/{id}/typing", s.limit("table-typing", 90, s.verified(s.postTyping)))
	m.HandleFunc("POST /api/tables/{id}/chat/{chat}/react", s.limit("table-react", 120, s.verified(s.postReaction)))
	m.HandleFunc("POST /api/tables/{id}/leave", s.authed(s.leaveTable))
	m.HandleFunc("POST /api/tables/{id}/back", s.limit("table-back", 60, s.authed(s.backTable)))
	m.HandleFunc("POST /api/tables/{id}/rematch", s.limit("table-create", 20, s.verified(s.rematch)))
	m.HandleFunc("GET /api/tables/{id}/stream", s.authed(s.tableStream))
}

// roomsErr maps service errors onto the contract's status codes.
func roomsErr(w http.ResponseWriter, err error) {
	var stale *rooms.StaleError
	var input *rooms.InputError
	switch {
	case errors.As(err, &stale):
		writeJSON(w, http.StatusConflict, map[string]any{"error": stale.Error(), "table": stale.Table})
	case errors.As(err, &input):
		writeErr(w, http.StatusBadRequest, input.Msg)
	case errors.Is(err, games.ErrIllegal):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, rooms.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, rooms.ErrForbidden):
		writeErr(w, http.StatusForbidden, "you are not at this table")
	case errors.Is(err, rooms.ErrConflict):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, rooms.ErrRateLimit):
		w.Header().Set("Retry-After", "10")
		writeErr(w, http.StatusTooManyRequests, "slow down a little")
	default:
		slog.Error("rooms", "err", err)
		writeErr(w, http.StatusInternalServerError, "something went wrong at the table")
	}
}

// ── Agents & games ──────────────────────────────────────────────────────────

// listAgents is public: the roster is what the landing page shows.
func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, agents.All())
}

func (s *Server) listGames(w http.ResponseWriter, r *http.Request, u *auth.User) {
	out, err := s.Rooms.Games(r.Context(), u.ID)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getGame(w http.ResponseWriter, r *http.Request, u *auth.User) {
	out, err := s.Rooms.GameDetail(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// gameCover serves a game's cover to whoever may see the game. A versioned
// URL (?v= the current version, as GameCard.cover has it) is immutable; the
// bare URL revalidates. Built-ins and seeded examples without a stored cover
// redirect to the cover shipped with the web app.
func (s *Server) gameCover(w http.ResponseWriter, r *http.Request, u *auth.User) {
	c, err := s.Rooms.GameCover(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		roomsErr(w, err)
		return
	}
	if c.Static != "" {
		w.Header().Set("Cache-Control", "private, max-age=300")
		http.Redirect(w, r, c.Static, http.StatusFound)
		return
	}
	etag := fmt.Sprintf(`"cover-%s-%d"`, r.PathValue("id"), c.Version)
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Vary", "Cookie")
	if r.URL.Query().Get("v") == strconv.Itoa(c.Version) {
		h.Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	if match := r.Header.Get("If-None-Match"); match != "" && (match == etag || match == "*") {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", c.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(c.Bytes)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(c.Bytes)
	}
}

func (s *Server) patchGame(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in rooms.GamePatch
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	out, err := s.Rooms.PatchGame(r.Context(), u.ID, r.PathValue("id"), in)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteGame(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if err := s.Rooms.DeleteGame(r.Context(), u.ID, r.PathValue("id")); err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ── Tables ──────────────────────────────────────────────────────────────────

func (s *Server) createTable(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in rooms.CreateRequest
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	v, err := s.Rooms.Create(r.Context(), u.ID, in)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request, u *auth.User) {
	// ?limit= (with ?offset=) asks for a page: {tables, total, offset, limit}.
	// Without it, the newest 50 as a plain list.
	q := r.URL.Query()
	if q.Has("limit") {
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		pg, err := s.Rooms.ListPage(r.Context(), u.ID, q.Get("scope"), offset, limit)
		if err != nil {
			roomsErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, pg)
		return
	}
	out, err := s.Rooms.List(r.Context(), u.ID, q.Get("scope"))
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getTable(w http.ResponseWriter, r *http.Request, u *auth.User) {
	v, err := s.Rooms.View(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) joinTable(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	v, err := s.Rooms.Join(r.Context(), u.ID, in.Code)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) setSeat(w http.ResponseWriter, r *http.Request, u *auth.User) {
	seat, err := strconv.Atoi(r.PathValue("seat"))
	if err != nil || seat < 0 {
		writeErr(w, http.StatusBadRequest, "bad seat")
		return
	}
	var in rooms.SeatSpec
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	v, err := s.Rooms.SetSeat(r.Context(), u.ID, r.PathValue("id"), seat, in)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) startTable(w http.ResponseWriter, r *http.Request, u *auth.User) {
	v, err := s.Rooms.Start(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) postMove(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in rooms.MoveRequest
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	v, err := s.Rooms.Move(r.Context(), u.ID, r.PathValue("id"), in)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) postTableChat(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in rooms.ChatInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.Rooms.ChatWith(r.Context(), u.ID, r.PathValue("id"), in); err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// postTyping: the caller is writing a line (the client pings every few
// seconds while they type; the service drops pings closer than 2s).
func (s *Server) postTyping(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if err := s.Rooms.Typing(r.Context(), u.ID, r.PathValue("id")); err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// postReaction toggles one of the quick reactions on a chat line.
func (s *Server) postReaction(w http.ResponseWriter, r *http.Request, u *auth.User) {
	chatID, err := strconv.ParseInt(r.PathValue("chat"), 10, 64)
	if err != nil || chatID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad chat id")
		return
	}
	var in struct {
		Emoji string `json:"emoji"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	out, err := s.Rooms.React(r.Context(), u.ID, r.PathValue("id"), chatID, in.Emoji)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat_id": chatID, "reactions": out})
}

// backTable is "I'm back": clears the caller's away mark and resumes a
// paused table.
func (s *Server) backTable(w http.ResponseWriter, r *http.Request, u *auth.User) {
	v, err := s.Rooms.Back(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) leaveTable(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if err := s.Rooms.Leave(r.Context(), u.ID, r.PathValue("id")); err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) rematch(w http.ResponseWriter, r *http.Request, u *auth.User) {
	v, err := s.Rooms.Rematch(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// tableStream is the table's live feed (SSE):
//
//	event: table     {"version": N}            the view changed; refetch it
//	event: chat      ChatLine                  a new chat line (a whisper only to its two people)
//	event: typing    Typing                    someone is writing (never echoed to the writer)
//	event: reaction  {chat_id, reactions}      a line's reactions changed
//	event: presence  Presence                  who has the table open changed
//
// plus a comment every 20s so proxies keep the connection open. Table
// signals are coalesced briefly (one move writes several rows but is one
// change); chat goes out immediately.
//
// Access is checked when the stream opens and again while it runs: before
// sending anything once the last check is streamRecheck old, and on every
// keepalive. A person who may no longer watch (left as a spectator, account
// deleted) gets `event: closed` and the stream ends.
// streamRecheck is how stale a table stream's access check may get before
// the next event re-checks it.
var streamRecheck = 5 * time.Second

func newConnID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) tableStream(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id := r.PathValue("id")
	ok, err := s.Rooms.CanWatch(r.Context(), id, u.ID)
	if err != nil {
		roomsErr(w, err)
		return
	}
	if !ok {
		roomsErr(w, rooms.ErrForbidden)
		return
	}
	fl, isFlusher := w.(http.Flusher)
	if !isFlusher {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// Subscribe before reading the version, so no change can fall between.
	sub, done := s.Rooms.Subscribe(id)
	defer done()
	version, err := s.Rooms.Version(r.Context(), id)
	if err != nil {
		roomsErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		fl.Flush()
	}
	send("table", map[string]int64{"version": version})
	// Presence: this stream counts as "here" while it is open; the
	// keepalive refreshes it, and re-reads who else is here so a person
	// whose pod died drops off within PresenceTTL.
	connID := newConnID()
	var shown *rooms.Presence
	sendPresence := func(p *rooms.Presence) {
		if p != nil && !p.Equal(shown) {
			shown = p
			send("presence", p)
		}
	}
	if err := s.Rooms.Here(r.Context(), id, u.ID, connID); err != nil {
		slog.Warn("presence", "err", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer cancel()
		_ = s.Rooms.Gone(ctx, id, u.ID, connID)
	}()
	if p, err := s.Rooms.Presence(r.Context(), id); err == nil {
		sendPresence(p)
	}
	checked := time.Now()
	// allowed re-checks access when the last check is older than maxAge
	// (0: always). A failed check closes the stream rather than keep
	// sending to someone who may have lost access.
	allowed := func(maxAge time.Duration) bool {
		if time.Since(checked) < maxAge {
			return true
		}
		ok, err := s.Rooms.CanWatch(r.Context(), id, u.ID)
		if err == nil && ok {
			if !s.sessionAlive(r) {
				ok = false
			}
		}
		if err != nil || !ok {
			if r.Context().Err() == nil {
				send("closed", map[string]string{"reason": "access"})
			}
			return false
		}
		checked = time.Now()
		return true
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	var pending int64 // a table version waiting out the coalescing window
	flush := time.NewTimer(time.Hour)
	flush.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case sig := <-sub:
			switch sig.Kind {
			case "chat":
				if sig.Chat != nil && sig.For(u.ID) {
					if !allowed(streamRecheck) {
						return
					}
					send("chat", sig.Chat)
				}
			case "typing":
				if sig.Typing != nil && sig.From != u.ID {
					if !allowed(streamRecheck) {
						return
					}
					send("typing", sig.Typing)
				}
			case "reaction":
				if sig.Reaction != nil && sig.For(u.ID) {
					if !allowed(streamRecheck) {
						return
					}
					send("reaction", map[string]any{"chat_id": sig.Reaction.ChatID, "reactions": sig.Reaction.For(u.ID)})
				}
			case "presence":
				if !allowed(streamRecheck) {
					return
				}
				sendPresence(sig.Presence)
			case "table":
				if pending == 0 {
					flush.Reset(150 * time.Millisecond)
				}
				pending = max(pending, sig.Version)
			}
		case <-flush.C:
			if pending > version {
				if !allowed(streamRecheck) {
					return
				}
				version = pending
				send("table", map[string]int64{"version": version})
			}
			pending = 0
		case <-ping.C:
			if !allowed(0) {
				return
			}
			_ = s.Rooms.Here(r.Context(), id, u.ID, connID)
			if p, err := s.Rooms.Presence(r.Context(), id); err == nil {
				sendPresence(p)
			}
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}

// ── comments under a game ────────────────────────────────────────────────────

func (s *Server) gameComments(w http.ResponseWriter, r *http.Request, u *auth.User) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	out, err := s.Rooms.Comments(r.Context(), u.ID, r.PathValue("id"), before)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) postGameComment(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Body string `json:"body"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	c, err := s.Rooms.AddComment(r.Context(), u.ID, r.PathValue("id"), in.Body)
	if err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) deleteGameComment(w http.ResponseWriter, r *http.Request, u *auth.User) {
	id, err := strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.Rooms.DeleteComment(r.Context(), u.ID, r.PathValue("id"), id); err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
