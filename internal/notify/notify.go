// Package notify is the email outbox: rows are enqueued inside the transaction
// that makes the announced fact true, and delivered later with retries.
//
// Privacy: a notification says WHAT is waiting and for WHICH mission, never
// the content. In particular an email never carries a game's source code or
// rules text; those stay behind sign-in, and the email only brings the player
// there.
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

// Notification kinds the outbox renders.
const (
	// KindApprovalRequested: a mission action waits for the owner's approval.
	KindApprovalRequested = "approval_requested"
	// KindBuildReady: a game is built, playtested and reviewed, and waits for
	// the owner's approval to publish it.
	KindBuildReady = "build_ready"
)

// ApprovalRequested enqueues the owner's notification for a G1 approval, as
// the given kind (KindApprovalRequested or KindBuildReady). It honours the
// owner's email_build_done preference (default on) and is idempotent per
// approval.
func ApprovalRequested(ctx context.Context, tx pgx.Tx, approvalID, kind string) (int64, error) {
	if kind != KindBuildReady {
		kind = KindApprovalRequested
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO outbox (key, kind, user_id, to_email, lang, params)
		SELECT 'approval-req:' || a.id, $2, u.id, u.email,
		       coalesce(p.data->>'language', 'en'),
		       jsonb_build_object('approval_id', a.id, 'goal_id', a.goal_id, 'tool', a.tool, 'goal_title', g.title)
		FROM approvals a
		JOIN goals g ON g.id = a.goal_id
		JOIN users u ON u.id = a.user_id
		LEFT JOIN user_preferences p ON p.user_id = u.id
		WHERE a.id = $1 AND u.deleted_at IS NULL AND u.email_verified_at IS NOT NULL
		  AND coalesce((p.data->>'email_build_done')::boolean, true)
		ON CONFLICT (key) DO NOTHING`, approvalID, kind)
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
		// or withdrawn (e.g. the build was cancelled) — don't send it.
		if r.kind == KindApprovalRequested || r.kind == KindBuildReady {
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

// toolNames are the player-facing names of actions that can wait for
// approval. An unknown tool falls back to a generic phrase rather than its
// internal name.
var toolNames = map[string]map[string]string{
	"publish_game": {"en": "publish your game", "zh": "发布你的游戏", "ko": "게임 공개하기", "ja": "ゲームを公開する"},
	"":             {"en": "a step in your mission", "zh": "任务中的一个步骤", "ko": "미션의 한 단계", "ja": "ミッションの一ステップ"},
}

func toolName(tool, lang string) string {
	n, ok := toolNames[tool]
	if !ok {
		n = toolNames[""]
	}
	if s, ok := n[lang]; ok {
		return s
	}
	return n["en"]
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// Render builds the message, in en | zh | ko | ja, for an outbox row. Only the mission
// title travels in params; nothing a game contains is ever rendered here.
func Render(kind, lang string, p map[string]any, origin string) (mail.Message, error) {
	title := str(p, "goal_title")
	sig := mail.Signature(lang)
	switch kind {
	case KindBuildReady:
		link := origin + "/app/studio/" + str(p, "goal_id")
		switch lang {
		case "ja":
			lead := fmt.Sprintf("いい知らせだよ！「%s」が完成しました。ルールを書いて、ゲームを組み立てて、エージェントたちとテストプレイも済ませたよ。公開はあなたが OK を出してから。その前に、いつでも自分で遊んでみてね。", title)
			return mail.Message{Subject: "ゲームが完成、公開を待っています · Play with Agents",
				Text: lead + "\n\n確認して公開する：" + link + "\n\n" + sig,
				HTML: mail.Page(lang, "ゲームが完成したよ", lead, "確認して公開する", link, "このメールは 設定 → 通知 でオフにできます。")}, nil
		case "ko":
			lead := fmt.Sprintf("좋은 소식이에요! “%s” 완성됐어요. 규칙도 쓰고, 게임도 만들고, 에이전트들이랑 테스트 플레이까지 끝냈어요. 공개는 직접 OK해 주셔야 진행돼요 — 그 전에 언제든 먼저 직접 플레이해 보세요.", title)
			return mail.Message{Subject: "게임이 완성됐어요, 공개를 기다리고 있어요 · Play with Agents",
				Text: lead + "\n\n확인하고 공개하기: " + link + "\n\n" + sig,
				HTML: mail.Page(lang, "게임이 완성됐어요", lead, "확인하고 공개하기", link, "이 메일은 설정 → 알림에서 끌 수 있어요.")}, nil
		}
		if lang == "zh" {
			lead := fmt.Sprintf("好消息！「%s」已经做好了：规则写完、代码搭好、模拟对局也跑过了。你同意之后它才会发布；在那之前你随时可以先自己玩一局。", title)
			return mail.Message{Subject: "你的游戏做好了，等你发布 · Play with Agents",
				Text: lead + "\n\n查看并发布：" + link + "\n\n" + sig,
				HTML: mail.Page(lang, "你的游戏做好了", lead, "查看并发布", link, "你可以在设置 → 通知 中关闭此类邮件。")}, nil
		}
		lead := fmt.Sprintf("Good news! “%s” is built: the rules are written, the game is coded, and the agents have playtested it. It goes live only when you say so — and you can play it yourself first, any time.", title)
		return mail.Message{Subject: "Your game is ready to publish · Play with Agents",
			Text: lead + "\n\nReview and publish: " + link + "\n\n" + sig,
			HTML: mail.Page(lang, "Your game is ready", lead, "Review and publish", link, "You can turn these emails off in Settings → Notifications.")}, nil
	case KindApprovalRequested:
		link := origin + "/app/approvals"
		action := toolName(str(p, "tool"), lang)
		switch lang {
		case "ja":
			lead := fmt.Sprintf("「%s」で、あなたの OK が必要なステップがあるよ：%s。あなたが決めるまで、何も起きません。", title, action)
			return mail.Message{Subject: "あなたの確認を待っているステップがあります · Play with Agents",
				Text: lead + "\n\n確認して決める：" + link + "\n\n" + sig,
				HTML: mail.Page(lang, "あなたの確認待ち", lead, "確認して決める", link, "このメールは 設定 → 通知 でオフにできます。")}, nil
		case "ko":
			lead := fmt.Sprintf("“%s”에서 확인이 필요한 단계가 있어요: %s. 결정해 주시기 전까지는 아무 일도 일어나지 않아요.", title, action)
			return mail.Message{Subject: "확인을 기다리는 단계가 있어요 · Play with Agents",
				Text: lead + "\n\n확인하고 결정하기: " + link + "\n\n" + sig,
				HTML: mail.Page(lang, "확인을 기다리고 있어요", lead, "확인하고 결정하기", link, "이 메일은 설정 → 알림에서 끌 수 있어요.")}, nil
		}
		if lang == "zh" {
			lead := fmt.Sprintf("「%s」里有一步需要你点头：%s。你确认之前，什么都不会发生。", title, action)
			return mail.Message{Subject: "有一步等你确认 · Play with Agents",
				Text: lead + "\n\n查看并决定：" + link + "\n\n" + sig,
				HTML: mail.Page(lang, "等你确认", lead, "查看并决定", link, "你可以在设置 → 通知 中关闭此类邮件。")}, nil
		}
		lead := fmt.Sprintf("A step in “%s” needs your OK: %s. Nothing happens until you decide.", title, action)
		return mail.Message{Subject: "A step is waiting for your OK · Play with Agents",
			Text: lead + "\n\nReview and decide: " + link + "\n\n" + sig,
			HTML: mail.Page(lang, "Waiting for your OK", lead, "Review and decide", link, "You can turn these emails off in Settings → Notifications.")}, nil
	}
	return mail.Message{}, fmt.Errorf("unknown notification kind %q", kind)
}
