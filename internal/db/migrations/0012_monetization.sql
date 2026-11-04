CREATE TABLE IF NOT EXISTS creator_monetization (
    channel_id TEXT PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    eligible BOOLEAN NOT NULL DEFAULT FALSE,
    rpm NUMERIC(12, 2) NOT NULL DEFAULT 0,
    holdback NUMERIC(12, 2) NOT NULL DEFAULT 0,
    balance NUMERIC(12, 2) NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    next_payout_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS creator_earnings (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    occurred_at TIMESTAMPTZ NOT NULL,
    description TEXT NOT NULL,
    kind TEXT NOT NULL,
    amount NUMERIC(12, 2) NOT NULL,
    balance NUMERIC(12, 2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS creator_earnings_channel_date
    ON creator_earnings (channel_id, occurred_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS creator_payouts (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    period TEXT NOT NULL,
    amount NUMERIC(12, 2) NOT NULL,
    status TEXT NOT NULL,
    paid_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS creator_payouts_channel_date
    ON creator_payouts (channel_id, paid_at DESC, id DESC);
