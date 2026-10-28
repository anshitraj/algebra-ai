"use client";

// The comparison the agent asked for, as a short list the person chooses from:
// every provider that was priced (live, unpaid), and the best offer each way:
// Cheapest, Balanced (recommended) and Fastest. Nothing is paid until the
// person picks one or more and presses Pay; that press is their approval, and
// the server still checks each payment against the Spend Pass and the router's
// guards. Several pieces of work (a weather forecast and a token price, say)
// are chosen and paid together. A person who has turned "ask before paying" off
// gets the recommended offer paid at once, when the pass allows it outright.

import Link from "next/link";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Choice, OptionGroup } from "@/lib/agent/events";
import { autoPicks, sharedRefusal, type AutoPick } from "@/lib/agent/option-view";
import type { Recommendation } from "@/lib/routing-types";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { latencySourceText, pickLabel } from "@/lib/strategy";
import { reasonLine, vaultFix } from "@/lib/decision";
import { SellerChips } from "../trust-badges";
import { IconArrowRight, IconBan, IconCheck, IconChevronDown, IconClock, IconLock } from "@/components/icons";
import styles from "./options-card.module.css";

type Picked = AutoPick;

const WAY_ORDER = ["cheapest", "balanced", "fastest"] as const;

function providerName(id: string) {
  const name = id.replace(/^(paysh|circle|payai|cdp|monid|sandbox|demo):/, "");
  return name.length > 34 ? `${name.slice(0, 33)}…` : name;
}

function priceText(r: Recommendation) {
  if (r.price_source === "unlisted") return "price on request";
  return `${formatUSDC(r.total_minor)}${r.price_source === "listed" ? " listed" : ""}`;
}

function speedText(r: Recommendation) {
  return r.latency_ms ? `${r.latency_ms.toLocaleString("en-US")} ms` : "speed unknown";
}

function reliabilityText(r: Recommendation) {
  const pct = `${Math.round((r.reliability ?? 0) * 100)}%`;
  // The figure is an estimate that starts at an assumed rate and moves with each paid call: never a raw ratio.
  return r.calls ? `${pct} expected to deliver · ${r.calls} paid call${r.calls === 1 ? "" : "s"} on record` : `${pct} assumed: no paid calls yet`;
}

const MAX_CARDS = 3;

/**
 * Up to three different offers: the winner each way (one row when an offer
 * wins more than one), then the next best overall, so there are three to
 * choose from whenever three were compared.
 */
function cardsOf(g: OptionGroup): { rec: Recommendation; ways: string[] }[] {
  const out: { rec: Recommendation; ways: string[] }[] = [];
  for (const way of WAY_ORDER) {
    const rec = g.recommendations.find((r) => r.strategy === way);
    if (!rec) continue;
    const same = out.find((c) => c.rec.candidate_id === rec.candidate_id);
    if (same) same.ways.push(way);
    else out.push({ rec, ways: [way] });
  }
  for (const rec of g.compared) {
    if (out.length >= MAX_CARDS) break;
    if (!out.some((c) => c.rec.candidate_id === rec.candidate_id)) out.push({ rec, ways: [] });
  }
  return out;
}

const DECISION_STYLE = {
  ALLOW: { text: "Allowed by your pass", icon: <IconCheck size={12} strokeWidth={3} />, cls: "text-primary" },
  HOLD: { text: "Needs your yes: pressing Pay is it", icon: <IconClock size={12} />, cls: "text-accent" },
  BLOCK: { text: "Can't be paid", icon: <IconBan size={12} />, cls: "text-danger" },
} as const;

function choicesOf(list: Picked[]): { choices: Choice[]; text: string } {
  const choices: Choice[] = list.map((p) => ({
    group: p.group.id,
    work: p.group.work,
    input: p.group.input,
    max_price_usdc: p.group.maxPriceUsdc,
    provider: p.rec.provider,
    // The provider is the person's choice; the way only names how the offer was listed.
    strategy: p.ways.includes("balanced") || p.ways.length === 0 ? "balanced" : (p.ways[0] as Choice["strategy"]),
    label: `${p.ways.length ? p.ways.map((w) => pickLabel(w)).join(" · ") : "Chosen"}: ${p.group.title}`,
  }));
  const text = `Pay for ${list
    .map((p) => `${p.group.title.toLowerCase()} from ${providerName(p.rec.provider)} (${p.ways.map((w) => pickLabel(w)).join(", ") || "chosen"}, ${priceText(p.rec)})`)
    .join("; ")}.`;
  return { choices, text };
}

