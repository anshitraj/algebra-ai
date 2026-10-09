"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Logo } from "@/components/logo";
import { IconCheck, IconShield } from "@/components/icons";

export function AuthShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid min-h-dvh lg:grid-cols-[minmax(0,1fr)_minmax(0,1.05fr)]">
      <div className="flex flex-col px-6 py-6 sm:px-10">
        <Link href="/" className="flex w-fit items-center gap-2.5 text-foreground">
          <Logo size={24} />
          <span className="font-display text-[1.02rem] font-semibold tracking-tight">Algebra</span>
        </Link>
        <main className="flex flex-1 items-center justify-center py-12">
          <div className="w-full max-w-[380px]">{children}</div>
        </main>
        <p className="text-xs text-muted">
          Agents never hold a key: Algebra pays from a wallet it controls, inside limits you set.{" "}
          <Link href="/#security" className="underline decoration-border-strong hover:text-foreground">
            How that works
          </Link>
        </p>
      </div>
      <aside className="auth-brand-panel relative hidden overflow-hidden border-l border-border lg:block">
        <Vignette />
      </aside>
    </div>
  );
}

const STEPS = [
  { label: "Found 3 providers on Pay.sh and Circle", detail: "Best quote: 0.003 USDC · Birdeye" },
  { label: "Checked the Spend Pass", detail: "Under your 0.05 USDC line · approved" },
  { label: "Paid in USDC on Solana", detail: "Result delivered · receipt signed" },
];

// A looping, honest miniature of the product: one request, the checks it
// passes, the result. No fake metrics.
function Vignette() {
  const reduce = useReducedMotion();
  const [tick, setTick] = useState(reduce ? STEPS.length : 0);

  useEffect(() => {
    if (reduce) return;
    const id = window.setInterval(() => setTick((t) => (t >= STEPS.length + 2 ? 0 : t + 1)), 1300);
    return () => window.clearInterval(id);
  }, [reduce]);

  return (
    <div className="flex h-full flex-col justify-between p-12 xl:p-16">
      <div className="max-w-md">
        <p className="font-display text-[2rem] leading-[1.1] font-semibold tracking-tight text-balance text-foreground xl:text-[2.4rem]">
          Your agent pays.
          <br />
          <span className="text-primary">You hold the limits.</span>
        </p>
        <p className="mt-4 max-w-sm text-[0.95rem] leading-relaxed text-muted">
          Every payment clears the Spend Pass you set, on the server, before any USDC moves. Anything above your line
          waits for your yes.
        </p>
      </div>

      <div className="relative mx-auto w-full max-w-md">
        <div className="rounded-2xl border border-border bg-surface p-5 shadow-[0_18px_50px_-24px_rgba(11,16,32,0.35)]">
          <div className="flex justify-end">
            <p className="max-w-[80%] rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-sm text-primary-tint">
              Token risk for this mint, at most 0.01 USDC
            </p>
          </div>
          <ol className="mt-5 space-y-3">
            {STEPS.map((s, i) => {
              const done = tick > i;
              const active = tick === i;
              return (
                <li key={s.label} className="flex items-start gap-3">
                  <span
                    className={`mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full border transition-colors duration-300 ${
                      done
                        ? "border-primary bg-primary text-primary-tint"
                        : active
                          ? "border-primary text-primary"
                          : "border-border-strong text-transparent"
                    }`}
                  >
                    {done ? <IconCheck size={12} strokeWidth={2.4} /> : active ? <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-primary" /> : null}
                  </span>
                  <div className={`transition-opacity duration-300 ${done || active ? "opacity-100" : "opacity-40"}`}>
                    <p className="text-sm font-medium text-foreground">{s.label}</p>
                    <AnimatePresence initial={false}>
                      {done && (
                        <motion.p
                          initial={{ opacity: 0, y: -4 }}
                          animate={{ opacity: 1, y: 0 }}
                          exit={{ opacity: 0 }}
                          transition={{ duration: 0.3, ease: [0.16, 1, 0.3, 1] }}
                          className="font-mono text-xs text-muted"
                        >
                          {s.detail}
                        </motion.p>
                      )}
                    </AnimatePresence>
                  </div>
                </li>
              );
            })}
          </ol>
        </div>
        <div className="mt-5 flex items-center gap-2 text-xs text-muted">
          <IconShield size={15} className="text-primary" />
          Policy runs server-side. No model can talk its way past it.
        </div>
      </div>
    </div>
  );
}
