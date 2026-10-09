-- Spend Passes enforced on Solana (solana-program/programs/spend-pass).
--
-- onchain_passes ties a Spend Pass to the on-chain pass that funds it: a
-- vault of the owner's USDC that only Algebra's payer can pull from, only
-- into the payer's own USDC account, within limits the program checks. The
-- owner's wallet signs everything else (create, deposit, freeze, withdraw,
-- close); Algebra never holds that key. Public addresses only.
CREATE TABLE IF NOT EXISTS onchain_passes (
    pass_id      TEXT PRIMARY KEY REFERENCES spend_passes(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    network      TEXT NOT NULL,
    program_id   TEXT NOT NULL,
    address      TEXT NOT NULL,
    owner_wallet TEXT NOT NULL,
    pass_number  NUMERIC(20, 0) NOT NULL,
    linked_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (network, address)
);
CREATE INDEX IF NOT EXISTS idx_onchain_passes_user ON onchain_passes(user_id);

-- onchain_pulls is one pull per paid attempt: the money moved from a pass's
-- vault into the payer just before the payment was signed, and what became of
-- it. A row is written before its transaction is sent, so a crash can never
-- leave a pull nobody knows about. Whatever the attempt didn't spend is
-- refunded to the vault (or, if the pass was closed, to its owner).
--
--   SENDING   signed and maybe sent; whether it landed isn't known yet
--   PULLED    on chain; waiting for the attempt to end
--   VOID      never landed, or the program refused it: nothing moved
--   SETTLED   the attempt spent all of it
--   REFUNDING a refund is signed and maybe sent
--   REFUNDED  what the attempt didn't spend is back
CREATE TABLE IF NOT EXISTS onchain_pulls (
    reservation_id     TEXT PRIMARY KEY,
    intent_id          TEXT NOT NULL,
    pass_id            TEXT NOT NULL,
    network            TEXT NOT NULL,
    pass_address       TEXT NOT NULL,
    owner_wallet       TEXT NOT NULL,
    amount_minor       BIGINT NOT NULL CHECK (amount_minor > 0),
    pull_signature     TEXT NOT NULL UNIQUE,
    pull_valid_until   BIGINT NOT NULL,
    state              TEXT NOT NULL CHECK (state IN ('SENDING', 'PULLED', 'VOID', 'SETTLED', 'REFUNDING', 'REFUNDED')),
    used_minor         BIGINT CHECK (used_minor >= 0),
    refund_minor       BIGINT CHECK (refund_minor >= 0),
    refund_signature   TEXT,
    refund_valid_until BIGINT,
    detail             TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_onchain_pulls_open ON onchain_pulls(updated_at) WHERE state IN ('SENDING', 'PULLED', 'REFUNDING');
CREATE INDEX IF NOT EXISTS idx_onchain_pulls_pass ON onchain_pulls(pass_id, created_at DESC);
