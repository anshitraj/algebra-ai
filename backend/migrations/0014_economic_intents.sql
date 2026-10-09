-- Economic coordination (internal/domain/econ, docs/ECONOMIC_COORDINATION.md).
-- One outcome, many possible executors: the database, not application
-- memory, guarantees at most one live attempt and at most one commitment.

CREATE TABLE IF NOT EXISTS economic_intents (
    id                    TEXT PRIMARY KEY,
    principal_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pass_id               TEXT REFERENCES spend_passes(id),
    created_by_agent      TEXT,
    capability            TEXT NOT NULL,
    input_hash            TEXT NOT NULL,
    input                 JSONB NOT NULL DEFAULT '{}'::jsonb,
    quantity              INTEGER NOT NULL CHECK (quantity > 0),
    validity_window       TEXT NOT NULL,
    effect_key            TEXT NOT NULL,
    intent_hash           TEXT NOT NULL,
    currency              TEXT NOT NULL,
    budget_max_minor      BIGINT NOT NULL CHECK (budget_max_minor > 0),
    constraints           JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_policy       JSONB NOT NULL DEFAULT '{}'::jsonb,
    state                 TEXT NOT NULL,
    commitment            TEXT NOT NULL DEFAULT 'NONE',
    fulfillment           TEXT NOT NULL DEFAULT 'NONE',
    committed_minor       BIGINT NOT NULL DEFAULT 0,
    active_reservation_id TEXT,
    attempts              INTEGER NOT NULL DEFAULT 0,
    blocked_attempts      INTEGER NOT NULL DEFAULT 0,
    requires_approval     BOOLEAN NOT NULL DEFAULT false,
    approved_at           TIMESTAMPTZ,
    approved_by           TEXT,
    version               BIGINT NOT NULL DEFAULT 1,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at            TIMESTAMPTZ NOT NULL,
    -- One economic identity per principal: asking again returns the same
    -- intent, whichever agent asks.
    UNIQUE (principal_id, effect_key)
);
CREATE INDEX IF NOT EXISTS idx_economic_intents_principal ON economic_intents(principal_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_economic_intents_state ON economic_intents(state) WHERE state IN ('RESERVED', 'EXECUTING', 'UNKNOWN', 'RECONCILING', 'OPEN', 'AWAITING_APPROVAL');

CREATE TABLE IF NOT EXISTS economic_reservations (
    id                  TEXT PRIMARY KEY,
    intent_id           TEXT NOT NULL REFERENCES economic_intents(id) ON DELETE CASCADE,
    executor_agent_id   TEXT NOT NULL,
    executor_pass_id    TEXT,
    attempt             INTEGER NOT NULL,
    state               TEXT NOT NULL,
    hold_minor          BIGINT NOT NULL CHECK (hold_minor >= 0),
    currency            TEXT NOT NULL,
    provider_id         TEXT,
    rail                TEXT,
    quote_minor         BIGINT,
    semantics           TEXT,
    idempotency_key     TEXT NOT NULL,
    policy_version      TEXT,
    lease_expires_at    TIMESTAMPTZ NOT NULL,
    execution_deadline  TIMESTAMPTZ,
    evidence            JSONB NOT NULL DEFAULT '{}'::jsonb,
    outcome             TEXT,
    release_reason      TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at         TIMESTAMPTZ,
    UNIQUE (intent_id, attempt)
);
-- THE invariant: at most one live attempt per intent. A second executor
-- racing for the same intent fails here even if application checks raced.
CREATE UNIQUE INDEX IF NOT EXISTS uq_economic_reservations_one_live
    ON economic_reservations(intent_id) WHERE state IN ('RESERVED', 'EXECUTING', 'UNKNOWN', 'RECONCILING');
-- At most one commitment per intent.
CREATE UNIQUE INDEX IF NOT EXISTS uq_economic_reservations_one_commit
    ON economic_reservations(intent_id) WHERE state = 'COMMITTED';
CREATE INDEX IF NOT EXISTS idx_economic_reservations_pass ON economic_reservations(executor_pass_id, state);
CREATE INDEX IF NOT EXISTS idx_economic_reservations_sweep ON economic_reservations(state, lease_expires_at);

-- Append-only lifecycle telemetry: every decision and observation, with
-- the IDs needed to reconstruct what happened. Never tokens, keys, or
-- result payloads.
CREATE TABLE IF NOT EXISTS economic_events (
    id             TEXT PRIMARY KEY,
    -- seq orders events exactly, even ones written in the same microsecond.
    seq            BIGSERIAL,
    intent_id      TEXT NOT NULL REFERENCES economic_intents(id) ON DELETE CASCADE,
    reservation_id TEXT,
    agent_id       TEXT,
    event          TEXT NOT NULL,
    attempt        INTEGER,
    trace_id       TEXT,
    data           JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_economic_events_intent ON economic_events(intent_id, seq);
CREATE INDEX IF NOT EXISTS idx_economic_events_event ON economic_events(event, created_at);

-- Signed Intent Receipts (v2), one per final economic state.
CREATE TABLE IF NOT EXISTS economic_receipts (
    id         TEXT PRIMARY KEY,
    intent_id  TEXT NOT NULL UNIQUE REFERENCES economic_intents(id) ON DELETE CASCADE,
    jws        TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
