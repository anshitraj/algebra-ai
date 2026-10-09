-- Spend Passes (internal/domain/spendpass): a person's limited, revocable
-- permission for one AI agent — theirs, like Claude or ChatGPT, or a
-- company's — to spend on their behalf. Each pass owns exactly one agent
-- identity (and so one token); revoking the pass revokes the agent.
CREATE TABLE IF NOT EXISTS spend_passes (
    id                           TEXT PRIMARY KEY,
    user_id                      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id                     TEXT NOT NULL UNIQUE REFERENCES agents(id),
    label                        TEXT NOT NULL,
    agent_kind                   TEXT NOT NULL,
    currency                     TEXT NOT NULL DEFAULT 'INR',
    budget_minor_units           BIGINT NOT NULL CHECK (budget_minor_units > 0),
    budget_period                TEXT NOT NULL CHECK (budget_period IN ('total', 'week', 'month')),
    max_per_purchase_minor_units BIGINT CHECK (max_per_purchase_minor_units > 0),
    approve_above_minor_units    BIGINT CHECK (approve_above_minor_units >= 0),
    allowed_categories           JSONB NOT NULL DEFAULT '[]'::jsonb,
    allowed_merchants            JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at                   TIMESTAMPTZ NOT NULL,
    revoked_at                   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_spend_passes_user ON spend_passes(user_id, created_at DESC);

-- Signed spend receipts (internal/domain/receipt): proof, verifiable by
-- anyone holding Algebra's public key, that a person authorized exactly
-- this purchase. One per order.
CREATE TABLE IF NOT EXISTS spend_receipts (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    order_id   TEXT NOT NULL UNIQUE REFERENCES orders(id),
    pass_id    TEXT REFERENCES spend_passes(id),
    jws        TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_spend_receipts_user ON spend_receipts(user_id, created_at DESC);
