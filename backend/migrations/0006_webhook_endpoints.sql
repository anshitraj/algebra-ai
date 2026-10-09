-- A tenant's registered outbound-webhook destination(s) — the sending half
-- of what internal/app/webhook_service.go only ever received. secret is an
-- HMAC-SHA256 shared secret Algebra generates and shows the tenant once
-- (same posture as an agent/tenant/integrator bearer token: stored, never
-- re-displayed). event_types is empty means "all events".
CREATE TABLE IF NOT EXISTS tenant_webhook_endpoints (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    url          TEXT NOT NULL,
    secret       TEXT NOT NULL,
    event_types  TEXT[] NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_tenant_webhook_endpoints_tenant_id ON tenant_webhook_endpoints(tenant_id);
