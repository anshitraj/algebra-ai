"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { motion, useReducedMotion } from "motion/react";
import * as api from "@/lib/api-client";
import { firstName, useSession } from "@/lib/session";
import type { OnboardingAnswers } from "@/lib/types";
import { categoryLabel } from "@/lib/paysh";
import { Logo } from "@/components/logo";
import { IconArrowRight, IconCheck, Spinner } from "@/components/icons";

// What agents can pay for, in the catalogs' own categories. The answer is a
// starting point for the console, never a limit: limits live on Spend Passes.
const USES = ["finance", "ai_ml", "search", "data", "media", "maps", "translation", "messaging", "infrastructure"];

const EASE = [0.16, 1, 0.3, 1] as const;

/**
 * Onboarding is one screen: a name if we don't have one, what the agents
 * will pay for, then on to issuing the first Spend Pass. The account's
 * legacy shopping guardrails get their defaults; nothing here asks for an
 * address, a card or a wallet.
 */
export function OnboardingFlow() {
  const router = useRouter();
  const reduce = useReducedMotion();
  const { user, status, setUser } = useSession();
  const [name, setName] = useState("");
  const [uses, setUses] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const needsName = status === "authenticated" && !user?.name;

  function answers(): OnboardingAnswers {
    return {
      name: name.trim() || undefined,
      // Every paid call an agent makes through Algebra is a digital service.
      use_cases: ["digital_services"],
      priority: "best_value",
      household: "solo",
      dietary: [],
      preferred_merchants: [],
      guardrails: {
        currency: "INR",
        approval_threshold_minor_units: 100_000,
        max_per_day_minor_units: 500_000,
        max_per_purchase_minor_units: 500_000,
        blocked_categories: ["gift_cards"],
        international_requires_approval: true,
      },
    };
  }

  async function finish(next: string) {
    setBusy(true);
    setError(null);
    try {
      setUser(await api.completeOnboarding(answers()));
      router.replace(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't finish setting up. Try again.");
      setBusy(false);
    }
  }

  return (
    <div className="min-h-dvh bg-background">
      <header className="flex items-center justify-between px-6 py-5 sm:px-10">
        <span className="flex items-center gap-2.5 text-foreground">
          <Logo size={22} />
          <span className="font-display text-[1rem] font-semibold tracking-tight">Algebra</span>
        </span>
        <button type="button" onClick={() => finish("/console")} disabled={busy} className="text-sm text-muted hover:text-foreground disabled:opacity-50">
          Skip for now
        </button>
      </header>

      <motion.main
        initial={reduce ? { opacity: 0 } : { opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.45, ease: EASE }}
        className="mx-auto max-w-xl px-6 pt-8 pb-20"
      >
        <h1 className="font-display text-[2rem] leading-tight font-semibold tracking-tight text-balance text-foreground">
          {user?.name ? `Welcome, ${firstName(user)}.` : "Welcome to Algebra."}
        </h1>
        <p className="mt-3 text-[1.0125rem] leading-relaxed text-muted">
          Algebra lets your AI agents pay for APIs in USDC on Solana, inside limits you set. Agents get a Spend Pass, never a
          key, and every payment ends in a signed receipt.
        </p>

        {needsName && (
          <label className="mt-9 block">
            <span className="text-sm font-medium text-foreground">What should we call you?</span>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoComplete="name"
              maxLength={80}
              placeholder="Your name"
              className="mt-2 w-full rounded-xl border border-border-strong bg-surface px-3.5 py-2.5 text-[0.95rem] text-foreground placeholder:text-muted focus-visible:border-primary"
            />
          </label>
        )}

        <fieldset className="mt-9">
          <legend className="text-sm font-medium text-foreground">What will your agents pay for?</legend>
          <p className="mt-1 text-xs text-muted">Optional. It only decides what the console shows you first.</p>
          <div className="mt-3 flex flex-wrap gap-2">
            {USES.map((u) => {
              const on = uses.includes(u);
              return (
                <button
                  key={u}
                  type="button"
                  role="checkbox"
                  aria-checked={on}
                  onClick={() => setUses((s) => (s.includes(u) ? s.filter((x) => x !== u) : [...s, u]))}
                  className={`inline-flex h-9 items-center gap-1.5 rounded-full border px-3.5 text-sm transition-colors ${
                    on ? "border-primary bg-primary text-primary-tint" : "border-border-strong bg-surface text-foreground hover:border-foreground/35"
                  }`}
                >
                  {on && <IconCheck size={13} strokeWidth={2.4} />}
                  {categoryLabel(u)}
                </button>
              );
            })}
          </div>
        </fieldset>

        <ol className="mt-10 space-y-3 rounded-2xl border border-border bg-surface p-5 text-sm">
          {[
            ["Issue a Spend Pass", "A USDC budget, the most one call may cost, when to ask you, and which providers it may pay."],
            ["Connect your agent", "Over MCP or REST, with the pass's token."],
            ["Watch it work", "Every call, its price, the payment and the receipt, in Executions."],
          ].map(([t, b], i) => (
            <li key={t} className="flex gap-3">
              <span className="grid h-6 w-6 shrink-0 place-items-center rounded-full bg-primary-tint text-xs font-medium text-primary">{i + 1}</span>
              <span>
                <span className="block font-medium text-foreground">{t}</span>
                <span className="block text-muted">{b}</span>
              </span>
            </li>
          ))}
        </ol>

        {error && (
          <p role="alert" className="mt-6 rounded-xl bg-danger-tint px-4 py-3 text-sm text-danger">
            {error}
          </p>
        )}

        <button
          type="button"
          onClick={() => finish(uses.length ? `/console/passes?new=1&for=${encodeURIComponent(uses.join(","))}` : "/console/passes?new=1")}
          disabled={busy}
          className="mt-8 flex h-12 w-full items-center justify-center gap-2 rounded-xl bg-primary text-[0.95rem] font-medium text-primary-tint transition-opacity hover:opacity-95 disabled:opacity-70"
        >
          {busy ? <Spinner size={16} /> : null}
          Issue my first Spend Pass <IconArrowRight size={16} />
        </button>
      </motion.main>
    </div>
  );
}
