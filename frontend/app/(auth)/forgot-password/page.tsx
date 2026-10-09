"use client";

import Link from "next/link";
import { useState } from "react";
import { motion } from "motion/react";
import * as api from "@/lib/api-client";
import { AuthInput, FormError, SubmitButton } from "@/components/auth/fields";
import { IconArrowLeft, IconMail } from "@/components/icons";

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.forgotPassword(email);
      setSent(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't send the email. Try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.45, ease: [0.16, 1, 0.3, 1] }}>
      <Link href="/login" className="inline-flex items-center gap-1.5 text-sm text-muted hover:text-foreground">
        <IconArrowLeft size={15} /> Back to sign in
      </Link>
      {sent ? (
        <div className="mt-8">
          <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-primary-tint text-primary">
            <IconMail size={20} />
          </span>
          <h1 className="font-display mt-5 text-[1.75rem] leading-tight font-semibold tracking-tight text-foreground">
            Check your inbox
          </h1>
          <p className="mt-3 text-sm leading-relaxed text-muted">
            If <span className="font-medium text-foreground">{email}</span> has an Algebra account, a reset link is on its
            way. It works once and expires in 30 minutes.
          </p>
          <button
            type="button"
            onClick={() => setSent(false)}
            className="mt-6 text-sm font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary"
          >
            Use a different email
          </button>
        </div>
      ) : (
        <>
          <h1 className="font-display mt-8 text-[1.75rem] leading-tight font-semibold tracking-tight text-foreground">
            Reset your password
          </h1>
          <p className="mt-2 text-sm text-muted">We&apos;ll email you a link to set a new one.</p>
          <form onSubmit={handleSubmit} className="mt-8 space-y-4">
            <AuthInput
              label="Email"
              type="email"
              autoComplete="email"
              required
              autoFocus
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@company.com"
            />
            {error && <FormError>{error}</FormError>}
            <SubmitButton busy={busy}>Send reset link</SubmitButton>
          </form>
        </>
      )}
    </motion.div>
  );
}
