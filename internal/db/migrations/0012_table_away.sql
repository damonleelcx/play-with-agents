-- 0012 · Away seats and paused tables.
--
-- A person whose clock runs out twice in a row is marked away: their turns
-- then resolve with the default move after a short grace instead of the full
-- clock, so everyone else is not kept waiting. A move or "I'm back" clears it.
ALTER TABLE table_seats ADD COLUMN timeouts int NOT NULL DEFAULT 0;
ALTER TABLE table_seats ADD COLUMN away_since timestamptz;

-- A playing table where every person is away (or nobody has done anything)
-- for a while is paused: status stays 'playing', but no jobs are scheduled
-- until a person moves or comes back.
ALTER TABLE tables ADD COLUMN paused_at timestamptz;
