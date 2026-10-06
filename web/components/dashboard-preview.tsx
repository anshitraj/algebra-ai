"use client";

import { useEffect, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Logo } from "./logo";
import { ProviderMark } from "./provider-mark";
import { IconCheck } from "./icons";

// An illustrative run of the console: the agent asks for a token risk score,
// three catalog providers answer with a price, the Spend Pass clears the best
// one, the payment settles, the receipt is signed. Prices are examples (the
// panel says so); the stages and the audit events are the ones Algebra's
// coordinator really records.

const STAGES = ["Agent", "Discovery", "Policy", "Approval", "Payment", "Receipt"] as const;

type Tick = { stage: number; event?: { text: string; detail: string } };

const TICKS: Tick[] = [
  { stage: 0, event: { text: "intent.created", detail: "solana.token-risk · up to 0.01 USDC" } },
  { stage: 1 },
  { stage: 1 },
  { stage: 2, event: { text: "authority.evaluated", detail: "ALLOW · PASS_OK" } },
  { stage: 3 },
  { stage: 4, event: { text: "reservation.acquired", detail: "one live attempt · 0.003 USDC held" } },
  { stage: 4, event: { text: "execution.started", detail: "Birdeye Data" } },
  { stage: 4, event: { text: "payment.authorized", detail: "x402 · USDC on Solana" } },
  { stage: 4, event: { text: "payment.confirmed", detail: "settled, proven from chain state" } },
  { stage: 5, event: { text: "intent.committed", detail: "paid once · result delivered" } },
  { stage: 5, event: { text: "receipt.signed", detail: "verifies against the published keys" } },
];

const QUOTES = [
  { id: "birdeye", name: "Birdeye Data", what: "token security · GET", price: "0.003 USDC", best: true },
  { id: "vybe", name: "Vybe Solana Analytics", what: "token details · GET", price: "0.012 USDC" },
  { id: "nansen", name: "Nansen API", what: "token screener · POST", price: "0.060 USDC" },
];

const NAV = ["Overview", "Providers", "Spend passes", "Executions", "Approvals", "Activity"];

const TICK_MS = 900;
const HOLD_MS = 2600;
const VISIBLE_LOG_LINES = 5;
const EASE = [0.16, 1, 0.3, 1] as const;

