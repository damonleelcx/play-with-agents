-- 0001 · Everything the agent knows lives here. The model's context window is
-- rebuilt from these tables on every cycle; nothing is remembered anywhere else.

-- ── Accounts ────────────────────────────────────────────────────────────────
CREATE TABLE users (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email             text NOT NULL,
  password_hash     text NOT NULL,
  name              text NOT NULL DEFAULT '',
  role              text NOT NULL DEFAULT 'client'
                    CHECK (role IN ('client','attorney','physician','admin')),
  email_verified_at timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  deleted_at        timestamptz
);
-- Case-insensitive uniqueness without the citext extension, which the shared
-- instance does not have.
CREATE UNIQUE INDEX users_email_uq ON users (lower(email)) WHERE deleted_at IS NULL;

-- A G2 gate needs a VERIFIED licence, not just a role: the role is what the
-- person asked to be, the licence is what somebody checked.
CREATE TABLE professional_licenses (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         text NOT NULL CHECK (kind IN ('bar','medical')),
  number       text NOT NULL,
  jurisdiction text NOT NULL,
  status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','verified','rejected')),
  verified_by  uuid REFERENCES users(id),
  verified_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX professional_licenses_user ON professional_licenses (user_id);

-- The cookie carries a random token; only its SHA-256 is stored, so a database
-- read does not hand out live sessions.
CREATE TABLE sessions (
  id           text PRIMARY KEY,
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  user_agent   text NOT NULL DEFAULT '',
  ip           text NOT NULL DEFAULT '',
  revoked_at   timestamptz
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE email_tokens (
  token_hash text PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose    text NOT NULL CHECK (purpose IN ('verify','reset')),
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_tokens_user ON email_tokens (user_id, purpose);

-- ── Long-term memory, four kinds kept apart ─────────────────────────────────
-- preferences: how the user wants to be treated (language, tone, limits)
CREATE TABLE user_preferences (
  user_id    uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  data       jsonb NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL DEFAULT now()
);
-- memories: durable facts the agent learned ("allergic to penicillin",
-- "lives in California"). Visible and deletable in settings.
CREATE TABLE memories (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       text NOT NULL DEFAULT 'fact' CHECK (kind IN ('fact','preference')),
  content    text NOT NULL,
  source     text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memories_user ON memories (user_id, created_at DESC);

-- ── Conversation ────────────────────────────────────────────────────────────
CREATE TABLE conversations (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title      text NOT NULL DEFAULT '',
  archived   boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX conversations_user ON conversations (user_id, updated_at DESC);

CREATE TABLE messages (
  id              bigserial PRIMARY KEY,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role            text NOT NULL CHECK (role IN ('user','assistant','event')),
  content         text NOT NULL,
  meta            jsonb NOT NULL DEFAULT '{}',
  -- A retried POST carries the same client id and lands on the same row.
  client_msg_id   text,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX messages_client_uq ON messages (conversation_id, client_msg_id) WHERE client_msg_id IS NOT NULL;
CREATE INDEX messages_conv ON messages (conversation_id, id);

-- ── Durable workflow ────────────────────────────────────────────────────────
CREATE TABLE goals (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id     uuid REFERENCES conversations(id) ON DELETE SET NULL,
  title               text NOT NULL,
  objective           text NOT NULL,
  domain              text NOT NULL CHECK (domain IN ('legal','medical','medlegal','general')),
  skill               text NOT NULL,
  status              text NOT NULL DEFAULT 'planning' CHECK (status IN
                        ('planning','active','paused','needs_attention','completed','failed','cancelled')),
  completion_criteria jsonb NOT NULL DEFAULT '[]',
  milestones          jsonb NOT NULL DEFAULT '[]',
  limits              jsonb NOT NULL DEFAULT '{}',
  usage               jsonb NOT NULL DEFAULT '{}',
  plan_version        int NOT NULL DEFAULT 0,
  replans             int NOT NULL DEFAULT 0,
  next_review_at      timestamptz,
  attention_reason    text NOT NULL DEFAULT '',
  summary             text NOT NULL DEFAULT '',
  language            text NOT NULL DEFAULT 'en',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  finished_at         timestamptz
);
CREATE INDEX goals_user ON goals (user_id, updated_at DESC);
CREATE INDEX goals_review ON goals (next_review_at) WHERE status = 'active';

CREATE TABLE tasks (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  goal_id          uuid NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
  key              text NOT NULL,
  title            text NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('plan','llm','wait','finish')),
  spec             jsonb NOT NULL DEFAULT '{}',
  status           text NOT NULL DEFAULT 'blocked' CHECK (status IN
                     ('blocked','ready','leased','waiting_approval','succeeded','failed','cancelled','skipped')),
  attempts         int NOT NULL DEFAULT 0,
  max_attempts     int NOT NULL DEFAULT 5,
  run_after        timestamptz NOT NULL DEFAULT now(),
  lease_owner      text,
  -- Fencing token. Bumped on every claim; a worker whose lease was taken over
  -- holds a stale epoch and every write it attempts is refused.
  lease_epoch      bigint NOT NULL DEFAULT 0,
  lease_expires_at timestamptz,
  output           jsonb,
  error            text NOT NULL DEFAULT '',
  depth            int NOT NULL DEFAULT 0,
  plan_version     int NOT NULL DEFAULT 0,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  started_at       timestamptz,
  finished_at      timestamptz,
  UNIQUE (goal_id, key)
);
CREATE INDEX tasks_ready ON tasks (run_after) WHERE status = 'ready';
CREATE INDEX tasks_leased ON tasks (lease_expires_at) WHERE status = 'leased';
CREATE INDEX tasks_goal ON tasks (goal_id);

CREATE TABLE task_deps (
  task_id    uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  depends_on uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, depends_on)
);
CREATE INDEX task_deps_on ON task_deps (depends_on);

-- One row per completed step inside a task. Resuming a task replays from the
-- latest row, so a crash loses at most the step that was in flight.
CREATE TABLE checkpoints (
  id         bigserial PRIMARY KEY,
  task_id    uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  step       int NOT NULL,
  state      jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (task_id, step)
);

CREATE TABLE tool_calls (
  id              bigserial PRIMARY KEY,
  goal_id         uuid REFERENCES goals(id) ON DELETE CASCADE,
  task_id         uuid REFERENCES tasks(id) ON DELETE CASCADE,
  tool            text NOT NULL,
  input           jsonb NOT NULL,
  output          jsonb,
  status          text NOT NULL CHECK (status IN ('started','succeeded','failed','verify_failed')),
  -- Only tools with external side effects carry a key. The unique index is
  -- what actually prevents a retry from sending the same letter twice.
  idempotency_key text,
  attempt         int NOT NULL DEFAULT 1,
  error           text NOT NULL DEFAULT '',
  latency_ms      int NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  finished_at     timestamptz
);
CREATE UNIQUE INDEX tool_calls_idem_uq ON tool_calls (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX tool_calls_task ON tool_calls (task_id);

CREATE TABLE approvals (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  goal_id         uuid NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
  task_id         uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tool            text NOT NULL,
  args            jsonb NOT NULL,
  call_id         text NOT NULL,
  preview         text NOT NULL DEFAULT '',
  gate            text NOT NULL CHECK (gate IN ('G1','G2')),
  required_role   text NOT NULL DEFAULT 'client',
  status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','superseded')),
  decided_by      uuid REFERENCES users(id),
  decided_at      timestamptz,
  note            text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approvals_goal ON approvals (goal_id);
CREATE INDEX approvals_pending ON approvals (status, required_role) WHERE status = 'pending';

-- The execution timeline. Append-only: what happened, why, when.
CREATE TABLE events (
  id              bigserial PRIMARY KEY,
  user_id         uuid REFERENCES users(id) ON DELETE CASCADE,
  goal_id         uuid REFERENCES goals(id) ON DELETE CASCADE,
  task_id         uuid REFERENCES tasks(id) ON DELETE SET NULL,
  type            text NOT NULL,
  data            jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_goal ON events (goal_id, id);
CREATE INDEX events_user ON events (user_id, id);

-- Compressed history: old events folded into one paragraph so a goal that has
-- run for a month does not carry a month of events into every prompt.
CREATE TABLE episode_summaries (
  id            bigserial PRIMARY KEY,
  goal_id       uuid NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
  upto_event_id bigint NOT NULL,
  summary       text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX episode_summaries_goal ON episode_summaries (goal_id, upto_event_id DESC);

CREATE TABLE documents (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  goal_id    uuid REFERENCES goals(id) ON DELETE SET NULL,
  task_id    uuid REFERENCES tasks(id) ON DELETE SET NULL,
  title      text NOT NULL,
  kind       text NOT NULL,
  content    text NOT NULL,
  sha256     text NOT NULL,
  version    int NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX documents_user ON documents (user_id, created_at DESC);

-- Retrieval over documents with Postgres full-text search. 'simple' config so
-- Chinese text is at least indexed by token rather than dropped by a stemmer.
CREATE TABLE knowledge_chunks (
  id          bigserial PRIMARY KEY,
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  document_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  ord         int NOT NULL,
  content     text NOT NULL,
  tsv         tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED
);
CREATE INDEX knowledge_chunks_tsv ON knowledge_chunks USING gin (tsv);
CREATE INDEX knowledge_chunks_user ON knowledge_chunks (user_id);

-- Wake-ups the client sees: deadlines, hearings, check-ins. Delivered by the
-- scheduler exactly once (sent_at is set in the same statement that claims it).
CREATE TABLE reminders (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  goal_id    uuid REFERENCES goals(id) ON DELETE CASCADE,
  due_at     timestamptz NOT NULL,
  text       text NOT NULL,
  key        text NOT NULL UNIQUE,
  sent_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reminders_due ON reminders (due_at) WHERE sent_at IS NULL;

CREATE TABLE llm_calls (
  id                bigserial PRIMARY KEY,
  user_id           uuid REFERENCES users(id) ON DELETE CASCADE,
  goal_id           uuid REFERENCES goals(id) ON DELETE CASCADE,
  task_id           uuid REFERENCES tasks(id) ON DELETE SET NULL,
  purpose           text NOT NULL,
  model             text NOT NULL,
  prompt_tokens     int NOT NULL DEFAULT 0,
  completion_tokens int NOT NULL DEFAULT 0,
  latency_ms        int NOT NULL DEFAULT 0,
  error             text NOT NULL DEFAULT '',
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_calls_user_day ON llm_calls (user_id, created_at);
