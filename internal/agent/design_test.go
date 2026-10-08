package agent

import (
	"context"
	"strings"
	"testing"
)

func TestDesignModeDesignsPlansAndBuildsOnlyWhenAsked(t *testing.T) {
	// The router would start a build; a design conversation never routes.
	r := newRig(t, map[string]any{"intent": IntentBuild, "confidence": 0.95, "prompt": "a game"}, nil)
	ctx := context.Background()
	studio := &fakeStudio{}
	r.agent.Studio = studio
	conv, err := r.agent.NewDesign(ctx, r.uid)
	if err != nil {
		t.Fatal(err)
	}
	r.conv = conv

	meta := r.turn(t, "Let's build a game about tides and islands!")
	if meta["intent"] != IntentDesign || meta["cards"] != nil || studio.prompt != "" {
		t.Fatalf("a design reply routed or built something: %v (studio %q)", meta, studio.prompt)
	}
	if sys := r.fake.systemPrompt(); !strings.Contains(sys, "GAME DESIGN MODE") {
		t.Fatalf("the reply was not briefed for design:\n%s", sys)
	}
	st, err := r.agent.ConversationDesign(ctx, conv)
	if err != nil || st.Mode != "design" || st.Design.Title != "Tide Lords" || st.Design.Readiness != 45 || len(st.Design.Mechanics) != 1 {
		t.Fatalf("design notes: %+v %v", st, err)
	}
	var title string
	_ = r.pool.QueryRow(ctx, `SELECT title FROM conversations WHERE id=$1`, conv).Scan(&title)
	if title != "Tide Lords" {
		t.Fatalf("conversation title %q", title)
	}

	u := User{ID: r.uid, Name: "Sam", Lang: "en"}
	if _, err := r.agent.DesignBuild(ctx, u, conv); err == nil || studio.prompt != "" {
		t.Fatal("built without a plan")
	}
	st, err = r.agent.DesignPlan(ctx, u, conv)
	if err != nil || !strings.HasPrefix(st.Plan, "# Tide Lords") || st.PlanAt == nil || st.Stale {
		t.Fatalf("plan: %+v %v", st, err)
	}
	st, err = r.agent.DesignBuild(ctx, u, conv)
	if err != nil || studio.prompt != st.Plan || st.GoalID == "" {
		t.Fatalf("build: %+v %v (studio got %q)", st, err, studio.prompt)
	}
	var cards int
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1 AND meta->'cards'->0->>'kind'='mission'`, conv).Scan(&cards)
	if cards != 1 {
		t.Fatalf("mission card messages: %d", cards)
	}

	// Talking on after the plan marks it stale.
	r.turn(t, "Actually, make the tide move twice a round.")
	if st, _ = r.agent.ConversationDesign(ctx, conv); !st.Stale {
		t.Fatalf("the plan should be stale after more design: %+v", st)
	}

	// A plain chat is not a design session.
	if _, err := r.agent.DesignPlan(ctx, u, r.newChat(t)); err != ErrNoDesign {
		t.Fatalf("plan for a chat: %v", err)
	}
}

func (r *rig) newChat(t *testing.T) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `INSERT INTO conversations (user_id) VALUES ($1) RETURNING id`, r.uid).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
