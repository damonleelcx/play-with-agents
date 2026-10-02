package httpapi

// The studio's HTTP face (docs/00-architecture.md, "Studio"):
//
//	POST /api/studio/build { prompt, base_game_id? } → { goal_id, game_id }
//
// Progress then uses the goal endpoints. A build started here gets its own
// conversation (titled after the idea, with the idea as its first message),
// so the studio's agents have the player's words and Aoi has somewhere to
// post the result.

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/damonleelcx/play-with-agents/internal/agent"
	"github.com/damonleelcx/play-with-agents/internal/auth"
)

// studioBuildsPerMinute bounds builds per IP; each one spends real tokens.
const studioBuildsPerMinute = 6

func (s *Server) studioBuild(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.Agent == nil || s.Agent.Studio == nil {
		writeErr(w, 503, "the studio is not open on this server")
		return
	}
	var in struct {
		Prompt     string `json:"prompt"`
		BaseGameID string `json:"base_game_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" || len([]rune(in.Prompt)) > 4000 {
		writeErr(w, 400, "prompt must be 1 to 4000 characters")
		return
	}
	ctx := r.Context()
	lang := "en"
	_ = s.Pool.QueryRow(ctx, `SELECT coalesce(data->>'language','en') FROM user_preferences WHERE user_id=$1`, u.ID).Scan(&lang)

	title := []rune(strings.SplitN(in.Prompt, "\n", 2)[0])
	if len(title) > 60 {
		title = append(title[:57], '…')
	}
	var convID string
	if err := s.Pool.QueryRow(ctx, `INSERT INTO conversations (user_id, title) VALUES ($1, $2) RETURNING id`, u.ID, string(title)).Scan(&convID); err != nil {
		writeErr(w, 500, "error")
		return
	}
	_, _ = s.Pool.Exec(ctx, `INSERT INTO messages (conversation_id, role, content, meta) VALUES ($1, 'user', $2, '{"source":"studio"}')`, convID, in.Prompt)

	goalID, gameID, err := s.Agent.Studio.StartBuild(ctx, u.ID, convID, in.Prompt, strings.TrimSpace(in.BaseGameID), lang)
	if err != nil {
		_, _ = s.Pool.Exec(ctx, `DELETE FROM conversations WHERE id=$1`, convID)
		if errors.Is(err, agent.ErrRejected) {
			writeErr(w, 422, strings.TrimPrefix(err.Error(), agent.ErrRejected.Error()+": "))
			return
		}
		slog.Error("studio build", "user", u.ID, "err", err)
		writeErr(w, 500, "error")
		return
	}
	writeJSON(w, 200, map[string]string{"goal_id": goalID, "game_id": gameID, "conversation_id": convID})
}
