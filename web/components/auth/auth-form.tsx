"use client";

import Link from "next/link";
import dynamic from "next/dynamic";
import { useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { motion } from "motion/react";
import * as api from "@/lib/api-client";
import type { AuthProviders, User } from "@/lib/types";
import { GitHubMark, GoogleMark, IconReceipt, IconShield, IconStore, Spinner } from "@/components/icons";
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

function safeNext(next: string | null): string | null {
  if (!next || !next.startsWith("/") || next.startsWith("//")) return null;
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
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(params.get("error"));
  // Someone sent here from a protected page (?next=) already has an account.
  const [entry, setEntry] = useState<Entry>(params.get("mode") === "production" || next ? "production" : "demo");
  const [demoBusy, setDemoBusy] = useState(false);

  useEffect(() => {
    api.getAuthProviders().then(setProviders).catch(() => setProviders({ password: true, google: false, github: false }));
    // Already signed in? Skip the form — unless it's a demo account, which
    // comes here to create a real one.
    api
      .getSession()
      .then(({ user }) => {
        if (user && user.mode !== "demo") router.replace(destinationFor(user, next));
      })
      .catch(() => {});
  }, [router, next]);

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
  const anyOAuth = providers?.google || providers?.github;
  const passwordOn = providers?.password !== false;
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
        {isSignup ? "Create your account" : showDemo ? "Try Algebra with simulated USDC" : "Welcome back"}
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
              Try the demo
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
          <div className={`${showChooser ? "mt-6" : "mt-8"} grid gap-2.5`}>
            {providers?.privy && providers.privy_app_id && (
              <>
                <PrivySignIn
                  appId={providers.privy_app_id}
                  label={isSignup ? "Sign up with a wallet or email" : "Continue with a wallet or email"}
                  onSignedIn={(user) => router.replace(destinationFor(user, next))}
                  onError={(message) => setError(message || null)}
                />
                <p className="text-xs leading-relaxed text-muted">
                  Phantom, Solflare or any Solana wallet, or an email code. No wallet yet? Privy makes you one; Algebra never sees its key.
                </p>
              </>
            )}
            <OAuthButton provider="github" enabled={!!providers?.github} loading={!providers} next={next}>
              <GitHubMark size={18} /> Continue with GitHub
            </OAuthButton>
            <OAuthButton provider="google" enabled={!!providers?.google} loading={!providers} next={next}>
              <GoogleMark size={18} /> Continue with Google
            </OAuthButton>
            {providers && !anyOAuth && (
              <p className="text-xs leading-relaxed text-muted">
                Google and GitHub sign-in appear here once their OAuth client IDs are set on the API server.
              </p>
            )}
            {!passwordOn && error && <FormError>{error}</FormError>}
          </div>

          {passwordOn && (
            <>
              <div className="my-7 flex items-center gap-3 text-xs text-muted">
                <span className="h-px flex-1 bg-border" />
                or with email
                <span className="h-px flex-1 bg-border" />
              </div>

              <form onSubmit={handleSubmit} className="space-y-4" noValidate={false}>
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
            </>
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
    { value: "demo", label: "Demo", hint: "No signup · simulated USDC" },
    { value: "production", label: "Production", hint: "Your real account" },
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
    { icon: <IconStore size={16} />, text: "Browse 1,600+ paid APIs from Pay.sh, Circle's Agent Marketplace, PayAI and Coinbase's Bazaar, read live." },
    { icon: <IconShield size={16} />, text: "Issue a Spend Pass and let an agent pay the sandbox provider: limits, approvals and the coordinator run for real, the USDC is simulated." },
    { icon: <IconReceipt size={16} />, text: "Every payment ends in a signed receipt you can verify against Algebra's published keys." },
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
        {busy ? "Setting up your demo…" : "Start the demo"}
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

function OAuthButton({
  provider,
  enabled,
  loading,
  next,
  children,
}: {
  provider: "google" | "github";
  enabled: boolean;
  loading: boolean;
  next: string | null;
  children: React.ReactNode;
}) {
  const className =
    "flex h-11 w-full items-center justify-center gap-2.5 rounded-xl border border-border-strong bg-surface text-[0.95rem] font-medium text-foreground transition-[background-color,transform] hover:bg-primary-tint/70 active:scale-[0.99]";
  if (!enabled) {
    return (
      <button
        type="button"
        disabled
        title={loading ? undefined : `${provider === "google" ? "Google" : "GitHub"} sign-in isn't configured on this server`}
        className={`${className} cursor-not-allowed opacity-45 hover:bg-surface`}
      >
        {children}
      </button>
    );
  }
  // A full navigation, not fetch: the OAuth dance is redirects all the way.
  return (
    <a href={api.oauthStartURL(provider, next)} className={className}>
      {children}
    </a>
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
