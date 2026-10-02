package httpapi

// ── Aoi's voice ────────────────────────────────────────────────────────────
//
// GET  /api/speech → { enabled }
// POST /api/speech → audio/mpeg, for exactly one of:
//   { message_id }          one of Aoi's replies in a conversation the caller owns
//   { table_id, chat_id }   one of Aoi's table-talk lines at a table the caller may watch
//   { sample: "en"|"zh" }   the fixed "Hear Aoi" line of the settings page
// The endpoint only ever speaks text the server already holds as Aoi's: it is
// not a general text-to-speech service. Signed in and verified, 20 requests a
// minute and a daily character allowance per account (PLAY_TTS_DAILY_CHARS,
// counted in Postgres, charged only for lines that reach the vendor: cached
// lines are free). Text is cleaned of Markdown and emoji and capped at
// tts.MaxSpokenChars. 503 when the deployment has no voice configured.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/auth"
	"github.com/damonleelcx/play-with-agents/internal/rooms"
	"github.com/damonleelcx/play-with-agents/internal/tts"
)

const speechPerMinute = 20

// DefaultSpeechDailyChars is the per-account daily allowance when the server
// is not given one.
const DefaultSpeechDailyChars = 20000

// speechSamples are the only fixed lines the endpoint speaks: the settings
// page's "Hear Aoi" sample, chosen by language, never sent by the client.
var speechSamples = map[string]string{
	"en": "Hi, I’m Aoi! Pull up a chair — the cards are about to be dealt. 你好，我是葵！",
	"zh": "你好，我是葵！快坐下，马上就要发牌啦。Nice to meet you!",
}

var errSpeechCap = errors.New("today's voice allowance is used up; it resets at 00:00 UTC")

type speechRequest struct {
	MessageID int64  `json:"message_id,omitempty"`
	TableID   string `json:"table_id,omitempty"`
	ChatID    int64  `json:"chat_id,omitempty"`
	Sample    string `json:"sample,omitempty"`
}

func (s *Server) speechStatus(w http.ResponseWriter, r *http.Request, u *auth.User) {
	writeJSON(w, 200, map[string]bool{"enabled": s.Speech != nil})
}

func (s *Server) speechDailyChars() int {
	if s.SpeechDailyChars > 0 {
		return s.SpeechDailyChars
	}
	return DefaultSpeechDailyChars
}

// speechText resolves a request to the stored line it names. On failure it
// returns the HTTP status and message to answer with.
func (s *Server) speechText(ctx context.Context, u *auth.User, in speechRequest) (string, int, string) {
	sources := 0
	if in.MessageID != 0 {
		sources++
	}
	if in.TableID != "" || in.ChatID != 0 {
		sources++
	}
	if in.Sample != "" {
		sources++
	}
	if sources != 1 {
		return "", 400, "send one of message_id, table_id with chat_id, or sample"
	}
	switch {
	case in.Sample != "":
		text, ok := speechSamples[in.Sample]
		if !ok {
			return "", 400, "sample must be en or zh"
		}
		return text, 0, ""
	case in.MessageID != 0:
		var text string
		err := s.Pool.QueryRow(ctx, `SELECT m.content FROM messages m JOIN conversations c ON c.id = m.conversation_id
			WHERE m.id=$1 AND c.user_id=$2 AND m.role='assistant'`, in.MessageID, u.ID).Scan(&text)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 404, "not found"
		}
		if err != nil {
			slog.Error("speech: load message", "err", err)
			return "", 500, "could not load the message"
		}
		return text, 0, ""
	default:
		if in.TableID == "" || in.ChatID <= 0 || s.Rooms == nil {
			return "", 400, "table_id and chat_id go together"
		}
		text, err := s.Rooms.AoiLine(ctx, in.TableID, in.ChatID, u.ID)
		switch {
		case err == nil:
			return text, 0, ""
		case errors.Is(err, rooms.ErrNotFound):
			return "", 404, "not found"
		case errors.Is(err, rooms.ErrForbidden):
			return "", 403, "you are not at this table"
		default:
			slog.Error("speech: load table line", "err", err)
			return "", 500, "could not load the line"
		}
	}
}

// chargeSpeech adds chars to the user's count for today (UTC), refusing,
// and adding nothing, when that would pass the allowance. One statement, so
// concurrent requests cannot overshoot it together.
func (s *Server) chargeSpeech(ctx context.Context, userID string, chars int) error {
	limit := s.speechDailyChars()
	if chars > limit {
		return errSpeechCap
	}
	var total int
	err := s.Pool.QueryRow(ctx, `INSERT INTO tts_usage (user_id, day, chars) VALUES ($1, (now() AT TIME ZONE 'UTC')::date, $2)
		ON CONFLICT (user_id, day) DO UPDATE SET chars = tts_usage.chars + EXCLUDED.chars
			WHERE tts_usage.chars + EXCLUDED.chars <= $3
		RETURNING chars`, userID, chars, limit).Scan(&total)
	if errors.Is(err, pgx.ErrNoRows) {
		return errSpeechCap
	}
	return err
}

func (s *Server) speak(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if s.Speech == nil {
		writeErr(w, 503, "voice is not available")
		return
	}
	// Per user, not per IP: a whole household behind one address should not
	// share one bucket, and one account should not get more by changing IPs.
	if !s.limiter.allow("speech|"+u.ID, speechPerMinute) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "too many requests, try again in a minute")
		return
	}
	var in speechRequest
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	text, status, msg := s.speechText(r.Context(), u, in)
	if status != 0 {
		writeErr(w, status, msg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	audio, ct, err := s.Speech.SpeakCharged(ctx, text, func(n int) error { return s.chargeSpeech(ctx, u.ID, n) })
	if err != nil {
		switch {
		case errors.Is(err, tts.ErrEmpty):
			writeErr(w, 400, "nothing to speak")
		case errors.Is(err, errSpeechCap):
			w.Header().Set("Retry-After", "3600")
			writeErr(w, 429, errSpeechCap.Error())
		default:
			slog.Error("speech", "user", u.ID, "err", err)
			writeErr(w, 502, "the voice is unavailable right now")
		}
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprint(len(audio)))
	// The same line always renders the same audio, so the browser may keep it.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(200)
	_, _ = w.Write(audio)
}
