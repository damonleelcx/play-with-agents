-- 0011 · The game studio: build missions that produce script games.

-- A build goal names the game it builds. games.goal_id points the other way
-- (the latest build of a game); a revision re-points it, while the goal
-- keeps its own game for good.
ALTER TABLE goals ADD COLUMN game_id text;
CREATE INDEX goals_game ON goals (game_id) WHERE game_id IS NOT NULL;

-- Deterministic tool tasks (the Playtester): the engine invokes one tool
-- with fixed arguments, no model in the loop.
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check CHECK (kind IN ('plan','llm','wait','finish','tool'));

-- The Designer's structural decisions (seat range, hidden information), so
-- the Engineer's module can be checked against the rules it implements.
ALTER TABLE games ADD COLUMN spec jsonb NOT NULL DEFAULT '{}';

-- Community games seeded from the studio's own examples have no owner
-- ("From Aoi's studio"). Built-ins still never have one.
ALTER TABLE games DROP CONSTRAINT games_check;
ALTER TABLE games ADD CONSTRAINT games_owner_check CHECK (kind = 'script' OR owner_id IS NULL);
