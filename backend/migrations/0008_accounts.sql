-- Real accounts for Algebra's first-party app: email + password (argon2id)
-- and Google / GitHub OAuth, HttpOnly-cookie sessions, password reset, and
-- per-user spending guardrails. Replaces the dev-mode "assert your own
-- X-User-ID" placeholder for human-only endpoints.

ALTER TABLE users ADD COLUMN IF NOT EXISTS name              TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_url        TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS onboarded_at      TIMESTAMPTZ;

-- One row per (provider, provider account). A user may link several.
CREATE TABLE IF NOT EXISTS oauth_identities (
    provider         TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_user_id)
);
CREATE INDEX IF NOT EXISTS idx_oauth_identities_user_id ON oauth_identities(user_id);

-- A signed-in browser. token_hash is SHA-256 of the cookie value; the raw
-- token is never stored. agent_id is the per-session console agent, revoked
-- together with the session.
CREATE TABLE IF NOT EXISTS user_sessions (
    id                     TEXT PRIMARY KEY,
    user_id                TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash             TEXT NOT NULL UNIQUE,
    agent_id               TEXT REFERENCES agents(id),
    agent_token_ciphertext BYTEA,
    agent_token_nonce      BYTEA,
    user_agent             TEXT,
    ip                     TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at             TIMESTAMPTZ NOT NULL,
    revoked_at             TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON user_sessions(user_id);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    token_hash  TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_user_id ON password_reset_tokens(user_id);

-- A user's own guardrails (account.Guardrails), evaluated by the same
-- deterministic policy.LocalProvider as the platform default. No row means
-- the platform default (policy.DefaultRules) applies.
CREATE TABLE IF NOT EXISTS user_guardrails (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    guardrails JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Server-side activity lists (dashboard, approvals inbox, orders) replace
-- the console's old browser-local intent index.
CREATE INDEX IF NOT EXISTS idx_purchase_intents_user_created ON purchase_intents(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_approvals_user_status ON approvals(user_id, status);
