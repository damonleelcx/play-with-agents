-- Live table chat: replies, whispers, reactions, who prompted an agent line,
-- and presence (who has the table open right now).

-- reply_to: the line this one answers (agents reply to the speaker of it).
-- whisper_to: a private line only the sender and this person may ever see;
-- never shown to agents, other people or spectators.
-- direct: an agent line written because someone addressed that agent (its
-- own, generous budget); false for unprompted talk.
ALTER TABLE table_chat
  ADD COLUMN reply_to   bigint,
  ADD COLUMN whisper_to uuid REFERENCES users(id) ON DELETE CASCADE,
  ADD COLUMN direct     boolean NOT NULL DEFAULT false;

CREATE TABLE table_chat_reactions (
  chat_id  bigint NOT NULL REFERENCES table_chat(id) ON DELETE CASCADE,
  user_id  uuid   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  emoji    text   NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 8),
  at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (chat_id, user_id, emoji)
);

-- One row per open stream (conn_id), refreshed by its keepalive; a person is
-- here while any of their rows is fresh, so a pod that dies simply lets its
-- rows expire.
CREATE TABLE table_presence (
  table_id uuid NOT NULL REFERENCES tables(id) ON DELETE CASCADE,
  user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conn_id  text NOT NULL,
  seen_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (table_id, user_id, conn_id)
);
CREATE INDEX table_presence_seen ON table_presence (table_id, seen_at);
