-- Project Algebra — initial schema.
-- PostgreSQL is authoritative for all commerce/financial state (mandate §34);
-- Redis is cache/locks/idempotency-fast-path only, never the system of record.

CREATE TABLE IF NOT EXISTS users (
    id          TEXT PRIMARY KEY,
    email       TEXT UNIQUE NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agents (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id),
    client_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_agents_user_id ON agents(user_id);

CREATE TABLE IF NOT EXISTS agent_permissions (
    agent_id    TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    permission  TEXT NOT NULL,
    PRIMARY KEY (agent_id, permission)
);

CREATE TABLE IF NOT EXISTS purchase_intents (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users(id),
    agent_id          TEXT NOT NULL REFERENCES agents(id),
    status            TEXT NOT NULL,
    items             JSONB NOT NULL,
    constraints       JSONB NOT NULL,
    selected_quote_id TEXT,
    metadata          JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_purchase_intents_user_id ON purchase_intents(user_id);
CREATE INDEX IF NOT EXISTS idx_purchase_intents_status ON purchase_intents(status);

CREATE TABLE IF NOT EXISTS quotes (
    id           TEXT PRIMARY KEY,
    intent_id    TEXT NOT NULL REFERENCES purchase_intents(id),
    merchant     TEXT NOT NULL,
    payload      JSONB NOT NULL, -- full serialized quote.CheckoutQuote
    expires_at   TIMESTAMPTZ NOT NULL,
    retrieved_at TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_quotes_intent_id ON quotes(intent_id);

CREATE TABLE IF NOT EXISTS policy_decisions (
    id             TEXT PRIMARY KEY,
    intent_id      TEXT NOT NULL REFERENCES purchase_intents(id),
    decision       TEXT NOT NULL,
    reason_codes   JSONB NOT NULL,
    policy_version TEXT NOT NULL,
    raw            JSONB NOT NULL,
    evaluated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_policy_decisions_intent_id ON policy_decisions(intent_id);

CREATE TABLE IF NOT EXISTS approvals (
    id                    TEXT PRIMARY KEY,
    intent_id             TEXT NOT NULL REFERENCES purchase_intents(id),
    quote_id              TEXT NOT NULL,
    user_id               TEXT NOT NULL REFERENCES users(id),
    agent_id              TEXT NOT NULL REFERENCES agents(id),
    merchant              TEXT NOT NULL,
    amount_minor_units    BIGINT NOT NULL,
    currency              TEXT NOT NULL,
    payment_source_alias  TEXT NOT NULL,
    items_hash            TEXT NOT NULL,
    status                TEXT NOT NULL,
    authentication_method TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at            TIMESTAMPTZ,
    expires_at            TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_approvals_intent_id ON approvals(intent_id);

CREATE TABLE IF NOT EXISTS payment_sources (
    id                  TEXT PRIMARY KEY,
    user_id             TEXT NOT NULL REFERENCES users(id),
    alias               TEXT NOT NULL,
    type                TEXT NOT NULL,
    provider_token_ref  TEXT NOT NULL, -- opaque vault reference; never a PAN
    provider_mode       TEXT NOT NULL, -- sandbox | real
    network              TEXT,
    last4               TEXT,
    issuer_meta         JSONB,
    expiry_meta         TEXT,
    nickname            TEXT,
    billing_profile_id  TEXT,
    capabilities        JSONB NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ,
    UNIQUE (user_id, alias)
);

CREATE TABLE IF NOT EXISTS private_profiles (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id),
    alias       TEXT NOT NULL,
    type        TEXT NOT NULL, -- SHIPPING | BILLING
    ciphertext  BYTEA NOT NULL,
    nonce       BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, alias)
);

CREATE TABLE IF NOT EXISTS orders (
    id                TEXT PRIMARY KEY,
    intent_id         TEXT NOT NULL REFERENCES purchase_intents(id),
    approval_id       TEXT NOT NULL REFERENCES approvals(id),
    user_id           TEXT NOT NULL REFERENCES users(id),
    merchant          TEXT NOT NULL,
    merchant_order_id TEXT NOT NULL,
    items             JSONB NOT NULL,
    total_minor_units BIGINT NOT NULL,
    currency          TEXT NOT NULL,
    status            TEXT NOT NULL,
    provider_mode     TEXT NOT NULL, -- mock | sandbox | real
    placed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivery_eta      TIMESTAMPTZ,
    receipt_url       TEXT
);
CREATE INDEX IF NOT EXISTS idx_orders_intent_id ON orders(intent_id);
CREATE INDEX IF NOT EXISTS idx_orders_user_id_placed_at ON orders(user_id, placed_at);

CREATE TABLE IF NOT EXISTS order_events (
    id         TEXT PRIMARY KEY,
    order_id   TEXT NOT NULL REFERENCES orders(id),
    type       TEXT NOT NULL,
    payload    JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_order_events_order_id ON order_events(order_id);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key         TEXT NOT NULL,
    scope       TEXT NOT NULL,
    status      TEXT NOT NULL, -- IN_PROGRESS | COMPLETED
    response    BYTEA,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (key, scope)
);

CREATE TABLE IF NOT EXISTS webhook_events (
    id           TEXT PRIMARY KEY,
    provider     TEXT NOT NULL,
    event_id     TEXT NOT NULL,
    payload      JSONB NOT NULL,
    verified     BOOLEAN NOT NULL DEFAULT false,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    UNIQUE (provider, event_id)
);

-- Audit events are append-only. This is enforced structurally (Go interface
-- has no Update/Delete) AND at the database level here, so a bug or a
-- compromised process with DB access still cannot rewrite history.
CREATE TABLE IF NOT EXISTS audit_events (
    id                    TEXT PRIMARY KEY,
    trace_id              TEXT,
    "timestamp"           TIMESTAMPTZ NOT NULL,
    user_id               TEXT,
    agent_id              TEXT,
    intent_id             TEXT,
    action                TEXT NOT NULL,
    previous_state        TEXT,
    new_state             TEXT,
    policy_decision       TEXT,
    merchant              TEXT,
    payment_source_alias  TEXT,
    result                TEXT,
    metadata              JSONB,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_events_intent_id ON audit_events(intent_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_user_id ON audit_events(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_agent_id ON audit_events(agent_id);

CREATE OR REPLACE FUNCTION reject_audit_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only: % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_events_no_update ON audit_events;
CREATE TRIGGER audit_events_no_update
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION reject_audit_mutation();
