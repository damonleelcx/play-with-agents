// Package notify is the email outbox: rows are enqueued inside the transaction
// that makes the announced fact true, and delivered later with retries.
//
// Privacy: a notification says WHAT kind of action waits and for WHICH case,
// never the content (a letter body, a prescription, a diagnosis). The content
// stays behind sign-in; the email only brings the person there.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/mail"
)

// ApprovalRequested enqueues "an action waits for you" to whoever may decide
// the approval: the client for G1; every verified professional of the
// required role for G2. Each recipient's notify_approvals preference is
// honoured (default on). Idempotent per (approval, recipient).
func ApprovalRequested(ctx context.Context, tx pgx.Tx, approvalID string) (int64, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO outbox (key, kind, user_id, to_email, lang, params)
		SELECT 'approval-req:' || a.id || ':' || u.id, 'approval_requested', u.id, u.email,
		       coalesce(p.data->>'language', 'en'),
		       jsonb_build_object('approval_id', a.id, 'tool', a.tool, 'gate', a.gate, 'role', a.required_role,
		                          'goal_title', g.title, 'own', u.id = a.user_id,
		                          'client', CASE WHEN u.id = a.user_id THEN '' ELSE coalesce(nullif(owner.name, ''), 'a client') END)
		FROM approvals a
		JOIN goals g ON g.id = a.goal_id
		JOIN users owner ON owner.id = a.user_id
		JOIN users u ON (a.gate = 'G1' AND u.id = a.user_id)
		             OR (a.gate = 'G2' AND u.role = a.required_role AND EXISTS (
		                   SELECT 1 FROM professional_licenses l WHERE l.user_id = u.id AND l.status = 'verified'
		                   AND l.kind = CASE a.required_role WHEN 'attorney' THEN 'bar' WHEN 'physician' THEN 'medical' END))
		LEFT JOIN user_preferences p ON p.user_id = u.id
		WHERE a.id = $1 AND u.deleted_at IS NULL AND u.email_verified_at IS NOT NULL
		  AND coalesce((p.data->>'notify_approvals')::boolean, true)
		ON CONFLICT (key) DO NOTHING`, approvalID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ApprovalDecided tells the client that a professional decided an action on
// their case. A client deciding their own G1 approval is not emailed about it.
func ApprovalDecided(ctx context.Context, tx pgx.Tx, approvalID string) (int64, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO outbox (key, kind, user_id, to_email, lang, params)
		SELECT 'approval-dec:' || a.id, 'approval_decided', u.id, u.email, coalesce(p.data->>'language', 'en'),
		       jsonb_build_object('approval_id', a.id, 'tool', a.tool, 'decision', a.status, 'role', a.required_role, 'goal_title', g.title)
		FROM approvals a
		JOIN goals g ON g.id = a.goal_id
		JOIN users u ON u.id = a.user_id
		LEFT JOIN user_preferences p ON p.user_id = u.id
		WHERE a.id = $1 AND a.status IN ('approved', 'rejected') AND a.decided_by IS DISTINCT FROM a.user_id
		  AND u.deleted_at IS NULL AND u.email_verified_at IS NOT NULL
		  AND coalesce((p.data->>'notify_email')::boolean, true)
		ON CONFLICT (key) DO NOTHING`, approvalID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Deliver sends due outbox rows. The claim pushes next_attempt_at forward in
// the same statement, so a second scheduler (or this one after a crash) does
// not pick the row up again until the backoff expires. Returns rows sent.
func Deliver(ctx context.Context, pool *pgxpool.Pool, m mail.Mailer, origin string) int {
	rows, err := pool.Query(ctx, `
		UPDATE outbox SET attempts = attempts + 1,
		       next_attempt_at = now() + make_interval(secs => least(3600, 30 * power(2, attempts)))
		WHERE id IN (SELECT id FROM outbox WHERE sent_at IS NULL AND failed_at IS NULL AND next_attempt_at <= now()
		             ORDER BY id LIMIT 20 FOR UPDATE SKIP LOCKED)
		RETURNING id, key, kind, to_email, lang, params, attempts`)
	if err != nil {
		return 0
	}
	type row struct {
		id            int64
		key, kind, to string
		lang          string
		params        map[string]any
		attempts      int
	}
	var rs []row
	for rows.Next() {
		var r row
		var raw []byte
		if rows.Scan(&r.id, &r.key, &r.kind, &r.to, &r.lang, &raw, &r.attempts) == nil {
			_ = json.Unmarshal(raw, &r.params)
			rs = append(rs, r)
		}
	}
	rows.Close()
	sent := 0
	for _, r := range rs {
		// "Waiting for your approval" is stale once the approval was decided
		// or withdrawn (e.g. the case was cancelled) — don't send it.
		if r.kind == "approval_requested" {
			var pending bool
			_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approvals WHERE id=$1::uuid AND status='pending')`, str(r.params, "approval_id")).Scan(&pending)
			if !pending {
				_, _ = pool.Exec(context.WithoutCancel(ctx), `UPDATE outbox SET sent_at = now(), error = 'skipped: approval no longer pending' WHERE id = $1`, r.id)
				continue
			}
		}
		msg, err := Render(r.kind, r.lang, r.params, origin)
		if err == nil {
			msg.To = r.to
			msg.MessageID = "outbox-" + r.key
			_, err = m.Send(ctx, msg)
		}
		c := context.WithoutCancel(ctx)
		if err == nil {
			_, _ = pool.Exec(c, `UPDATE outbox SET sent_at = now(), error = '' WHERE id = $1`, r.id)
			sent++
			continue
		}
		slog.Warn("outbox send failed", "key", r.key, "attempt", r.attempts, "err", err)
		if r.attempts >= 6 {
			_, _ = pool.Exec(c, `UPDATE outbox SET failed_at = now(), error = $2 WHERE id = $1`, r.id, err.Error())
		} else {
			_, _ = pool.Exec(c, `UPDATE outbox SET error = $2 WHERE id = $1`, r.id, err.Error())
		}
	}
	return sent
}

var toolNames = map[string][2]string{
	"email_send":                   {"send an email", "发送一封邮件"},
	"court_efile":                  {"file a document with the court", "向法院提交文书"},
	"rx_submit":                    {"send a prescription", "开具处方"},
	"lab_order":                    {"order tests", "开具检查单"},
	"referral_send":                {"send a referral", "开具转诊"},
	"request_professional_signoff": {"sign off on a work product", "对工作成果签字确认"},
}

func toolName(tool, lang string) string {
	n, ok := toolNames[tool]
	if !ok {
		n = [2]string{tool, tool}
	}
	if lang == "zh" {
		return n[1]
	}
	return n[0]
}

func roleName(role, lang string) string {
	zh := map[string]string{"attorney": "律师", "physician": "医生"}
	if lang == "zh" {
		if v, ok := zh[role]; ok {
			return v
		}
	}
	return role
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// Render builds the bilingual message for an outbox row.
func Render(kind, lang string, p map[string]any, origin string) (mail.Message, error) {
	link := origin + "/app/approvals"
	action := toolName(str(p, "tool"), lang)
	title := str(p, "goal_title")
	switch kind {
	case "approval_requested":
		own := p["own"] == true
		if lang == "zh" {
			lead := fmt.Sprintf("维拉准备好了一项需要你同意的操作：%s（案件：「%s」）。在你确认之前，它不会被执行。", action, title)
			if !own {
				lead = fmt.Sprintf("客户 %s 的案件「%s」中，有一项需要持证%s审批的操作：%s。", str(p, "client"), title, roleName(str(p, "role"), lang), action)
			}
			return mail.Message{Subject: "有一项操作等待你的审批 · ACT",
				Text: lead + "\n\n查看并决定：" + link + "\n\n出于隐私考虑，具体内容只在登录后显示。\n\n—— 维拉，ACT",
				HTML: mail.Page("等待你的审批", lead, "查看并决定", link, "出于隐私考虑，具体内容只在登录后显示。你可以在设置 → 通知中关闭此类邮件。")}, nil
		}
		lead := fmt.Sprintf("Vera has an action ready that needs your approval: %s (case: “%s”). Nothing happens until you decide.", action, title)
		if !own {
			lead = fmt.Sprintf("A case for %s (“%s”) has an action that needs a licensed %s's approval: %s.", str(p, "client"), title, str(p, "role"), action)
		}
		return mail.Message{Subject: "An action is waiting for your approval · ACT",
			Text: lead + "\n\nReview and decide: " + link + "\n\nFor privacy, the details are only shown after you sign in.\n\n— Vera, ACT",
			HTML: mail.Page("Waiting for your approval", lead, "Review and decide", link, "For privacy, the details are only shown after you sign in. You can turn these emails off in Settings → Notifications.")}, nil
	case "approval_decided":
		approved := str(p, "decision") == "approved"
		link = origin + "/app/cases"
		if lang == "zh" {
			verb := "已同意"
			if !approved {
				verb = "未同意"
			}
			lead := fmt.Sprintf("持证%s%s你案件「%s」中的一项操作：%s。维拉会据此继续推进。", roleName(str(p, "role"), lang), verb, title, action)
			return mail.Message{Subject: "你的案件有新进展 · ACT", Text: lead + "\n\n" + link + "\n\n—— 维拉，ACT",
				HTML: mail.Page("你的案件有新进展", lead, "查看案件", link, "你可以在设置 → 通知中关闭此类邮件。")}, nil
		}
		verb := "approved"
		if !approved {
			verb = "declined"
		}
		lead := fmt.Sprintf("A licensed %s %s an action on your case “%s”: %s. Vera will carry on from here.", str(p, "role"), verb, title, action)
		return mail.Message{Subject: "An update on your case · ACT", Text: lead + "\n\n" + link + "\n\n— Vera, ACT",
			HTML: mail.Page("An update on your case", lead, "View your case", link, "You can turn these emails off in Settings → Notifications.")}, nil
	}
	return mail.Message{}, fmt.Errorf("unknown notification kind %q", kind)
}
