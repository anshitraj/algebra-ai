// The chains the vault holds USDC on, per place to work. One EVM address
// receives on Base and Arc; Solana has its own.

export type ChainKind = "solana" | "evm";

export type VaultChain = {
  /** Algebra's network name (internal/domain/chain). */
  id: string;
  name: "Solana" | "Base" | "Arc";
  kind: ChainKind;
  test: boolean;
  /** EVM chain ID. */
  chainId?: number;
  /** USDC: the mint (Solana) or the token contract (EVM). */
  usdc: string;
  usdcDecimals: number;
  /** The native coin, which pays the network fee (on Arc, USDC itself). */
  native: { symbol: string; decimals: number };
  explorerTx?: (hash: string) => string;
  faucet?: string;
};

export const VAULT_CHAINS: VaultChain[] = [
  {
    id: "solana-devnet", name: "Solana", kind: "solana", test: true, usdc: "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU", usdcDecimals: 6,
    native: { symbol: "SOL", decimals: 9 }, explorerTx: (h) => `https://explorer.solana.com/tx/${h}?cluster=devnet`, faucet: "https://faucet.circle.com",
  },
  {
    id: "base-sepolia", name: "Base", kind: "evm", test: true, chainId: 84532, usdc: "0x036CbD53842c5426634e7929541eC2318f3dCF7e", usdcDecimals: 6,
    native: { symbol: "ETH", decimals: 18 }, explorerTx: (h) => `https://sepolia.basescan.org/tx/${h}`, faucet: "https://faucet.circle.com",
  },
  {
    id: "arc-testnet", name: "Arc", kind: "evm", test: true, chainId: 5042002, usdc: "0x3600000000000000000000000000000000000000", usdcDecimals: 6,
    native: { symbol: "USDC", decimals: 18 }, explorerTx: (h) => `https://testnet.arcscan.app/tx/${h}`, faucet: "https://faucet.circle.com",
  },
  {
    id: "solana", name: "Solana", kind: "solana", test: false, usdc: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", usdcDecimals: 6,
    native: { symbol: "SOL", decimals: 9 }, explorerTx: (h) => `https://explorer.solana.com/tx/${h}`,
  },
  {
    id: "base", name: "Base", kind: "evm", test: false, chainId: 8453, usdc: "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", usdcDecimals: 6,
    native: { symbol: "ETH", decimals: 18 }, explorerTx: (h) => `https://basescan.org/tx/${h}`,
  },
  {
    id: "arc", name: "Arc", kind: "evm", test: false, chainId: 5042, usdc: "0x3600000000000000000000000000000000000000", usdcDecimals: 6,
    native: { symbol: "USDC", decimals: 18 },
  },
];

export function vaultChain(id: string): VaultChain | undefined {
  return VAULT_CHAINS.find((c) => c.id === id);
}

/** The vault's chains on Testnet or on Mainnet. */
export function vaultChains(test: boolean): VaultChain[] {
  return VAULT_CHAINS.filter((c) => c.test === test);
}

/** A decimal amount ("1.25") in minor units, or null if it isn't one with at most `decimals` places. */
export function toMinor(value: string, decimals: number): bigint | null {
  const v = value.trim();
  if (!/^\d+(\.\d+)?$/.test(v)) return null;
  const [whole, frac = ""] = v.split(".");
  if (frac.length > decimals) return null;
  return BigInt(whole) * 10n ** BigInt(decimals) + BigInt(frac.padEnd(decimals, "0") || "0");
}

/** Minor units as a decimal string, without trailing zeros. */
export function fromMinor(minor: bigint | number | string, decimals: number, maxPlaces = decimals): string {
  const n = BigInt(minor);
  const neg = n < 0n;
  const abs = neg ? -n : n;
  const base = 10n ** BigInt(decimals);
  const whole = abs / base;
  const frac = (abs % base).toString().padStart(decimals, "0").slice(0, maxPlaces).replace(/0+$/, "");
  return `${neg ? "-" : ""}${whole}${frac ? "." + frac : ""}`;
}
