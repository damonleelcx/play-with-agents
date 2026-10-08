-- Game-play design mode and comments on published games.

-- A design conversation: the player and Aoi explore a game before anything
-- is built. design is the living design document (updated after every
-- exchange); design_plan the build plan made from it once the player is
-- happy, and design_goal_id the build that plan started.
ALTER TABLE conversations
  ADD COLUMN mode              text NOT NULL DEFAULT 'chat' CHECK (mode IN ('chat','design')),
  ADD COLUMN design            jsonb NOT NULL DEFAULT '{}',
  ADD COLUMN design_updated_at timestamptz,
  ADD COLUMN design_plan       text NOT NULL DEFAULT '',
  ADD COLUMN design_plan_at    timestamptz,
  ADD COLUMN design_goal_id    uuid;

-- Comments under a game's page. Removing one keeps the row (deleted_at) so a
-- thread keeps its shape; the text is cleared.
CREATE TABLE game_comments (
  id         bigserial PRIMARY KEY,
  game_id    text NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body       text NOT NULL CHECK (char_length(body) <= 1000),
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
CREATE INDEX game_comments_game ON game_comments (game_id, id DESC);
CREATE INDEX game_comments_user ON game_comments (user_id, created_at DESC);
