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

// addUser creates a verified user with an optional role, licence and prefs.
func (r *rig) addUser(t *testing.T, email, role, licence string, notifyApprovals bool, lang string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := r.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name, role, email_verified_at) VALUES ($1,'x',$1,$2,now()) RETURNING id`,
		email, role).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if licence != "" {
		_, _ = r.pool.Exec(ctx, `INSERT INTO professional_licenses (user_id, kind, number, jurisdiction, status) VALUES ($1,$2,'N1','CA',$3)`,
			id, strings.Split(licence, ":")[0], strings.Split(licence, ":")[1])
	}
	_, _ = r.pool.Exec(ctx, `INSERT INTO user_preferences (user_id, data) VALUES ($1, jsonb_build_object('notify_approvals', $2::boolean, 'language', $3::text))`,
		id, notifyApprovals, lang)
	return id
}

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

func (r *rig) parkOn(t *testing.T, tool string, args map[string]any) (*Goal, string) {
	t.Helper()
	calls := 0
	r.fake.mu.Lock()
	r.fake.script = func(req fakeReq) llm.Message {
		calls++
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
	if err := r.pool.QueryRow(ctx, `SELECT id FROM approvals WHERE goal_id=$1`, g.ID).Scan(&ap); err != nil {
		t.Fatalf("no approval created: %v", err)
	}
	return g, ap
}

// G1: the client — and only the client — is told an action waits for them.
func TestApprovalEmailGoesToClientForG1(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	r.addUser(t, "doc@example.com", "physician", "medical:verified", true, "en")
	r.parkOn(t, "email_send", map[string]any{"to": "landlord@example.com", "subject": "s", "body": "b", "on_behalf_of": "client"})
	got := r.outbox(t)
	if len(got) != 1 || got["c@example.com"] != "approval_requested" {
		t.Fatalf("outbox = %v", got)
	}
}

// G2: every VERIFIED professional of the required role who has not opted out.
func TestApprovalEmailGoesToVerifiedProfessionalsForG2(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	r.addUser(t, "att1@example.com", "attorney", "bar:verified", true, "en")
	r.addUser(t, "att2@example.com", "attorney", "bar:verified", true, "zh")
	r.addUser(t, "pending@example.com", "attorney", "bar:pending", true, "en")   // not verified
	r.addUser(t, "optout@example.com", "attorney", "bar:verified", false, "en")  // opted out
	r.addUser(t, "doc@example.com", "physician", "medical:verified", true, "en") // wrong role
	r.addUser(t, "fake@example.com", "attorney", "medical:verified", true, "en") // licence of the wrong kind
	r.parkOn(t, "email_send", map[string]any{"to": "oc@example.com", "subject": "s", "body": "b", "on_behalf_of": "counsel"})
	got := r.outbox(t)
	if len(got) != 2 || got["att1@example.com"] == "" || got["att2@example.com"] == "" {
		t.Fatalf("outbox = %v", got)
	}
}

// A professional's decision tells the client; delivery sends each row once,
// retries a failure, and skips a request that is no longer pending.
func TestApprovalEmailsDecisionDeliveryAndRetry(t *testing.T) {
	r := newRig(t, func(fakeReq) llm.Message { return finish })
	att := r.addUser(t, "att1@example.com", "attorney", "bar:verified", true, "zh")
	g1, ap := r.parkOn(t, "email_send", map[string]any{"to": "oc@example.com", "subject": "s", "body": "b", "on_behalf_of": "counsel"})
	ctx := context.Background()

	flaky := &flakyMailer{failFirst: 1}
	if n := notify.Deliver(ctx, r.pool, flaky, "https://act.test"); n != 0 {
		t.Fatalf("first attempt should fail, sent %d", n)
	}
	_, _ = r.pool.Exec(ctx, `UPDATE outbox SET next_attempt_at = now()`) // backoff elapses
	if n := notify.Deliver(ctx, r.pool, flaky, "https://act.test"); n != 1 {
		t.Fatalf("retry sent %d", n)
	}
	if n := notify.Deliver(ctx, r.pool, flaky, "https://act.test"); n != 0 {
		t.Fatalf("delivered twice: %d", n)
	}
	m := flaky.sent[0]
	if m.To != "att1@example.com" || !strings.Contains(m.Subject, "审批") || !strings.Contains(m.Text, "https://act.test/app/approvals") {
		t.Fatalf("wrong message: %+v", m)
	}
	if strings.Contains(m.Text, "oc@example.com") {
		t.Fatal("the email leaked the action's content")
	}

	if err := r.store.Decide(ctx, ap, att, true, ""); err != nil {
		t.Fatal(err)
	}
	if got := r.outbox(t); got["c@example.com"] != "approval_decided" {
		t.Fatalf("client not told of the decision: %v", got)
	}
	if n := notify.Deliver(ctx, r.pool, flaky, "https://act.test"); n != 1 {
		t.Fatalf("decision email sent %d", n)
	}

	// A request whose approval is withdrawn before delivery is never sent.
	_, _ = r.pool.Exec(ctx, `UPDATE goals SET status='completed' WHERE id=$1`, g1.ID) // its task is not what we claim next
	_, ap2 := r.parkOn(t, "email_send", map[string]any{"to": "x@example.com", "subject": "t", "body": "c", "on_behalf_of": "counsel"})
	_, _ = r.pool.Exec(ctx, `UPDATE approvals SET status='superseded' WHERE id=$1`, ap2)
	before := len(flaky.sent)
	notify.Deliver(ctx, r.pool, flaky, "https://act.test")
	if len(flaky.sent) != before {
		t.Fatal("sent a notification for a withdrawn approval")
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
