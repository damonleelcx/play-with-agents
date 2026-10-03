package studio

import "testing"

func TestBuildLimitsHonourOwnerCeilingsWithinStudioMaxima(t *testing.T) {
	cases := []struct {
		prefs       map[string]any
		cost        float64
		days        int
		description string
	}{
		{map[string]any{}, Limits.MaxCostUSD, Limits.MaxDays, "no preferences: the studio's limits"},
		{map[string]any{"goal_max_cost_usd": 3.0, "goal_max_days": 1.0}, 3, 1, "lower ceilings apply"},
		{map[string]any{"goal_max_cost_usd": 200.0, "goal_max_days": 180.0}, Limits.MaxCostUSD, Limits.MaxDays, "higher ones are clamped"},
		{map[string]any{"goal_max_cost_usd": "4", "goal_max_days": "x"}, 4, Limits.MaxDays, "numeric strings; junk ignored"},
		{map[string]any{"goal_max_cost_usd": 0.0, "goal_max_days": -2.0}, Limits.MaxCostUSD, Limits.MaxDays, "out of range ignored"},
	}
	for _, c := range cases {
		l := buildLimits(c.prefs)
		if l.MaxCostUSD != c.cost || l.MaxDays != c.days {
			t.Errorf("%s: got $%v / %d days, want $%v / %d days", c.description, l.MaxCostUSD, l.MaxDays, c.cost, c.days)
		}
		if l.MaxTokens != Limits.MaxTokens || l.MaxReplans != Limits.MaxReplans {
			t.Errorf("%s: other limits changed: %+v", c.description, l)
		}
	}
}
