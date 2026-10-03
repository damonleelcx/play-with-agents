package tools

import (
	"context"
	"encoding/json"
	"time"
)

// The generic tools every playbook may use. Game-specific tools (the studio's
// save_rules, save_module, check_module, playtest, publish_game) register
// themselves next to the script runtime they drive.
func init() {
	Register(&Tool{
		Name: "memory_save",
		Description: "Remember a durable fact about this player for future conversations (e.g. 'prefers short games', " +
			"'learning Texas Hold'em', 'likes cooperative games'). Not for details of the current task.",
		Input:  `{"type":"object","required":["fact"],"additionalProperties":false,"properties":{"fact":{"type":"string","minLength":3,"maxLength":300},"kind":{"type":"string","enum":["fact","preference"]}}}`,
		Output: `{"type":"object","required":["saved"]}`,
		Effect: Write, Gate: G0, Timeout: 5 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			// Memory is on unless the player switched it off; a missing
			// preferences row means the default.
			enabled := true
			_ = env.Pool.QueryRow(ctx, `SELECT coalesce((data->>'memory_enabled')::boolean, true) FROM user_preferences WHERE user_id=$1`, env.UserID).Scan(&enabled)
			if !enabled {
				return map[string]any{"saved": false, "reason": "the player turned long-term memory off"}, nil
			}
			kind := Str(a, "kind")
			if kind == "" {
				kind = "fact"
			}
			tag, err := env.Pool.Exec(ctx, `INSERT INTO memories (user_id, kind, content, source)
				SELECT $1, $2, $3, $4 WHERE NOT EXISTS (SELECT 1 FROM memories WHERE user_id=$1 AND lower(content)=lower($3))`,
				env.UserID, kind, Str(a, "fact"), "goal:"+env.GoalID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"saved": tag.RowsAffected() == 1}, nil
		},
	})

	Register(&Tool{
		Name: "notify_user",
		Description: "Post a progress update into the player's conversation with Aoi (e.g. 'The rules are drafted — now building the game'). " +
			"Use for meaningful milestones only, in the player's language.",
		Input:  `{"type":"object","required":["message"],"additionalProperties":false,"properties":{"message":{"type":"string","minLength":2,"maxLength":4000}}}`,
		Output: `{"type":"object","required":["posted"]}`,
		Effect: Write, Gate: G0, Timeout: 5 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			if env.ConversationID == "" {
				return map[string]any{"posted": false}, nil
			}
			// The key makes a retried step land on the same message row.
			key := Key(env, "notify", Str(a, "message"))
			meta, _ := json.Marshal(map[string]any{"goal_id": env.GoalID, "kind": "update",
				"cards": []map[string]any{{"kind": "mission", "goal_id": env.GoalID}}})
			_, err := env.Pool.Exec(ctx, `INSERT INTO messages (conversation_id, role, content, meta, client_msg_id)
				VALUES ($1::uuid, 'assistant', $2, $3, $4) ON CONFLICT DO NOTHING`, env.ConversationID, Str(a, "message"), meta, key)
			if err != nil {
				return nil, err
			}
			_, _ = env.Pool.Exec(ctx, `SELECT pg_notify('play_user', $1)`, env.UserID)
			return map[string]any{"posted": true}, nil
		},
	})
}
