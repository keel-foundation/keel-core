CREATE INDEX IF NOT EXISTS videos_content_hash_idx
    ON videos(content_hash)
    WHERE content_hash <> '';

CREATE INDEX IF NOT EXISTS video_versions_channel_video_idx
    ON video_versions(channel_id, video_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS video_subtitles_channel_video_idx
    ON video_subtitles(channel_id, video_id, created_at ASC, id ASC);

CREATE INDEX IF NOT EXISTS media_scans_upload_idx
    ON media_scans(upload_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS media_uploads_expiry_idx
    ON media_uploads(status, expires_at)
    WHERE status IN ('pending', 'uploading');

CREATE INDEX IF NOT EXISTS media_jobs_running_idx
    ON media_jobs(locked_at)
    WHERE status = 'running';
