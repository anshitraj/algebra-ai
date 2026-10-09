-- The provider's answer for a paid call, kept for a short while.
--
-- Algebra never charges twice for the same outcome: asking again for something
-- already paid for used to return "already committed" and nothing to show for
-- it. With the answer kept, the repeat request returns it (flagged as a replay)
-- and nothing is paid. It is also how an agent re-reads a result later.
--
-- The body is untrusted third-party data and may be sensitive, so it is sealed
-- with AES-256-GCM under a key derived from the master key, bound to the intent,
-- the attempt and the person it belongs to (the additional data), so a row
-- copied to another intent or person doesn't open. It is kept only until
-- expires_at (RESULT_RETENTION, 24 hours by default), is never kept beyond
-- RESULT_MAX_BYTES, can be declined per request, and goes with the account.
--
-- One row per intent: the committed attempt's.

CREATE TABLE IF NOT EXISTS intent_results (
    intent_id      TEXT PRIMARY KEY REFERENCES economic_intents(id) ON DELETE CASCADE,
    principal_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reservation_id TEXT NOT NULL REFERENCES economic_reservations(id) ON DELETE CASCADE,
    content_type   TEXT NOT NULL DEFAULT '',
    http_status    INTEGER NOT NULL DEFAULT 0,
    size_bytes     INTEGER NOT NULL CHECK (size_bytes >= 0),
    -- "sha256:<hex>" of the plaintext: the same hash the receipt and the
    -- reservation's evidence carry, so a kept result can be checked against them.
    body_sha256    TEXT NOT NULL,
    sealed         BYTEA NOT NULL,
    nonce          BYTEA NOT NULL,
    stored_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL
);

-- Purging what has expired.
CREATE INDEX IF NOT EXISTS idx_intent_results_expiry ON intent_results(expires_at);
