package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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
		{"theme": "sepia"},
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
		"turn_seconds", "agent_speed", "table_talk", "four_color_deck", "show_hand_strength", "sound", "motion",
		"card_back", "felt", "agent_difficulty", "fill_empty_seats", "favorite_agents", "studio_visibility", "playtest_games",
		"email_table_invites", "email_your_turn", "email_build_done",
		"theme", "font_size", "goal_max_cost_usd", "goal_max_days"} {
		if !seen[k] {
			t.Errorf("setting %s missing from the schema", k)
		}
	}
	if len(seen) != 32 {
		t.Errorf("schema has %d settings, the contract has 32", len(seen))
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
