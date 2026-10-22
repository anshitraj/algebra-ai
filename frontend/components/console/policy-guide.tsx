"use client";

// Setting a pass's rules as a short guided conversation instead of a form:
// one question per step, answered by picking a card, with a plain-English
// summary at the end. What it saves is exactly what the server enforces before
// every payment: the ask-me amount, the per-call cap and the provider list
// (PUT /me/passes/{id}/rules), and the speed limits and the new-provider rule
// (PUT /me/passes/{id}/controls).

import { motion } from "motion/react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import * as api from "@/lib/api-client";
import type { SpendPass } from "@/lib/types";
import type { NewProviderRule, UnverifiedRule, WebFoundRule } from "@/lib/routing-types";
import { formatUSDC } from "@/lib/money";
import { hasOwnRules } from "@/lib/policy-check";
import { IconArrowLeft, IconArrowRight, IconBan, IconCheck, IconGauge, IconShield, IconStore, IconTag, IconX, Spinner } from "@/components/icons";
import { Button, StatusBadge } from "./ui";
import GlassFolder, { hashString } from "@/components/ui/glass-folder-cards";
import styles from "./policy-guide.module.css";

const USDC = 1_000_000;
const toMinor = (v: string) => Math.round(Number(v) * USDC);

type Ask = "every" | "above" | "never";
type Speed = "careful" | "normal" | "busy";

const SPEEDS: Record<Speed, { label: string; perMin: number; perProvider: number; hint: string }> = {
  careful: { label: "Careful", perMin: 10, perProvider: 5, hint: "10 paid calls a minute, 5 to any one provider. A looping agent stops fast." },
  normal: { label: "Normal", perMin: 60, perProvider: 20, hint: "60 a minute, 20 per provider. Algebra's default." },
  busy: { label: "Busy", perMin: 300, perProvider: 100, hint: "300 a minute, 100 per provider. For agents that batch work." },
};

type Draft = {
  ask: Ask;
  askAbove: string;
  cap: string; // "" = no cap
  providers: "any" | "picked";
  picked: string[];
  newRule: NewProviderRule;
  newCap: string;
  /** A provider whose identity no person has verified in Algebra's registry. */
  unverified: UnverifiedRule;
  /** An endpoint the agent found on the open web and supplied itself. */
  webFound: WebFoundRule;
  speed: Speed;
};

function draftOf(p: SpendPass): Draft {
  const a = p.approve_above_minor_units;
  const c = p.controls;
  const speed = (Object.keys(SPEEDS) as Speed[]).find((k) => SPEEDS[k].perMin === (c?.max_calls_per_minute || 60)) ?? "normal";
  return {
    ask: a == null ? "never" : a === 0 ? "every" : "above",
    askAbove: a && a > 0 ? String(a / USDC) : "0.05",
    cap: p.max_per_purchase_minor_units != null ? String(p.max_per_purchase_minor_units / USDC) : "",
    providers: p.allowed_merchants.length ? "picked" : "any",
    picked: p.allowed_merchants,
    newRule: c?.new_providers ?? "cap",
    newCap: String((c?.new_provider_cap_minor_units ?? 50_000) / USDC),
    unverified: c?.unverified_providers ?? "allow",
    webFound: c?.web_found_providers ?? "approve",
    speed,
  };
}

/** The draft in sentences: what the review step shows and the card's summary. */
function sentences(d: Draft, p: SpendPass): { icon: React.ReactNode; text: string }[] {
  return [
    {
      icon: <IconShield size={15} />,
      text:
        d.ask === "every"
          ? "Every payment waits for your yes."
          : d.ask === "above"
            ? `Payments of ${d.askAbove} USDC or more wait for your yes; smaller ones run on their own.`
            : "Nothing waits for you: payments within the rules run on their own.",
    },
    { icon: <IconTag size={15} />, text: d.cap ? `No single call costs more than ${d.cap} USDC.` : `No cap per call: any call up to the budget (${formatUSDC(p.budget_minor_units)}).` },
    {
      icon: <IconStore size={15} />,
      text:
        d.providers === "picked" && d.picked.length
          ? `Only ${d.picked.length === 1 ? d.picked[0] : `${d.picked.length} providers you picked`} can be paid.`
          : d.newRule === "approve"
            ? "Any provider, but one you've never paid waits for your yes."
            : d.newRule === "cap"
              ? `Any provider; one you've never paid gets at most ${d.newCap} USDC a call until it has been paid once.`
              : "Any provider in the catalogs.",
    },
    ...(d.unverified !== "allow"
      ? [
          {
            icon: <IconShield size={15} />,
            text:
              d.unverified === "approve"
                ? "A provider whose identity isn't verified waits for your yes."
                : "Only providers whose identity is verified are paid.",
          },
        ]
      : []),
    {
      icon: <IconShield size={15} />,
      text:
        d.webFound === "approve"
          ? "An endpoint your agent finds on the open web, which no catalog lists, waits for your yes."
          : d.webFound === "deny"
            ? "Endpoints found on the open web are never paid."
            : "Endpoints found on the open web are paid within the other rules.",
    },
    { icon: <IconGauge size={15} />, text: `${SPEEDS[d.speed].label} speed: ${SPEEDS[d.speed].hint}` },
  ];
}

