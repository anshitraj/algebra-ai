-- The payer key Algebra generated for a devnet deployment, kept sealed.
--
-- Algebra's payer is every on-chain Spend Pass's agent and the account every
-- pull pays into, and a pass's destination is fixed when it is made. A
-- different payer key therefore cannot pull from any existing vault (the
-- owner can still withdraw and close it). The key must outlive restarts and
-- deploys, and must never be replaced without anyone noticing, so it lives
-- here, sealed with AES-256-GCM under a key derived from ALGEBRA_MASTER_KEY.
--
-- Devnet only, by schema as well as by code: a mainnet payer is held in
-- Cloud KMS (or a file the operator controls), never generated and never
-- stored here.
CREATE TABLE IF NOT EXISTS platform_signers (
    cluster    TEXT PRIMARY KEY CHECK (cluster = 'devnet'),
    public_key TEXT        NOT NULL,
    sealed_key BYTEA       NOT NULL,
    nonce      BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
