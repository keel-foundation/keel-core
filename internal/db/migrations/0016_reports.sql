CREATE TABLE IF NOT EXISTS reports (
    id TEXT PRIMARY KEY,
    subject_type TEXT NOT NULL CHECK (subject_type IN ('video', 'channel')),
    subject_id TEXT NOT NULL,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    reason TEXT NOT NULL,
    details TEXT NOT NULL,
    reporter_id TEXT REFERENCES actors(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'received',
    moderation_case_id TEXT,
    moderation_signal_id TEXT,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS reports_reporter_created_idx
    ON reports (reporter_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS reports_channel_created_idx
    ON reports (channel_id, created_at DESC, id DESC);
