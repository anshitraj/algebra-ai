// A USDC withdrawal, built and signed in this browser from the vault's keys
// and the chain facts the server gave (a blockhash; a nonce and fees). The
// server's answer never decides the recipient or the amount, and fees it
// suggests above a sane ceiling are refused.

import type { OwnerKeys } from "./keys.ts";
import type { VaultChain } from "./chains.ts";
import { signEip1559, usdcTransferTx } from "./evm-tx.ts";
import { signSolana, tokenTransfer } from "./solana-tx.ts";

export type WithdrawFacts = {
  blockhash?: string;
  recipient_has_token_account?: boolean;
  evm?: { chain_id: number | string; nonce: number | string; max_priority_fee_per_gas: number | string; max_fee_per_gas: number | string; gas_limit: number | string };
};

/** The most a withdrawal's network fee may cost, in the chain's native minor units (0.005 ETH, or 0.005 USDC-as-gas on Arc). */
export const MAX_EVM_FEE = 5_000_000_000_000_000n;
const MAX_GAS = 300_000n;

export class FeeTooHighError extends Error {
  constructor() {
    super("The network fee the server suggested is far above normal, so nothing was signed. Try again in a minute.");
  }
}

export function buildWithdrawal(keys: OwnerKeys, chain: VaultChain, to: string, amount: bigint, facts: WithdrawFacts): { raw: string; id: string } {
  if (amount <= 0n) throw new Error("Send more than zero");
  if (chain.kind === "solana") {
    if (!facts.blockhash) throw new Error("The network didn't give a recent block to sign against; try again");
    const built = tokenTransfer({ owner: keys.solana.address, to, mint: chain.usdc, decimals: chain.usdcDecimals, amount, blockhash: facts.blockhash, createRecipientAccount: facts.recipient_has_token_account !== true });
    const { raw, signature } = signSolana(built.message, keys.solana.secret);
    return { raw, id: signature };
  }
  const p = facts.evm;
  if (!p || !chain.chainId) throw new Error("The network didn't give a nonce and fees; try again");
  if (BigInt(p.chain_id) !== BigInt(chain.chainId)) throw new Error("The server answered for a different chain, so nothing was signed");
  const gas = BigInt(p.gas_limit);
  const maxFee = BigInt(p.max_fee_per_gas);
  const tip = BigInt(p.max_priority_fee_per_gas);
  if (gas > MAX_GAS || gas * maxFee > MAX_EVM_FEE || tip > maxFee) throw new FeeTooHighError();
  const tx = usdcTransferTx({ chainId: chain.chainId, usdc: chain.usdc, to, amount, nonce: BigInt(p.nonce), maxPriorityFeePerGas: tip, maxFeePerGas: maxFee, gasLimit: gas });
  const { raw, hash } = signEip1559(tx, keys.evm.secret);
  return { raw, id: hash };
}
