// Solana wallets installed in this browser, found through the Wallet Standard:
// the event protocol Phantom, Solflare, Backpack and the rest use to announce
// themselves to a page. Sign-in drives them directly (connect, then sign one
// message), so every failure can be named instead of surfacing as a bare code.
//
// No imports and only erasable TypeScript, so node can run the tests beside it.

export type WalletAccount = { address: string; publicKey: Uint8Array };

export type InstalledWallet = {
  name: string;
  icon: string;
  chains: readonly string[];
  accounts: readonly WalletAccount[];
  features: Readonly<Record<string, unknown>>;
};

type ConnectFeature = {
  connect: (input?: { silent?: boolean }) => Promise<{ accounts: readonly WalletAccount[] }>;
};

type SignMessageFeature = {
  signMessage: (input: { account: WalletAccount; message: Uint8Array }) => Promise<readonly { signedMessage: Uint8Array; signature: Uint8Array }[]>;
};

const CONNECT = "standard:connect";
const SIGN_MESSAGE = "solana:signMessage";

/** Wallets people expect first, in this order; the rest follow alphabetically. */
const PREFERRED = ["Phantom", "Solflare", "Backpack"];

/** A wallet that can sign Algebra in: Solana, connectable, able to sign a message. Privy's own embedded wallet is not one. */
export function canSignIn(w: InstalledWallet): boolean {
  if (!w || typeof w.name !== "string" || !w.features) return false;
  if ("privy:" in w.features || (w as { isPrivyWallet?: boolean }).isPrivyWallet === true) return false;
  return (
    w.chains.some((c) => typeof c === "string" && c.startsWith("solana:")) &&
    typeof (w.features[CONNECT] as ConnectFeature | undefined)?.connect === "function" &&
    typeof (w.features[SIGN_MESSAGE] as SignMessageFeature | undefined)?.signMessage === "function"
  );
}

export function sortWallets(wallets: InstalledWallet[]): InstalledWallet[] {
  const rank = (name: string) => {
    const i = PREFERRED.findIndex((p) => p.toLowerCase() === name.toLowerCase());
    return i === -1 ? PREFERRED.length : i;
  };
  return [...wallets].sort((a, b) => rank(a.name) - rank(b.name) || a.name.localeCompare(b.name));
}

/**
 * Calls onChange with every Solana wallet announced to this page, now and as
 * extensions load later. Returns the function that stops listening.
 *
 * The protocol: a wallet that loaded first fired "register-wallet" with a
 * callback, which the app answers with its register function; one that loads
 * later waits for the app's "app-ready" event, whose detail is that same
 * function. Both are answered here. Registering twice is harmless: wallets are
 * kept by name.
 */
export function watchWallets(onChange: (wallets: InstalledWallet[]) => void): () => void {
  if (typeof window === "undefined") return () => {};
  const found = new Map<string, InstalledWallet>();
  const api = {
    register: (...wallets: InstalledWallet[]) => {
      let changed = false;
      for (const w of wallets) {
        if (canSignIn(w) && !found.has(w.name)) {
          found.set(w.name, w);
          changed = true;
        }
      }
      if (changed) onChange(sortWallets([...found.values()]));
      return () => {};
    },
  };
  const onRegister = (e: Event) => {
    const callback = (e as CustomEvent<unknown>).detail;
    if (typeof callback !== "function") return;
    try {
      callback(api);
    } catch {
      // a wallet whose registration throws is not one we can use
    }
  };
  window.addEventListener("wallet-standard:register-wallet", onRegister);
  try {
    window.dispatchEvent(new CustomEvent("wallet-standard:app-ready", { detail: api }));
  } catch {
    // no CustomEvent: only wallets that registered after this listener are seen
  }
  return () => window.removeEventListener("wallet-standard:register-wallet", onRegister);
}

/** Asks the wallet to connect (it prompts unless this site was approved before) and returns the account to sign with. */
export async function connectAccount(wallet: InstalledWallet): Promise<WalletAccount> {
  const feature = wallet.features[CONNECT] as ConnectFeature;
  const { accounts } = await feature.connect();
  const account = accounts[0] ?? wallet.accounts[0];
  if (!account) throw new Error(`${wallet.name} connected without sharing an account`);
  return account;
}

/** Has the wallet sign these bytes; returns the signature as base64, the form Privy expects. */
export async function signWithWallet(wallet: InstalledWallet, account: WalletAccount, message: Uint8Array): Promise<string> {
  const feature = wallet.features[SIGN_MESSAGE] as SignMessageFeature;
  const [signed] = await feature.signMessage({ account, message });
  if (!signed?.signature) throw new Error(`${wallet.name} returned no signature`);
  let binary = "";
  for (const byte of signed.signature) binary += String.fromCharCode(byte);
  return btoa(binary);
}

export type WalletFailureKind = "cancelled" | "pending" | "locked" | "unsupported" | "other";

/** The words in an error the wallet threw (it may be wrapped, or a plain {code, message} object) and its code, if any. */
function readError(err: unknown): { text: string; code: string } {
  const texts: string[] = [];
  let code = "";
  let cur: unknown = err;
  for (let depth = 0; cur && depth < 4; depth++) {
    if (typeof cur === "string") {
      texts.push(cur);
      break;
    }
    if (typeof cur !== "object") break;
    const o = cur as { message?: unknown; code?: unknown; name?: unknown; error?: unknown; cause?: unknown };
    if (typeof o.message === "string" && o.message) texts.push(o.message);
    if (!code && (typeof o.code === "number" || typeof o.code === "string")) code = String(o.code);
    cur = o.error ?? o.cause;
  }
  return { text: [...new Set(texts)].join(": "), code };
}

/**
 * What went wrong in a wallet, said so the person can act on it: which
 * wallet, whether anything was sent, and the next step. text is the wallet's
 * own words, kept so a report of the problem carries them.
 */
export function walletFailure(err: unknown, walletName: string): { kind: WalletFailureKind; message: string; text: string } {
  const { text, code } = readError(err);
  const w = walletName || "Your wallet";
  const say = (kind: WalletFailureKind, message: string) => ({ kind, message: `${message}${text ? ` (${w}: ${text})` : ""}`, text });
  // A second request while one is waiting is refused (Phantom: -32002). It is
  // the commonest way to get stuck: the first popup is hidden behind a window.
  if (code === "-32002" || /already (pending|processing)|request.*pending|pending request/i.test(text)) {
    return say("pending", `${w} already has a request waiting. Open the ${w} extension, approve or reject it, then try again.`);
  }
  if (/locked|unlock/i.test(text)) {
    return say("locked", `${w} is locked. Unlock it, then press the button again.`);
  }
  if (code === "4001" || /reject|denied|declin|cancel|closed|dismiss|refus/i.test(text)) {
    return say("cancelled", `You cancelled in ${w}, so you're not signed in and nothing was sent. Press the button to try again.`);
  }
  if (/ledger|hardware|off-?chain|not supported|unsupported|method not found|not implemented/i.test(text)) {
    return say("unsupported", `${w} can't sign the sign-in message with this account. Hardware-wallet accounts often can't. Switch ${w} to a regular account, or sign in with email.`);
  }
  return say("other", `${w} didn't finish signing you in. Open ${w}, check it's unlocked on Solana, and try again.`);
}

/** Whether a failure is the kind where signing the same message the hardware-wallet way is worth one try. */
export function shouldTryOffchain(err: unknown): boolean {
  return walletFailure(err, "").kind === "unsupported";
}
