package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	mailer "github.com/damonleelcx/play-with-agents/internal/mail"
)

func init() {
	register(&Tool{
		Name:        "kb_search",
		Description: "Search this client's own documents (uploads, drafts, memos, plans) by keywords. Use before asking the client for something they may already have provided.",
		Input:       `{"type":"object","required":["query"],"additionalProperties":false,"properties":{"query":{"type":"string","minLength":2,"maxLength":200},"limit":{"type":"integer","minimum":1,"maximum":10}}}`,
		Output:      `{"type":"object","required":["hits"],"properties":{"hits":{"type":"array"}}}`,
		Effect:      Read, Gate: G0, Timeout: 10 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			hits, err := SearchKnowledge(ctx, env, str(a, "query"), int(num(a, "limit")))
			if err != nil {
				return nil, err
			}
			return map[string]any{"hits": hits}, nil
		},
	})

	register(&Tool{
		Name:        "get_document",
		Description: "Read the full text of one of this client's documents by id.",
		Input:       `{"type":"object","required":["document_id"],"additionalProperties":false,"properties":{"document_id":{"type":"string","minLength":36,"maxLength":36}}}`,
		Output:      `{"type":"object","required":["title","content"]}`,
		Effect:      Read, Gate: G0, Timeout: 5 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			var title, kind, content string
			err := env.Pool.QueryRow(ctx, `SELECT title, kind, content FROM documents WHERE id=$1::uuid AND user_id=$2`,
				str(a, "document_id"), env.UserID).Scan(&title, &kind, &content)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, &InvalidInput{fmt.Errorf("no such document for this client")}
			}
			if err != nil {
				return nil, err
			}
			return map[string]any{"title": title, "kind": kind, "content": truncate(content, 30000)}, nil
		},
	})

	register(&Tool{
		Name: "save_document",
		Description: "Save a work product (draft letter, memo, contract redline, hearing kit, care plan, summary) to the client's file. " +
			"It becomes searchable and visible to the client. Saving the same title again creates a new version.",
		Input: `{"type":"object","required":["title","kind","content"],"additionalProperties":false,"properties":{
			"title":{"type":"string","minLength":2,"maxLength":200},
			"kind":{"type":"string","enum":["letter","memo","pleading","contract_review","hearing_kit","evidence_index","care_plan","clinical_summary","patient_education","records_summary","report","other"]},
			"content":{"type":"string","minLength":20,"maxLength":120000}}}`,
		Output: `{"type":"object","required":["document_id","version","sha256"]}`,
		Effect: Write, Gate: G0, Timeout: 10 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			id, ver, sum, err := SaveDocument(ctx, env, str(a, "title"), str(a, "kind"), str(a, "content"))
			if err != nil {
				return nil, err
			}
			return map[string]any{"document_id": id, "version": ver, "sha256": sum}, nil
		},
		// Read it back: a write is only done when the stored bytes are the bytes.
		Verify: func(ctx context.Context, env *Env, a, out map[string]any) error {
			var sum string
			if err := env.Pool.QueryRow(ctx, `SELECT sha256 FROM documents WHERE id=$1::uuid`, str(out, "document_id")).Scan(&sum); err != nil {
				return err
			}
			if sum != str(out, "sha256") || sum != sha(str(a, "content")) {
				return fmt.Errorf("stored document does not match what was written")
			}
			return nil
		},
	})

	register(&Tool{
		Name:        "memory_save",
		Description: "Remember a durable fact about this client for future conversations (e.g. 'allergic to penicillin', 'lives in Oakland, CA', 'prefers email'). Not for temporary task details.",
		Input:       `{"type":"object","required":["fact"],"additionalProperties":false,"properties":{"fact":{"type":"string","minLength":3,"maxLength":300},"kind":{"type":"string","enum":["fact","preference"]}}}`,
		Output:      `{"type":"object","required":["saved"]}`,
		Effect:      Write, Gate: G0, Timeout: 5 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			var enabled bool
			_ = env.Pool.QueryRow(ctx, `SELECT coalesce((data->>'memory_enabled')::boolean, true) FROM user_preferences WHERE user_id=$1`, env.UserID).Scan(&enabled)
			if !enabled {
				var exists bool
				_ = env.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_preferences WHERE user_id=$1)`, env.UserID).Scan(&exists)
				if exists {
					return map[string]any{"saved": false, "reason": "the client turned long-term memory off"}, nil
				}
			}
			kind := str(a, "kind")
			if kind == "" {
				kind = "fact"
			}
			tag, err := env.Pool.Exec(ctx, `INSERT INTO memories (user_id, kind, content, source)
				SELECT $1, $2, $3, $4 WHERE NOT EXISTS (SELECT 1 FROM memories WHERE user_id=$1 AND lower(content)=lower($3))`,
				env.UserID, kind, str(a, "fact"), "goal:"+env.GoalID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"saved": tag.RowsAffected() == 1}, nil
		},
	})

	register(&Tool{
		Name: "email_send",
		Description: "Send an email. on_behalf_of decides who must approve: 'client' (the client approves their own letter), 'counsel' (a licensed attorney approves — required for anything sent as a lawyer or to opposing counsel/courts), " +
			"'physician' (a licensed physician approves). Mail to the client's own address needs no approval.",
		Input: `{"type":"object","required":["to","subject","body","on_behalf_of"],"additionalProperties":false,"properties":{
			"to":{"type":"string","minLength":3,"maxLength":254},"subject":{"type":"string","minLength":1,"maxLength":200},
			"body":{"type":"string","minLength":1,"maxLength":50000},"on_behalf_of":{"type":"string","enum":["client","counsel","physician"]}}}`,
		Output: `{"type":"object","required":["message_id","accepted"]}`,
		Effect: External, Gate: G1, Timeout: 60 * time.Second, MaxRetries: 2,
		GateFor: func(a map[string]any) (Gate, string) {
			switch str(a, "on_behalf_of") {
			case "counsel":
				return G2, "attorney"
			case "physician":
				return G2, "physician"
			}
			return G1, "client"
		},
		IdemKey: func(env *Env, a map[string]any) string {
			return Key(env, "email_send", strings.ToLower(str(a, "to")), str(a, "subject"), sha(str(a, "body")))
		},
		Preview: func(a map[string]any) string {
			return fmt.Sprintf("To: %s\nSubject: %s\n\n%s", str(a, "to"), str(a, "subject"), truncate(str(a, "body"), 1500))
		},
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			addr, err := mail.ParseAddress(str(a, "to"))
			if err != nil {
				return nil, &InvalidInput{fmt.Errorf("recipient: %v", err)}
			}
			if env.Mailer == nil {
				return nil, fmt.Errorf("mail is not configured")
			}
			key := Key(env, "email_send", strings.ToLower(str(a, "to")), str(a, "subject"), sha(str(a, "body")))
			id, err := env.Mailer.Send(ctx, mailer.Message{To: addr.Address, Subject: str(a, "subject"), Text: str(a, "body"),
				ReplyTo: env.UserEmail, MessageID: strings.ReplaceAll(key, ":", "-")})
			if err != nil {
				// The relay refused before accepting DATA; nothing was sent,
				// so a retry cannot duplicate.
				return nil, &Transient{err}
			}
			return map[string]any{"message_id": id, "accepted": env.Mailer.Enabled(), "to": addr.Address}, nil
		},
		Verify: func(_ context.Context, env *Env, _, out map[string]any) error {
			if out["accepted"] != true && env.Mailer != nil && env.Mailer.Enabled() {
				return fmt.Errorf("relay did not accept the message")
			}
			return nil
		},
	})

	register(&Tool{
		Name:        "schedule_reminder",
		Description: "Schedule a reminder for the client (deadlines, hearings, medication check-ins, follow-ups). It is delivered in the conversation and by email when due.",
		Input: `{"type":"object","required":["due_at","text"],"additionalProperties":false,"properties":{
			"due_at":{"type":"string","description":"RFC3339 timestamp or YYYY-MM-DD (09:00 client local)"},"text":{"type":"string","minLength":3,"maxLength":500}}}`,
		Output: `{"type":"object","required":["reminder_id","due_at"]}`,
		Effect: Write, Gate: G0, Timeout: 5 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			due, err := parseDue(str(a, "due_at"))
			if err != nil {
				return nil, &InvalidInput{err}
			}
			if due.Before(time.Now().Add(-time.Minute)) {
				return nil, &InvalidInput{fmt.Errorf("due_at %s is in the past", due.Format(time.RFC3339))}
			}
			key := Key(env, "reminder", due.Unix(), str(a, "text"))
			var id string
			err = env.Pool.QueryRow(ctx, `INSERT INTO reminders (user_id, goal_id, due_at, text, key)
				VALUES ($1, nullif($2,'')::uuid, $3, $4, $5)
				ON CONFLICT (key) DO UPDATE SET text = EXCLUDED.text RETURNING id`,
				env.UserID, env.GoalID, due, str(a, "text"), key).Scan(&id)
			if err != nil {
				return nil, err
			}
			return map[string]any{"reminder_id": id, "due_at": due.Format(time.RFC3339)}, nil
		},
	})

	register(&Tool{
		Name: "notify_user",
		Description: "Post a progress update into the client's conversation (e.g. 'Your demand letter draft is ready for your review'). " +
			"Use for meaningful milestones only, in the client's language.",
		Input:  `{"type":"object","required":["message"],"additionalProperties":false,"properties":{"message":{"type":"string","minLength":2,"maxLength":4000}}}`,
		Output: `{"type":"object","required":["posted"]}`,
		Effect: Write, Gate: G0, Timeout: 5 * time.Second,
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			if env.ConversationID == "" {
				return map[string]any{"posted": false}, nil
			}
			key := Key(env, "notify", str(a, "message"))
			meta, _ := json.Marshal(map[string]any{"goal_id": env.GoalID, "kind": "update"})
			_, err := env.Pool.Exec(ctx, `INSERT INTO messages (conversation_id, role, content, meta, client_msg_id)
				VALUES ($1::uuid, 'assistant', $2, $3, $4) ON CONFLICT DO NOTHING`, env.ConversationID, str(a, "message"), meta, key)
			if err != nil {
				return nil, err
			}
			_, _ = env.Pool.Exec(ctx, `SELECT pg_notify('act_user', $1)`, env.UserID)
			return map[string]any{"posted": true}, nil
		},
	})

	register(&Tool{
		Name: "request_professional_signoff",
		Description: "Put a work product in front of a licensed professional for review and sign-off (attorney for legal work, physician for clinical work). " +
			"Required before any diagnosis, treatment plan, filing or legal advice is presented as final. The task pauses until they decide.",
		Input: `{"type":"object","required":["role","document_id","summary"],"additionalProperties":false,"properties":{
			"role":{"type":"string","enum":["attorney","physician"]},"document_id":{"type":"string","minLength":36,"maxLength":36},
			"summary":{"type":"string","minLength":10,"maxLength":4000}}}`,
		Output: `{"type":"object","required":["signed_off"]}`,
		Effect: Write, Gate: G2,
		GateFor: func(a map[string]any) (Gate, string) { return G2, str(a, "role") },
		Timeout: 5 * time.Second,
		Preview: func(a map[string]any) string {
			return str(a, "summary") + "\n\n(document " + str(a, "document_id") + ")"
		},
		Run: func(ctx context.Context, env *Env, a map[string]any) (map[string]any, error) {
			if err := docBelongs(ctx, env, str(a, "document_id")); err != nil {
				return nil, err
			}
			return map[string]any{"signed_off": true, "document_id": str(a, "document_id")}, nil
		},
	})
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func docBelongs(ctx context.Context, env *Env, id string) error {
	var ok bool
	err := env.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE id=$1::uuid AND user_id=$2)`, id, env.UserID).Scan(&ok)
	if err != nil {
		return &InvalidInput{fmt.Errorf("document id: %v", err)}
	}
	if !ok {
		return &InvalidInput{fmt.Errorf("document %s does not exist in this client's file", id)}
	}
	return nil
}

