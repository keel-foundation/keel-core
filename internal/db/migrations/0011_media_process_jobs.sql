-- The media pipeline is a single download-and-process job. Keep the legacy
-- scan/transcode kinds readable while new uploads enqueue "process".
ALTER TABLE media_jobs DROP CONSTRAINT IF EXISTS media_jobs_kind_check;
ALTER TABLE media_jobs
    ADD CONSTRAINT media_jobs_kind_check
    CHECK (kind IN ('scan', 'transcode', 'process'));

CREATE INDEX IF NOT EXISTS media_jobs_queued_idx
    ON media_jobs (run_after, id)
    WHERE status = 'queued';

CREATE INDEX IF NOT EXISTS media_jobs_retention_idx
    ON media_jobs (updated_at)
    WHERE status IN ('succeeded', 'failed');
