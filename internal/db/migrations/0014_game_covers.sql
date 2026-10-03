-- Game covers (internal/art): one 16:9 image per game, small enough
-- (< 250 KB) to keep in Postgres, the only store. Built-ins and the seeded
-- examples ship static covers in the web app; a row here overrides them.
CREATE TABLE game_covers (
  game_id      text PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
  version      int  NOT NULL DEFAULT 1 CHECK (version > 0),   -- bumps on every replace; the URL's ?v=
  content_type text NOT NULL CHECK (content_type IN ('image/jpeg','image/webp','image/png')),
  bytes        bytea NOT NULL CHECK (octet_length(bytes) BETWEEN 1 AND 1048576),
  prompt       text NOT NULL DEFAULT '',
  source       text NOT NULL DEFAULT 'generated' CHECK (source IN ('generated','procedural')),
  name         text NOT NULL DEFAULT '',   -- the game's name it was made for: a rename regenerates it
  model        text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now()
);
