-- Wallets people signed in with, or were given when they signed in (Privy
-- makes an embedded Solana wallet for anyone who has none). Public addresses
-- only: Algebra never holds these keys.
--
-- A wallet belongs to one account. It goes with the account when the
-- account is erased (account_repo.go, EraseUser).

CREATE TABLE IF NOT EXISTS user_wallets (
    chain      TEXT NOT NULL,
    address    TEXT NOT NULL,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('embedded', 'external')),
    source     TEXT NOT NULL,
    linked_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (chain, address)
);
CREATE INDEX IF NOT EXISTS idx_user_wallets_user_id ON user_wallets(user_id);
