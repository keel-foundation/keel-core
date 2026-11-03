ALTER TABLE videos ADD COLUMN IF NOT EXISTS public_id TEXT;

UPDATE videos SET public_id = substr(md5(id), 1, 11) WHERE public_id IS NULL;

ALTER TABLE videos ALTER COLUMN public_id SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'videos_public_id_format'
    ) THEN
        ALTER TABLE videos ADD CONSTRAINT videos_public_id_format
            CHECK (public_id ~ '^[A-Za-z0-9]{11}$');
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS videos_public_id_idx ON videos (public_id);
