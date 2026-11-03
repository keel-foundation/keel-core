CREATE TABLE IF NOT EXISTS notifications (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL REFERENCES actors (id) ON DELETE CASCADE,
    channel_id TEXT REFERENCES channels (id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    href TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    dedupe_key TEXT,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS notifications_actor_dedupe_idx
    ON notifications (actor_id, dedupe_key)
    WHERE dedupe_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS notifications_actor_created_idx
    ON notifications (actor_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS notifications_actor_unread_idx
    ON notifications (actor_id, created_at DESC)
    WHERE read_at IS NULL;
