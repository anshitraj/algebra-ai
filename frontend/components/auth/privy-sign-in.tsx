"use client";

// Sign-in with Privy, without Privy's own window and without a password: an
// email field (Privy emails a six-digit code) and a button for a Solana
// wallet (Phantom, Solflare, Backpack…). Anyone who arrives without a Solana
// wallet is given a Privy embedded one, whose key Privy secures for them;
// Algebra only ever sees its address.
//
// The wallet path talks to the wallet itself (lib/solana-wallets.ts): connect,
// then sign one message Privy issued, so each failure (cancelled, locked,
// another request already waiting, a hardware account that can't sign) is
// named instead of arriving as a bare Privy code.
//
// Privy's session is used once: its identity token, signed by Privy for this
// app, is traded for Algebra's own session cookie, and Privy is then signed
// out, so signing out of Algebra signs the person out.
//
// Loaded only on the sign-in pages (next/dynamic), so the console doesn't
// carry Privy's SDK.

import { useCallback, useEffect, useRef, useState } from "react";
import { PrivyProvider, getIdentityToken, useLoginWithEmail, useLoginWithSiws, useLogout, usePrivy, useUser, type User as PrivyUser } from "@privy-io/react-auth";
import { toSolanaWalletConnectors, useCreateWallet } from "@privy-io/react-auth/solana";
import * as api from "@/lib/api-client";
import type { User } from "@/lib/types";
import { connectAccount, shouldTryOffchain, signWithWallet, walletFailure, watchWallets, type InstalledWallet } from "@/lib/solana-wallets";
import { IconWallet, Spinner } from "@/components/icons";
import { AuthInput, FormError, SubmitButton } from "./fields";

type Props = {
  appId: string;
  /** An error to show on arrival, such as one an OAuth redirect came back with. */
  initialError?: string | null;
  onSignedIn: (user: User) => void;
};

const solanaConnectors = toSolanaWalletConnectors({ shouldAutoConnect: false });

