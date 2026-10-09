-- Routed execution telemetry (internal/domain/routing).
-- One row per attempt the executor ran: what was chosen and why, what it cost,
-- how long it took, what the rail proved, and how good the delivered result
-- was. It is what the router learns provider reliability and quality from, so
-- a record that says "UNKNOWN" is brought up to date when reconciliation
-- resolves the attempt.
--
-- Hashes, identifiers and statuses only. Never a response body, a payment
-- value or a key.

CREATE TABLE IF NOT EXISTS route_executions (
    id                TEXT PRIMARY KEY,
    principal_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    intent_id         TEXT NOT NULL REFERENCES economic_intents(id) ON DELETE CASCADE,
    reservation_id    TEXT REFERENCES economic_reservations(id) ON DELETE CASCADE,
    attempt           INTEGER,
    plan_id           TEXT,
    plan_rank         INTEGER,
    -- How the attempt was chosen: the routing mode, the plan it came from and
    -- the exact priced offer that was taken.
    mode              TEXT,
    plan_hash         TEXT,
    quote_hash        TEXT,
    candidate_id      TEXT NOT NULL,
    quote_id          TEXT,
    provider          TEXT NOT NULL,
    capability        TEXT NOT NULL,
    execution_type    TEXT NOT NULL,
    -- Minor units of the settlement asset (micro-USDC).
    quoted_cost_minor BIGINT NOT NULL DEFAULT 0 CHECK (quoted_cost_minor >= 0),
    actual_cost_minor BIGINT NOT NULL DEFAULT 0 CHECK (actual_cost_minor >= 0),
    started_at        TIMESTAMPTZ NOT NULL,
    completed_at      TIMESTAMPTZ,
    latency_ms        BIGINT NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
    http_status       INTEGER,
    payment_status    TEXT NOT NULL CHECK (payment_status IN ('NOT_ATTEMPTED', 'AUTHORIZED', 'SETTLED', 'NOT_SETTLED', 'UNKNOWN')),
    delivery_status   TEXT NOT NULL CHECK (delivery_status IN ('NONE', 'FULFILLED', 'NOT_FULFILLED', 'UNKNOWN')),
    transaction_ref   TEXT,
    network           TEXT,
    asset             TEXT,
    request_hash      TEXT,
    response_hash     TEXT,
    failure_class     TEXT,
    failure_message   TEXT,
    -- Sandbox or test-network evidence: never counted as real spend.
    test              BOOLEAN NOT NULL DEFAULT false,
    -- The evaluator's verdict (routing.QualityResult) and its headline number,
    -- kept apart so provider averages don't have to parse JSON.
    quality           JSONB,
    final_quality     DOUBLE PRECISION CHECK (final_quality IS NULL OR (final_quality >= 0 AND final_quality <= 100)),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One record per attempt.
CREATE UNIQUE INDEX IF NOT EXISTS uq_route_executions_reservation
    ON route_executions(reservation_id) WHERE reservation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_route_executions_intent ON route_executions(intent_id, started_at);
-- The router's history query: one provider's record for one capability.
CREATE INDEX IF NOT EXISTS idx_route_executions_provider ON route_executions(capability, provider, started_at DESC);