export function OptionsBoard({
  groups,
  paid,
  active,
  auto,
  remainingMinor,
  passId,
  onPay,
}: {
  groups: OptionGroup[];
  /** Comparisons already paid for (or sent): their rows are done. */
  paid?: string[];
  /** The latest turn, with the chat free: offers can be picked. */
  active: boolean;
  /** "Ask before paying" is off for this turn: the recommended offer is paid when the pass allows it outright. */
  auto?: boolean;
  /** What the pass has left, so an automatic payment never starts one it can't cover. */
  remainingMinor?: number;
  /** The pass this chat pays under, so "Create the vault" lands on its card. */
  passId?: string;
  onPay: (choices: Choice[], text: string) => void;
}) {
  const [picked, setPicked] = useState<Record<string, Picked>>({});
  const [several, setSeveral] = useState(false);
  const done = useMemo(() => new Set(paid ?? []), [paid]);
  const open = groups.filter((g) => !done.has(g.id));

  function toggle(g: OptionGroup, rec: Recommendation, ways: string[]) {
    if (!active || done.has(g.id) || rec.decision === "BLOCK") return;
    const key = `${g.id}|${rec.candidate_id}`;
    setPicked((cur) => {
      const next = { ...cur };
      if (next[key]) {
        delete next[key];
        return next;
      }
      // One offer per comparison unless "several" is on: two offers for the same work are two payments.
      if (!several) for (const k of Object.keys(next)) if (k.startsWith(`${g.id}|`)) delete next[k];
      next[key] = { group: g, rec, ways };
      return next;
    });
  }

  const chosen = Object.values(picked).filter((p) => !done.has(p.group.id));
  const total = chosen.reduce((n, p) => n + (p.rec.price_source === "unlisted" ? 0 : p.rec.total_minor), 0);
  const unknownPrice = chosen.some((p) => p.rec.price_source === "unlisted");

  function pay() {
    if (!chosen.length || !active) return;
    const { choices, text } = choicesOf(chosen);
    setPicked({});
    onPay(choices, text);
  }

  // With "ask before paying" off, the recommended offer of each new comparison is paid once, as soon as
  // the answer is in. A comparison is only ever looked at once: one that isn't allowed outright
  // (waits for a yes, refused) is left for the person.
  const autoSent = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!auto || !active) return;
    const fresh = groups.filter((g) => !done.has(g.id) && !autoSent.current.has(g.id));
    if (fresh.length === 0) return;
    for (const g of fresh) autoSent.current.add(g.id);
    const picks = autoPicks(fresh, remainingMinor);
    if (picks.length === 0) return;
    const { choices, text } = choicesOf(picks);
    onPay(choices, `${text} (paid automatically: "ask before paying" is off)`);
  }, [auto, active, groups, done, remainingMinor, onPay]);

  return (
    <div className={styles.board} role="group" aria-label="Choose what to pay for">
      {groups.map((g) => (
        <GroupView key={g.id} g={g} showHeading={groups.length > 1} paid={done.has(g.id)} active={active} picked={picked} passId={passId} onToggle={toggle} />
      ))}

      {open.length > 0 && (
        <div className={styles.checkout}>
          <details className={styles.moreOptions}>
            <summary>Selection options</summary>
          <label className="inline-flex cursor-pointer items-center gap-2 text-xs text-muted">
            <input type="checkbox" checked={several} onChange={(e) => setSeveral(e.target.checked)} disabled={!active} className="accent-[var(--color-primary)]" />
            Pick several providers for the same work
          </label>
          </details>
          <span className={styles.selectionSummary} aria-live="polite">
            {chosen.length === 0 ? (
              auto ? (
                "Paid automatically when your pass allows it"
              ) : open.length > 1 ? (
                "Pick an offer for each thing you want"
              ) : (
                "Pick an offer to pay"
              )
            ) : (
              <>
                {chosen.length} selected · <span className="font-mono text-foreground tabular-nums">{formatUSDC(total)}</span>
                {unknownPrice ? " + prices on request" : ""}
              </>
            )}
          </span>
          <button
            type="button"
            onClick={pay}
            disabled={!active || chosen.length === 0}
            className={styles.pay}
          >
            {chosen.length > 1 ? `Pay for ${chosen.length}` : "Pay"}
          </button>
        </div>
      )}
    </div>
  );
}

