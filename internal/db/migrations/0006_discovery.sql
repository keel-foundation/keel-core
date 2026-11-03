CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS video_embeddings (
    video_id TEXT PRIMARY KEY REFERENCES videos (id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    dimensions INTEGER NOT NULL CHECK (dimensions > 0),
    embedding vector(384) NOT NULL,
    content_hash TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS video_embeddings_model_idx ON video_embeddings (model);
CREATE INDEX IF NOT EXISTS video_embeddings_embedding_idx
    ON video_embeddings USING hnsw (embedding vector_cosine_ops);

CREATE TABLE IF NOT EXISTS actor_embeddings (
    actor_id TEXT PRIMARY KEY REFERENCES actors (id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    dimensions INTEGER NOT NULL CHECK (dimensions > 0),
    embedding vector(384) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS actor_embeddings_model_idx ON actor_embeddings (model);
