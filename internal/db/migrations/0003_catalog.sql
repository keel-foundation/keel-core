ALTER TABLE channels
    ADD COLUMN IF NOT EXISTS subscribers BIGINT NOT NULL DEFAULT 0 CHECK (subscribers >= 0),
    ADD COLUMN IF NOT EXISTS verified BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS format TEXT NOT NULL DEFAULT 'video'
        CHECK (format IN ('video', 'short')),
    ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'Documentaries'
        CHECK (category IN ('Documentaries', 'Protocol', 'Moderation', 'Studio', 'Federation')),
    ADD COLUMN IF NOT EXISTS tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS views BIGINT NOT NULL DEFAULT 0 CHECK (views >= 0),
    ADD COLUMN IF NOT EXISTS likes BIGINT NOT NULL DEFAULT 0 CHECK (likes >= 0),
    ADD COLUMN IF NOT EXISTS comments_count BIGINT NOT NULL DEFAULT 0 CHECK (comments_count >= 0);

CREATE INDEX IF NOT EXISTS channels_actor_id_idx ON channels (actor_id);
CREATE INDEX IF NOT EXISTS videos_public_idx ON videos (status, visibility, published_at DESC);
CREATE INDEX IF NOT EXISTS videos_channel_public_idx ON videos (channel_id, status, visibility, published_at DESC);
CREATE INDEX IF NOT EXISTS videos_category_idx ON videos (category);
CREATE INDEX IF NOT EXISTS videos_format_idx ON videos (format);
CREATE INDEX IF NOT EXISTS videos_views_idx ON videos (views DESC);
