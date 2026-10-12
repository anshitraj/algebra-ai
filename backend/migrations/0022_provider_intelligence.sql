-- Provider Intelligence (internal/app/intel_service.go): a registry of every
-- paid endpoint the catalogs list, refreshed on a schedule, with what changed
-- between refreshes and the trust Algebra places in each endpoint.
--
-- A row is one endpoint on one network, keyed by a hash of provider, URL and
-- network. Its payee is pinned the first time Algebra sees one (in a listing
-- or a paid call); a later payee that differs is refused at payment time and
-- blocks the endpoint until a person looks at it.

CREATE TABLE IF NOT EXISTS provider_endpoints (
    id                 TEXT PRIMARY KEY,
    source             TEXT NOT NULL,
    provider_id        TEXT NOT NULL,
    provider_name      TEXT NOT NULL DEFAULT '',
    capability         TEXT NOT NULL DEFAULT '',
    method             TEXT NOT NULL DEFAULT '',
    url                TEXT NOT NULL,
    network            TEXT NOT NULL DEFAULT '',
    protocol           TEXT NOT NULL DEFAULT 'x402',
    asset              TEXT NOT NULL DEFAULT 'USDC',
    -- Micro-USDC. Zero means free or unknown.
    listed_price_minor BIGINT NOT NULL DEFAULT 0 CHECK (listed_price_minor >= 0),
    pay_to             TEXT NOT NULL DEFAULT '',
    pinned_pay_to      TEXT NOT NULL DEFAULT '',
    callable           BOOLEAN NOT NULL DEFAULT false,
    trust              TEXT NOT NULL DEFAULT 'unverified' CHECK (trust IN ('verified', 'unverified', 'blocked')),
    trust_reason       TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'removed', 'unreachable')),
    -- How long its last paid call took; zero when it was never paid.
    latency_ms         INTEGER NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
    fingerprint        TEXT NOT NULL DEFAULT '',
    quoted_at          TIMESTAMPTZ,
    tested_at          TIMESTAMPTZ,
    verified_at        TIMESTAMPTZ,
    first_seen_at      TIMESTAMPTZ NOT NULL,
    last_seen_at       TIMESTAMPTZ NOT NULL,
    last_changed_at    TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_provider_endpoints_provider ON provider_endpoints(provider_id);
CREATE INDEX IF NOT EXISTS idx_provider_endpoints_source ON provider_endpoints(source);
CREATE INDEX IF NOT EXISTS idx_provider_endpoints_first_seen ON provider_endpoints(first_seen_at DESC);

-- Everything that changed about an endpoint, and every trust decision, with
-- who made it. Suspicious changes (a new payee) are flagged.
CREATE TABLE IF NOT EXISTS provider_changes (
    id          TEXT PRIMARY KEY,
    endpoint_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL CHECK (kind IN ('added', 'removed', 'restored', 'price_changed', 'payee_changed',
                                              'network_changed', 'callable_changed', 'unreachable', 'trust_changed')),
    suspicious  BOOLEAN NOT NULL DEFAULT false,
    before      JSONB,
    after       JSONB,
    actor       TEXT NOT NULL DEFAULT 'system',
    run_id      TEXT,
    created_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_provider_changes_created ON provider_changes(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_provider_changes_endpoint ON provider_changes(endpoint_id, created_at DESC);

-- One row per refresh: when, how it went, and each catalog's answer.
CREATE TABLE IF NOT EXISTS catalog_sync_runs (
    id          TEXT PRIMARY KEY,
    trigger     TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('running', 'ok', 'partial', 'failed')),
    sources     JSONB NOT NULL DEFAULT '[]',
    seen        INTEGER NOT NULL DEFAULT 0,
    added       INTEGER NOT NULL DEFAULT 0,
    changed     INTEGER NOT NULL DEFAULT 0,
    removed     INTEGER NOT NULL DEFAULT 0,
    error       TEXT,
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_catalog_sync_runs_started ON catalog_sync_runs(started_at DESC);

-- Scheduled jobs shared by every API instance: whoever takes the lease runs
-- the job; the next run time survives restarts.
CREATE TABLE IF NOT EXISTS scheduled_jobs (
    name             TEXT PRIMARY KEY,
    schedule         TEXT NOT NULL,
    next_run_at      TIMESTAMPTZ NOT NULL,
    lease_owner      TEXT,
    lease_until      TIMESTAMPTZ,
    last_started_at  TIMESTAMPTZ,
    last_finished_at TIMESTAMPTZ,
    last_success_at  TIMESTAMPTZ,
    last_error       TEXT,
    checkpoint       JSONB NOT NULL DEFAULT '{}'
);
