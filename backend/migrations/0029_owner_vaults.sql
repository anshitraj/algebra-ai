-- The owner's vault wallet (internal/app/owner_vault.go, docs/WALLET.md).
--
-- One row per account: the two public addresses the owner's browser derived
-- from its recovery phrase, recorded only after both signatures over the
-- ownership message checked out. No key, no phrase and no keystore is ever
-- stored here. An address belongs to one account at a time.
CREATE TABLE IF NOT EXISTS owner_vaults (
    user_id        TEXT        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    solana_address TEXT        NOT NULL UNIQUE,
    evm_address    TEXT        NOT NULL UNIQUE,
    proof_message  TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL
);
