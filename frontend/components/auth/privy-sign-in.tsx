"use client";

// Sign-in with Privy: an email code, Google, or a Solana wallet (Phantom,
// Solflare, Backpack…). Anyone who arrives without a Solana wallet is given
// a Privy embedded one, whose key Privy secures for them; Algebra only ever
// sees its address.
//
// Privy's session is used once: its identity token, signed by Privy for this
// app, is traded for Algebra's own session cookie, and Privy is then signed
// out, so signing out of Algebra signs the person out.
//
// Loaded only on the sign-in pages (next/dynamic), so the console doesn't
// carry Privy's SDK.

import { useCallback, useRef, useState } from "react";
import { PrivyProvider, getIdentityToken, useLogin, useLogout, usePrivy, useUser, type User as PrivyUser } from "@privy-io/react-auth";
import { toSolanaWalletConnectors, useCreateWallet } from "@privy-io/react-auth/solana";
import * as api from "@/lib/api-client";
import type { User } from "@/lib/types";
import { IconWallet, Spinner } from "@/components/icons";

type Props = {
  appId: string;
  label: string;
  onSignedIn: (user: User) => void;
  onError: (message: string) => void;
};

const solanaConnectors = toSolanaWalletConnectors({ shouldAutoConnect: false });

export default function PrivySignIn({ appId, ...rest }: Props) {
  return (
    <PrivyProvider
      appId={appId}
      config={{
        // Which methods are offered (email, Google, wallets) is set in the
        // Privy dashboard; only Solana wallets are listed.
        appearance: {
          walletChainType: "solana-only",
          landingHeader: "Sign in to Algebra",
          accentColor: "#4f46e5",
          showWalletLoginFirst: false,
        },
        // Created below, after sign-in, so the wallet exists before the
        // identity token Algebra receives is issued.
        embeddedWallets: { ethereum: { createOnLogin: "off" }, solana: { createOnLogin: "off" } },
        externalWallets: { solana: { connectors: solanaConnectors } },
      }}
    >
      <PrivyButton {...rest} />
    </PrivyProvider>
  );
}

/**
 * What other servers refused since t0 (a performance.now() reading), from the browser's own
 * record of each request: "429 from auth.privy.io/api/v1/siws/authenticate". Privy reports a
 * failure as a bare code, and the same code can come from any of its calls, so this is what
 * tells them apart. Empty when nothing was refused or the browser doesn't record statuses.
 */
function refusedSince(t0: number): string {
  try {
    const seen: string[] = [];
    for (const e of performance.getEntriesByType("resource") as (PerformanceResourceTiming & { responseStatus?: number })[]) {
      const status = e.responseStatus ?? 0;
      if (e.startTime < t0 || status < 400) continue;
      const u = new URL(e.name);
      if (u.origin === window.location.origin) continue;
      // IDs in a path (an app, a user, a wallet) say nothing a person can act on.
      const line = `${status} from ${u.host}${u.pathname.replace(/[A-Za-z0-9_-]{20,}/g, "…")}`;
      if (!seen.includes(line)) seen.push(line);
    }
    return seen.slice(-3).join("; ");
  } catch {
    return "";
  }
}

// Privy's servers answer 429 when one of their limits is used up. Some of those limits are
// counted per week (wallet sign-ins are, per wallet), so a few minutes' wait may change nothing.
// Algebra's own API was never asked. detail names the call Privy refused, when the browser saw it.
function rateLimited(detail: string) {
  return `Privy, which runs this sign-in, refused it for too many requests. Algebra's server was never asked, and trying again right away won't help: use another sign-in method, or try later. (Privy: too_many_requests${detail ? `; ${detail}` : ""})`;
}

