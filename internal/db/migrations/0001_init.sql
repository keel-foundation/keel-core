CREATE TABLE IF NOT EXISTS actors (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('person', 'channel', 'instance')),
    handle TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    public_key TEXT NOT NULL DEFAULT '',
    inbox_url TEXT NOT NULL DEFAULT '',
    outbox_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS channels (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL REFERENCES actors (id) ON DELETE CASCADE,
    handle TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS videos (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('draft', 'processing', 'published', 'removed', 'demonetized')),
    visibility TEXT NOT NULL CHECK (visibility IN ('public', 'unlisted', 'private')),
    content_hash TEXT NOT NULL,
    duration_seconds INTEGER NOT NULL CHECK (duration_seconds >= 0),
    playback JSONB NOT NULL,
    license TEXT NOT NULL,
    policy JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS videos_channel_id_idx ON videos (channel_id);
CREATE INDEX IF NOT EXISTS videos_status_idx ON videos (status);
