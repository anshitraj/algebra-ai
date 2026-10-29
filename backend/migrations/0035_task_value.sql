-- What a task is worth, as its agent or its person stated it: the other side
-- of the task's P&L (internal/app/pnl.go). NULL when nobody said.
ALTER TABLE job_passes ADD COLUMN IF NOT EXISTS value_minor BIGINT CHECK (value_minor IS NULL OR value_minor >= 0);