func parseDue(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Add(9 * time.Hour), nil
	}
	if t, err := time.Parse("2006-01-02T15:04", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("due_at %q is not RFC3339 or YYYY-MM-DD", s)
}

// SaveDocument versions by title within the client's file and indexes the
// content for retrieval, in one transaction.
func SaveDocument(ctx context.Context, env *Env, title, kind, content string) (string, int, string, error) {
	sum := sha(content)
	var id string
	var ver int
	err := pgx.BeginFunc(ctx, env.Pool, func(tx pgx.Tx) error {
		// Same bytes under the same title is the same document: a retried
		// save must not mint version 2 of identical text.
		err := tx.QueryRow(ctx, `SELECT id, version FROM documents WHERE user_id=$1 AND title=$2 AND sha256=$3`,
			env.UserID, title, sum).Scan(&id, &ver)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(version),0)+1 FROM documents WHERE user_id=$1 AND title=$2`, env.UserID, title).Scan(&ver); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO documents (user_id, goal_id, task_id, title, kind, content, sha256, version)
			VALUES ($1, nullif($2,'')::uuid, nullif($3,'')::uuid, $4, $5, $6, $7, $8) RETURNING id`,
			env.UserID, env.GoalID, env.TaskID, title, kind, content, sum, ver).Scan(&id); err != nil {
			return err
		}
		for i, ch := range Chunk(content, 1200) {
			if _, err := tx.Exec(ctx, `INSERT INTO knowledge_chunks (user_id, document_id, ord, content) VALUES ($1,$2,$3,$4)`,
				env.UserID, id, i, ch); err != nil {
				return err
			}
		}
		return nil
	})
	return id, ver, sum, err
}

