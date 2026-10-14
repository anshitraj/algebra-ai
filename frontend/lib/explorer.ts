// Where a Solana transaction can be looked at, and what the console calls its
// network. Shared by the chat's step cards, the receipt verifier and the
// executions page.

const NETWORK_WORDS: Record<string, string> = { solana: "mainnet", "solana-devnet": "devnet" };

/** "mainnet" or "devnet" for a network ID; the ID itself when it's something else. */
export function networkWord(n: unknown): string {
  return typeof n === "string" ? (NETWORK_WORDS[n] ?? n) : "";
}

/**
 * Whether a network is a real Solana cluster, so a transaction id on it can be looked up.
 * The sandbox's "transactions" (sbxtx_…) are simulated: no explorer has them.
 */
export function isOnChain(network: unknown): boolean {
  return network === "solana" || network === "solana-devnet";
}

/** A transaction on Solana Explorer, on its own cluster. */
export function explorerTx(signature: string, network: string | undefined): string {
  return `https://explorer.solana.com/tx/${encodeURIComponent(signature)}${network === "solana-devnet" ? "?cluster=devnet" : ""}`;
}

/** A transaction on Solscan, on its own cluster. */
export function solscanTx(signature: string, network: string | undefined): string {
  return `https://solscan.io/tx/${encodeURIComponent(signature)}${network === "solana-devnet" ? "?cluster=devnet" : ""}`;
}

/** An address on Solscan, on its own cluster. */
export function solscanAddress(address: string, network: string | undefined): string {
  return `https://solscan.io/account/${encodeURIComponent(address)}${network === "solana-devnet" ? "?cluster=devnet" : ""}`;
}

/** An address on Solana Explorer, on its own cluster. */
export function explorerAddress(address: string, network: string | undefined): string {
  return `https://explorer.solana.com/address/${encodeURIComponent(address)}${network === "solana-devnet" ? "?cluster=devnet" : ""}`;
}
