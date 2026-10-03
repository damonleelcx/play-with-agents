-- 0013 · Deleting an account must not break other people's tables, and
-- table jobs that keep failing must not freeze a table; plus the bookkeeping
-- for move retries and Aoi's daily voice allowance.

-- ── Data that outlives its user ─────────────────────────────────────────────
-- A table whose host deletes their account keeps going: host rights pass to
-- another person at the table (rooms.ReleaseUser) or, failing that, to no one.
ALTER TABLE tables ALTER COLUMN host_id DROP NOT NULL;
ALTER TABLE tables DROP CONSTRAINT tables_host_id_fkey;
ALTER TABLE tables ADD CONSTRAINT tables_host_id_fkey
  FOREIGN KEY (host_id) REFERENCES users(id) ON DELETE SET NULL;

-- A published game stays on the shelf (and every table of it stays) when its
-- owner leaves; it is credited to "a former player". owner_gone tells that
-- apart from the studio's own ownerless examples ("Aoi's studio").
ALTER TABLE games DROP CONSTRAINT games_owner_id_fkey;
ALTER TABLE games ADD CONSTRAINT games_owner_id_fkey
  FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE games ADD COLUMN owner_gone boolean NOT NULL DEFAULT false;

-- table_moves.user_id and table_chat.user_id were already ON DELETE SET NULL
-- (0010); restated here so this file is the whole account-deletion story.
ALTER TABLE table_moves DROP CONSTRAINT table_moves_user_id_fkey;
ALTER TABLE table_moves ADD CONSTRAINT table_moves_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE table_chat DROP CONSTRAINT table_chat_user_id_fkey;
ALTER TABLE table_chat ADD CONSTRAINT table_chat_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

-- ── Faults ──────────────────────────────────────────────────────────────────
-- A move or clock job that fails for good first plays the default move; if
-- even that fails the table is paused with a visible event (fault_at set),
-- the host may resume it once, and a second fault abandons it.
ALTER TABLE tables ADD COLUMN faults int NOT NULL DEFAULT 0;
ALTER TABLE tables ADD COLUMN fault_at timestamptz;

-- ── Move retries ────────────────────────────────────────────────────────────
-- A retry must carry the same move as the original under its client_move_id;
-- the hash tells a retry from a different move reusing the id (409).
ALTER TABLE table_moves ADD COLUMN move_hash text;

-- ── Aoi's voice: characters synthesised per account per UTC day ─────────────
CREATE TABLE tts_usage (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day     date NOT NULL,
  chars   int  NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, day)
);
