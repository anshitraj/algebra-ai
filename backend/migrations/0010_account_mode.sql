-- Demo vs live accounts (account.Mode). A demo account is created with one
-- click from the sign-in page: it shops real listings but checks out through
-- the simulated demo store, with fake money. Every existing account is live.
ALTER TABLE users ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'live';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_mode_check') THEN
        ALTER TABLE users ADD CONSTRAINT users_mode_check CHECK (mode IN ('live', 'demo'));
    END IF;
END $$;
