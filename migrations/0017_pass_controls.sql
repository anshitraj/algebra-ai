-- Spend Pass controls for autonomous agents (internal/domain/spendpass/controls.go)
-- and the kill switch.
--
-- controls holds how fast an agent may spend (calls per minute, overall and per
-- provider) and how it treats providers its person has never paid (allow, cap
-- each call, or ask). An empty object means the defaults.
--
-- frozen_at is set while the kill switch is on for the pass: nothing is
-- authorized, including a payment already in flight. Unlike revoked_at it can
-- be cleared.

ALTER TABLE spend_passes ADD COLUMN IF NOT EXISTS controls  JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE spend_passes ADD COLUMN IF NOT EXISTS frozen_at TIMESTAMPTZ;

-- The velocity check counts a pass's attempts in the last minute, overall and
-- per provider, under the pass's row lock.
CREATE INDEX IF NOT EXISTS idx_economic_reservations_pass_created
    ON economic_reservations(executor_pass_id, created_at DESC);
-- The new-provider gate asks whether a person has ever paid a provider.
CREATE INDEX IF NOT EXISTS idx_economic_reservations_provider_committed
    ON economic_reservations(provider_id) WHERE state = 'COMMITTED';
