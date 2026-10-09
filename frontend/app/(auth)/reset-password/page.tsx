"use client";

import Link from "next/link";
import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { motion } from "motion/react";
import * as api from "@/lib/api-client";
import { FormError, PasswordInput, SubmitButton } from "@/components/auth/fields";

function ResetForm() {
  const router = useRouter();
  const token = useSearchParams().get("token") ?? "";
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(token ? null : "This reset link is missing its token. Request a new one.");

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setError("The two passwords don't match.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.resetPassword(token, password);
      router.replace("/login?reset=1");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't reset your password.");
      setBusy(false);
    }
  }

  return (
    <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.45, ease: [0.16, 1, 0.3, 1] }}>
      <h1 className="font-display text-[1.75rem] leading-tight font-semibold tracking-tight text-foreground">
        Choose a new password
      </h1>
      <p className="mt-2 text-sm text-muted">This signs you out everywhere else.</p>
      <form onSubmit={handleSubmit} className="mt-8 space-y-4">
        <PasswordInput
          label="New password"
          autoComplete="new-password"
          required
          minLength={8}
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="At least 8 characters"
        />
        <PasswordInput
          label="Confirm password"
          autoComplete="new-password"
          required
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {error && <FormError>{error}</FormError>}
        <SubmitButton busy={busy}>Set password</SubmitButton>
      </form>
      <p className="mt-6 text-sm text-muted">
        Link expired?{" "}
        <Link href="/forgot-password" className="font-medium text-primary underline decoration-primary/30 underline-offset-4">
          Send a new one
        </Link>
      </p>
    </motion.div>
  );
}

export default function ResetPasswordPage() {
  return (
    <Suspense>
      <ResetForm />
    </Suspense>
  );
}
