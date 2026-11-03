-- Creator Studio and media engine state.
--
-- Videos remain the canonical published record. These tables add immutable
-- edit versions, resumable multipart upload intents, a durable media job
-- queue, scan results, media assets, subtitle tracks, and a content blocklist.

CREATE INDEX videos_channel_updated_idx ON videos(channel_id, updated_at DESC, id DESC);

CREATE TABLE video_versions (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    label TEXT NOT NULL DEFAULT '',
    trim JSONB NOT NULL DEFAULT '{}'::jsonb,
    cuts JSONB NOT NULL DEFAULT '[]'::jsonb,
    audio JSONB,
    volume DOUBLE PRECISION NOT NULL DEFAULT 1 CHECK (volume >= 0 AND volume <= 2),
    created_by TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX video_versions_video_idx ON video_versions(video_id, created_at DESC, id DESC);

CREATE TABLE video_subtitles (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    language TEXT NOT NULL,
    label TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('subtitles', 'captions')),
    object_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed')),
    auto BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX video_subtitles_video_idx ON video_subtitles(video_id, created_at DESC);

CREATE TABLE media_uploads (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    s3_upload_id TEXT NOT NULL,
    bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    part_size BIGINT NOT NULL CHECK (part_size > 0),
    content_sha256 TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'uploading', 'scanning', 'processing', 'ready', 'review', 'blocked', 'failed', 'aborted')),
    error TEXT NOT NULL DEFAULT '',
    scan_verdict TEXT NOT NULL DEFAULT '',
    scan_reason TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX media_uploads_video_idx ON media_uploads(video_id, created_at DESC);
CREATE INDEX media_uploads_channel_idx ON media_uploads(channel_id, created_at DESC);
CREATE INDEX media_uploads_status_idx ON media_uploads(status, updated_at);

CREATE TABLE media_jobs (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('scan', 'transcode')),
    upload_id TEXT NOT NULL REFERENCES media_uploads(id) ON DELETE CASCADE,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    run_after TIMESTAMPTZ NOT NULL,
    locked_at TIMESTAMPTZ,
    locked_by TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX media_jobs_queue_idx ON media_jobs(status, run_after, id);
CREATE INDEX media_jobs_upload_idx ON media_jobs(upload_id, created_at DESC);
CREATE UNIQUE INDEX media_jobs_active_idx
    ON media_jobs(kind, upload_id)
    WHERE status IN ('queued', 'running');

CREATE TABLE media_scans (
    id TEXT PRIMARY KEY,
    upload_id TEXT NOT NULL REFERENCES media_uploads(id) ON DELETE CASCADE,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    scanner TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('allow', 'review', 'block')),
    findings JSONB NOT NULL DEFAULT '[]'::jsonb,
    content_sha256 TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX media_scans_video_idx ON media_scans(video_id, created_at DESC);

CREATE TABLE media_blocklist (
    content_sha256 TEXT PRIMARY KEY CHECK (content_sha256 ~ '^[a-f0-9]{64}$'),
    kind TEXT NOT NULL CHECK (kind IN ('copyright', 'policy', 'malware', 'other')),
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE media_assets (
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('source', 'mp4', 'hls', 'thumbnail', 'caption')),
    object_key TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (video_id, kind)
);
