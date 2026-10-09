-- A tenant's persisted, versioned policy — see internal/domain/policyset's
-- package doc. Rules is the exact JSON shape of policy.Rules (the same
-- deterministic Go engine, just now with storage/versioning around it
-- instead of only hardcoded/inline). Superseded versions are kept, never
-- deleted, so a decision made under an older version stays explainable.
CREATE TABLE IF NOT EXISTS policy_sets (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    user_id       TEXT REFERENCES users(id), -- NULL = tenant-wide default
    rules         JSONB NOT NULL,
    version       INTEGER NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_policy_sets_tenant_id ON policy_sets(tenant_id);
CREATE INDEX IF NOT EXISTS idx_policy_sets_tenant_user_active ON policy_sets(tenant_id, user_id) WHERE superseded_at IS NULL;