const STEPS = ["Approval", "Per call", "Providers", "Speed", "Review"] as const;

/** When this pass asks the person, in a few words: the cover of its folder. */
export function askLine(p: SpendPass) {
  const a = p.approve_above_minor_units;
  return a == null ? "Never asks" : a === 0 ? "Asks every time" : `Asks at ${formatUSDC(a)}+`;
}

/** One pass: its rules as sentences, and "Change rules" opens the guide. */
export function PolicyGuide({ pass, onSaved }: { pass: SpendPass; onSaved: () => void }) {
  const detailsId = useId();
  const folderRef = useRef<HTMLDivElement>(null);
  const [folderWidth, setFolderWidth] = useState(240);
  const [expanded, setExpanded] = useState(false);
  const [open, setOpen] = useState(false);
  const [startAt, setStartAt] = useState(0);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const frozen = !!pass.frozen_at;
  const own = hasOwnRules(pass);
  const summary = useMemo(() => sentences(draftOf(pass), pass), [pass]);

  useEffect(() => {
    const el = folderRef.current;
    if (!el) return;
    const observer = new ResizeObserver(([entry]) => {
      setFolderWidth(Math.min(280, entry.contentRect.width));
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  function toggleExpanded(next: boolean) {
    // Keep an unsaved editing session open until its Cancel or Save action.
    if (!next && open) return;
    setExpanded(next);
  }

  function editRule(step = 0) {
    if (open) return;
    setStartAt(step);
    setExpanded(true);
    setOpen(true);
  }

  async function revoke() {
    if (!window.confirm(`Revoke "${pass.label}"? The agent loses access immediately and the pass can't be turned back on.`)) return;
    setBusy(true);
    setMsg(null);
    try {
      await api.revokePass(pass.id);
      onSaved();
    } catch (e) {
      // Revoking is how a person stops an agent spending: a failure must never be silent.
      setMsg(`${e instanceof Error ? e.message : "Couldn't reach Algebra"}. The pass is still active.`);
    } finally {
      setBusy(false);
    }
  }

  async function freeze() {
    setBusy(true);
    setMsg(null);
    try {
      await api.freezePass(pass.id, !frozen);
      onSaved();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "Couldn't change it");
    } finally {
      setBusy(false);
    }
  }

  return (
    <article id={`pass-${pass.id}`} className={`${styles.card} ${expanded ? styles.expanded : ""}`}>
      <div className={styles.cover}>
        <div className={styles.eyebrow}>
          <span><IconShield size={13} /> Spending policy</span>
          {frozen ? <StatusBadge status="DENIED" label="Frozen" /> : <span className={styles.live}>Active</span>}
        </div>
        <div ref={folderRef} className={styles.folder}>
          <GlassFolder
            label={pass.label}
            meta={askLine(pass)}
            tone="dark"
            art="aurora"
            seed={hashString(pass.id) % 997}
            action="none"
            width={folderWidth}
            open={expanded}
            onOpenChange={toggleExpanded}
            closeOnOutside={false}
            ariaControls={detailsId}
            appear={false}
            files={[
              { name: "Approval", meta: "01" },
              { name: "Per call", meta: "02" },
              { name: "Providers", meta: "03" },
              { name: "Speed", meta: "04" },
            ]}
            onFileSelect={(_, index) => editRule(index)}
            className={styles.glass}
          />
        </div>
        <div className={styles.balance}>
          <span>Available to spend</span>
          <strong>{formatUSDC(pass.remaining_minor_units)}</strong>
          <span>of {formatUSDC(pass.budget_minor_units)}{pass.budget_period !== "total" ? ` per ${pass.budget_period}` : " total budget"}</span>
        </div>
        <button type="button" className={styles.openButton} aria-expanded={expanded} aria-controls={detailsId} onClick={() => toggleExpanded(!expanded)} disabled={open}>
          {expanded ? "Close policy" : "Open spending policy"}
          {expanded ? <IconX size={14} /> : <IconArrowRight size={14} />}
        </button>
        {!expanded && msg && <p role="status" className="mt-3 text-xs text-muted">{msg}</p>}
      </div>

      <div id={detailsId} hidden={!expanded} className={styles.details}>
        <div className="flex flex-wrap items-start justify-between gap-3 px-5 pt-5">
          <div className="min-w-0">
            <h3 className="text-base font-semibold text-foreground">{pass.label}</h3>
            <p className="mt-0.5 text-xs text-muted">Your rules, checked before every payment.</p>
          </div>
          <div className="flex gap-2">
            {!open && (
              <Button onClick={() => editRule()}>
                <IconShield size={14} /> {own ? "Change rules" : "Set up rules"}
              </Button>
            )}
            <Button variant={frozen ? "secondary" : "danger"} onClick={freeze} disabled={busy}>
              {busy ? <Spinner size={14} /> : frozen ? <IconCheck size={14} /> : <IconBan size={14} />} {frozen ? "Unfreeze" : "Freeze"}
            </Button>
            <Button variant="secondary" onClick={revoke} disabled={busy}>
              Revoke
            </Button>
          </div>
        </div>

        {!open ? (
          <div className="px-5 pt-3 pb-5">
            {!own && (
              <p className="mb-3 rounded-xl bg-accent-tint/50 px-3.5 py-2.5 text-xs leading-relaxed text-foreground">
                No rules of your own yet: only the budget and Algebra&apos;s defaults stand between this agent and a payment. Set them up in four short steps.
              </p>
            )}
            <ul className="space-y-2">
              {summary.map((s, i) => (
                <li key={i} className="flex items-start gap-2.5 text-sm text-foreground">
                  <span className="mt-0.5 text-primary">{s.icon}</span>
                  {s.text}
                </li>
              ))}
            </ul>
            {msg && <p role="status" className="mt-3 text-sm text-muted">{msg}</p>}
          </div>
        ) : (
          <Guide
            pass={pass}
            startAt={startAt}
            onClose={() => setOpen(false)}
            onSaved={() => {
              setOpen(false);
              setMsg("Saved. These rules apply from the next payment.");
              onSaved();
            }}
          />
        )}
      </div>
    </article>
  );
}

function Guide({ pass, startAt = 0, onClose, onSaved }: { pass: SpendPass; startAt?: number; onClose: () => void; onSaved: () => void }) {
  const [step, setStep] = useState(startAt);
  const [d, setD] = useState<Draft>(() => draftOf(pass));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [known, setKnown] = useState<string[]>([]);
  const [typed, setTyped] = useState("");
  const set = (patch: Partial<Draft>) => setD((x) => ({ ...x, ...patch }));

  // Providers this person's agents have already paid: the easiest ones to allow.
  useEffect(() => {
    api
      .listMyEconomicIntents(100)
      .then((r) => {
        const ids = new Set<string>();
        for (const i of r.intents ?? []) for (const res of i.reservations ?? []) if (res.provider_id) ids.add(res.provider_id);
        setKnown([...ids].slice(0, 12));
      })
      .catch(() => setKnown([]));
  }, []);

  const problem = (() => {
    if (step === 0 && d.ask === "above" && !(toMinor(d.askAbove) > 0)) return "Enter an amount above zero.";
    if (step === 1 && d.cap && !(toMinor(d.cap) > 0 && toMinor(d.cap) <= pass.budget_minor_units))
      return `The cap must be above zero and at most the budget (${formatUSDC(pass.budget_minor_units)}).`;
    if (step === 2 && d.providers === "picked" && d.picked.length === 0) return "Pick at least one provider, or allow any.";
    if (step === 2 && d.providers === "any" && d.newRule === "cap" && !(toMinor(d.newCap) > 0)) return "Enter a first-payment cap above zero.";
    return null;
  })();

  async function save() {
    setBusy(true);
    setError(null);
    try {
      await api.setPassRules(pass.id, {
        approve_above_minor_units: d.ask === "never" ? null : d.ask === "every" ? 0 : toMinor(d.askAbove),
        max_per_purchase_minor_units: d.cap ? toMinor(d.cap) : null,
        allowed_merchants: d.providers === "picked" ? d.picked : [],
      });
      await api.setPassControls(pass.id, {
        max_calls_per_minute: SPEEDS[d.speed].perMin,
        max_calls_per_provider_per_minute: SPEEDS[d.speed].perProvider,
        new_providers: d.newRule,
        new_provider_cap_minor_units: d.newRule === "cap" ? toMinor(d.newCap) : 0,
        unverified_providers: d.unverified,
        web_found_providers: d.webFound,
      });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't save the rules");
    } finally {
      setBusy(false);
    }
  }

  function addProvider(id: string) {
    const v = id.trim().toLowerCase();
    if (v && !d.picked.includes(v)) set({ picked: [...d.picked, v], providers: "picked" });
    setTyped("");
  }

  return (
    <div className="mt-4 border-t border-border">
      {/* Progress */}
      <ol className="flex items-center gap-1 px-5 pt-4" aria-label="Steps">
        {STEPS.map((s, i) => (
          <li key={s} className="flex flex-1 flex-col gap-1.5">
            <span className={`h-1 rounded-full transition-colors duration-300 ${i <= step ? "bg-primary" : "bg-border"}`} />
            <span className={`text-[0.68rem] font-medium ${i === step ? "text-foreground" : "text-muted"}`} aria-current={i === step ? "step" : undefined}>
              {s}
            </span>
          </li>
        ))}
      </ol>

      <div className="relative min-h-[17rem] px-5 py-5">
        {/* A new step shows at once and only fades in: nothing waits on an exit animation. */}
        <motion.div key={step} initial={{ opacity: 0, x: 12 }} animate={{ opacity: 1, x: 0 }} transition={{ duration: 0.2, ease: [0.16, 1, 0.3, 1] }}>
            {step === 0 && (
              <Question title="When should Algebra ask you before paying?" hint="Your yes is a tap in the console or the chat. Nothing is paid while it waits.">
                <Choice on={d.ask === "every"} onPick={() => set({ ask: "every" })} title="Every payment" body="Nothing is paid without your yes. Best while you get to know an agent." />
                <Choice on={d.ask === "above"} onPick={() => set({ ask: "above" })} title="Only above an amount" body="Small calls run on their own; bigger ones wait for you.">
                  {d.ask === "above" && (
                    <Amount value={d.askAbove} onChange={(v) => set({ askAbove: v })} presets={["0.01", "0.05", "0.25"]} suffix="USDC or more" />
                  )}
                </Choice>
                <Choice on={d.ask === "never"} onPick={() => set({ ask: "never" })} title="Never" body="The other rules decide alone. For agents you already trust." />
              </Question>
            )}

            {step === 1 && (
              <Question title="What's the most one call may cost?" hint="A call above this is refused before anything is paid, whatever the provider asks.">
                {["0.01", "0.10", "1"].map((v) => (
                  <Choice key={v} on={d.cap === v} onPick={() => set({ cap: v })} title={`${v} USDC`} body={v === "0.01" ? "Price lookups and other tiny calls." : v === "0.10" ? "Most paid APIs." : "Heavier work, such as LLM answers or images."} />
                ))}
                <Choice on={!["", "0.01", "0.10", "1"].includes(d.cap)} onPick={() => set({ cap: "0.5" })} title="Another amount" body="Pick your own cap.">
                  {!["", "0.01", "0.10", "1"].includes(d.cap) && <Amount value={d.cap} onChange={(v) => set({ cap: v })} suffix="USDC a call" />}
                </Choice>
                <Choice on={d.cap === ""} onPick={() => set({ cap: "" })} title="No cap" body={`Any call up to the whole budget (${formatUSDC(pass.budget_minor_units)}).`} warn />
              </Question>
            )}

            {step === 2 && (
              <Question title="Who may be paid?" hint="Algebra still refuses providers that are down, overcharge their listing, or change their payee.">
                <Choice on={d.providers === "any"} onPick={() => set({ providers: "any" })} title="Any provider" body="Algebra's router picks the best one live.">
                  {d.providers === "any" && (
                    <div className="mt-3 space-y-1.5" onClick={(e) => e.stopPropagation()}>
                      <p className="text-xs font-medium text-foreground">A provider you&apos;ve never paid:</p>
                      {(
                        [
                          ["cap", "Cap its first payments"],
                          ["approve", "Ask me first"],
                          ["allow", "Treat it like any other"],
                        ] as [NewProviderRule, string][]
                      ).map(([id, label]) => (
                        <label key={id} className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                          <input type="radio" name={`new-${pass.id}`} checked={d.newRule === id} onChange={() => set({ newRule: id })} className="accent-[var(--color-primary)]" />
                          {label}
                          {id === "cap" && d.newRule === "cap" && (
                            <span className="inline-flex items-center gap-1">
                              at
                              <input
                                value={d.newCap}
                                onChange={(e) => set({ newCap: e.target.value })}
                                inputMode="decimal"
                                aria-label="First-payment cap in USDC"
                                className="w-16 rounded-md border border-border-strong bg-surface px-1.5 py-0.5 font-mono text-xs"
                              />
                              USDC
                            </span>
                          )}
                        </label>
                      ))}
                      <p className="pt-2 text-xs font-medium text-foreground">A provider whose identity isn&apos;t verified:</p>
                      <p className="text-[0.7rem] text-muted">Verified means a person reviewed who runs it. Most listed providers aren&apos;t; a payment that worked never verifies one.</p>
                      {(
                        [
                          ["allow", "Pay it within the other rules"],
                          ["approve", "Ask me first"],
                          ["deny", "Never pay it"],
                        ] as [UnverifiedRule, string][]
                      ).map(([id, label]) => (
                        <label key={id} className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                          <input type="radio" name={`unverified-${pass.id}`} checked={d.unverified === id} onChange={() => set({ unverified: id })} className="accent-[var(--color-primary)]" />
                          {label}
                        </label>
                      ))}
                      <p className="pt-2 text-xs font-medium text-foreground">An endpoint your agent found on the open web:</p>
                      <p className="text-[0.7rem] text-muted">No catalog lists it and nobody has reviewed it. Once it has been paid here a few times it counts as known.</p>
                      {(
                        [
                          ["approve", "Ask me first"],
                          ["allow", "Pay it within the other rules"],
                          ["deny", "Never pay it"],
                        ] as [WebFoundRule, string][]
                      ).map(([id, label]) => (
                        <label key={id} className="flex cursor-pointer items-center gap-2 text-xs text-foreground">
                          <input type="radio" name={`webfound-${pass.id}`} checked={d.webFound === id} onChange={() => set({ webFound: id })} className="accent-[var(--color-primary)]" />
                          {label}
                        </label>
                      ))}
                    </div>
                  )}
                </Choice>
                <Choice on={d.providers === "picked"} onPick={() => set({ providers: "picked" })} title="Only providers I pick" body="Everything else is refused before it's asked for a price.">
                  {d.providers === "picked" && (
                    <div className="mt-3" onClick={(e) => e.stopPropagation()}>
                      <div className="flex flex-wrap gap-1.5">
                        {d.picked.map((p) => (
                          <span key={p} className="inline-flex items-center gap-1 rounded-lg bg-primary px-2 py-1 font-mono text-[0.7rem] text-primary-tint">
                            {p}
                            <button type="button" aria-label={`Remove ${p}`} onClick={() => set({ picked: d.picked.filter((x) => x !== p) })}>
                              <IconX size={11} />
                            </button>
                          </span>
                        ))}
                      </div>
                      {known.filter((k) => !d.picked.includes(k)).length > 0 && (
                        <div className="mt-2 flex flex-wrap gap-1.5">
                          <span className="text-[0.7rem] text-muted">Paid before:</span>
                          {known
                            .filter((k) => !d.picked.includes(k))
                            .map((k) => (
                              <button key={k} type="button" onClick={() => addProvider(k)} className="rounded-lg border border-dashed border-border-strong px-2 py-0.5 font-mono text-[0.7rem] text-foreground hover:bg-primary-tint">
                                + {k}
                              </button>
                            ))}
                        </div>
                      )}
                      <form
                        className="mt-2 flex gap-1.5"
                        onSubmit={(e) => {
                          e.preventDefault();
                          addProvider(typed);
                        }}
                      >
                        <input
                          value={typed}
                          onChange={(e) => setTyped(e.target.value)}
                          placeholder="paysh:birdeye.data"
                          aria-label="Provider ID"
                          className="min-w-0 flex-1 rounded-lg border border-border-strong bg-surface px-2.5 py-1.5 font-mono text-xs"
                        />
                        <button type="submit" className="rounded-lg border border-border-strong px-2.5 text-xs font-medium text-foreground hover:bg-primary-tint">
                          Add
                        </button>
                      </form>
                    </div>
                  )}
                </Choice>
              </Question>
            )}

            {step === 3 && (
              <Question title="How fast may it spend?" hint="A cap on paid calls per minute: the fuse that stops a looping agent.">
                {(Object.keys(SPEEDS) as Speed[]).map((k) => (
                  <Choice key={k} on={d.speed === k} onPick={() => set({ speed: k })} title={SPEEDS[k].label} body={SPEEDS[k].hint} />
                ))}
              </Question>
            )}

            {step === 4 && (
              <div>
                <p className="font-display text-lg font-semibold text-foreground">Here&apos;s how &ldquo;{pass.label}&rdquo; will spend</p>
                <p className="mt-1 text-sm text-muted">Checked by Algebra&apos;s server before every payment, and recorded in each signed receipt.</p>
                <ul className="mt-4 space-y-2.5">
                  {sentences(d, pass).map((s, i) => (
                    <li key={i} className="flex items-start gap-3 rounded-xl border border-border px-3.5 py-3 text-sm text-foreground">
                      <span className="mt-0.5 text-primary">{s.icon}</span>
                      {s.text}
                    </li>
                  ))}
                </ul>
              </div>
            )}
        </motion.div>
      </div>

      {(problem || error) && <p className="px-5 pb-2 text-sm text-danger">{error ?? problem}</p>}

      <div className="flex items-center justify-between gap-3 border-t border-border bg-background/40 px-5 py-3">
        <button type="button" onClick={step === 0 ? onClose : () => setStep(step - 1)} className="inline-flex items-center gap-1.5 text-sm font-medium text-muted hover:text-foreground">
          <IconArrowLeft size={14} /> {step === 0 ? "Cancel" : "Back"}
        </button>
        {step < STEPS.length - 1 ? (
          <Button onClick={() => setStep(step + 1)} disabled={!!problem}>
            Next <IconArrowRight size={14} />
          </Button>
        ) : (
          <Button onClick={save} disabled={busy}>
            {busy ? <Spinner size={14} /> : <IconCheck size={14} />} Save these rules
          </Button>
        )}
      </div>
    </div>
  );
}

function Question({ title, hint, children }: { title: string; hint: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="font-display text-lg font-semibold text-foreground">{title}</p>
      <p className="mt-1 text-sm text-muted">{hint}</p>
      <div className="mt-4 grid gap-2.5 sm:grid-cols-2" role="radiogroup" aria-label={title}>
        {children}
      </div>
    </div>
  );
}

function Choice({ on, onPick, title, body, warn, children }: { on: boolean; onPick: () => void; title: string; body: string; warn?: boolean; children?: React.ReactNode }) {
  return (
    <div
      role="radio"
      aria-checked={on}
      tabIndex={0}
      onClick={onPick}
      onKeyDown={(e) => {
        if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
          e.preventDefault();
          onPick();
        }
      }}
      className={`cursor-pointer rounded-xl border px-4 py-3.5 text-left transition-[border-color,background-color,box-shadow] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary ${
        on ? "border-primary bg-primary-tint/50 shadow-[0_0_0_1px_var(--color-primary)]" : "border-border hover:border-border-strong hover:bg-primary-tint/20"
      }`}
    >
      <div className="flex items-start justify-between gap-3">
        <p className="text-sm font-semibold text-foreground">{title}</p>
        <span className={`mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded-full border ${on ? "border-primary bg-primary text-primary-tint" : "border-border-strong"}`}>
          {on && <IconCheck size={10} strokeWidth={3} />}
        </span>
      </div>
      <p className={`mt-1 text-xs leading-relaxed ${warn ? "text-accent" : "text-muted"}`}>{body}</p>
      {children}
    </div>
  );
}

function Amount({ value, onChange, presets, suffix }: { value: string; onChange: (v: string) => void; presets?: string[]; suffix: string }) {
  return (
    <div className="mt-3 flex flex-wrap items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
      {presets?.map((p) => (
        <button
          key={p}
          type="button"
          onClick={() => onChange(p)}
          className={`rounded-lg px-2 py-1 font-mono text-xs ${value === p ? "bg-primary text-primary-tint" : "border border-border-strong text-foreground hover:bg-primary-tint"}`}
        >
          {p}
        </button>
      ))}
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        inputMode="decimal"
        aria-label={suffix}
        className="w-20 rounded-lg border border-border-strong bg-surface px-2 py-1 font-mono text-xs text-foreground"
      />
      <span className="text-xs text-muted">{suffix}</span>
    </div>
  );
}