export default function PrivySignIn({ appId, ...rest }: Props) {
  return (
    <PrivyProvider
      appId={appId}
      config={{
        // Which methods are allowed (email, wallets) is set in the Privy
        // dashboard. The sign-in is drawn here, so Privy's own is never opened.
        appearance: { walletChainType: "solana-only" },
        // Created after sign-in, so the wallet exists before the identity
        // token Algebra receives is issued.
        embeddedWallets: { ethereum: { createOnLogin: "off" }, solana: { createOnLogin: "off" } },
        externalWallets: { solana: { connectors: solanaConnectors } },
      }}
    >
      <Panel {...rest} />
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

/** The Privy error code on a thrown error, or "". */
function privyCode(err: unknown): string {
  if (!err || typeof err !== "object") return "";
  const e = err as { privyErrorCode?: unknown; code?: unknown };
  return typeof e.privyErrorCode === "string" ? e.privyErrorCode : typeof e.code === "string" ? e.code : "";
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : typeof err === "string" ? err : "";
}

/** What a Privy error code means for the person, always with the code itself and Privy's own reason when it gave one. */
function privyFailure(code: string, reason: string, refused = "", method: "email" | "wallet" = "wallet") {
  if (code === "too_many_requests" || /too[ _]many[ _]requests|HTTP 429/i.test(reason) || /^429 /.test(refused)) return rateLimited(refused);
  // Privy only signs people in from the addresses listed for the app. It is a dashboard
  // setting, so trying again never helps: name the address and where to add it.
  if (code === "invalid_origin" || /invalid_origin/i.test(reason)) {
    const here = typeof window === "undefined" ? "this address" : window.location.origin;
    return `Privy doesn't allow sign-in from ${here}. Add exactly that address under Allowed origins (Privy dashboard, Configuration, App settings, Domains), then try again. (Privy: invalid_origin)`;
  }
  const why: Record<string, string> = {
    disallowed_login_method:
      method === "email"
        ? "Email sign-in is turned off for this app. Turn on Email under Login methods in the Privy dashboard."
        : "Wallet sign-in is turned off for this app. Turn on Wallets (Solana) under Login methods in the Privy dashboard.",
    allowlist_rejected: "This Privy app only admits accounts on its allowlist, and this one isn't on it.",
    client_request_timeout: "Privy didn't answer in time. Check your connection and try again.",
    captcha_failure: "Privy's bot check didn't pass. Reload the page and try again.",
    invalid_captcha: "Privy's bot check didn't pass. Reload the page and try again.",
    captcha_timeout: "Privy's bot check timed out. Reload the page and try again.",
    not_supported: "This wallet or sign-in method isn't supported here.",
    unsupported_chain_id: "The wallet is on a network this app doesn't use. Algebra signs in with Solana wallets.",
  };
  return `${why[code] ?? "Privy didn't complete the sign-in. Try again."} (Privy: ${code || "unknown"}${reason ? `: ${reason}` : ""}${refused ? `; ${refused}` : ""})`;
}

/** Why an email step failed: a wrong code gets its own words, everything else is Privy's. */
function emailFailure(err: unknown, step: "send" | "code", email: string, refused: string) {
  if (isRateLimited(err)) return rateLimited(refused);
  const code = privyCode(err);
  const reason = errorText(err);
  if (step === "code" && (code === "invalid_credentials" || /invalid|incorrect|expired|wrong/i.test(reason))) {
    return `That code didn't work. Check the six digits, or send a new code: each code allows 5 tries. (Privy: ${code || reason})`;
  }
  if (step === "send" && !code && /email/i.test(reason)) {
    return `Privy couldn't send a code to ${email}. Check the address and try again. (Privy: ${reason})`;
  }
  return privyFailure(code, reason, refused, "email");
}

function hasSolanaWallet(user: PrivyUser) {
  return user.linkedAccounts.some((a) => a.type === "wallet" && a.chainType === "solana");
}

type Busy = null | "email" | "code" | "wallet" | "finishing";

const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function Panel({ initialError, onSignedIn }: Omit<Props, "appId">) {
  const { ready, authenticated } = usePrivy();
  const { logout } = useLogout();
  const { refreshUser } = useUser();
  const { createWallet } = useCreateWallet();
  const { generateSiwsMessage, generateSiwsOffchainMessage, loginWithSiws } = useLoginWithSiws();

  const [step, setStep] = useState<"email" | "code">("email");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState<Busy>(null);
  const [error, setError] = useState<string | null>(initialError ?? null);
  const [wallets, setWallets] = useState<InstalledWallet[]>([]);
  const [walletsOpen, setWalletsOpen] = useState(false);
  const [busyWallet, setBusyWallet] = useState("");
  const [cooldown, setCooldown] = useState(0);
  // Only a sign-in the person started here is traded: Privy's callbacks also
  // fire for a session left over from an earlier visit.
  const started = useRef(false);
  // When the attempt began (performance.now()), to read what was refused during it.
  const attemptAt = useRef(0);

  useEffect(() => watchWallets(setWallets), []);

  // The input is disabled while a code is checked, which drops focus: put it back for the next try.
  const codeInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (step === "code" && busy === null) codeInput.current?.focus();
  }, [step, busy]);

  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);

  // A sign-in that never answers (a request that hangs, a Privy callback that never fires) must end in a
  // message, not in a form that looks alive.
  const logoutRef = useRef(logout);
  useEffect(() => {
    logoutRef.current = logout;
  }, [logout]);
  useEffect(() => {
    if (busy !== "code" && busy !== "finishing") return;
    const t = setTimeout(() => {
      started.current = false;
      void logoutRef.current().catch(() => {});
      setError("Sign-in is taking too long. Check your connection, then send a new code and try again.");
      setStep("email");
      setCode("");
      setBusy(null);
    }, 45_000);
    return () => clearTimeout(t);
  }, [busy]);

  /** Whatever got Privy signed in, trade it for Algebra's session. */
  const finish = useCallback(
    async (privyUser: PrivyUser) => {
      if (!started.current) return;
      started.current = false;
      setBusy("finishing");
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
        setError(
          isRateLimited(err)
            ? rateLimited(where)
            : err instanceof api.ApiError
              ? err.message
              : `${errorText(err) || "Sign-in with Privy didn't complete. Try again."} (${where})`,
        );
        // Privy is signed out and its one-time code is spent: start again from the top.
        setStep("email");
        setCode("");
        setBusy(null);
      }
    },
    [createWallet, refreshUser, logout, onSignedIn],
  );

  const { sendCode, loginWithCode } = useLoginWithEmail({ onComplete: ({ user }) => void finish(user) });

  function begin() {
    setError(null);
    try {
      // The browser keeps a short list of requests; start this attempt's from empty.
      performance.clearResourceTimings();
    } catch {
      // not every browser lets a page clear it: the readings are filtered by time anyway
    }
    attemptAt.current = performance.now();
  }

  async function requestCode(e?: React.FormEvent) {
    e?.preventDefault();
    if (busy) return;
    const address = email.trim();
    if (!EMAIL_SHAPE.test(address)) {
      setError("Enter your email address, like you@company.com.");
      return;
    }
    begin();
    setBusy("email");
    try {
      // A Privy session left from an abandoned attempt would refuse a new one.
      if (authenticated) await logout().catch(() => {});
      await sendCode({ email: address });
      setStep("code");
      setCode("");
      setCooldown(30);
    } catch (err) {
      setError(emailFailure(err, "send", address, refusedSince(attemptAt.current)));
    } finally {
      setBusy(null);
    }
  }

  async function submitCode(value: string) {
    if (busy) return;
    begin();
    started.current = true;
    setBusy("code");
    try {
      // On success Privy's onComplete runs finish(), which carries on from here.
      await loginWithCode({ code: value });
    } catch (err) {
      started.current = false;
      setError(emailFailure(err, "code", email.trim(), refusedSince(attemptAt.current)));
      setCode("");
      setBusy(null);
    }
  }

  async function signInWithWallet(w: InstalledWallet) {
    if (busy) return;
    begin();
    setBusy("wallet");
    setBusyWallet(w.name);
    // Who to blame if this throws: the wallet's own errors and Privy's read differently.
    let stage: "wallet" | "privy" = "wallet";
    try {
      if (authenticated) await logout().catch(() => {});
      const account = await connectAccount(w);
      stage = "privy";
      const message = await generateSiwsMessage({ address: account.address });
      stage = "wallet";
      let signature: string;
      let messageType: "plain" | "offchain-message" = "plain";
      try {
        signature = await signWithWallet(w, account, new TextEncoder().encode(message));
      } catch (err) {
        // A hardware-wallet account refuses a plain message; its off-chain form is one more try.
        if (!shouldTryOffchain(err)) throw err;
        messageType = "offchain-message";
        signature = await signWithWallet(w, account, generateSiwsOffchainMessage({ message, address: account.address }));
      }
      stage = "privy";
      const user = await loginWithSiws({ message, signature, messageType, walletClientType: w.name.toLowerCase(), connectorType: "solana_adapter" });
      started.current = true;
      await finish(user);
    } catch (err) {
      started.current = false;
      const refused = refusedSince(attemptAt.current);
      if (stage === "wallet") {
        setError(walletFailure(err, w.name).message);
      } else {
        setError(isRateLimited(err) ? rateLimited(refused) : privyFailure(privyCode(err), errorText(err), refused, "wallet"));
      }
      setBusy(null);
    } finally {
      setBusyWallet("");
    }
  }

  function chooseWallet() {
    if (busy) return;
    // One wallet installed: no list to pick from.
    if (wallets.length === 1) void signInWithWallet(wallets[0]);
    else setWalletsOpen((o) => !o);
  }

  function changeEmail() {
    setStep("email");
    setCode("");
    setError(null);
  }

  const working = busy === "code" || busy === "finishing";

  if (step === "code") {
    return (
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (code.length === 6) {
            void submitCode(code);
            return;
          }
          // Nothing to send yet: say so, rather than a button that does nothing.
          setError(
            code.length === 0
              ? "Enter the six-digit code from the email."
              : `The code has six digits and you've entered ${code.length}. Check the email for the latest code.`,
          );
        }}
        className="space-y-4"
      >
        <p className="text-sm leading-relaxed text-foreground">
          We sent a 6-digit code to <span className="font-medium break-all">{email.trim()}</span>. It can take a minute to arrive; check spam too.
        </p>
        <div>
          <label htmlFor="sign-in-code" className="text-sm font-medium text-foreground">
            Code
          </label>
          <input
            id="sign-in-code"
            ref={codeInput}
            name="code"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]*"
            maxLength={6}
            autoFocus
            required
            disabled={working}
            value={code}
            onChange={(e) => {
              const digits = e.target.value.replace(/\D/g, "").slice(0, 6);
              setCode(digits);
              // The sixth digit is the submit: nothing left to press.
              if (digits.length === 6) void submitCode(digits);
            }}
            placeholder="000000"
            className="mt-1.5 h-12 w-full rounded-xl border border-border-strong bg-surface px-3.5 text-center font-mono text-xl tracking-[0.5em] text-foreground transition-[border-color,box-shadow] placeholder:text-muted/50 focus-visible:border-primary focus-visible:shadow-[0_0_0_3px_color-mix(in_srgb,var(--color-primary)_18%,transparent)] focus-visible:outline-none disabled:opacity-70"
          />
          <p className="mt-1.5 text-xs text-muted" aria-live="polite">
            {code.length} of 6 digits{code.length === 6 ? "" : ": the sixth one signs you in"}
          </p>
        </div>
        {error && <FormError>{error}</FormError>}
        <SubmitButton busy={working}>{busy === "finishing" ? "Signing you in…" : "Sign in"}</SubmitButton>
        <div className="flex items-center justify-between gap-3 text-xs">
          <button
            type="button"
            onClick={() => void requestCode()}
            disabled={cooldown > 0 || busy !== null}
            className="font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary disabled:cursor-not-allowed disabled:text-muted disabled:no-underline"
          >
            {cooldown > 0 ? `Send a new code in ${cooldown}s` : "Send a new code"}
          </button>
          <button type="button" onClick={changeEmail} disabled={working} className="font-medium text-muted hover:text-foreground">
            Use a different email
          </button>
        </div>
      </form>
    );
  }

  const phantomHere = typeof window !== "undefined" ? `https://phantom.app/ul/browse/${encodeURIComponent(window.location.href)}?ref=${encodeURIComponent(window.location.origin)}` : "https://phantom.app";
  const onPhone = typeof navigator !== "undefined" && /Android|iPhone|iPad/i.test(navigator.userAgent);

  return (
    <div>
      <form onSubmit={requestCode} className="space-y-4" noValidate>
        <AuthInput
          label="Email"
          name="email"
          type="email"
          inputMode="email"
          autoComplete="email"
          autoCapitalize="none"
          spellCheck={false}
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="you@company.com"
        />
        <SubmitButton busy={busy === "email" || !ready}>Continue with email</SubmitButton>
      </form>
      <p className="mt-2.5 text-xs leading-relaxed text-muted">We email you a 6-digit code. No password.</p>

      <div className="my-6 flex items-center gap-3 text-xs text-muted">
        <span className="h-px flex-1 bg-border" />
        or
        <span className="h-px flex-1 bg-border" />
      </div>

      <button
        type="button"
        onClick={chooseWallet}
        disabled={busy !== null || !ready}
        aria-expanded={wallets.length === 1 ? undefined : walletsOpen}
        className="flex h-11 w-full items-center justify-center gap-2.5 rounded-xl border border-border-strong bg-surface text-[0.95rem] font-medium text-foreground transition-[background-color,transform] hover:bg-primary-tint/50 active:scale-[0.99] disabled:cursor-wait disabled:opacity-70"
      >
        {busy === "wallet" || busy === "finishing" ? <Spinner size={16} /> : <IconWallet size={18} />}
        {busy === "wallet" && busyWallet ? `Waiting for ${busyWallet}…` : busy === "finishing" ? "Signing you in…" : "Continue with a Solana wallet"}
      </button>

      {walletsOpen && wallets.length !== 1 && busy === null && (
        <div className="mt-2.5">
          {wallets.length > 1 ? (
            <ul className="grid gap-2" aria-label="Solana wallets in this browser">
              {wallets.map((w) => (
                <li key={w.name}>
                  <button
                    type="button"
                    onClick={() => void signInWithWallet(w)}
                    className="flex w-full items-center gap-3 rounded-xl border border-border bg-surface px-3.5 py-2.5 text-left text-sm font-medium text-foreground transition-colors hover:border-border-strong hover:bg-primary-tint/40"
                  >
                    {/* eslint-disable-next-line @next/next/no-img-element -- a data: icon the wallet supplies */}
                    <img src={w.icon} alt="" width={24} height={24} className="h-6 w-6 rounded-md" />
                    <span className="flex-1">{w.name}</span>
                    <span className="text-xs font-normal text-muted">Connect</span>
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <div className="rounded-xl bg-primary-tint/50 px-3.5 py-3 text-sm leading-relaxed text-foreground">
              <p className="font-medium">No Solana wallet found in this browser.</p>
              <p className="mt-1 text-muted">
                Install{" "}
                <a href="https://phantom.app/download" target="_blank" rel="noreferrer" className="font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary">
                  Phantom
                </a>{" "}
                or{" "}
                <a href="https://solflare.com/download" target="_blank" rel="noreferrer" className="font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary">
                  Solflare
                </a>
                , then reload this page. Or use email above: Algebra makes you a wallet.
                {onPhone && (
                  <>
                    {" "}
                    On a phone?{" "}
                    <a href={phantomHere} className="font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary">
                      Open this page in the Phantom app
                    </a>
                    .
                  </>
                )}
              </p>
            </div>
          )}
        </div>
      )}

      {busy === "wallet" && busyWallet && (
        <p role="status" className="mt-2.5 text-xs leading-relaxed text-muted">
          Approve the request in {busyWallet}. You sign one message to prove the wallet is yours: it sends no funds and costs nothing.
        </p>
      )}

      {error && (
        <div className="mt-4">
          <FormError>{error}</FormError>
        </div>
      )}
    </div>
  );
}