// Chunk splits on paragraph boundaries into pieces of at most n runes.
func Chunk(s string, n int) []string {
	var out []string
	var cur strings.Builder
	for _, p := range strings.Split(s, "\n\n") {
		if cur.Len() > 0 && len([]rune(cur.String()))+len([]rune(p)) > n {
			out = append(out, cur.String())
			cur.Reset()
		}
		for len([]rune(p)) > n {
			r := []rune(p)
			out = append(out, string(r[:n]))
			p = string(r[n:])
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

func SearchKnowledge(ctx context.Context, env *Env, query string, limit int) ([]any, error) {
	if limit <= 0 {
		limit = 5
	}
	// websearch_to_tsquery with OR semantics: any keyword may match, ranked.
	terms := strings.Fields(query)
	q := strings.Join(terms, " or ")
	rows, err := env.Pool.Query(ctx, `SELECT d.id, d.title, d.kind, k.content, ts_rank(k.tsv, websearch_to_tsquery('simple', $2)) r
		FROM knowledge_chunks k JOIN documents d ON d.id = k.document_id
		WHERE k.user_id=$1 AND (k.tsv @@ websearch_to_tsquery('simple', $2) OR k.content ILIKE '%'||$3||'%')
		ORDER BY r DESC, d.created_at DESC LIMIT $4`, env.UserID, q, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []any{}
	for rows.Next() {
		var id, title, kind, content string
		var r float32
		if err := rows.Scan(&id, &title, &kind, &content, &r); err != nil {
			return nil, err
		}
		hits = append(hits, map[string]any{"document_id": id, "title": title, "kind": kind, "excerpt": truncate(content, 700)})
	}
	return hits, rows.Err()
}
