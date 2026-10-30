-- Pulls from an owner's vault on an EVM chain (Base, Arc) through the USDC
-- permit it gave Algebra's payer (internal/app/evm_allowance.go): settled and
-- refunded by their own service, so they are their own source.
ALTER TABLE onchain_pulls DROP CONSTRAINT IF EXISTS onchain_pulls_source_check;
ALTER TABLE onchain_pulls ADD CONSTRAINT onchain_pulls_source_check
    CHECK (source IN ('spend_pass', 'owner_vault', 'owner_vault_evm'));
