-- 0002 · Transactional outbox for notification email.
--
-- A notification is written in the SAME transaction as the fact it announces
-- (an approval requested, an approval decided), and delivered later by the
-- scheduler. So a crash can neither lose the email nor send one for a change
-- that rolled back. Delivery is at-least-once; the Message-ID is the key, so a
-- rare duplicate is recognisable as the same message.
CREATE TABLE outbox (
  id              bigserial PRIMARY KEY,
  key             text NOT NULL UNIQUE,
  kind            text NOT NULL,
  user_id         uuid REFERENCES users(id) ON DELETE CASCADE,
  to_email        text NOT NULL,
  lang            text NOT NULL DEFAULT 'en',
  params          jsonb NOT NULL DEFAULT '{}',
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  sent_at         timestamptz,
  failed_at       timestamptz,
  error           text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_due ON outbox (next_attempt_at) WHERE sent_at IS NULL AND failed_at IS NULL;
