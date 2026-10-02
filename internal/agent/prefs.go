package agent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The settings a player can change, with their allowed values and defaults —
// the Settings table of docs/00-architecture.md. It lives here because two
// places write preferences and must agree: the settings API and Aoi herself
// ("call me Captain", "be quieter"). Stored in user_preferences.data; a
// missing key means the default, so defaults can change without a migration.
//
// display_name is part of the Profile group but is stored as users.name (the
// one name every page shows), so it is not in this list.

// PrefKind is the shape of a preference value.
type PrefKind int

const (
	PrefEnum   PrefKind = iota // one of Values (strings)
	PrefInt                    // one of Values (numbers written as strings: "15")
	PrefBool                   // true / false
	PrefText                   // free text up to MaxLen runes
	PrefAgents                 // list of roster ids
	PrefRange                  // an integer from Min to Max
)

type PrefSpec struct {
	Key     string
	Group   string // profile | aoi | table | agents | studio | notifications
	Kind    PrefKind
	Values  []string
	MaxLen  int
	Min     int // PrefRange
	Max     int // PrefRange
	Default any
	// Chat is true when Aoi may change it from conversation. Lists and
	// anything that only makes sense with the settings page in front of you
	// stay on the page.
	Chat bool
}

var PrefSpecs = []PrefSpec{
	{Key: "language", Group: "profile", Kind: PrefEnum, Values: []string{"en", "zh"}, Default: "en", Chat: true},

	{Key: "aoi_tone", Group: "aoi", Kind: PrefEnum, Values: []string{"playful", "calm", "competitive"}, Default: "playful", Chat: true},
	{Key: "aoi_talk", Group: "aoi", Kind: PrefEnum, Values: []string{"chatty", "normal", "quiet"}, Default: "normal", Chat: true},
	{Key: "aoi_coaching", Group: "aoi", Kind: PrefBool, Default: false, Chat: true},
	{Key: "call_me", Group: "aoi", Kind: PrefText, MaxLen: 40, Default: "", Chat: true},
	{Key: "memory_enabled", Group: "aoi", Kind: PrefBool, Default: true, Chat: true},
	// Aoi's voice (docs/00-architecture.md, "Aoi's voice"): a speaker button
	// on her messages, reading new replies aloud, reading her table talk.
	{Key: "aoi_voice", Group: "aoi", Kind: PrefBool, Default: true, Chat: true},
	{Key: "voice_autoplay", Group: "aoi", Kind: PrefBool, Default: false, Chat: true},
	{Key: "table_voice", Group: "aoi", Kind: PrefBool, Default: false, Chat: true},
	{Key: "voice_volume", Group: "aoi", Kind: PrefRange, Min: 0, Max: 100, Default: 80, Chat: true},

	{Key: "turn_seconds", Group: "table", Kind: PrefInt, Values: []string{"15", "30", "60", "0"}, Default: 30, Chat: true},
	{Key: "agent_speed", Group: "table", Kind: PrefEnum, Values: []string{"fast", "natural", "slow"}, Default: "natural", Chat: true},
	{Key: "table_talk", Group: "table", Kind: PrefEnum, Values: []string{"all", "quiet", "off"}, Default: "all", Chat: true},
	{Key: "four_color_deck", Group: "table", Kind: PrefBool, Default: false, Chat: true},
	{Key: "show_hand_strength", Group: "table", Kind: PrefBool, Default: true, Chat: true},
	{Key: "sound", Group: "table", Kind: PrefBool, Default: true, Chat: true},
	{Key: "motion", Group: "table", Kind: PrefEnum, Values: []string{"full", "reduced"}, Default: "full", Chat: true},
	{Key: "card_back", Group: "table", Kind: PrefEnum, Values: []string{"aoi", "classic", "midnight"}, Default: "aoi", Chat: true},
	{Key: "felt", Group: "table", Kind: PrefEnum, Values: []string{"navy", "emerald", "crimson"}, Default: "navy", Chat: true},

	{Key: "agent_difficulty", Group: "agents", Kind: PrefEnum, Values: []string{"casual", "regular", "shark"}, Default: "regular", Chat: true},
	{Key: "fill_empty_seats", Group: "agents", Kind: PrefBool, Default: true, Chat: true},
	{Key: "favorite_agents", Group: "agents", Kind: PrefAgents, Default: []string{}},

	{Key: "studio_visibility", Group: "studio", Kind: PrefEnum, Values: []string{"private", "unlisted", "public"}, Default: "private", Chat: true},
	{Key: "playtest_games", Group: "studio", Kind: PrefInt, Values: []string{"50", "200", "500"}, Default: 200, Chat: true},

	// Appearance: applied by the browser, stored here so it follows the
	// player to every device.
	{Key: "theme", Group: "appearance", Kind: PrefEnum, Values: []string{"dark", "light", "system"}, Default: "dark", Chat: true},
	{Key: "font_size", Group: "appearance", Kind: PrefEnum, Values: []string{"small", "medium", "large"}, Default: "medium", Chat: true},

	// Usage & limits: ceilings for one studio build (a mission), on top of
	// the account and deployment token caps.
	{Key: "goal_max_cost_usd", Group: "limits", Kind: PrefRange, Min: 1, Max: 200, Default: 20},
	{Key: "goal_max_days", Group: "limits", Kind: PrefRange, Min: 1, Max: 180, Default: 30},

	{Key: "email_table_invites", Group: "notifications", Kind: PrefBool, Default: true, Chat: true},
	{Key: "email_your_turn", Group: "notifications", Kind: PrefBool, Default: true, Chat: true},
	{Key: "email_build_done", Group: "notifications", Kind: PrefBool, Default: true, Chat: true},
}

