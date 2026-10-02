-- 0010 · Play: the game catalog and the tables people sit at.
--
-- A table's game state is an opaque document owned by the game. It is stored
-- as `json` (not `jsonb`) on purpose: script games are JavaScript, where object
-- key order is observable, and jsonb would reorder keys between moves and
-- break replay determinism.

-- ── Catalog ─────────────────────────────────────────────────────────────────
-- Built-in games get a row too (upserted on first use) so that plays are
-- counted and tables can reference every game the same way.
CREATE TABLE games (
  id              text PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{1,39}$'),
  owner_id        uuid REFERENCES users(id) ON DELETE CASCADE,  -- NULL for built-ins
  kind            text NOT NULL CHECK (kind IN ('builtin','script')),
  name            text NOT NULL,
  summary         text NOT NULL DEFAULT '',
  rules_md        text NOT NULL DEFAULT '',
  status          text NOT NULL DEFAULT 'building' CHECK (status IN ('building','draft','published')),
  visibility      text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','unlisted','public')),
  current_version int  NOT NULL DEFAULT 0,
  plays           int  NOT NULL DEFAULT 0,
  -- The build mission that produced the game. No FK: missions live in the
  -- engine's tables, and a game outlives the goal that built it.
  goal_id         uuid,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'builtin') = (owner_id IS NULL))
);
CREATE INDEX games_owner ON games (owner_id, updated_at DESC);
CREATE INDEX games_public ON games (plays DESC) WHERE status = 'published' AND visibility = 'public';

CREATE TABLE game_versions (
  game_id    text NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  version    int  NOT NULL CHECK (version > 0),
  source     text NOT NULL,
  meta       jsonb NOT NULL DEFAULT '{}',   -- games.Meta
  report     jsonb NOT NULL DEFAULT '{}',   -- playtest + critic report
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (game_id, version)
);

-- ── Tables ──────────────────────────────────────────────────────────────────
CREATE TABLE tables (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code          text NOT NULL CHECK (code ~ '^[A-Z2-9]{6}$'),
  name          text NOT NULL,
  host_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  game_id       text NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  game_version  int  NOT NULL DEFAULT 0,
  status        text NOT NULL DEFAULT 'lobby' CHECK (status IN ('lobby','playing','finished','abandoned')),
  options       jsonb NOT NULL DEFAULT '{}',
  -- Snapshot of the host's table preferences at creation (turn_seconds,
  -- agent_speed, table_talk, agent_difficulty, language, ...), so workers never
  -- re-read preferences mid-game and a game keeps the pace it started with.
  settings      jsonb NOT NULL DEFAULT '{}',
  seed          bigint NOT NULL,
  state         json,                       -- NULL until started
  version       bigint NOT NULL DEFAULT 0,  -- optimistic-concurrency token
  move_seq      int NOT NULL DEFAULT 0,
  event_seq     int NOT NULL DEFAULT 0,
  to_move       int[] NOT NULL DEFAULT '{}',
  deadline      timestamptz,
  outcome       jsonb,
  rematch_of    uuid REFERENCES tables(id) ON DELETE SET NULL,
  rematch_id    uuid REFERENCES tables(id) ON DELETE SET NULL,
  last_human_at timestamptz NOT NULL DEFAULT now(),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  started_at    timestamptz,
  finished_at   timestamptz
);
-- Codes only need to be unique among tables someone can still join; finished
-- tables release theirs.
CREATE UNIQUE INDEX tables_code_live ON tables (code) WHERE status IN ('lobby','playing');
CREATE INDEX tables_host ON tables (host_id, updated_at DESC);
CREATE INDEX tables_idle ON tables (last_human_at) WHERE status IN ('lobby','playing');

CREATE TABLE table_seats (
  table_id  uuid NOT NULL REFERENCES tables(id) ON DELETE CASCADE,
  seat      int  NOT NULL CHECK (seat >= 0 AND seat < 16),
  kind      text NOT NULL CHECK (kind IN ('human','agent','open')),
  user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
  agent_id  text,
  name      text NOT NULL DEFAULT '',
  joined_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (table_id, seat),
  CHECK ((kind = 'agent') = (agent_id IS NOT NULL)),
  CHECK (kind = 'human' OR user_id IS NULL)
);
-- One seat per person per table.
CREATE UNIQUE INDEX table_seats_user ON table_seats (table_id, user_id) WHERE user_id IS NOT NULL;
CREATE INDEX table_seats_by_user ON table_seats (user_id) WHERE user_id IS NOT NULL;