function GroupView({
  g,
  showHeading,
  paid,
  active,
  picked,
  passId,
  onToggle,
}: {
  g: OptionGroup;
  showHeading: boolean;
  paid: boolean;
  active: boolean;
  picked: Record<string, Picked>;
  passId?: string;
  onToggle: (g: OptionGroup, rec: Recommendation, ways: string[]) => void;
}) {
  const cards = cardsOf(g);
  // With one offer there is nothing to rank: the three ways would all name it.
  const only = g.compared.length <= 1;
  const [showAll, setShowAll] = useState(false);
  // Offers refused for want of a (funded) vault stay on the list; the way out sits above them.
  const reasonsOf = [...g.recommendations, ...g.compared].flatMap((r) => r.reasons ?? []);
  const fix = vaultFix(reasonsOf, passId);
  // Every offer refused for the same reason: say it once.
  const refusal = sharedRefusal(cards.map((c) => c.rec));
  const testMoney = g.compared.length > 0 && g.compared.every((r) => r.test);
  return (
    <section className={styles.comparison} aria-label={g.title} data-sent={paid || undefined}>
      <header className={showHeading ? styles.groupHeading : "sr-only"}>
        <h3 className="text-sm font-semibold text-foreground">{g.title}</h3>
        <span className="text-xs text-muted">
          {g.compared.length} compared · up to {g.maxPriceUsdc} USDC · {networkLabel(g.network)}
          {testMoney ? (g.network === "sandbox" ? " · play money" : " · test money") : ""}
        </span>
        {paid && <span className="ml-auto rounded-full bg-primary-tint px-2 py-0.5 text-[0.6875rem] font-medium text-foreground">Sent to pay</span>}
      </header>

      {!paid && (fix || refusal) && (
        <p className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-border bg-danger-tint/40 px-3 py-2 text-xs leading-snug text-foreground">
          {fix ? <IconLock size={13} className="shrink-0" /> : <IconBan size={13} className="shrink-0 text-danger" />}
          <span>{fix ? fix.text : refusal?.map((r) => reasonLine(r).text).join("; ")}</span>
          {fix && (
            <Link href={fix.href} className="font-semibold text-primary hover:underline">
              {fix.action}
            </Link>
          )}
        </p>
      )}

      <ul className={styles.cards}>
        {cards.map(({ rec, ways }) => {
          const key = `${g.id}|${rec.candidate_id}`;
          const on = !!picked[key];
          const style = DECISION_STYLE[rec.decision];
          const recommended = ways.includes("balanced");
          const label = only ? "Only offer" : ways.length ? ways.map((w) => pickLabel(w)).join(" · ") : "Next best";
          const tone = recommended ? "balanced" : ways.includes("cheapest") ? "cheapest" : ways.includes("fastest") ? "fastest" : "other";
          const savings = rec.total_minor > 0 && g.compared.length > 1
            ? Math.round((1 - rec.total_minor / Math.max(...g.compared.map((r) => r.total_minor))) * 100)
            : 0;
          return (
            <li key={rec.candidate_id} className={styles.card} data-tone={tone} data-selected={on || undefined} data-blocked={rec.decision === "BLOCK" || undefined}>
              <button
                type="button"
                aria-pressed={on}
                disabled={!active || paid || rec.decision === "BLOCK"}
                onClick={() => onToggle(g, rec, ways)}
                className={styles.cardButton}
              >
                <span className={styles.cardTop}>
                  <span className={styles.strategy}><span aria-hidden="true">{tone === "balanced" ? "✦" : tone === "fastest" ? "ϟ" : tone === "cheapest" ? "↘" : "◇"}</span>{label}</span>
                  {recommended && !only ? <span className={styles.badge}>Recommended</span> : tone === "cheapest" && savings > 0 ? <span className={styles.badge}>Save {savings}%</span> : null}
                </span>
                <span className={styles.provider}>
                  <span className={styles.providerMark} aria-hidden="true">{providerName(rec.provider).slice(0, 1).toUpperCase()}</span>
                  <span className="min-w-0"><strong title={rec.provider}>{providerName(rec.provider)}</strong><small>{g.title}</small></span>
                </span>
                <span className={styles.price}>{priceText(rec)}</span>
                <span className={styles.metrics}>
                  <span title={latencySourceText(rec.latency_source, rec.calls)}>{speedText(rec)}</span><span aria-hidden="true">·</span>
                  <span title={reliabilityText(rec)}>{Math.round((rec.reliability ?? 0) * 100)}% est. delivery</span>
                  <span><span className={styles.networkDot} />{networkLabel(g.network)}</span>
                </span>
                <span className={styles.select}>{paid ? "Sent to pay" : on ? "Selected" : rec.decision === "BLOCK" ? "Unavailable" : "Select"}{on ? <IconCheck size={14} /> : !paid && rec.decision !== "BLOCK" ? <IconArrowRight size={14} /> : null}</span>
                <span className={styles.facts}>
                  <span>{style.icon}{rec.decision === "ALLOW" ? "Within your pass limits" : style.text}</span>
                  <span><IconCheck size={12} />{rec.price_source === "listed" ? "Listed price" : rec.price_source === "unlisted" ? "Price confirmed before payment" : "Live price compared"}</span>
                  {rec.test && <span className={styles.testMoney}>Test money</span>}
                  {rec.decision !== "ALLOW" && !refusal && rec.reasons?.length ? <span className={styles.reason}>{reasonLine(rec.reasons[0]).text}</span> : null}
                </span>
              </button>
            </li>
          );
        })}
      </ul>

      {g.compared.length > 0 && (
        <div className="border-t border-border px-3 py-2">
          <button type="button" onClick={() => setShowAll((v) => !v)} aria-expanded={showAll} className="inline-flex items-center gap-1 text-xs font-medium text-muted hover:text-foreground">
            <IconChevronDown size={13} className={`transition-transform ${showAll ? "rotate-180" : ""}`} />
            {showAll ? "Hide" : "Show"} all {g.compared.length} compared{g.refused.length ? ` and ${g.refused.length} left out` : ""}
          </button>
          {showAll && (
            <div className="mt-2 overflow-x-auto">
              <table className="w-full min-w-[520px] text-left text-xs">
                <thead className="text-muted">
                  <tr>
                    <th className="py-1 pr-2 font-medium">Pick</th>
                    <th className="py-1 pr-2 font-medium">Provider</th>
                    <th className="py-1 pr-2 font-medium">Price</th>
                    <th className="py-1 pr-2 font-medium">Speed</th>
                    <th className="py-1 pr-2 font-medium">Delivered</th>
                    <th className="py-1 pr-2 font-medium">Seller</th>
                  </tr>
                </thead>
                <tbody>
                  {g.compared.map((rec) => {
                    const key = `${g.id}|${rec.candidate_id}`;
                    return (
                      <tr key={rec.candidate_id} className="border-t border-border align-top">
                        <td className="py-1.5 pr-2">
                          <input
                            type="checkbox"
                            aria-label={`Pay ${rec.provider}`}
                            checked={!!picked[key]}
                            disabled={!active || paid || rec.decision === "BLOCK"}
                            onChange={() => onToggle(g, rec, [])}
                            className="accent-[var(--color-primary)]"
                          />
                        </td>
                        <td className="max-w-[180px] truncate py-1.5 pr-2 font-mono text-foreground" title={rec.provider}>
                          {providerName(rec.provider)}
                        </td>
                        <td className="py-1.5 pr-2 tabular-nums">{priceText(rec)}</td>
                        <td className="py-1.5 pr-2 tabular-nums" title={latencySourceText(rec.latency_source, rec.calls)}>
                          {speedText(rec)}
                        </td>
                        <td className="py-1.5 pr-2 tabular-nums">{Math.round((rec.reliability ?? 0) * 100)}%</td>
                        <td className="py-1.5 pr-2">
                          <SellerChips seller={rec.seller} provider={rec.provider} />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
              {g.refused.length > 0 && (
                <ul className="mt-2 space-y-0.5 text-[0.6875rem] text-muted">
                  {g.refused.map((x) => (
                    <li key={x.provider + x.reason}>
                      <span className="font-mono">{providerName(x.provider)}</span>: left out, {x.reason}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </div>
      )}
    </section>
  );
}