function isRateLimited(err: unknown): boolean {
  // Algebra's own API has its own limit and says so in its own words.
  if (!err || typeof err !== "object" || err instanceof api.ApiError) return false;
  const e = err as { status?: unknown; privyErrorCode?: unknown; code?: unknown; message?: unknown };
  return e.status === 429 || e.privyErrorCode === "too_many_requests" || e.code === "too_many_requests" || (typeof e.message === "string" && /too many requests/i.test(e.message));
}

/** What a Privy error code means for the person, always with the code itself and Privy's own reason when it gave one. */
function privyFailure(code: string, reason: string, refused = "") {
  if (code === "too_many_requests" || /too[ _]many[ _]requests|HTTP 429/i.test(reason) || /^429 /.test(refused)) return rateLimited(refused);
  // Privy only signs people in from the addresses listed for the app. It is a dashboard
  // setting, so trying again never helps: name the address and where to add it.
  if (code === "invalid_origin" || /invalid_origin/i.test(reason)) {
    const here = typeof window === "undefined" ? "this address" : window.location.origin;
    return `Privy doesn't allow sign-in from ${here}. Add exactly that address under Allowed origins (Privy dashboard, Configuration, App settings, Domains), then try again. (Privy: invalid_origin)`;
  }
  const why: Record<string, string> = {
    generic_connect_wallet_error: "The wallet didn't finish connecting or signing. Unlock it, approve the signature request it shows, and try again.",
    unknown_connect_wallet_error: "The wallet didn't finish connecting or signing. Unlock it, approve the signature request it shows, and try again.",
    disallowed_login_method: "This sign-in method is turned off for this app. Solana wallets must be enabled under Login methods in the Privy dashboard.",
    allowlist_rejected: "This Privy app only admits accounts on its allowlist, and this one isn't on it.",
    client_request_timeout: "Privy didn't answer in time. Check your connection and try again.",
    not_supported: "This wallet or sign-in method isn't supported here.",
    unsupported_chain_id: "The wallet is on a network this app doesn't use. Algebra signs in with Solana wallets.",
  };
  return `${why[code] ?? "Sign-in with Privy didn't complete. Try again."} (Privy: ${code}${reason ? `: ${reason}` : ""}${refused ? `; ${refused}` : ""})`;
}

/** The readable part of something passed to console.warn: a string, an error (and its cause), or an API error object. */
function describeWarning(value: unknown): string {
  if (typeof value === "string") return value;
  if (!value || typeof value !== "object") return "";
  const v = value as { message?: unknown; error?: unknown; code?: unknown; privyErrorCode?: unknown; status?: unknown; cause?: unknown };
  const parts = [v.message, v.error, v.privyErrorCode ?? v.code, v.status && `HTTP ${String(v.status)}`]
    .filter((x): x is string | number => typeof x === "string" || typeof x === "number")
    .map(String);
  const cause = v.cause && v.cause !== value ? describeWarning(v.cause) : "";
  return [...new Set([...parts, cause].filter(Boolean))].join(", ");
}

function hasSolanaWallet(user: PrivyUser) {
  return user.linkedAccounts.some((a) => a.type === "wallet" && a.chainType === "solana");
}

