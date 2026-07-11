CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS guilds (
    id text PRIMARY KEY,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS channels (
    guild_id text NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    id text NOT NULL,
    name text NOT NULL DEFAULT '',
    type integer NOT NULL DEFAULT 0,
    parent_id text NOT NULL DEFAULT '',
    position integer NOT NULL DEFAULT 0,
    is_thread boolean NOT NULL DEFAULT false,
    source text NOT NULL DEFAULT '',
    last_message_id text NOT NULL DEFAULT '',
    payload jsonb NOT NULL,
    PRIMARY KEY (guild_id, id)
);

CREATE TABLE IF NOT EXISTS messages (
    guild_id text NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    id text NOT NULL,
    channel_id text NOT NULL,
    archive_date date NOT NULL,
    timestamp timestamptz NOT NULL,
    author_id text NOT NULL DEFAULT '',
    author_name text NOT NULL DEFAULT '',
    content text NOT NULL DEFAULT '',
    has_attachments boolean NOT NULL DEFAULT false,
    has_embeds boolean NOT NULL DEFAULT false,
    embed_text text NOT NULL DEFAULT '',
    payload jsonb NOT NULL,
    PRIMARY KEY (guild_id, id)
);

CREATE TABLE IF NOT EXISTS attachments (
    guild_id text NOT NULL,
    message_id text NOT NULL,
    id text NOT NULL,
    filename text NOT NULL,
    content_type text NOT NULL DEFAULT '',
    size bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (guild_id, message_id, id),
    FOREIGN KEY (guild_id, message_id) REFERENCES messages(guild_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS messages_channel_time_idx ON messages(guild_id, channel_id, timestamp DESC, id DESC);
CREATE INDEX IF NOT EXISTS messages_time_idx ON messages(guild_id, timestamp DESC, id DESC);
CREATE INDEX IF NOT EXISTS messages_date_idx ON messages(guild_id, archive_date);
CREATE INDEX IF NOT EXISTS messages_content_trgm_idx ON messages USING gin(content gin_trgm_ops);
CREATE INDEX IF NOT EXISTS messages_author_trgm_idx ON messages USING gin(author_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS messages_embed_trgm_idx ON messages USING gin(embed_text gin_trgm_ops);
CREATE INDEX IF NOT EXISTS attachments_filename_trgm_idx ON attachments USING gin(filename gin_trgm_ops);
