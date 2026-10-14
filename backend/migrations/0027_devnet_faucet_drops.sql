-- The devnet airdrop: one per account, ever.
--
-- The in-app faucet (internal/app/devnet_faucet.go) gives a signed-in person
-- test SOL and test USDC once. A row is the claim, made before the transfer
-- is sent so that two clicks, two tabs or two servers cannot both send, and
-- then the record of what was sent. A wallet can be claimed once as well, so
-- a second account that links the same wallet gets nothing.
--
-- Erasing an account (account_repo.go, EraseUser) keeps its row: the airdrop
-- was given, and erasing must not make it available again. The row holds an
-- opaque account id, a public wallet address and a public transaction
-- signature, nothing else.
CREATE TABLE IF NOT EXISTS devnet_faucet_drops (
    user_id      TEXT        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    address      TEXT        NOT NULL UNIQUE,
    -- Empty until the transfer has been sent: a claim nothing was sent for yet
    -- (or ever, if the server stopped in between: such a claim is given up
    -- after ten minutes, see faucet_repo.go).
    signature    TEXT        NOT NULL DEFAULT '',
    sol_lamports BIGINT      NOT NULL DEFAULT 0 CHECK (sol_lamports >= 0),
    usdc_minor   BIGINT      NOT NULL DEFAULT 0 CHECK (usdc_minor >= 0),
    created_at   TIMESTAMPTZ NOT NULL,
    sent_at      TIMESTAMPTZ
);
