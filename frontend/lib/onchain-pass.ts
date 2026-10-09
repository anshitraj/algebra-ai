// Spend Passes enforced on Solana: the API calls, and signing with the
// person's own wallet through the Wallet Standard (Phantom, Solflare,
// Backpack…). Algebra builds every transaction unsigned; the wallet signs and
// sends it, so the owner's key never leaves the wallet.

export type OnchainNetwork = {
  network: "solana" | "solana-devnet";
  cluster: "mainnet" | "devnet";
  program_id: string;
  agent: string;
  destination: string;
  mint: string;
  required: boolean;
};

export type OnchainState = {
  owner: string;
  agent: string;
  per_call_cap: number;
  total_budget: number;
  spent: number;
  window_secs: number;
  window_cap: number;
  expires_at: number;
  pulls: number;
  frozen: boolean;
  revoked: boolean;
};

export type OnchainPull = {
  reservation_id: string;
  network: string;
  amount_minor: number;
  pull_signature: string;
  state: "SENDING" | "PULLED" | "VOID" | "SETTLED" | "REFUNDING" | "REFUNDED";
  used_minor?: number;
  refund_minor?: number;
  refund_signature?: string;
  created_at: string;
};

export type OnchainPassView = {
  binding: { pass_id: string; network: string; program_id: string; address: string; owner_wallet: string; linked_at: string };
  exists: boolean;
  state?: OnchainState;
  vault: string;
  vault_minor?: number;
  remaining_minor: number;
  explorer: string;
  pulls: OnchainPull[];
};

export type OwnerAction = "freeze" | "unfreeze" | "deposit" | "withdraw" | "revoke" | "close";

export class OnchainError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function call<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      credentials: "same-origin",
      cache: "no-store",
    });
  } catch {
    throw new OnchainError(0, "Can't reach Algebra. Check your connection and try again.");
  }
  const text = await res.text();
  let data: unknown;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }
  if (!res.ok) throw new OnchainError(res.status, (data as { error?: string } | undefined)?.error ?? res.statusText);
  return data as T;
}

export const onchainNetworks = () => call<{ networks: OnchainNetwork[] }>("/api/v1/onchain/networks");
export const getOnchainPass = (passId: string) => call<OnchainPassView>(`/api/v1/me/passes/${encodeURIComponent(passId)}/onchain`);
export const prepareOnchainPass = (passId: string, b: { network: string; owner: string; deposit_minor: number }) =>
  call<{ pass_address: string; transaction: string; network: string }>(`/api/v1/me/passes/${encodeURIComponent(passId)}/onchain/prepare`, "POST", b);
export const linkOnchainPass = (passId: string, network: string, address: string) =>
  call<OnchainPassView["binding"]>(`/api/v1/me/passes/${encodeURIComponent(passId)}/onchain/link`, "POST", { network, address });
export const ownerTransaction = (passId: string, action: OwnerAction, amount_minor = 0) =>
  call<{ transaction: string }>(`/api/v1/me/passes/${encodeURIComponent(passId)}/onchain/tx`, "POST", { action, amount_minor });
export const unlinkOnchainPass = (passId: string) => call<void>(`/api/v1/me/passes/${encodeURIComponent(passId)}/onchain`, "DELETE");

// --- Wallet Standard (https://github.com/wallet-standard/wallet-standard) ---

type WalletAccount = { address: string; publicKey: Uint8Array; chains: readonly string[]; features: readonly string[] };
type StandardWallet = {
  name: string;
  icon: string;
  chains: readonly string[];
  accounts: readonly WalletAccount[];
  features: Record<string, unknown>;
};
type ConnectFeature = { connect: (input?: { silent?: boolean }) => Promise<{ accounts: readonly WalletAccount[] }> };
type SignAndSendFeature = {
  signAndSendTransaction: (
    ...inputs: { account: WalletAccount; transaction: Uint8Array; chain: string; options?: { preflightCommitment?: string } }[]
  ) => Promise<readonly { signature: Uint8Array }[]>;
};

const SIGN_AND_SEND = "solana:signAndSendTransaction";

/** Solana wallets installed in this browser that can sign and send. */
export function solanaWallets(): StandardWallet[] {
  if (typeof window === "undefined") return [];
  const found: StandardWallet[] = [];
  const api = {
    register: (...ws: StandardWallet[]) => {
      for (const w of ws) if (!found.includes(w)) found.push(w);
      return () => {};
    },
  };
  // Wallets that loaded before us answer this event; ones that load later
  // announce themselves with wallet-standard:register-wallet.
  try {
    window.dispatchEvent(new CustomEvent("wallet-standard:app-ready", { detail: api }));
  } catch {
    // an old browser without CustomEvent: no wallets
  }
  return found.filter((w) => SIGN_AND_SEND in w.features && w.chains.some((c) => c.startsWith("solana:")));
}

/** Asks a wallet for its account (a popup the first time). */
export async function connectWallet(w: StandardWallet): Promise<WalletAccount> {
  const connect = w.features["standard:connect"] as ConnectFeature | undefined;
  const accounts = connect ? (await connect.connect()).accounts : w.accounts;
  const account = accounts.find((a) => a.chains.some((c) => c.startsWith("solana:"))) ?? accounts[0];
  if (!account) throw new Error(`${w.name} didn't share an account.`);
  return account;
}

/** Has the wallet sign and send a transaction Algebra prepared. Returns its signature. */
export async function signAndSend(w: StandardWallet, account: WalletAccount, txBase64: string, network: string): Promise<string> {
  const feature = w.features[SIGN_AND_SEND] as SignAndSendFeature;
  const transaction = Uint8Array.from(atob(txBase64), (c) => c.charCodeAt(0));
  const chain = network === "solana-devnet" ? "solana:devnet" : "solana:mainnet";
  const [out] = await feature.signAndSendTransaction({ account, transaction, chain, options: { preflightCommitment: "confirmed" } });
  return base58(out.signature);
}

const ALPHABET = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz";

/** Base58 (Bitcoin alphabet), as Solana writes signatures and addresses. */
export function base58(bytes: Uint8Array): string {
  const digits: number[] = [];
  for (const b of bytes) {
    let carry = b;
    for (let i = 0; i < digits.length; i++) {
      carry += digits[i] << 8;
      digits[i] = carry % 58;
      carry = (carry / 58) | 0;
    }
    while (carry > 0) {
      digits.push(carry % 58);
      carry = (carry / 58) | 0;
    }
  }
  let out = "";
  for (const b of bytes) {
    if (b !== 0) break;
    out += "1";
  }
  for (let i = digits.length - 1; i >= 0; i--) out += ALPHABET[digits[i]];
  return bytes.length === 0 ? "" : out;
}

export type { StandardWallet, WalletAccount };
