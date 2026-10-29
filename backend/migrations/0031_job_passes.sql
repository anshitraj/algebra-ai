-- Job Passes: a temporary budget for one task, set aside from a Spend Pass
-- and shared by the agents that work on it (internal/domain/jobpass,
-- internal/app/job_passes.go).
--
-- A job is an allocation in this ledger, never separate money. While a job is
-- ACTIVE and unexpired its whole budget counts against its parent (the pass,
-- or the job it was carved from); once it ends, only what its payments held
-- or committed does. A reservation made under a job names it in job_id.
--
--   ACTIVE   the job can authorize payments until expires_at
--   CLOSED   the task finished; what it didn't use went back to the parent
--   REVOKED  the person stopped it
--   EXPIRED  its time ran out
CREATE TABLE IF NOT EXISTS job_passes (
    id           TEXT PRIMARY KEY,
    user_id      TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pass_id      TEXT        NOT NULL REFERENCES spend_passes(id) ON DELETE CASCADE,
    parent_id    TEXT        REFERENCES job_passes(id) ON DELETE CASCADE,
    depth        INTEGER     NOT NULL CHECK (depth BETWEEN 1 AND 3),
    label        TEXT        NOT NULL,
    currency     TEXT        NOT NULL,
    budget_minor BIGINT      NOT NULL CHECK (budget_minor > 0),
    state        TEXT        NOT NULL CHECK (state IN ('ACTIVE', 'CLOSED', 'REVOKED', 'EXPIRED')),
    created_by   TEXT        NOT NULL DEFAULT '',
    token_hash   TEXT        UNIQUE,
    close_reason TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    closed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_job_passes_pass ON job_passes(pass_id, state, expires_at);
CREATE INDEX IF NOT EXISTS idx_job_passes_parent ON job_passes(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_job_passes_user ON job_passes(user_id, created_at DESC);

ALTER TABLE economic_reservations ADD COLUMN IF NOT EXISTS job_id TEXT;
CREATE INDEX IF NOT EXISTS idx_economic_reservations_job ON economic_reservations(job_id) WHERE job_id IS NOT NULL;
