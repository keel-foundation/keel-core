-- Irreversible media purge audit. Videos and their child rows are removed, so
-- this table deliberately does not reference videos.
CREATE TABLE IF NOT EXISTS media_purges (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL,
    public_id TEXT NOT NULL DEFAULT '',
    channel_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    object_count INTEGER NOT NULL DEFAULT 0 CHECK (object_count >= 0),
    purged_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS media_purges_channel_idx
    ON media_purges (channel_id, purged_at DESC);
CREATE INDEX IF NOT EXISTS media_purges_video_idx ON media_purges (video_id);
