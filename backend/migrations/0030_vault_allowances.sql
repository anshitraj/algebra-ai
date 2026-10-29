-- Agents spend from the owner's vault through a capped allowance
-- (internal/app/vault_allowance.go, docs/WALLET.md).
--
-- The owner's browser signs an SPL Token ApproveChecked that names Algebra's
-- payer as the delegate of the vault's USDC account, for an amount the owner
-- chose. The token program refuses any transfer past that amount, whatever
-- Algebra's server does. vault_allowances records the owner's choice: that
-- payments on a network come from the vault, and how much was approved.
-- What is left is always read from the chain, never from here.
--
--   ACTIVE   the owner approved an amount; payments on the network come from the vault
--   REVOKED  the owner took the approval back; payments there don't come from the vault
CREATE TABLE IF NOT EXISTS vault_allowances (
    user_id        TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    network        TEXT        NOT NULL,
    state          TEXT        NOT NULL CHECK (state IN ('ACTIVE', 'REVOKED')),
    approved_minor BIGINT      NOT NULL CHECK (approved_minor >= 0),
    delegate       TEXT        NOT NULL,
    token_account  TEXT        NOT NULL,
    signature      TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, network)
);

-- A pull now says where its money came from: an on-chain Spend Pass's vault
-- (the program pulls), or the owner's vault through its allowance (the payer
-- pulls as the delegate). Each kind is settled and refunded by its own service.
ALTER TABLE onchain_pulls ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'spend_pass'
    CHECK (source IN ('spend_pass', 'owner_vault'));
