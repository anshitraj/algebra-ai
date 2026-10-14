// What the chat tells the model about the wallet that pays, so it neither
// promises nor refuses what the server can't do. Pure (types only), so it is
// testable without Next.

import type { RailStatus } from "../types";

const USDC = 1_000_000;
const usdc = (minor: number) => `${(minor / USDC).toLocaleString("en-US", { maximumFractionDigits: 6 })} USDC`;

/** The on-chain Spend Pass program on one network, as GET /api/v1/onchain/networks reports it. */
export type VaultNetwork = { required: boolean; signer_ready?: boolean };

/**
 * Lines about the paying wallet for this chat's network. Where every payment
 * must come from a Spend Pass vault, the payer's own USDC balance is beside the
 * point (it is zero by design): what matters is whether it can pay fees, and
 * that the pass's vault holds the money.
 */
export function railFacts(network: string, rail: RailStatus | undefined, vault: VaultNetwork | undefined): string[] {
  // The sandbox has no wallet, so there is nothing to say about one.
  if (network === "sandbox") return [];
  if (!rail?.configured) {
    return [
      `This server has no wallet for ${network === "solana" ? "mainnet" : "devnet"}, so calls can be searched and priced but not paid: payments will fail. Say so if the user asks to pay, and suggest switching networks at the top if the other one has a wallet.`,
    ];
  }
  if (rail.error) return ["The wallet is set but the Solana node can't be reached right now, so payments may fail."];
  if (vault?.required) {
    if (vault.signer_ready === false) {
      return [
        "Every payment on this network is pulled from the person's own Spend Pass vault, but Algebra's signing wallet has no SOL for network fees yet, so on-chain payments fail until it is topped up. Say so plainly if the user asks to pay; searching and pricing still work.",
      ];
    }
    return [
      "Every payment on this network is pulled from the person's own Spend Pass vault on Solana and then paid to the provider. Algebra's signing wallet holds no USDC of its own and needs none: a zero balance there is normal. What must hold USDC is the pass's vault; if a card says the pass has no vault or the vault holds too little, say that and point the user to Spend passes to create it or add USDC.",
    ];
  }
  if (typeof rail.funded === "boolean") {
    return [
      rail.funded
        ? `The paying wallet is funded${rail.max_payment_minor ? `; it pays at most ${usdc(rail.max_payment_minor)} per call` : ""}.`
        : "The paying wallet holds no USDC on this network right now.",
    ];
  }
  return [];
}
