// The agent allowance, built and signed in this browser: an SPL approve that
// lets Algebra's payer move up to an amount of the vault's USDC, or a revoke.
// The server names the payer and a recent block; this browser checks the rest
// against what it knows itself (the chain's USDC mint, the vault's own token
// account) and signs only what it built.

import type { OwnerKeys } from "./keys.ts";
import { fromMinor, vaultChain } from "./chains.ts";
import { allowanceTx, associatedTokenAddress, signAsSecond } from "./solana-tx.ts";

export type AllowanceFacts = {
  network: string;
  fee_payer: string;
  delegate: string;
  mint: string;
  decimals: number;
  token_account: string;
  blockhash: string;
  compute_unit_limit: number;
  compute_unit_price: number;
  max_minor: number;
};

/** The signed transaction (payer's slot empty) for an allowance of amount, or a revoke when amount is null. */
export function buildAllowance(keys: OwnerKeys, facts: AllowanceFacts, amount: bigint | null): string {
  const chain = vaultChain(facts.network);
  if (!chain || chain.kind !== "solana") throw new Error("Agent spending is set on Solana only for now");
  if (facts.mint !== chain.usdc || facts.decimals !== chain.usdcDecimals) throw new Error("The server named a token other than USDC, so nothing was signed");
  if (facts.delegate !== facts.fee_payer) throw new Error("The server's answer doesn't hang together, so nothing was signed");
  if (associatedTokenAddress(keys.solana.address, chain.usdc) !== facts.token_account) throw new Error("The server named a token account that isn't this vault's, so nothing was signed");
  if (amount !== null && amount > BigInt(facts.max_minor)) throw new Error(`At most ${fromMinor(BigInt(facts.max_minor), 6)} USDC`);
  const built = allowanceTx({
    feePayer: facts.fee_payer, owner: keys.solana.address, mint: chain.usdc, decimals: chain.usdcDecimals, amount,
    blockhash: facts.blockhash, unitLimit: facts.compute_unit_limit, unitPrice: BigInt(facts.compute_unit_price),
  });
  return signAsSecond(built.message, keys.solana.secret);
}
