-- Account deletion (right to erasure). A deleted account keeps its row so
-- orders and the audit trail still resolve their user_id, but every piece of
-- personal data on it is scrubbed and deleted_at is set.
ALTER TABLE users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
