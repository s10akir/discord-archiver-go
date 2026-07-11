CREATE TABLE authors (
    guild_id text NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    author_id text NOT NULL,
    display_name text NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (guild_id, author_id)
);

INSERT INTO authors(guild_id, author_id, display_name, observed_at)
SELECT DISTINCT ON (guild_id, author_id)
    guild_id,
    author_id,
    author_name,
    timestamp
FROM messages
WHERE author_id <> ''
ORDER BY guild_id, author_id, timestamp DESC, id DESC;

CREATE INDEX authors_display_name_idx ON authors(lower(display_name), author_id);
