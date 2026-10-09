-- Integrators: B2B API-key identities for third-party apps calling
-- Algebra's standalone policy-evaluation surface (POST
-- /api/v1/policy/evaluate-transaction). Deliberately independent of
-- users/agents — an integrator isn't an Algebra user and holds no
-- shopping permissions, just a bearer token scoped to one capability.
CREATE TABLE IF NOT EXISTS integrators (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);