export function DashboardPreview() {
  const [step, setStep] = useState(0);
  const reduce = useReducedMotion();

  useEffect(() => {
    let i = 0;
    let timeout: ReturnType<typeof setTimeout>;
    const advance = () => {
      i = i >= TICKS.length ? 0 : i + 1;
      setStep(i);
      timeout = setTimeout(advance, i === TICKS.length ? HOLD_MS : TICK_MS);
    };
    timeout = setTimeout(advance, TICK_MS);
    return () => clearTimeout(timeout);
  }, []);

  const done = step >= TICKS.length;
  const stage = step === 0 ? 0 : TICKS[Math.min(step, TICKS.length) - 1].stage;
  const visibleEvents = TICKS.slice(0, step)
    .flatMap((t) => (t.event ? [t.event] : []))
    .slice(-VISIBLE_LOG_LINES);
  const searching = step === 2;
  const quotesIn = step >= 3;
  const picked = step >= 5;

  return (
    <div className="brand-frame overflow-hidden rounded-2xl">
      <div className="flex items-center gap-3 border-b border-border px-4 py-3">
        <Logo size={16} className="text-primary" />
        <span className="rounded-md bg-background px-3 py-1 font-mono text-xs text-muted">app.algebra/console/executions</span>
        <span className="ml-auto text-[0.6875rem] text-muted">Illustrative prices</span>
      </div>

      <div className="flex flex-col md:flex-row">
        <div className="flex shrink-0 gap-1 overflow-x-auto border-b border-border px-3 py-2 md:w-40 md:flex-col md:gap-0.5 md:overflow-visible md:border-r md:border-b-0 md:px-3 md:py-4">
          {NAV.map((label) => (
            <span
              key={label}
              className={`shrink-0 rounded-lg px-2.5 py-1.5 text-xs whitespace-nowrap ${label === "Executions" ? "bg-primary-tint font-medium text-primary" : "text-muted"}`}
            >
              {label}
            </span>
          ))}
        </div>

        <div className="min-w-0 flex-1 p-5 md:p-6">
          <div className="flex items-center justify-between gap-3">
            <p className="font-display text-sm font-semibold text-foreground">Token risk for a new mint, up to 0.01 USDC</p>
            <span
              className={`shrink-0 rounded-full px-2.5 py-1 font-mono text-[0.6875rem] font-medium transition-colors duration-300 ${
                done ? "bg-primary-tint text-primary" : "bg-accent-tint text-accent"
              }`}
            >
              {done ? "COMMITTED" : STAGES[stage].toUpperCase()}
            </span>
          </div>

          <ol className="mt-4 flex items-center gap-1" aria-hidden="true">
            {STAGES.map((s, i) => (
              <li key={s} className="flex flex-1 items-center gap-1 last:flex-none">
                <span
                  className={`h-1.5 w-1.5 shrink-0 rounded-full transition-colors duration-300 ${
                    i < stage || done ? "bg-primary" : i === stage ? "bg-accent" : "bg-border-strong"
                  }`}
                />
                {i < STAGES.length - 1 && (
                  <span className={`h-px flex-1 transition-colors duration-300 ${i < stage || done ? "bg-primary" : "bg-border"}`} />
                )}
              </li>
            ))}
          </ol>

          <div className="mt-5 grid gap-4 md:grid-cols-[1.25fr_1fr]">
            {/* Quotes: the providers answering, then the pick. */}
            <div className="rounded-xl border border-border bg-background/60">
              <p className="flex items-center gap-2 border-b border-border px-3.5 py-2 text-[0.6875rem] text-muted">
                Quotes from Pay.sh and Circle
                {searching && (
                  <span className="flex items-center gap-1">
                    {QUOTES.map((q, i) => (
                      <motion.span
                        key={q.id}
                        animate={reduce ? undefined : { y: [0, -2, 0] }}
                        transition={{ duration: 0.8, repeat: Infinity, delay: i * 0.15 }}
                      >
                        <ProviderMark name={q.name} size={14} />
                      </motion.span>
                    ))}
                    <span className="ml-1">asking each for its price…</span>
                  </span>
                )}
              </p>
              <ul className="h-[156px] divide-y divide-border">
                <AnimatePresence initial={false}>
                  {quotesIn &&
                    QUOTES.map((q, i) => {
                      const chosen = picked && q.best;
                      return (
                        <motion.li
                          key={q.id}
                          initial={{ opacity: 0, y: reduce ? 0 : 6 }}
                          animate={{ opacity: picked && !q.best ? 0.55 : 1, y: 0 }}
                          exit={{ opacity: 0 }}
                          transition={{ duration: 0.35, delay: i * 0.12, ease: EASE }}
                          className={`flex items-center gap-3 px-3.5 py-2.5 transition-colors duration-300 ${chosen ? "bg-primary-tint/70" : ""}`}
                        >
                          <ProviderMark name={q.name} size={30} />
                          <div className="min-w-0 flex-1">
                            <p className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                              {q.name}
                              {chosen && (
                                <span className="inline-flex items-center gap-0.5 rounded-full bg-primary px-1.5 py-px text-[0.625rem] font-medium text-primary-tint">
                                  <IconCheck size={9} strokeWidth={3} /> Best price
                                </span>
                              )}
                            </p>
                            <p className="truncate text-[0.6875rem] text-muted">{q.what}</p>
                          </div>
                          <div className="shrink-0 text-right">
                            <p className="font-mono text-xs text-foreground tabular-nums">{q.price}</p>
                            <p className="text-[0.625rem] text-muted">quoted</p>
                          </div>
                        </motion.li>
                      );
                    })}
                </AnimatePresence>
              </ul>
            </div>

            {/* The audit trail underneath it, as recorded. */}
            <div className="relative h-[190px] rounded-xl bg-background px-3.5 py-2.5">
              <p className="text-[0.6875rem] text-muted">Audit trail</p>
              <ol className="mt-2 h-[150px] space-y-1.5 overflow-hidden">
                {visibleEvents.map((ev) => (
                  <motion.li
                    key={ev.text}
                    initial={{ opacity: 0, x: reduce ? 0 : -4 }}
                    animate={{ opacity: 1, x: 0 }}
                    transition={{ duration: 0.25 }}
                    className="font-mono text-[0.6875rem] leading-relaxed"
                  >
                    <span className="text-foreground">{ev.text}</span>
                    <span className="text-muted"> · {ev.detail}</span>
                  </motion.li>
                ))}
              </ol>
              <div className="pointer-events-none absolute inset-x-0 bottom-0 h-6 rounded-b-xl bg-gradient-to-t from-background to-transparent" />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
