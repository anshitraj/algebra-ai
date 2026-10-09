-- Tenants: the root of Algebra's B2B data model — a business (a card app,
-- wallet, fintech, stablecoin app) integrating Algebra so its own end users
-- can grant AI agents controlled spending authority. See
-- internal/domain/tenant's package doc for how this differs from the
-- existing, narrower `integrators` table (0002).
CREATE TABLE IF NOT EXISTS tenants (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);

-- Nullable: NULL means "Algebra's own first-party reference console" (the
-- existing web/ dev-mode flow), not a tenant's end user. Adding this column
-- to the existing users table — rather than a parallel tenant_users table —
-- is what lets the reference app and a tenant integration share the exact
-- same users/agents/payment_sources machinery end to end.
ALTER TABLE users ADD COLUMN IF NOT EXISTS tenant_id TEXT REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_users_tenant_id ON users(tenant_id);
