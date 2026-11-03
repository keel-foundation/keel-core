CREATE TABLE video_view_sessions (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    actor_id TEXT REFERENCES actors(id) ON DELETE SET NULL,
    viewer_key TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('feed', 'explore', 'search', 'channel', 'watch', 'shorts', 'external', 'unknown')),
    started_at TIMESTAMPTZ NOT NULL,
    last_event_at TIMESTAMPTZ NOT NULL,
    last_position_seconds DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (last_position_seconds >= 0),
    max_position_seconds DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (max_position_seconds >= 0),
    watched_seconds DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (watched_seconds >= 0),
    duration_seconds INTEGER NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    counted BOOLEAN NOT NULL DEFAULT FALSE,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX video_view_sessions_video_started_idx
    ON video_view_sessions(video_id, started_at DESC);
CREATE INDEX video_view_sessions_channel_started_idx
    ON video_view_sessions(channel_id, started_at DESC);
CREATE INDEX video_view_sessions_viewer_video_started_idx
    ON video_view_sessions(viewer_key, video_id, started_at DESC);
CREATE INDEX video_view_sessions_started_idx
    ON video_view_sessions(started_at);

CREATE TABLE video_view_events (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES video_view_sessions(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    position_seconds DOUBLE PRECISION NOT NULL CHECK (position_seconds >= 0),
    watched_delta_seconds DOUBLE PRECISION NOT NULL CHECK (watched_delta_seconds >= 0),
    completed BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (session_id, event_id)
);

CREATE INDEX video_view_events_video_created_idx
    ON video_view_events(video_id, created_at DESC);
CREATE INDEX video_view_events_created_idx
    ON video_view_events(created_at);

CREATE TABLE discovery_events (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL,
    viewer_key TEXT NOT NULL,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('impression', 'click')),
    source TEXT NOT NULL CHECK (source IN ('feed', 'explore', 'search', 'channel', 'watch', 'shorts', 'external', 'unknown')),
    position INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (viewer_key, event_id)
);

CREATE INDEX discovery_events_channel_occurred_idx
    ON discovery_events(channel_id, occurred_at DESC);
CREATE INDEX discovery_events_video_occurred_idx
    ON discovery_events(video_id, occurred_at DESC);
CREATE INDEX discovery_events_occurred_idx
    ON discovery_events(occurred_at);

CREATE TABLE video_daily_analytics (
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('feed', 'explore', 'search', 'channel', 'watch', 'shorts', 'external', 'unknown')),
    views BIGINT NOT NULL DEFAULT 0 CHECK (views >= 0),
    sessions BIGINT NOT NULL DEFAULT 0 CHECK (sessions >= 0),
    watch_seconds DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (watch_seconds >= 0),
    completions BIGINT NOT NULL DEFAULT 0 CHECK (completions >= 0),
    impressions BIGINT NOT NULL DEFAULT 0 CHECK (impressions >= 0),
    clicks BIGINT NOT NULL DEFAULT 0 CHECK (clicks >= 0),
    PRIMARY KEY (video_id, day, source)
);

CREATE INDEX video_daily_analytics_channel_day_idx
    ON video_daily_analytics(channel_id, day);
CREATE INDEX video_daily_analytics_video_day_idx
    ON video_daily_analytics(video_id, day);
