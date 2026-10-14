// Getting the identity token Algebra trades for its own session, with as few
// requests to Privy as possible.
//
// Privy's SDK already stores an identity token: its sign-in reply carries one
// and every refresh replaces it. The SDK's getIdentityToken() reads like a
// lookup but is a request: each call asks Privy's users/me for a new token and
// throws when Privy answers 429. A sign-in that had worked could therefore end
// in "too many requests" at its last step, and the retry spent the one-time
// code. This reads the stored token instead and asks Privy again only when
// that one lacks the wallet made a moment ago, pausing and retrying if Privy
// says it is too busy.
//
// No imports: the sign-in panel hands in everything that touches Privy.

/** Whose token it is and whether it lists a Solana wallet. */
export type TokenView = { did: string; solanaWallet: boolean };

function decodeSegment(seg: string): unknown {
  const bin = atob(seg.replace(/-/g, "+").replace(/_/g, "/"));
  return JSON.parse(new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0))));
}

/**
 * Reads a Privy identity token without checking its signature. Algebra's server does the
 * checking; this only decides whether another request to Privy is needed.
 */
export function viewIdentityToken(token: string | null | undefined): TokenView | null {
  try {
    const seg = token?.split(".")[1];
    if (!seg) return null;
    const claims = decodeSegment(seg) as { sub?: unknown; linked_accounts?: unknown };
    if (typeof claims.sub !== "string") return null;
    // Privy writes the linked accounts as a JSON string inside the token.
    const raw = typeof claims.linked_accounts === "string" ? JSON.parse(claims.linked_accounts) : claims.linked_accounts;
    const linked = Array.isArray(raw) ? (raw as { type?: unknown; chain_type?: unknown }[]) : [];
    return { did: claims.sub, solanaWallet: linked.some((a) => a?.type === "wallet" && a?.chain_type === "solana") };
  } catch {
    return null;
  }
}

/** Privy sent no identity token: the app's dashboard isn't set to include one. Retrying can't help. */
export class NoIdentityToken extends Error {
  constructor() {
    super("Privy didn't send an identity token. Turn on “Return user data in an identity token” in the Privy dashboard.");
    this.name = "NoIdentityToken";
  }
}

/** The pauses before each retry of a request Privy refused for being too busy. */
export const DEFAULT_WAITS_MS: readonly number[] = [2_000, 4_000, 8_000];

export type Step = "making your wallet" | "reading your Privy identity";

export type GatherOptions = {
  /** The Privy user's DID: a token naming anyone else (one left from an earlier sign-in) is never used. */
  did: string;
  /** Privy made the wallet during this sign-in, so the token it issued at sign-in predates it. */
  walletJustMade: boolean;
  createWallet: () => Promise<unknown>;
  /** One request to Privy that stores a fresh identity token. */
  refreshUser: () => Promise<unknown>;
  /** The identity token the SDK holds right now. No request. */
  stored: () => string | null;
  /** Whether an error is Privy refusing for too many requests. */
  isRefused: (err: unknown) => boolean;
  waitsMs?: readonly number[];
  sleep?: (ms: number) => Promise<void>;
  onStep?: (step: Step) => void;
  /** Called before each pause, with its length in seconds. */
  onWait?: (seconds: number) => void;
};

export type Gathered = {
  token: string;
  /** The wallet made during this sign-in isn't in the token: Algebra records it at the next sign-in. */
  walletPending: boolean;
};

// The SDK's store reaches the panel a render after the request that filled it.
const SETTLE_POLLS = 8;
const SETTLE_MS = 50;

export async function gatherIdentityToken(o: GatherOptions): Promise<Gathered> {
  const sleep = o.sleep ?? ((ms: number) => new Promise<void>((done) => setTimeout(done, ms)));
  const waits = o.waitsMs ?? DEFAULT_WAITS_MS;

  // Privy's 429 passes in seconds more often than not: ask again after a pause before giving up.
  async function patiently<T>(run: () => Promise<T>): Promise<T> {
    for (let tries = 0; ; tries++) {
      try {
        return await run();
      } catch (err) {
        if (!o.isRefused(err) || tries >= waits.length) throw err;
        o.onWait?.(Math.ceil(waits[tries] / 1000));
        await sleep(waits[tries]);
      }
    }
  }

  /** The stored token if it names this person (and lists a wallet, when one is needed). */
  function peek(needWallet: boolean): string | null {
    const token = o.stored();
    const seen = viewIdentityToken(token);
    return token && seen && seen.did === o.did && (!needWallet || seen.solanaWallet) ? token : null;
  }

  async function settle(needWallet: boolean): Promise<string | null> {
    for (let i = 0; ; i++) {
      const token = peek(needWallet);
      if (token || i >= SETTLE_POLLS) return token;
      await sleep(SETTLE_MS);
    }
  }

  if (o.walletJustMade) {
    o.onStep?.("making your wallet");
    await patiently(o.createWallet);
  }
  o.onStep?.("reading your Privy identity");

  const have = await settle(o.walletJustMade);
  if (have) return { token: have, walletPending: false };

  // Nothing stored that serves: one request to Privy for a fresh token.
  let refused: unknown;
  try {
    await patiently(o.refreshUser);
  } catch (err) {
    refused = err;
  }
  const fresh = await settle(o.walletJustMade);
  if (fresh) return { token: fresh, walletPending: false };

  // Privy wouldn't give a fresh one. The token from sign-in still proves who this is; it only
  // lacks the wallet made a moment ago, which the next sign-in records.
  const older = peek(false);
  if (older) return { token: older, walletPending: o.walletJustMade };
  throw refused ?? new NoIdentityToken();
}