-- Spectators are kept apart from seats: a seat is a position in the game,
-- a spectator is just a person allowed to watch (joined by code when the
-- table was full, or left their seat mid-game).
CREATE TABLE table_spectators (
  table_id  uuid NOT NULL REFERENCES tables(id) ON DELETE CASCADE,
  user_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  joined_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (table_id, user_id)
);
CREATE INDEX table_spectators_user ON table_spectators (user_id);

-- Every accepted move, so a table can be rebuilt by replay from its seed.
CREATE TABLE table_moves (
  table_id       uuid NOT NULL REFERENCES tables(id) ON DELETE CASCADE,
  seq            int  NOT NULL,
  seat           int  NOT NULL,
  actor          text NOT NULL CHECK (actor IN ('human','agent','timeout')),
  user_id        uuid REFERENCES users(id) ON DELETE SET NULL,
  client_move_id text,
  move           jsonb NOT NULL,
  version        bigint NOT NULL,           -- the version the move was applied to
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (table_id, seq)
);
CREATE UNIQUE INDEX table_moves_client ON table_moves (table_id, client_move_id) WHERE client_move_id IS NOT NULL;

CREATE TABLE table_events (
  table_id   uuid NOT NULL REFERENCES tables(id) ON DELETE CASCADE,
  seq        int  NOT NULL,
  type       text NOT NULL,
  seat       int  NOT NULL DEFAULT -1,
  text       text NOT NULL DEFAULT '',
  data       jsonb,
  only_seats int[] NOT NULL DEFAULT '{}',   -- empty = public
  version    bigint NOT NULL,
  at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (table_id, seq)
);

CREATE TABLE table_chat (
  id            bigserial PRIMARY KEY,
  table_id      uuid NOT NULL REFERENCES tables(id) ON DELETE CASCADE,
  seat          int  NOT NULL DEFAULT -1,
  user_id       uuid REFERENCES users(id) ON DELETE SET NULL,
  agent_id      text,
  name          text NOT NULL,
  text          text NOT NULL,
  client_msg_id text,
  at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX table_chat_table ON table_chat (table_id, id DESC);
CREATE UNIQUE INDEX table_chat_client ON table_chat (table_id, user_id, client_msg_id) WHERE client_msg_id IS NOT NULL;

-- Short, latency-sensitive table work. Same discipline as tasks: claimed with
-- FOR UPDATE SKIP LOCKED, leased, fenced by lease_epoch. The unique key makes
-- re-enqueueing a no-op; a job whose state_version no longer matches the
-- table completes without effect.
CREATE TABLE table_jobs (
  id               bigserial PRIMARY KEY,
  table_id         uuid NOT NULL REFERENCES tables(id) ON DELETE CASCADE,
  kind             text NOT NULL CHECK (kind IN ('agent_move','turn_timeout','agent_chat')),
  seat             int  NOT NULL,
  state_version    bigint NOT NULL,
  payload          jsonb NOT NULL DEFAULT '{}',
  status           text NOT NULL DEFAULT 'ready' CHECK (status IN ('ready','leased','done','failed')),
  run_after        timestamptz NOT NULL DEFAULT now(),
  attempts         int NOT NULL DEFAULT 0,
  max_attempts     int NOT NULL DEFAULT 3,
  lease_owner      text,
  lease_expires_at timestamptz,
  lease_epoch      bigint NOT NULL DEFAULT 0,
  result           text NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now(),
  finished_at      timestamptz,
  UNIQUE (table_id, kind, seat, state_version)
);
CREATE INDEX table_jobs_ready ON table_jobs (run_after, id) WHERE status = 'ready';
CREATE INDEX table_jobs_leased ON table_jobs (lease_expires_at) WHERE status = 'leased';
CREATE INDEX table_jobs_finished ON table_jobs (finished_at) WHERE status IN ('done','failed');