// PrefSpecFor returns the spec of a key.
func PrefSpecFor(key string) (PrefSpec, bool) {
	for _, s := range PrefSpecs {
		if s.Key == key {
			return s, true
		}
	}
	return PrefSpec{}, false
}

// PrefDefaults returns every preference at its default.
func PrefDefaults() map[string]any {
	out := make(map[string]any, len(PrefSpecs))
	for _, s := range PrefSpecs {
		out[s.Key] = s.Default
	}
	return out
}

// WithDefaults overlays stored preferences on the defaults. Stored values that
// no longer validate (a removed option) fall back to the default instead of
// reaching the client; unknown stored keys are dropped.
func WithDefaults(stored map[string]any) map[string]any {
	out := PrefDefaults()
	for k, v := range stored {
		if _, known := PrefSpecFor(k); known && ValidatePref(k, v) == nil {
			out[k] = v
		}
	}
	return out
}

// ValidatePref checks a JSON-decoded value (string, float64, bool, []any)
// against the key's spec.
func ValidatePref(key string, v any) error {
	s, ok := PrefSpecFor(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	bad := func() error {
		if len(s.Values) > 0 {
			return fmt.Errorf("%s must be one of %s", key, strings.Join(s.Values, ", "))
		}
		return fmt.Errorf("invalid value for %s", key)
	}
	switch s.Kind {
	case PrefEnum:
		if str, ok := v.(string); ok && contains(s.Values, str) {
			return nil
		}
		return bad()
	case PrefInt:
		if n, ok := wholeNumber(v); ok && contains(s.Values, strconv.Itoa(n)) {
			return nil
		}
		return bad()
	case PrefRange:
		if n, ok := wholeNumber(v); ok && n >= s.Min && n <= s.Max {
			return nil
		}
		return fmt.Errorf("%s must be a whole number from %d to %d", key, s.Min, s.Max)
	case PrefBool:
		if _, ok := v.(bool); ok {
			return nil
		}
		return fmt.Errorf("%s must be true or false", key)
	case PrefText:
		str, ok := v.(string)
		if !ok || utf8.RuneCountInString(str) > s.MaxLen || strings.ContainsAny(str, "\n\r\t") {
			return fmt.Errorf("%s must be a single line of at most %d characters", key, s.MaxLen)
		}
		return nil
	case PrefAgents:
		var ids []string
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				id, ok := e.(string)
				if !ok {
					return fmt.Errorf("%s must be a list of agent ids", key)
				}
				ids = append(ids, id)
			}
		case []string:
			ids = x
		default:
			return fmt.Errorf("%s must be a list of agent ids", key)
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !IsAgentID(id) || seen[id] {
				return fmt.Errorf("%s: %q is not a roster agent (or is listed twice)", key, id)
			}
			seen[id] = true
		}
		return nil
	}
	return bad()
}

// wholeNumber accepts a JSON number (float64) or an int that is integral.
func wholeNumber(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x == float64(int(x)) {
			return int(x), true
		}
	case int:
		return x, true
	}
	return 0, false
}

// NormalizePref turns a JSON-decoded, valid value into its stored form:
// numbers as JSON numbers, agent lists as []string, text trimmed.
func NormalizePref(key string, v any) any {
	s, _ := PrefSpecFor(key)
	switch s.Kind {
	case PrefInt, PrefRange:
		if f, ok := v.(float64); ok {
			return int(f)
		}
	case PrefText:
		if str, ok := v.(string); ok {
			return strings.TrimSpace(str)
		}
	case PrefAgents:
		if arr, ok := v.([]any); ok {
			out := make([]string, 0, len(arr))
			for _, e := range arr {
				out = append(out, e.(string))
			}
			return out
		}
	}
	return v
}

// coercePref converts the router's string value for a chat-changeable key
// into a typed value, or fails if it does not fit.
func coercePref(key, raw string) (any, error) {
	s, ok := PrefSpecFor(key)
	if !ok || !s.Chat {
		return nil, fmt.Errorf("%q cannot be changed from chat", key)
	}
	raw = strings.TrimSpace(raw)
	var v any = raw
	switch s.Kind {
	case PrefBool:
		switch strings.ToLower(raw) {
		case "true", "on", "yes", "1", "enable", "enabled":
			v = true
		case "false", "off", "no", "0", "disable", "disabled":
			v = false
		}
	case PrefInt, PrefRange:
		num := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(raw, "%"), " seconds"), "s"))
		n, err := strconv.Atoi(num)
		if err != nil {
			if s.Kind == PrefRange {
				return nil, fmt.Errorf("%s must be a whole number from %d to %d", key, s.Min, s.Max)
			}
			return nil, fmt.Errorf("%s must be one of %s", key, strings.Join(s.Values, ", "))
		}
		v = float64(n)
	case PrefEnum:
		v = strings.ToLower(raw)
	}
	if err := ValidatePref(key, v); err != nil {
		return nil, err
	}
	return NormalizePref(key, v), nil
}

// chatPrefKeys lists the keys Aoi may set, for the router prompt.
func chatPrefKeys() string {
	var parts []string
	for _, s := range PrefSpecs {
		if !s.Chat {
			continue
		}
		switch s.Kind {
		case PrefBool:
			parts = append(parts, s.Key+"=true|false")
		case PrefText:
			parts = append(parts, s.Key+"=<text>")
		case PrefRange:
			parts = append(parts, fmt.Sprintf("%s=%d..%d", s.Key, s.Min, s.Max))
		default:
			parts = append(parts, s.Key+"="+strings.Join(s.Values, "|"))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
