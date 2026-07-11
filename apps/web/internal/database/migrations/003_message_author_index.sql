CREATE INDEX messages_author_time_idx ON messages(guild_id, author_id, timestamp DESC, id DESC)
WHERE author_id <> '';
