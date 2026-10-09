"use client";

import { useState } from "react";
import { motion } from "motion/react";
import type { AskedQuestion } from "@/lib/agent/events";
import { IconArrowRight } from "@/components/icons";

const EASE = [0.16, 1, 0.3, 1] as const;

/**
 * The agent's clarifying questions (ask_user) as tappable answers. One
 * question sends on tap; several collect a pick each and send together, so
 * a vague request becomes a precise one without the user typing. Typing an
 * answer in the composer instead works too — this card just goes inactive.
 */
export function QuestionCard({
  questions,
  answered,
  active,
  onAnswer,
}: {
  questions: AskedQuestion[];
  /** The picks already sent, one per question ("" = skipped). */
  answered?: string[];
  /** Only the latest idle turn takes answers. */
  active: boolean;
  onAnswer: (picks: string[]) => void;
}) {
  const [picks, setPicks] = useState<string[]>(() => questions.map(() => ""));
  const shown = answered ?? picks;
  const live = active && !answered;
  const single = questions.length === 1;

  function pick(qi: number, option: string) {
    if (!live) return;
    if (single) {
      onAnswer([option]);
      return;
    }
    setPicks((p) => p.map((v, i) => (i === qi ? (v === option ? "" : option) : v)));
  }

  let chip = 0;
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: EASE }}
      className="space-y-5"
    >
      {questions.map((q, qi) => (
        <div key={qi} role="group" aria-label={q.question}>
          <p className="text-[0.975rem] leading-relaxed text-foreground">{q.question}</p>
          <div className="mt-2.5 flex flex-wrap gap-2">
            {q.options.map((o) => {
              const chosen = shown[qi] === o;
              const delay = 0.05 + chip++ * 0.035;
              return (
                <motion.button
                  key={o}
                  type="button"
                  onClick={() => pick(qi, o)}
                  disabled={!live}
                  aria-pressed={chosen}
                  initial={{ opacity: 0, y: 6 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={{ duration: 0.3, delay, ease: EASE }}
                  whileTap={live ? { scale: 0.97 } : undefined}
                  className={`rounded-full border px-3.5 py-1.5 text-sm transition-[background-color,border-color,color,opacity] ${
                    chosen
                      ? "border-primary bg-primary text-primary-tint"
                      : live
                        ? "border-border-strong bg-surface text-foreground hover:border-primary/60 hover:bg-primary-tint/60"
                        : "border-border bg-transparent text-muted opacity-60"
                  } disabled:cursor-default`}
                >
                  {o}
                </motion.button>
              );
            })}
          </div>
        </div>
      ))}

      {live && (
        <div className="flex items-center gap-3">
          {!single && (
            <button
              type="button"
              disabled={picks.every((p) => !p)}
              onClick={() => onAnswer(picks)}
              className="inline-flex h-9 items-center gap-1.5 rounded-xl bg-primary px-3.5 text-sm font-medium text-primary-tint transition-[opacity,transform] active:scale-[0.98] disabled:opacity-35"
            >
              Continue <IconArrowRight size={14} strokeWidth={2.2} />
            </button>
          )}
          <p className="text-xs text-muted">Or type your own answer below.</p>
        </div>
      )}
    </motion.div>
  );
}

/** What the user's picks read as in the chat (and to the agent). */
export function answerText(picks: string[]): string {
  return picks.filter(Boolean).join(" · ");
}
