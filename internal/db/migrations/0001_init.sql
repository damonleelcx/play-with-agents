-- 0001 · Accounts, conversation and the durable mission engine.
-- Everything an agent knows lives here. A model's context window is rebuilt
-- from these tables on every cycle; nothing is remembered anywhere else.
-- Tables, games and moves are in 0010_play.sql.

-- ── Accounts ────────────────────────────────────────────────────────────────
CREATE TABLE users (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email             text NOT NULL,
  password_hash     text NOT NULL,
  name              text NOT NULL DEFAULT '',
  role              text NOT NULL DEFAULT 'player' CHECK (role IN ('player','admin')),
  email_verified_at timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  deleted_at        timestamptz
);
-- Case-insensitive uniqueness without the citext extension, which the shared
-- instance does not have.
CREATE UNIQUE INDEX users_email_uq ON users (lower(email)) WHERE deleted_at IS NULL;

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

-- ── Long-term memory ────────────────────────────────────────────────────────
-- preferences: the settings page (language, Aoi's tone, table options …).
-- Keys and allowed values are validated by the API; absent keys mean the
-- documented default.
CREATE TABLE user_preferences (
  user_id    uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  data       jsonb NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL DEFAULT now()
);
-- memories: durable facts Aoi learned ("learning hold'em", "loves co-op
-- games"). Visible and deletable in settings; not written when memory is off.
CREATE TABLE memories (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       text NOT NULL DEFAULT 'fact' CHECK (kind IN ('fact','preference')),
  content    text NOT NULL,
  source     text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memories_user ON memories (user_id, created_at DESC);

-- ── Conversation with Aoi ───────────────────────────────────────────────────
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
  -- intent, mood and cards (table / mission / game) for the client to render.
  meta            jsonb NOT NULL DEFAULT '{}',
  -- A retried POST carries the same client id and lands on the same row.
  client_msg_id   text,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX messages_client_uq ON messages (conversation_id, client_msg_id) WHERE client_msg_id IS NOT NULL;
CREATE INDEX messages_conv ON messages (conversation_id, id);

-- ── Durable missions ────────────────────────────────────────────────────────
-- A goal is a mission (building a game is goal skill 'build_game').
CREATE TABLE goals (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id     uuid REFERENCES conversations(id) ON DELETE SET NULL,
  title               text NOT NULL,
  objective           text NOT NULL,
  domain              text NOT NULL DEFAULT 'general' CHECK (domain IN ('studio','general')),
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
  -- what actually prevents a retry from doing the same thing twice.
  idempotency_key text,
  attempt         int NOT NULL DEFAULT 1,
  error           text NOT NULL DEFAULT '',
  latency_ms      int NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  finished_at     timestamptz
);
CREATE UNIQUE INDEX tool_calls_idem_uq ON tool_calls (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX tool_calls_task ON tool_calls (task_id);

-- G1 approvals: the goal's owner decides this exact call (e.g. publish a game).
-- G0 never gets here and G3 is refused outright, so G1 is the only gate.
CREATE TABLE approvals (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  goal_id    uuid NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
  task_id    uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tool       text NOT NULL,
  args       jsonb NOT NULL,
  call_id    text NOT NULL,
  preview    text NOT NULL DEFAULT '',
  gate       text NOT NULL DEFAULT 'G1' CHECK (gate = 'G1'),
  status     text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','superseded')),
  decided_by uuid REFERENCES users(id),
  decided_at timestamptz,
  note       text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approvals_goal ON approvals (goal_id);
CREATE INDEX approvals_user_pending ON approvals (user_id) WHERE status = 'pending';

-- The execution timeline. Append-only: what happened, why, when.
CREATE TABLE events (
  id         bigserial PRIMARY KEY,
  user_id    uuid REFERENCES users(id) ON DELETE CASCADE,
  goal_id    uuid REFERENCES goals(id) ON DELETE CASCADE,
  task_id    uuid REFERENCES tasks(id) ON DELETE SET NULL,
  type       text NOT NULL,
  data       jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_goal ON events (goal_id, id);
CREATE INDEX events_user ON events (user_id, id);

-- Compressed history: old events folded into one paragraph so a long mission
-- does not carry every event into every prompt.
CREATE TABLE episode_summaries (
  id            bigserial PRIMARY KEY,
  goal_id       uuid NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
  upto_event_id bigint NOT NULL,
  summary       text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX episode_summaries_goal ON episode_summaries (goal_id, upto_event_id DESC);

-- Every model call, for budgets (per goal, per account per day, per
-- deployment per day) and the usage page.
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
CREATE INDEX llm_calls_day ON llm_calls (created_at);
