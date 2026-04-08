CREATE TABLE IF NOT EXISTS subscriptions (
    id BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL,
    repo TEXT NOT NULL,
    confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    last_seen_tag TEXT NOT NULL DEFAULT '',
    confirm_token TEXT NOT NULL UNIQUE,
    unsubscribe_token TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (email, repo)
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_confirmed ON subscriptions (confirmed);
CREATE INDEX IF NOT EXISTS idx_subscriptions_email ON subscriptions (email);
