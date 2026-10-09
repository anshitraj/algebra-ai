-- Provider health (internal/app/health_service.go): the latest free probe of
-- each routable candidate. A probe is the unpaid request the router prices
-- with, sent with the class's sample input: it shows whether the endpoint is
-- there, how fast it answers, and whether its 402 asks what its catalog says
-- it charges. No money moves, so the table holds no payment data at all.
--
-- One row per candidate (a provider endpoint on one network), overwritten by
-- each probe, with running counts so uptime survives restarts.

CREATE TABLE IF NOT EXISTS provider_health (
    candidate_id         TEXT PRIMARY KEY,
    provider             TEXT NOT NULL,
    capability           TEXT NOT NULL,
    endpoint             TEXT NOT NULL,
    network              TEXT,
    status               TEXT NOT NULL CHECK (status IN ('up', 'input_rejected', 'unpayable', 'down')),
    http_status          INTEGER,
    latency_ms           INTEGER NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
    -- Micro-USDC. Zero means unknown.
    listed_price_minor   BIGINT NOT NULL DEFAULT 0 CHECK (listed_price_minor >= 0),
    live_price_minor     BIGINT NOT NULL DEFAULT 0 CHECK (live_price_minor >= 0),
    overcharges          BOOLEAN NOT NULL DEFAULT false,
    checks               INTEGER NOT NULL DEFAULT 0 CHECK (checks >= 0),
    ups                  INTEGER NOT NULL DEFAULT 0 CHECK (ups >= 0 AND ups <= checks),
    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    error                TEXT,
    checked_at           TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_provider_health_capability ON provider_health(capability, checked_at DESC);
