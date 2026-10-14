"use client";

import Link from "next/link";
import dynamic from "next/dynamic";
import { useCallback, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { motion } from "motion/react";
import * as api from "@/lib/api-client";
import type { AuthProviders, User } from "@/lib/types";
import { IconReceipt, IconShield, IconStore, Spinner } from "@/components/icons";
import { AuthInput, FormError, PasswordInput, SubmitButton } from "./fields";

// Privy's SDK is large and needs the browser: loaded only when this server
// offers Privy sign-in.
const PrivySignIn = dynamic(() => import("./privy-sign-in"), {
  ssr: false,
  loading: () => (
    <div className="flex h-11 w-full items-center justify-center gap-2.5 rounded-xl bg-primary/70 text-[0.95rem] font-medium text-primary-tint">
      <Spinner size={16} />
    </div>
  ),
});

type Mode = "login" | "signup";
/** Which way in: a one-click demo account, or a real (production) account. */
type Entry = "demo" | "production";

/**
 * A path on this site, or null. Browsers read a backslash as a slash and drop
 * tabs and newlines, so "/\host" and "/\t/host" both become "//host", another
 * site: only one leading slash followed by a plain character is accepted, and
 * no backslash or control character anywhere.
 */
export function safeNext(next: string | null): string | null {
  if (!next || !/^\/(?![/\\])/.test(next) || /[\\\u0000-\u001f\u007f]/.test(next)) return null;
  return next;
}

export function destinationFor(user: User, next: string | null) {
  if (!user.onboarded) return "/onboarding";
  return safeNext(next) ?? "/console";
}

export function AuthForm({ mode }: { mode: Mode }) {
  const router = useRouter();
  const params = useSearchParams();
  const next = safeNext(params.get("next"));
  const [providers, setProviders] = useState<AuthProviders | null>(null);
  // Why the sign-in options never loaded, when the API couldn't be asked.
  const [providersDown, setProvidersDown] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(params.get("error"));
  // Someone sent here from a protected page (?next=) already has an account.
  const [entry, setEntry] = useState<Entry>(params.get("mode") === "production" || next ? "production" : "demo");
  const [demoBusy, setDemoBusy] = useState(false);

  const fetchProviders = useCallback(() => {
    api
      .getAuthProviders()
      .then(setProviders)
      .catch((err: unknown) => {
        // An API too old to have the endpoint still takes a password. One that
        // can't be reached takes nothing: a form there only fails later.
        if (err instanceof api.ApiError && err.status === 404) setProviders({ password: true, google: false, github: false });
        else setProvidersDown(err instanceof Error ? err.message : "Can't reach Algebra.");
      });
  }, []);

  useEffect(() => {
    fetchProviders();
    // Already signed in? Skip the form — unless it's a demo account, which
    // comes here to create a real one.
    api
      .getSession()
      .then(({ user }) => {
        if (user && user.mode !== "demo") router.replace(destinationFor(user, next));
      })
      .catch(() => {});
  }, [router, next, fetchProviders]);

  function retryProviders() {
    setProvidersDown(null);
    fetchProviders();
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { user } = mode === "signup" ? await api.signUp(name, email, password) : await api.signIn(email, password);
      router.replace(destinationFor(user, next));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong. Try again.");
      setBusy(false);
    }
  }

  async function handleDemo() {
    setDemoBusy(true);
    setError(null);
    try {
      await api.startDemo();
      router.replace("/console");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't start the demo. Try again.");
      setDemoBusy(false);
    }
  }

  const isSignup = mode === "signup";
  const passwordOn = providers?.password !== false;
  // Privy carries real sign-ins (an email code or a Solana wallet) when the server has it.
  const privyAppId = providers?.privy ? (providers.privy_app_id ?? "") : "";
  // The chooser shows on sign-in only, and only when this server offers the demo.
  const showChooser = !isSignup && providers?.demo === true;
  const showDemo = showChooser && entry === "demo";

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.45, ease: [0.16, 1, 0.3, 1] }}
    >
      <h1 className="font-display text-[1.75rem] leading-tight font-semibold tracking-tight text-foreground">
        {isSignup ? "Create your account" : showDemo ? "Try Algebra with play money" : "Welcome back"}
      </h1>
      <p className="mt-2 text-sm text-muted">
        {isSignup ? "Already have an account? " : "New to Algebra? "}
        <Link
          href={`${isSignup ? "/login" : "/signup"}${next ? `?next=${encodeURIComponent(next)}` : ""}`}
          className="font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary"
        >
          {isSignup ? "Sign in" : "Create an account"}
        </Link>
        {isSignup && providers?.demo && (
          <>
            {" · Just looking? "}
            <Link
              href="/login?mode=demo"
              className="font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary"
            >
              Try it first
            </Link>
          </>
        )}
      </p>

      {showChooser && (
        <EntryChooser
          entry={entry}
          onChange={(e) => {
            setEntry(e);
            setError(null);
          }}
        />
      )}

      {showDemo ? (
        <DemoPanel busy={demoBusy} error={error} onStart={handleDemo} />
      ) : (
        <>
          {providersDown ? (
            <div className={`${showChooser ? "mt-6" : "mt-8"} grid gap-3`}>
              <FormError>The sign-in options couldn&apos;t load. {providersDown}</FormError>
              <button
                type="button"
                onClick={retryProviders}
                className="flex h-11 w-full items-center justify-center rounded-xl border border-border-strong bg-surface text-[0.95rem] font-medium text-foreground transition-[background-color,transform] hover:bg-primary-tint/50 active:scale-[0.99]"
              >
                Try again
              </button>
            </div>
          ) : providers === null ? (
            // The server hasn't said yet which ways in it offers: a placeholder
            // rather than a form that would change shape.
            <div className={`${showChooser ? "mt-6" : "mt-8"} grid gap-3`} aria-hidden="true">
              <div className="h-11 animate-pulse rounded-xl bg-primary-tint/60" />
              <div className="h-11 animate-pulse rounded-xl bg-primary-tint/60" />
            </div>
          ) : privyAppId ? (
            // Real accounts: an email code or a Solana wallet, no password.
            <div className={showChooser ? "mt-6" : "mt-8"}>
              <PrivySignIn
                appId={privyAppId}
                initialError={error}
                onSignedIn={(user) => router.replace(destinationFor(user, next))}
              />
              <p className="mt-6 text-xs leading-relaxed text-muted">
                {isSignup ? "Signing in later with the same email or wallet opens this account. " : "New here? Your first sign-in makes your account. "}
                Algebra never sees a wallet&apos;s key.
              </p>
            </div>
          ) : !passwordOn ? (
            <div className={showChooser ? "mt-6" : "mt-8"}>
              <FormError>{error ?? "No sign-in method is turned on for this server."}</FormError>
            </div>
          ) : (
            // No Privy on this server (local development, say): email and password.
            <form onSubmit={handleSubmit} className={`${showChooser ? "mt-6" : "mt-8"} space-y-4`} noValidate={false}>
              {isSignup && (
                <AuthInput
                  label="Full name"
                  name="name"
                  autoComplete="name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Ada Lovelace"
                  maxLength={80}
                />
              )}
              <AuthInput
                label="Email"
                name="email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@company.com"
                autoFocus={!isSignup}
              />
              <PasswordInput
                label="Password"
                name="password"
                autoComplete={isSignup ? "new-password" : "current-password"}
                required
                minLength={isSignup ? 8 : undefined}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={isSignup ? "At least 8 characters" : "Your password"}
                trailing={
                  !isSignup ? (
                    <Link href="/forgot-password" className="text-xs font-medium text-muted hover:text-foreground">
                      Forgot password?
                    </Link>
                  ) : undefined
                }
              />
              {isSignup && password.length > 0 && <StrengthHint password={password} />}
              {!isSignup && params.get("reset") === "1" && !error && (
                <p role="status" className="rounded-xl bg-primary-tint px-3.5 py-2.5 text-sm text-primary">
                  Password updated. Sign in with your new password.
                </p>
              )}
              {error && <FormError>{error}</FormError>}
              <SubmitButton busy={busy}>{isSignup ? "Create account" : "Sign in"}</SubmitButton>
            </form>
          )}

          {isSignup && (
            <p className="mt-6 text-xs leading-relaxed text-muted">
              By creating an account you agree to the{" "}
              <Link href="/terms" className="underline hover:text-foreground">
                Terms
              </Link>{" "}
              and{" "}
              <Link href="/privacy" className="underline hover:text-foreground">
                Privacy Policy
              </Link>
              . Algebra acts only within the guardrails you set; anything above them waits for your approval.
            </p>
          )}
        </>
      )}
    </motion.div>
  );
}

