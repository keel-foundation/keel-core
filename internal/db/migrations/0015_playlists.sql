CREATE TABLE IF NOT EXISTS playlists (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT 'playlist' CHECK (kind IN ('playlist', 'collection')),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('public', 'unlisted', 'private')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS playlists_actor_updated_idx
    ON playlists (actor_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS playlist_items (
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    added_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (playlist_id, video_id)
);

CREATE INDEX IF NOT EXISTS playlist_items_position_idx
    ON playlist_items (playlist_id, position ASC, added_at ASC);
