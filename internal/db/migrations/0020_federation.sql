-- Federation v0.1: instance identity, peers, followers, outbound follows,
-- signed delivery queue, and inbox idempotency. Remote actors and videos are
-- stored in the existing catalog using deterministic remote_* identifiers.

CREATE TABLE IF NOT EXISTS federation_instance (
    id TEXT PRIMARY KEY CHECK (id = 'instance'),
    actor_id TEXT NOT NULL,
    domain TEXT NOT NULL,
    base_url TEXT NOT NULL,
    public_key_pem TEXT NOT NULL,
    private_key_pem TEXT NOT NULL,
    policy_version TEXT NOT NULL DEFAULT 'demo-0.1',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS federation_peers (
    id TEXT PRIMARY KEY,
    domain TEXT NOT NULL,
    actor_url TEXT NOT NULL UNIQUE,
    inbox_url TEXT NOT NULL,
    shared_inbox_url TEXT NOT NULL DEFAULT '',
    public_key_pem TEXT NOT NULL DEFAULT '',
    actor_type TEXT NOT NULL DEFAULT '',
    preferred_username TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    banner_url TEXT NOT NULL DEFAULT '',
    outbox_url TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS federation_peers_domain_idx ON federation_peers (domain);

CREATE TABLE IF NOT EXISTS federation_followers (
    actor_url TEXT PRIMARY KEY,
    local_actor_id TEXT NOT NULL DEFAULT 'instance',
    actor_id TEXT NOT NULL DEFAULT '',
    inbox_url TEXT NOT NULL,
    shared_inbox_url TEXT NOT NULL DEFAULT '',
    activity_id TEXT NOT NULL DEFAULT '',
    accepted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS federation_following (
    actor_url TEXT PRIMARY KEY,
    local_actor_id TEXT NOT NULL DEFAULT 'instance',
    actor_id TEXT NOT NULL DEFAULT '',
    inbox_url TEXT NOT NULL,
    shared_inbox_url TEXT NOT NULL DEFAULT '',
    follow_activity_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS federation_deliveries (
    id BIGSERIAL PRIMARY KEY,
    activity_id TEXT NOT NULL,
    activity JSONB NOT NULL,
    inbox_url TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    locked_at TIMESTAMPTZ,
    locked_by TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (activity_id, inbox_url)
);

CREATE INDEX IF NOT EXISTS federation_deliveries_queue_idx
    ON federation_deliveries (status, next_attempt_at, id);
CREATE INDEX IF NOT EXISTS federation_deliveries_retention_idx
    ON federation_deliveries (updated_at);

CREATE TABLE IF NOT EXISTS federation_inbox_activities (
    activity_id TEXT PRIMARY KEY,
    actor_url TEXT NOT NULL,
    type TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL
);