function EntryChooser({ entry, onChange }: { entry: Entry; onChange: (e: Entry) => void }) {
  const options: { value: Entry; label: string; hint: string }[] = [
    { value: "demo", label: "Try it first", hint: "No sign-up · play money" },
    { value: "production", label: "Sign in", hint: "Your own account" },
  ];
  return (
    <div role="tablist" aria-label="How do you want to continue?" className="mt-7 grid grid-cols-2 gap-1 rounded-2xl bg-primary-tint/50 p-1">
      {options.map((o) => {
        const active = entry === o.value;
        return (
          <button
            key={o.value}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onChange(o.value)}
            className={`rounded-xl px-3 py-2.5 text-left transition-[background-color,box-shadow] ${
              active ? "bg-surface shadow-[0_1px_3px_rgb(0_0_0/0.08)]" : "hover:bg-surface/60"
            }`}
          >
            <span className={`block text-sm font-semibold ${active ? "text-foreground" : "text-muted"}`}>{o.label}</span>
            <span className="block text-xs text-muted">{o.hint}</span>
          </button>
        );
      })}
    </div>
  );
}

function DemoPanel({ busy, error, onStart }: { busy: boolean; error: string | null; onStart: () => void }) {
  const points = [
    { icon: <IconStore size={16} />, text: "Browse 1,600+ paid APIs from the biggest catalogs, with live prices." },
    { icon: <IconShield size={16} />, text: "Set a spending limit and let an agent pay for data. The limits and approvals work for real; the money is play money." },
    { icon: <IconReceipt size={16} />, text: "Every payment comes with a signed receipt you can check yourself." },
  ];
  return (
    <div className="mt-6">
      <ul className="space-y-3.5">
        {points.map((p, i) => (
          <li key={i} className="flex gap-3 text-sm leading-relaxed text-foreground">
            <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-primary-tint text-primary">{p.icon}</span>
            <span>{p.text}</span>
          </li>
        ))}
      </ul>
      {error && (
        <div className="mt-5">
          <FormError>{error}</FormError>
        </div>
      )}
      <button
        type="button"
        onClick={onStart}
        disabled={busy}
        className="mt-7 flex h-11 w-full items-center justify-center gap-2 rounded-xl bg-primary text-[0.95rem] font-medium text-primary-tint shadow-[0_6px_16px_-8px_color-mix(in_srgb,var(--color-primary)_80%,transparent)] transition-[transform,opacity] hover:opacity-95 active:scale-[0.99] disabled:cursor-wait disabled:opacity-70"
      >
        {busy && <Spinner size={16} />}
        {busy ? "Setting things up…" : "Try it now"}
      </button>
      <p className="mt-3 text-center text-xs text-muted">
        A fresh demo account just for you. No email, no password; it ends after 3 days. By starting it you agree to the{" "}
        <Link href="/terms" className="underline hover:text-foreground">
          Terms
        </Link>
        .
      </p>
    </div>
  );
}

function StrengthHint({ password }: { password: string }) {
  const len = password.length;
  const varied = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(password)).length;
  const score = len < 8 ? 0 : len >= 14 || (len >= 10 && varied >= 3) ? 2 : 1;
  const label = ["Too short — 8 characters minimum", "Good", "Strong"][score];
  return (
    <div className="flex items-center gap-2.5" aria-live="polite">
      <div className="flex flex-1 gap-1">
        {[0, 1, 2].map((i) => (
          <span
            key={i}
            className={`h-1 flex-1 rounded-full transition-colors duration-300 ${
              i <= score && len > 0 ? (score === 0 ? "bg-danger" : score === 1 ? "bg-accent" : "bg-primary") : "bg-border"
            }`}
          />
        ))}
      </div>
      <span className="text-xs text-muted">{label}</span>
    </div>
  );
}
