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
  /** Where Algebra's payer key comes from: "generated" (devnet) or "env". */
  signer_mode?: "env" | "generated";
  /** False when the payer is too low on SOL to pay the fees of vault pulls and refunds. */
  signer_ready?: boolean;
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
  /** The vault was made for an earlier Algebra payer key and can't fund payments. */
  agent_changed?: boolean;
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

// Several components ask about the same networks and the same pass within a moment (the
// passes page makes one panel per pass; strict mode runs effects twice). The API limits
// each session to 120 requests a minute, so these share one request each and run at
// most three at a time: a page with many passes can't use the minute up on its own.
const NETWORKS_TTL_MS = 60_000;
let networksCache: { at: number; value: Promise<{ networks: OnchainNetwork[] }> } | null = null;

export const onchainNetworks = () => {
  const now = Date.now();
  if (networksCache && now - networksCache.at < NETWORKS_TTL_MS) return networksCache.value;
  const value = call<{ networks: OnchainNetwork[] }>("/api/v1/onchain/networks");
  networksCache = { at: now, value };
  // A failure isn't kept: the next caller asks again.
  value.catch(() => {
    if (networksCache?.value === value) networksCache = null;
  });
  return value;
};

const MAX_PASS_LOOKUPS = 3;
let lookupsRunning = 0;
const lookupsWaiting: (() => void)[] = [];
const lookupsInFlight = new Map<string, Promise<OnchainPassView>>();

async function inLookupSlot<T>(fn: () => Promise<T>): Promise<T> {
  if (lookupsRunning >= MAX_PASS_LOOKUPS) await new Promise<void>((resolve) => lookupsWaiting.push(resolve));
  lookupsRunning++;
  try {
    return await fn();
  } finally {
    lookupsRunning--;
    lookupsWaiting.shift()?.();
  }
}

/** A pass's vault. Callers that ask while one request is out share it; a later call asks again, so a read after a transaction is fresh. */
export const getOnchainPass = (passId: string): Promise<OnchainPassView> => {
  const pending = lookupsInFlight.get(passId);
  if (pending) return pending;
  const p = inLookupSlot(() => call<OnchainPassView>(`/api/v1/me/passes/${encodeURIComponent(passId)}/onchain`)).finally(() => {
    lookupsInFlight.delete(passId);
  });
  lookupsInFlight.set(passId, p);
  return p;
};
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

/**
 * A wallet's account. One the wallet already shares with this site (the accounts
 * it lists, or a silent connect, which a wallet that trusts the site and is
 * unlocked answers at once) is used without a popup; only a wallet that has not
 * been connected here asks the person. `want` picks the account when the wallet
 * shares several.
 */
export async function connectWallet(w: StandardWallet, want?: string): Promise<WalletAccount> {
  const onSolana = (a: WalletAccount) => a.chains.some((c) => c.startsWith("solana:"));
  const pick = (list: readonly WalletAccount[]) => (want ? list.find((a) => a.address === want && onSolana(a)) : undefined) ?? list.find(onSolana) ?? list[0];
  let account = pick(w.accounts);
  if (!account || (want && account.address !== want)) {
    const connect = w.features["standard:connect"] as ConnectFeature | undefined;
    if (connect) {
      const silent = await connect.connect({ silent: true }).then(
        (r) => r.accounts,
        () => [] as readonly WalletAccount[],
      );
      account = pick(silent);
      if (!account || (want && account.address !== want)) account = pick((await connect.connect()).accounts) ?? account;
    }
  }
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
