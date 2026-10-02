package httpapi

// Tables, games and agents: the HTTP face of internal/rooms
// (docs/00-architecture.md, "HTTP API"). Handlers stay thin: they decode,
// call the service and map its errors to status codes.

import (
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
	m.HandleFunc("PATCH /api/games/{id}", s.verified(s.patchGame))
	m.HandleFunc("DELETE /api/games/{id}", s.verified(s.deleteGame))

	m.HandleFunc("POST /api/tables", s.limit("table-create", 20, s.verified(s.createTable)))
	m.HandleFunc("GET /api/tables", s.authed(s.listTables))
	m.HandleFunc("POST /api/tables/join", s.limit("table-join", 30, s.verified(s.joinTable)))
	m.HandleFunc("GET /api/tables/{id}", s.authed(s.getTable))
	m.HandleFunc("PUT /api/tables/{id}/seats/{seat}", s.verified(s.setSeat))
	m.HandleFunc("POST /api/tables/{id}/start", s.verified(s.startTable))
	m.HandleFunc("POST /api/tables/{id}/moves", s.limit("table-move", 240, s.verified(s.postMove)))
	m.HandleFunc("POST /api/tables/{id}/chat", s.limit("table-chat", 40, s.verified(s.postTableChat)))
	m.HandleFunc("POST /api/tables/{id}/leave", s.authed(s.leaveTable))
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
	out, err := s.Rooms.List(r.Context(), u.ID, r.URL.Query().Get("scope"))
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
	var in struct {
		Text        string `json:"text"`
		ClientMsgID string `json:"client_msg_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.Rooms.Chat(r.Context(), u.ID, r.PathValue("id"), in.Text, in.ClientMsgID); err != nil {
		roomsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
//	event: table  {"version": N}   the view changed; refetch it
//	event: chat   ChatLine         a new chat line
//
// plus a comment every 20s so proxies keep the connection open. Table
// signals are coalesced briefly (one move writes several rows but is one
// change); chat goes out immediately.
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
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		fl.Flush()
	}
	send("table", map[string]int64{"version": version})
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
				if sig.Chat != nil {
					send("chat", sig.Chat)
				}
			case "table":
				if pending == 0 {
					flush.Reset(150 * time.Millisecond)
				}
				pending = max(pending, sig.Version)
			}
		case <-flush.C:
			if pending > version {
				version = pending
				send("table", map[string]int64{"version": version})
			}
			pending = 0
		case <-ping.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}
