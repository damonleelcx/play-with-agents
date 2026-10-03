package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/notify"
)

func (r *rig) outbox(t *testing.T) map[string]string {
	rows, _ := r.pool.Query(context.Background(), `SELECT to_email, kind FROM outbox ORDER BY id`)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var to, kind string
		_ = rows.Scan(&to, &kind)
		out[to] = kind
	}
	return out
}

// parkOn runs a one-task goal whose model calls tool once, which must park it
// for approval; returns the goal and the approval id.
func (r *rig) parkOn(t *testing.T, tool string, args map[string]any) (*Goal, string) {
	t.Helper()
	r.fake.mu.Lock()
	r.fake.script = func(req fakeReq) llm.Message {
		if toolResults(req) == 0 {
			return llm.Message{ToolCalls: []llm.ToolCall{call("c1", tool, args)}}
		}
		return finish
	}
	r.fake.mu.Unlock()
	g := r.goalWith(t, []PlannedTask{{Key: "a", Title: "A", Instructions: "x", Tools: []string{tool}}}, Limits{})
	ctx := context.Background()
	task, _ := r.store.Claim(ctx, "w", time.Minute)
	r.worker("w").Execute(ctx, task)
	var ap string
	if err := r.pool.QueryRow(ctx, `SELECT id FROM approvals WHERE goal_id=$1 AND status='pending'`, g.ID).Scan(&ap); err != nil {
		t.Fatalf("no approval created: %v", err)
	}
	return g, ap
}

// G1: the owner — and only the owner — is told an action waits for them, as
// the kind the tool declares. Nobody else is emailed.
func TestApprovalEmailGoesToTheOwnerOnly(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	newUser(t, r.pool, nil) // another verified account, which must not be emailed
	r.parkOn(t, "test_send", map[string]any{"to": "friend@example.com", "body": "b"})
	if got := r.outbox(t); len(got) != 1 || got[r.email] != notify.KindApprovalRequested {
		t.Fatalf("outbox = %v", got)
	}
	_, _ = r.pool.Exec(context.Background(), `TRUNCATE outbox`)
	_, _ = r.pool.Exec(context.Background(), `UPDATE goals SET status='completed'`)
	r.parkOn(t, "test_publish", map[string]any{})
	if got := r.outbox(t); len(got) != 1 || got[r.email] != notify.KindBuildReady {
		t.Fatalf("publish approval should be announced as build_ready: %v", got)
	}
}

// The owner's email_build_done=false silences approval emails; the approval
// itself still exists.
func TestApprovalEmailHonoursOptOut(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	_, _ = r.pool.Exec(context.Background(), `INSERT INTO user_preferences (user_id, data) VALUES ($1, '{"email_build_done": false}')`, r.userID)
	r.parkOn(t, "test_publish", map[string]any{})
	if got := r.outbox(t); len(got) != 0 {
		t.Fatalf("opted-out owner was emailed: %v", got)
	}
}

// Delivery sends each row once, retries a failure, renders the owner's
// language, never leaks the action's content, and skips a request that is no
// longer pending.
func TestApprovalEmailDeliveryRetryAndStaleness(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	_, _ = r.pool.Exec(context.Background(), `INSERT INTO user_preferences (user_id, data) VALUES ($1, '{"language": "zh"}')`, r.userID)
	g1, _ := r.parkOn(t, "test_publish", map[string]any{})
	ctx := context.Background()

	flaky := &flakyMailer{failFirst: 1}
	if n := notify.Deliver(ctx, r.pool, flaky, "https://play.test"); n != 0 {
		t.Fatalf("first attempt should fail, sent %d", n)
	}
	_, _ = r.pool.Exec(ctx, `UPDATE outbox SET next_attempt_at = now()`) // backoff elapses
	if n := notify.Deliver(ctx, r.pool, flaky, "https://play.test"); n != 1 {
		t.Fatalf("retry sent %d", n)
	}
	if n := notify.Deliver(ctx, r.pool, flaky, "https://play.test"); n != 0 {
		t.Fatalf("delivered twice: %d", n)
	}
	m := flaky.sent[0]
	if m.To != r.email || !strings.Contains(m.Subject, "发布") || !strings.Contains(m.Text, "https://play.test/app/studio/"+g1.ID) ||
		!strings.Contains(m.Text, "葵") {
		t.Fatalf("wrong message: %+v", m)
	}

	// A request whose approval is withdrawn before delivery is never sent.
	_, _ = r.pool.Exec(ctx, `UPDATE goals SET status='completed' WHERE id=$1`, g1.ID)
	_, ap2 := r.parkOn(t, "test_send", map[string]any{"to": "secret-recipient@example.com", "body": "secret body"})
	var text string
	_ = r.pool.QueryRow(ctx, `SELECT params::text FROM outbox WHERE params->>'approval_id'=$1`, ap2).Scan(&text)
	if strings.Contains(text, "secret") {
		t.Fatal("the outbox row carries the action's content")
	}
	_, _ = r.pool.Exec(ctx, `UPDATE approvals SET status='superseded' WHERE id=$1`, ap2)
	before := len(flaky.sent)
	notify.Deliver(ctx, r.pool, flaky, "https://play.test")
	if len(flaky.sent) != before {
		t.Fatal("sent a notification for a withdrawn approval")
	}
}

// Every kind renders in both languages, signed by Aoi, without game content.
func TestRenderEveryKindBothLanguages(t *testing.T) {
	for _, kind := range []string{notify.KindApprovalRequested, notify.KindBuildReady} {
		for _, lang := range []string{"en", "zh"} {
			m, err := notify.Render(kind, lang, map[string]any{"goal_title": "Dragon Chess", "goal_id": "g1", "tool": "publish_game"}, "https://o")
			if err != nil {
				t.Fatalf("%s/%s: %v", kind, lang, err)
			}
			if !strings.Contains(m.Text, "Dragon Chess") || !strings.Contains(m.HTML, "PLAY WITH AGENTS") || !strings.Contains(m.Text, mail.Signature(lang)) {
				t.Errorf("%s/%s: %q", kind, lang, m.Text)
			}
		}
	}
	if _, err := notify.Render("nope", "en", nil, ""); err == nil {
		t.Error("unknown kind rendered")
	}
}

type flakyMailer struct {
	mu        sync.Mutex
	failFirst int
	sent      []mail.Message
}

func (f *flakyMailer) Enabled() bool { return true }
func (f *flakyMailer) Send(_ context.Context, m mail.Message) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFirst > 0 {
		f.failFirst--
		return "", errors.New("relay unavailable")
	}
	f.sent = append(f.sent, m)
	return m.MessageID, nil
}
