"use client";

// Sign in with an emailed code: type an address, Algebra emails a six-digit
// code, and the code signs you in (making the account the first time). No
// password, no wallet. The sixth digit is the submit.

import { useEffect, useRef, useState } from "react";
import * as api from "@/lib/api-client";
import type { User } from "@/lib/types";
import { AuthInput, FormError, SubmitButton } from "./fields";

type Props = {
  /** An error to show on arrival, such as one an OAuth redirect came back with. */
  initialError?: string | null;
  onSignedIn: (user: User) => void;
};

const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** The server's "wait 20 seconds" in seconds, or null. */
function waitFrom(message: string): number | null {
  const m = /wait (\d+) seconds?/i.exec(message);
  return m ? Number(m[1]) : null;
}

export default function EmailCodeSignIn({ initialError, onSignedIn }: Props) {
  const [step, setStep] = useState<"email" | "code">("email");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState<null | "send" | "verify">(null);
  const [error, setError] = useState<string | null>(initialError ?? null);
  const [cooldown, setCooldown] = useState(0);
  const codeInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);

  // The input is disabled while a code is checked, which drops focus: put it back for the next try.
  useEffect(() => {
    if (step === "code" && busy === null) codeInput.current?.focus();
  }, [step, busy]);

  async function sendCode(e?: React.FormEvent) {
    e?.preventDefault();
    if (busy) return;
    const address = email.trim();
    if (!EMAIL_SHAPE.test(address)) {
      setError("Enter your email address, like you@company.com.");
      return;
    }
    setBusy("send");
    setError(null);
    try {
      const sent = await api.sendLoginCode(address);
      setCooldown(sent.resend_in);
      setStep("code");
      setCode("");
    } catch (err) {
      const message = err instanceof Error ? err.message : "We couldn't send the code. Try again in a moment.";
      const wait = err instanceof api.ApiError && err.status === 429 ? waitFrom(message) : null;
      if (wait !== null) {
        // Asked again at once: the code from a moment ago is still the one to enter.
        setCooldown(wait);
        setStep("code");
        setCode("");
      } else {
        setError(message);
      }
    } finally {
      setBusy(null);
    }
  }

  async function submitCode(value: string) {
    if (busy) return;
    setBusy("verify");
    setError(null);
    try {
      const { user } = await api.verifyLoginCode(email.trim(), value);
      onSignedIn(user);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Sign-in didn't complete. Try again.");
      setCode("");
      setBusy(null);
    }
  }

  if (step === "code") {
    return (
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (code.length === 6) void submitCode(code);
          else setError(code.length === 0 ? "Enter the six-digit code from the email." : `The code has six digits and you've entered ${code.length}.`);
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
            disabled={busy === "verify"}
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
        <SubmitButton busy={busy === "verify"}>{busy === "verify" ? "Signing you in…" : "Sign in"}</SubmitButton>
        <div className="flex items-center justify-between gap-3 text-xs">
          <button
            type="button"
            onClick={() => void sendCode()}
            disabled={cooldown > 0 || busy !== null}
            className="font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary disabled:cursor-not-allowed disabled:text-muted disabled:no-underline"
          >
            {cooldown > 0 ? `Send a new code in ${cooldown}s` : "Send a new code"}
          </button>
          <button
            type="button"
            onClick={() => {
              setStep("email");
              setCode("");
              setError(null);
            }}
            disabled={busy !== null}
            className="font-medium text-muted hover:text-foreground"
          >
            Use a different email
          </button>
        </div>
      </form>
    );
  }

  return (
    <form onSubmit={sendCode} className="space-y-4" noValidate>
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
      {error && <FormError>{error}</FormError>}
      <SubmitButton busy={busy === "send"}>Continue with email</SubmitButton>
      <p className="text-xs leading-relaxed text-muted">We email you a 6-digit code. No password, no wallet.</p>
    </form>
  );
}
