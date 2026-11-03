CREATE TABLE IF NOT EXISTS subscriptions (
    actor_id TEXT NOT NULL REFERENCES actors (id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor_id, channel_id)
);

CREATE INDEX IF NOT EXISTS subscriptions_channel_id_idx ON subscriptions (channel_id);
CREATE INDEX IF NOT EXISTS subscriptions_actor_created_idx ON subscriptions (actor_id, created_at DESC);

CREATE TABLE IF NOT EXISTS video_reactions (
    actor_id TEXT NOT NULL REFERENCES actors (id) ON DELETE CASCADE,
    video_id TEXT NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('like', 'dislike')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor_id, video_id)
);

CREATE INDEX IF NOT EXISTS video_reactions_video_id_idx ON video_reactions (video_id);
CREATE INDEX IF NOT EXISTS video_reactions_video_kind_idx ON video_reactions (video_id, kind);
CREATE INDEX IF NOT EXISTS video_reactions_actor_kind_updated_idx
    ON video_reactions (actor_id, kind, updated_at DESC);

CREATE TABLE IF NOT EXISTS saved_videos (
    actor_id TEXT NOT NULL REFERENCES actors (id) ON DELETE CASCADE,
    video_id TEXT NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor_id, video_id)
);

CREATE INDEX IF NOT EXISTS saved_videos_actor_created_idx ON saved_videos (actor_id, created_at DESC);

CREATE TABLE IF NOT EXISTS watch_history (
    actor_id TEXT NOT NULL REFERENCES actors (id) ON DELETE CASCADE,
    video_id TEXT NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    watched_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor_id, video_id)
);

CREATE INDEX IF NOT EXISTS watch_history_actor_watched_idx ON watch_history (actor_id, watched_at DESC);

CREATE TABLE IF NOT EXISTS comments (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    parent_id TEXT REFERENCES comments (id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL REFERENCES actors (id) ON DELETE CASCADE,
    body TEXT NOT NULL CHECK (length(body) > 0),
    status TEXT NOT NULL DEFAULT 'visible' CHECK (status IN ('visible', 'hidden', 'removed')),
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    likes_count BIGINT NOT NULL DEFAULT 0 CHECK (likes_count >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS comments_video_created_idx ON comments (video_id, created_at DESC);
CREATE INDEX IF NOT EXISTS comments_parent_id_idx ON comments (parent_id);
CREATE INDEX IF NOT EXISTS comments_actor_id_idx ON comments (actor_id);

CREATE TABLE IF NOT EXISTS comment_likes (
    comment_id TEXT NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL REFERENCES actors (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (comment_id, actor_id)
);

CREATE INDEX IF NOT EXISTS comment_likes_actor_id_idx ON comment_likes (actor_id);
