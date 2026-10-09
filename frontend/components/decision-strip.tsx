"use client";

import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";

type Step = {
  decision: "ALLOW" | "REQUIRE_APPROVAL" | "DENY";
  intent: string;
  reason: string;
};

const steps: Step[] = [
  { intent: "Token risk for a new mint, up to 0.01 USDC", decision: "ALLOW", reason: "PASS_OK" },
  { intent: "Arkham entity balances, 1.00 USDC", decision: "REQUIRE_APPROVAL", reason: "PASS_APPROVAL_REQUIRED" },
  { intent: "Any provider that isn't on this pass", decision: "DENY", reason: "PASS_MERCHANT_NOT_ALLOWED" },
];

const decisionStyle: Record<Step["decision"], string> = {
  ALLOW: "bg-primary-tint text-primary",
  REQUIRE_APPROVAL: "bg-accent-tint text-accent",
  DENY: "bg-danger-tint text-danger",
};

export function DecisionStrip() {
  const [i, setI] = useState(0);

  useEffect(() => {
    const id = setInterval(() => setI((n) => (n + 1) % steps.length), 3200);
    return () => clearInterval(id);
  }, []);

  const step = steps[i];

  return (
    <div className="w-full max-w-md rounded-2xl border border-border bg-surface p-5 shadow-[0_1px_2px_rgba(11,16,32,0.06),0_12px_28px_-16px_rgba(11,16,32,0.18)]">
      <div className="flex items-center gap-2 text-xs text-muted">
        <span className="font-mono">economic_intent</span>
        <span className="h-1 w-1 rounded-full bg-border-strong" />
        <span>checked against the Spend Pass</span>
      </div>

      <AnimatePresence mode="wait">
        <motion.div
          key={i}
          initial={{ opacity: 0, y: 6 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: -6 }}
          transition={{ duration: 0.32, ease: [0.16, 1, 0.3, 1] }}
          className="mt-3"
        >
          <p className="font-display text-[0.95rem] leading-snug text-foreground">
            &ldquo;{step.intent}&rdquo;
          </p>
          <div className="mt-3 flex items-center gap-2.5">
            <span
              className={`rounded-full px-2.5 py-1 font-mono text-xs font-medium ${decisionStyle[step.decision]}`}
            >
              {step.decision}
            </span>
            <span className="font-mono text-xs text-muted">{step.reason}</span>
          </div>
        </motion.div>
      </AnimatePresence>

      <div className="mt-4 flex gap-1.5">
        {steps.map((_, idx) => (
          <span
            key={idx}
            className={`h-1 flex-1 rounded-full transition-colors duration-500 ${idx === i ? "bg-primary" : "bg-border"}`}
          />
        ))}
      </div>
    </div>
  );
}
