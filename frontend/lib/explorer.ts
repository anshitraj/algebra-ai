// Where a Solana transaction can be looked at, and what the console calls its
// network. Shared by the chat's step cards, the receipt verifier and the
// executions page.

const NETWORK_WORDS: Record<string, string> = { solana: "mainnet", "solana-devnet": "devnet" };

/** "mainnet" or "devnet" for a network ID; the ID itself when it's something else. */
export function networkWord(n: unknown): string {
  return typeof n === "string" ? (NETWORK_WORDS[n] ?? n) : "";
}

/** A transaction on Solana Explorer, on its own cluster. */
export function explorerTx(signature: string, network: string | undefined): string {
  return `https://explorer.solana.com/tx/${encodeURIComponent(signature)}${network === "solana-devnet" ? "?cluster=devnet" : ""}`;
}

/** An address on Solana Explorer, on its own cluster. */
export function explorerAddress(address: string, network: string | undefined): string {
  return `https://explorer.solana.com/address/${encodeURIComponent(address)}${network === "solana-devnet" ? "?cluster=devnet" : ""}`;
}
