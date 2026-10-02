package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/damonleelcx/play-with-agents/internal/auth"
	"github.com/damonleelcx/play-with-agents/internal/tts"
)

func TestValidateSettings(t *testing.T) {
	name, set, reset, err := validateSettings(nil, map[string]any{
		"display_name": "  Sam  ", "aoi_tone": "competitive", "turn_seconds": float64(60),
		"favorite_agents": []any{"mika", "nova"}, "felt": nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if name == nil || *name != "Sam" {
		t.Errorf("display_name should become users.name, got %v", name)
	}
	if set["aoi_tone"] != "competitive" || set["turn_seconds"] != 60 || len(set["favorite_agents"].([]string)) != 2 {
		t.Errorf("set = %v", set)
	}
	if _, stored := set["display_name"]; stored {
		t.Error("display_name was stored as a preference")
	}
	if len(reset) != 1 || reset[0] != "felt" {
		t.Errorf("reset = %v", reset)
	}

	for _, bad := range []map[string]any{
		{"aoi_tone": "grumpy"},
		{"turn_seconds": float64(45)},
		{"playtest_games": "200"},
		{"theme": "dark"},               // not a setting any more
		{"nonsense": nil},               // cannot reset what does not exist
		{"favorite_agents": []any{"x"}}, // not on the roster
		{"display_name": strings.Repeat("n", maxNameLen+1)},
		{"call_me": "two\nlines"},
		{"voice_volume": float64(101)},
		{"voice_volume": 7.5},
	} {
		if _, _, _, err := validateSettings(nil, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestSchemaCoversEverySettingOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range settingsSchema() {
		if seen[p.Key] || p.Kind == "" {
			t.Errorf("schema entry %+v duplicated or without a kind", p)
		}
		seen[p.Key] = true
	}
	// The contract's Settings table, key by key.
	for _, k := range []string{"display_name", "language", "aoi_tone", "aoi_talk", "aoi_coaching", "call_me", "memory_enabled",
		"aoi_voice", "voice_autoplay", "table_voice", "voice_volume",
		"turn_seconds", "agent_speed", "table_talk", "four_color_deck", "auto_muck", "show_hand_strength", "sound", "motion",
		"card_back", "felt", "agent_difficulty", "fill_empty_seats", "favorite_agents", "studio_visibility", "playtest_games",
		"email_table_invites", "email_your_turn", "email_build_done"} {
		if !seen[k] {
			t.Errorf("setting %s missing from the schema", k)
		}
	}
	if len(seen) != 29 {
		t.Errorf("schema has %d settings, the contract has 29", len(seen))
	}
}

// State-changing API calls without the custom header are refused before any
// handler runs (CSRF); GETs and non-API paths are not affected.
func TestCSRFHeaderRequiredOnMutations(t *testing.T) {
	s := &Server{Static: fstest.MapFS{"index.html": {Data: []byte("<html>")}}, Hub: NewHub()}
	h := s.Handler()
	do := func(method, path string, hdr map[string]string) int {
		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do(http.MethodPost, "/api/auth/signout", nil); c != http.StatusForbidden {
		t.Errorf("mutation without header: %d", c)
	}
	if c := do(http.MethodPost, "/api/auth/signout", map[string]string{"X-Play": "1"}); c != http.StatusOK {
		t.Errorf("mutation with X-Play: %d", c)
	}
	if c := do(http.MethodGet, "/", nil); c != http.StatusOK {
		t.Errorf("SPA shell: %d", c)
	}
	for _, gone := range []string{"/api/documents", "/api/admin/licenses"} {
		if c := do(http.MethodGet, gone, nil); c != http.StatusNotFound {
			t.Errorf("%s still routed: %d", gone, c)
		}
	}
}

type fakeVoice struct{ got []string }

func (f *fakeVoice) SpeakMP3(_ context.Context, text string) ([]byte, string, error) {
	f.got = append(f.got, text)
	return []byte("ID3mp3"), "audio/mpeg", nil
}

func TestSpeechEndpoint(t *testing.T) {
	u := &auth.User{ID: "u1", EmailVerified: true}
	post := func(s *Server, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.speak(rec, httptest.NewRequest(http.MethodPost, "/api/speech", strings.NewReader(body)), u)
		return rec
	}

	off := &Server{limiter: newLimiter()}
	rec := httptest.NewRecorder()
	off.speechStatus(rec, httptest.NewRequest(http.MethodGet, "/api/speech", nil), u)
	if !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Errorf("status without a voice: %s", rec.Body)
	}
	if c := post(off, `{"text":"hi"}`).Code; c != http.StatusServiceUnavailable {
		t.Errorf("no voice configured: %d", c)
	}

	voice := &fakeVoice{}
	on := &Server{limiter: newLimiter(), Speech: tts.NewService(voice, 200)}
	rec = post(on, `{"text":"**Table's up!** 🎉"}`)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "audio/mpeg" || rec.Body.String() != "ID3mp3" {
		t.Fatalf("speak: %d %s %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if len(voice.got) != 1 || voice.got[0] != "Table's up!" {
		t.Errorf("vendor got %q", voice.got)
	}
	if c := post(on, `{"text":"🎉"}`).Code; c != http.StatusBadRequest {
		t.Errorf("emoji-only: %d", c)
	}
	// 2 used above; the bucket holds 20 a minute per user.
	for i := 0; i < 18; i++ {
		if c := post(on, `{"text":"Table's up!"}`).Code; c != 200 {
			t.Fatalf("request %d: %d", i+3, c)
		}
	}
	if c := post(on, `{"text":"Table's up!"}`).Code; c != http.StatusTooManyRequests {
		t.Errorf("21st request in a minute: %d", c)
	}
	if len(voice.got) != 1 {
		t.Errorf("repeats were not served from cache: %d vendor calls", len(voice.got))
	}
	other := httptest.NewRecorder()
	on.speak(other, httptest.NewRequest(http.MethodPost, "/api/speech", strings.NewReader(`{"text":"hi"}`)), &auth.User{ID: "u2", EmailVerified: true})
	if other.Code != 200 {
		t.Errorf("the limit is per user, another user got %d", other.Code)
	}
}