function PrivyButton({ label, onSignedIn, onError }: Omit<Props, "appId">) {
  const { ready, authenticated } = usePrivy();
  const { logout } = useLogout();
  const { refreshUser } = useUser();
  const { createWallet } = useCreateWallet();
  const [busy, setBusy] = useState(false);
  // Only a sign-in the person started here is traded: onComplete also fires
  // for a Privy session left over from an earlier visit.
  const started = useRef(false);
  // Privy reports a failure as a bare code and writes the reason (the wallet's
  // own error, say) to console.warn. While a sign-in is running, keep the last
  // one so the message can say what actually went wrong.
  const lastWarning = useRef("");
  // When the attempt began (performance.now()), to read what was refused during it.
  const attemptAt = useRef(0);
  const stopWatching = useRef<(() => void) | null>(null);
  const watchWarnings = useCallback(() => {
    stopWatching.current?.();
    lastWarning.current = "";
    const original = console.warn;
    console.warn = (...args: unknown[]) => {
      const text = args.map(describeWarning).filter(Boolean).join(" ");
      // Privy warns twice on a failure: a general line, then the cause. Keep both.
      if (text && !lastWarning.current.includes(text)) {
        lastWarning.current = [lastWarning.current, text].filter(Boolean).slice(-2).join(": ").slice(0, 300);
      }
      original(...args);
    };
    stopWatching.current = () => {
      console.warn = original;
      stopWatching.current = null;
    };
  }, []);

  const finish = useCallback(
    async (privyUser: PrivyUser) => {
      if (!started.current) return;
      started.current = false;
      stopWatching.current?.();
      setBusy(true);
      // Which step is running, for the message when one fails: Privy is signed in by now.
      let step = "making your wallet";
      try {
        const made = !hasSolanaWallet(privyUser);
        if (made) await createWallet();
        // The token Privy issued at sign-in already names the wallet they came with. Only a wallet
        // made just now needs a fresh one: a refresh is one more request against Privy's limits.
        step = "reading your Privy identity";
        let token = made ? null : await getIdentityToken();
        let refreshFailed: unknown;
        if (!token) {
          // A refresh Privy refuses is not the end: the token from sign-in still proves who this
          // is. It only lacks the wallet made a moment ago, which the next sign-in records.
          await refreshUser().catch((e: unknown) => (refreshFailed = e));
          token = await getIdentityToken();
        }
        if (!token) {
          throw (
            refreshFailed ??
            new Error("Privy didn't send an identity token. Turn on “Return user data in an identity token” in the Privy dashboard.")
          );
        }
        step = "signing in to Algebra";
        const { user } = await api.signInWithPrivy(token);
        await logout().catch(() => {});
        onSignedIn(user);
      } catch (err) {
        // Read before signing out of Privy, which makes requests of its own.
        const refused = err instanceof api.ApiError ? "" : refusedSince(attemptAt.current);
        await logout().catch(() => {});
        const where = [`while ${step}`, refused].filter(Boolean).join("; ");
        onError(
          isRateLimited(err)
            ? rateLimited(where)
            : err instanceof api.ApiError
              ? err.message
              : `${err instanceof Error ? err.message : "Sign-in with Privy didn't complete. Try again."} (${where})`,
        );
        setBusy(false);
      }
    },
    [createWallet, refreshUser, logout, onSignedIn, onError],
  );

  const { login } = useLogin({
    onComplete: ({ user }) => void finish(user),
    onError: (code) => {
      started.current = false;
      stopWatching.current?.();
      if (code === "exited_auth_flow") return;
      // Privy's own step failed, before Algebra was asked anything: say which,
      // or the person has nothing to act on.
      onError(privyFailure(String(code), lastWarning.current, refusedSince(attemptAt.current)));
    },
  });

  async function start() {
    onError("");
    started.current = true;
    // The browser keeps a short list of requests; start this attempt's from empty.
    try {
      performance.clearResourceTimings();
    } catch {
      // not every browser lets a page clear it: the readings are filtered by time anyway
    }
    attemptAt.current = performance.now();
    watchWarnings();
    // A Privy session left from an abandoned attempt would skip the modal.
    if (authenticated) await logout().catch(() => {});
    login();
  }

  return (
    <button
      type="button"
      onClick={start}
      disabled={!ready || busy}
      className="flex h-11 w-full items-center justify-center gap-2.5 rounded-xl bg-primary text-[0.95rem] font-medium text-primary-tint shadow-[0_6px_16px_-8px_color-mix(in_srgb,var(--color-primary)_80%,transparent)] transition-[transform,opacity] hover:opacity-95 active:scale-[0.99] disabled:cursor-wait disabled:opacity-70"
    >
      {busy || !ready ? <Spinner size={16} /> : <IconWallet size={18} />}
      {busy ? "Signing you in…" : label}
    </button>
  );
}
