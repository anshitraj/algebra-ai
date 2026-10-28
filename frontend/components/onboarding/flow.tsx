"use client";

import Link from "next/link";
import { useRef, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import * as api from "@/lib/api-client";
import { firstName, useSession } from "@/lib/session";
import type { OnboardingAnswers } from "@/lib/types";
import { categoryLabel } from "@/lib/paysh";
import { saveNetwork } from "@/lib/network";
import { Logo } from "@/components/logo";
import {
  IconArrowLeft,
  IconArrowRight,
  IconCheck,
  Spinner,
} from "@/components/icons";
import styles from "./onboarding.module.css";

const USES = [
  { id: "ai_ml", symbol: "✦", hint: "Models & inference" },
  { id: "search", symbol: "⌕", hint: "Answers & discovery" },
  { id: "data", symbol: "▥", hint: "Insights & enrichment" },
  { id: "finance", symbol: "↗", hint: "Markets & onchain data" },
  { id: "media", symbol: "◈", hint: "Images, audio & video" },
  { id: "maps", symbol: "◎", hint: "Places & navigation" },
  { id: "translation", symbol: "文", hint: "Language & localization" },
  { id: "messaging", symbol: "▤", hint: "Messages & notifications" },
  { id: "infrastructure", symbol: "⌘", hint: "Tools & compute" },
];
const STEPS = ["Welcome", "Your agents", "How it works"];
const EASE = [0.16, 1, 0.3, 1] as const;
// The first question, asked on Testnet (test credits, no wallet): Algebra's devnet price sellers answer it.
const FIRST_QUESTION = "What does SOL cost right now? Use the best provider";

export function OnboardingFlow() {
  const reduce = useReducedMotion();
  const { user, status, setUser } = useSession();
  const heading = useRef<HTMLHeadingElement>(null);
  const submitting = useRef(false);
  const [step, setStep] = useState(0);
  const [direction, setDirection] = useState(1);
  const [name, setName] = useState("");
  const [uses, setUses] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [destination, setDestination] = useState(`/console/agent?prompt=${encodeURIComponent(FIRST_QUESTION)}`);
  const needsName = !user?.name;

  function go(next: number) {
    if (submitting.current) return;
    setDirection(next > step ? 1 : -1);
    setStep(next);
    setError(null);
    window.scrollTo({ top: 0, behavior: "instant" });
  }

  async function finish(skipped = false) {
    if (submitting.current || status !== "authenticated") return;
    submitting.current = true;
    setBusy(true);
    setError(null);
    const answers: OnboardingAnswers = {
      name: name.trim() || undefined,
      provider_categories: uses,
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
    try {
      setUser(await api.completeOnboarding(answers));
      // A new person's first place is Testnet, in the agent chat: an answer in seconds, paid from the
      // account's test credits, with no wallet and nothing real moving.
      saveNetwork("solana-devnet");
      setDestination(skipped ? "/console/agent" : `/console/agent?prompt=${encodeURIComponent(FIRST_QUESTION)}`);
      setDone(true);
      window.scrollTo({ top: 0, behavior: "instant" });
    } catch (err) {
      const message =
        err instanceof Error
          ? err.message
          : "We couldn’t save your setup. Please try again.";
      setError(
        message.startsWith("cross-origin request rejected")
          ? "Your setup couldn’t be saved because this site’s connection is misconfigured. Your answers are still here; try again once the connection is restored."
          : message,
      );
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <Link href="/" className={styles.brand} aria-label="Algebra home">
          <Logo size={30} />
          <span>
            algebra<span className={styles.brandDot}>.</span>
          </span>
        </Link>
        {!done && status === "authenticated" && (
          <button
            className={styles.skip}
            type="button"
            onClick={() => finish(true)}
            disabled={busy}
          >
            Set up later <IconArrowRight size={14} />
          </button>
        )}
        {done && (
          <span className={styles.saved}>
            <IconCheck size={15} /> Setup complete
          </span>
        )}
      </header>
      {status !== "authenticated" ? (
        <main className={styles.loading} role="status">
          <Spinner size={24} />
          <span>Getting your workspace ready…</span>
        </main>
      ) : (
        <main
          className={`${styles.main} ${step === 2 && !done ? styles.wide : ""}`}
        >
          {!done && (
            <nav className={styles.progress} aria-label="Onboarding progress">
              <ol>
                {STEPS.map((label, i) => (
                  <li
                    key={label}
                    aria-current={i === step ? "step" : undefined}
                    className={i <= step ? styles.activeStep : ""}
                  >
                    <span>{i < step ? <IconCheck size={13} /> : i + 1}</span>
                    <span className={styles.stepLabel}>{label}</span>
                  </li>
                ))}
              </ol>
              <div
                className={styles.progressTrack}
                role="progressbar"
                aria-label="Setup progress"
                aria-valuemin={0}
                aria-valuemax={3}
                aria-valuenow={step + 1}
              >
                <motion.span
                  initial={false}
                  animate={{ width: `${((step + 1) / 3) * 100}%` }}
                  transition={{ duration: reduce ? 0 : 0.35, ease: EASE }}
                />
              </div>
            </nav>
          )}
          <AnimatePresence mode="wait" initial={false}>
            <motion.div
              key={done ? "complete" : step}
              initial={reduce ? false : { opacity: 0, y: 12, x: direction * 8 }}
              animate={{ opacity: 1, y: 0, x: 0 }}
              exit={
                reduce
                  ? { opacity: 0 }
                  : { opacity: 0, y: -8, x: direction * -8 }
              }
              transition={{ duration: reduce ? 0 : 0.25, ease: EASE }}
              onAnimationComplete={() => {
                if (step > 0 || done)
                  heading.current?.focus({ preventScroll: true });
              }}
            >
              {done ? (
                <>
                  <Confetti />
                  <div className={styles.completeArt} aria-hidden="true">
                    <span className={styles.completeRing} />
                    <span className={styles.completeCheck}>
                      <IconCheck size={40} strokeWidth={2} />
                    </span>
                    <span className={styles.sparkOne}>✦</span>
                    <span className={styles.sparkTwo}>✧</span>
                  </div>
                  <div className={styles.center}>
                    <span className={styles.eyebrow}>A GOOD START</span>
                    <h1 ref={heading} tabIndex={-1} className={styles.title}>
                      You’re all set<span className={styles.accent}>.</span>
                    </h1>
                    <p className={styles.description}>
                      {firstName(user) ? `${firstName(user)}, your` : "Your"}{" "}
                      account is ready, with 10 USDC of Testnet credits. Let’s
                      ask your first question: no wallet needed.
                    </p>
                  </div>
                  <div className={styles.readyCard}>
                    <span className={styles.readyIcon}>
                      <Logo size={28} />
                    </span>
                    <div>
                      <strong>Your Algebra workspace</strong>
                      <span>
                        {uses.length
                          ? uses.slice(0, 3).map(categoryLabel).join(" · ") +
                            (uses.length > 3 ? ` +${uses.length - 3}` : "")
                          : "Ready for your first agent"}
                      </span>
                    </div>
                    <span className={styles.readyBadge}>
                      Ready <IconCheck size={12} />
                    </span>
                  </div>
                  <Link className={styles.primary} href={destination}>
                    {destination === "/console/agent"
                      ? "Open the agent chat"
                      : "Ask my first question"}
                    <IconArrowRight size={18} />
                  </Link>
                  {destination !== "/console/agent" && (
                    <Link href="/console" className={styles.secondaryLink}>
                      Explore my workspace first
                    </Link>
                  )}
                  <p className={styles.footnote}>
                    Testnet uses test credits: nothing real moves.
                  </p>
                </>
              ) : (
                <form
                  onSubmit={(event) => {
                    event.preventDefault();
                    if (step === 2) void finish();
                    else go(step + 1);
                  }}
                  aria-busy={busy}
                >
                  {step === 0 && (
                    <>
                      <div className={styles.welcomeArt} aria-hidden="true">
                        <div className={styles.orbit} />
                        <div className={styles.orbitInner} />
                        <span className={styles.logoTile}>
                          <Logo size={62} />
                        </span>
                        <span className={styles.orbitAgent}>AI</span>
                        <span className={styles.orbitCheck}>
                          <IconCheck size={17} />
                        </span>
                        <span className={styles.orbitSpark}>✦</span>
                      </div>
                      <div className={styles.center}>
                        <span className={styles.eyebrow}>
                          YOUR AGENTS. YOUR RULES.
                        </span>
                        <h1
                          ref={heading}
                          tabIndex={-1}
                          className={styles.title}
                        >
                          A little power.
                          <br />
                          <span className={styles.wordart}>
                            A lot of control.
                          </span>
                        </h1>
                        <p className={styles.description}>
                          Give your AI agents permission to pay for APIs.
                          <br className={styles.desktopBreak} /> You set the
                          limits. Algebra handles the rest.
                        </p>
                      </div>
                      {needsName && (
                        <label className={styles.nameField}>
                          <span>
                            First, what should we call you?{" "}
                            <span className={styles.optional}>Optional</span>
                          </span>
                          <input
                            value={name}
                            onChange={(e) => setName(e.target.value)}
                            autoComplete="given-name"
                            maxLength={80}
                            placeholder="Your first name"
                            disabled={busy}
                          />
                        </label>
                      )}
                      <button
                        className={styles.primary}
                        type="submit"
                        disabled={busy}
                      >
                        Let’s get you started <IconArrowRight size={18} />
                      </button>
                      <p className={styles.footnote}>
                        About a minute. Start in the sandbox, on your terms.
                      </p>
                    </>
                  )}
                  {step === 1 && (
                    <>
                      <div className={styles.questionHeader}>
                        <span className={styles.eyebrow}>MAKE IT YOURS</span>
                        <h1
                          ref={heading}
                          tabIndex={-1}
                          className={styles.title}
                        >
                          What will your
                          <br />
                          <span className={styles.wordart}>
                            agents work on?
                          </span>
                        </h1>
                        <p className={styles.description}>
                          Pick as many as you like. We’ll save these as your
                          preferences.
                        </p>
                      </div>
                      <fieldset className={styles.choices} disabled={busy}>
                        <legend className="sr-only">
                          Agent interests, select all that apply
                        </legend>
                        {USES.map(({ id, symbol, hint }) => (
                          <label
                            key={id}
                            className={`${styles.choice} ${uses.includes(id) ? styles.selected : ""}`}
                          >
                            <input
                              type="checkbox"
                              name="interests"
                              value={id}
                              checked={uses.includes(id)}
                              onChange={() =>
                                setUses((prev) =>
                                  prev.includes(id)
                                    ? prev.filter((v) => v !== id)
                                    : [...prev, id],
                                )
                              }
                            />
                            <span
                              className={styles.choiceSymbol}
                              aria-hidden="true"
                            >
                              {symbol}
                            </span>
                            <span className={styles.choiceCopy}>
                              <strong>{categoryLabel(id)}</strong>
                              <span>{hint}</span>
                            </span>
                            <span
                              className={styles.choiceCheck}
                              aria-hidden="true"
                            >
                              {uses.includes(id) && (
                                <IconCheck size={12} strokeWidth={2.5} />
                              )}
                            </span>
                          </label>
                        ))}
                      </fieldset>
                      <div className={styles.selectionNote} aria-live="polite">
                        {uses.length
                          ? `${uses.length} ${uses.length === 1 ? "interest" : "interests"} selected`
                          : "Just exploring? You can continue without choosing."}
                      </div>
                    </>
                  )}
                  {step === 2 && (
                    <>
                      <div
                        className={`${styles.center} ${styles.walkthroughHeader}`}
                      >
                        <span className={styles.eyebrow}>
                          THREE SIMPLE STEPS
                        </span>
                        <h1
                          ref={heading}
                          tabIndex={-1}
                          className={styles.title}
                        >
                          Big possibilities.
                          <br />
                          <span className={styles.wordart}>
                            Clear boundaries.
                          </span>
                        </h1>
                        <p className={styles.description}>
                          Here’s how your first paid API call comes together.
                        </p>
                      </div>
                      <ol className={styles.walkthrough}>
                        <WalkthroughCard
                          index={0}
                          title="Set the limits"
                          description="Create a Spend Pass with a USDC budget, a per-call cap, and the providers your agent can pay."
                        />
                        <WalkthroughCard
                          index={1}
                          title="Connect your agent"
                          description="Give your agent a key, and it pays for data through Algebra. Every call stays inside your limit."
                        />
                        <WalkthroughCard
                          index={2}
                          title="Watch it work"
                          description="Follow each call, payment, and signed receipt. Start safely on Testnet with test credits."
                        />
                      </ol>
                      <div className={styles.sandboxNote}>
                        <span aria-hidden="true">✦</span> Your first question runs
                        on Testnet, from 10 USDC of test credits. No wallet or real funds needed.
                      </div>
                    </>
                  )}
                  {error && (
                    <div className={styles.error} role="alert">
                      <strong>Couldn’t finish setup</strong>
                      <p>{error}</p>
                    </div>
                  )}
                  {step > 0 && (
                    <div className={styles.actions}>
                      <button
                        type="button"
                        className={styles.back}
                        onClick={() => go(step - 1)}
                        disabled={busy}
                      >
                        <IconArrowLeft size={17} /> Back
                      </button>
                      <button
                        type="submit"
                        className={styles.primary}
                        disabled={busy}
                      >
                        {busy ? (
                          <>
                            <Spinner size={17} /> Saving your setup…
                          </>
                        ) : (
                          <>
                            {step === 2 ? "Finish setup" : "Continue"}
                            <IconArrowRight size={18} />
                          </>
                        )}
                      </button>
                    </div>
                  )}
                  {step > 0 && (
                    <p className={styles.footnote}>
                      {step === 1
                        ? "Your preferences don’t restrict what your agents can use."
                        : "You’ll set your own spending limit when you go real."}
                    </p>
                  )}
                </form>
              )}
            </motion.div>
          </AnimatePresence>
        </main>
      )}
      <footer className={styles.footer}>
        <span>Built for agents. Controlled by you.</span>
        <Link href="/privacy">Privacy</Link>
      </footer>
    </div>
  );
}

function WalkthroughCard({
  index,
  title,
  description,
}: {
  index: number;
  title: string;
  description: string;
}) {
  return (
    <li className={styles.walkthroughCard}>
      <span className={styles.cardStep}>
        STEP 0{index + 1}
        <span aria-hidden="true">↴</span>
      </span>
      <h2>{title}</h2>
      <div className={styles.diagram} aria-hidden="true">
        {index === 0 && (
          <>
            <span className={styles.connector} />
            <div className={styles.passArt}>
              <span className={styles.artLabel}>
                <Logo size={15} /> SPEND PASS <span className={styles.artDot} />
              </span>
              <strong>
                100<span> USDC</span>
              </strong>
              <div className={styles.budgetTrack}>
                <span />
              </div>
              <span className={styles.artRow}>
                Per-call limit <b>0.05 USDC</b>
              </span>
            </div>
            <div className={styles.artPill}>
              Your rules <IconCheck size={12} />
            </div>
          </>
        )}
        {index === 1 && (
          <>
            <span className={styles.connector} />
            <div className={styles.agentArt}>
              AI<span>Your agent</span>
            </div>
            <div className={styles.keyArt}>
              <span className={styles.keyDots}>•••• •••• ••••</span>
              <span className={styles.keyBadge}>
                <IconCheck size={12} /> API key connected
              </span>
            </div>
            <div className={styles.artPill}>
              MCP <span>↔</span> REST
            </div>
          </>
        )}
        {index === 2 && (
          <>
            <span className={styles.receiptBack} />
            <div className={styles.receiptArt}>
              <span className={styles.artLabel}>SIGNED RECEIPT</span>
              <span className={styles.receiptLine} />
              <span className={styles.receiptLineShort} />
              <span className={styles.receiptBlocks}>
                <i />
                <i />
                <i />
              </span>
              <span className={styles.artRow}>
                Paid <b>0.02 USDC</b>
              </span>
            </div>
            <span className={styles.receiptCheck}>
              <IconCheck size={25} strokeWidth={2.5} />
            </span>
          </>
        )}
      </div>
      <p>{description}</p>
    </li>
  );
}

function Confetti() {
  const reduce = useReducedMotion();
  if (reduce) return null;
  return (
    <div className={styles.confetti} aria-hidden="true">
      {Array.from({ length: 48 }, (_, i) => {
        const side = i % 2 === 0 ? -1 : 1;
        return (
          <motion.i
            key={i}
            className={styles.confettiPiece}
            style={{
              background: `var(--ob-confetti-${i % 4})`,
              left: side < 0 ? "8%" : "92%",
              borderRadius: i % 3 === 0 ? "50%" : "2px",
            }}
            initial={{ opacity: 0, x: 0, y: 80, rotate: 0 }}
            animate={{
              opacity: [0, 1, 1, 0],
              x: side * -(60 + ((i * 31) % 340)),
              y: [80, -130 - ((i * 17) % 180), 420],
              rotate: side * (160 + i * 13),
            }}
            transition={{
              duration: 2.4 + (i % 5) * 0.12,
              delay: (i % 8) * 0.025,
              ease: "easeOut",
            }}
          />
        );
      })}
    </div>
  );
}
