-- The EVM payer on Base Sepolia and Arc testnet (payersigner.EVMTestnetCluster)
-- is generated and kept sealed here too, like the devnet Solana payer. Real
-- money is never generated: only test networks are allowed.
ALTER TABLE platform_signers DROP CONSTRAINT IF EXISTS platform_signers_cluster_check;
ALTER TABLE platform_signers ADD CONSTRAINT platform_signers_cluster_check
    CHECK (cluster IN ('devnet', 'evm-testnet'));
