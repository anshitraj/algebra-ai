"use client";

// The comparison the agent asked for, as cards the person chooses from: every
// provider that was priced (live, unpaid), and the best offer each way:
// Cheapest, Balanced (recommended) and Fastest. Nothing is paid until the
// person picks one or more and presses Pay; that press is their approval,
// and the server still checks each payment against the Spend Pass and the
// router's guards. Several pieces of work (a weather forecast and a token
// price, say) are chosen and paid together.

import { useMemo, useState } from "react";
import type { Choice, OptionGroup } from "@/lib/agent/events";
import type { Recommendation } from "@/lib/routing-types";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { latencySourceText, pickLabel } from "@/lib/strategy";
import { reasonLine } from "@/lib/decision";
import { SellerChips } from "../trust-badges";
import { IconBan, IconCheck, IconChevronDown, IconClock } from "@/components/icons";

type Picked = { group: OptionGroup; rec: Recommendation; ways: string[] };

const WAY_ORDER = ["cheapest", "balanced", "fastest"] as const;

function providerName(id: string) {
  const name = id.replace(/^(paysh|circle|payai|cdp|monid|sandbox):/, "");
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
 * Up to three different offers: the winner each way (one card when an offer
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
  BLOCK: { text: "Refused: can't be paid", icon: <IconBan size={12} />, cls: "text-danger" },
} as const;

export function OptionsBoard({
  groups,
  paid,
  active,
  onPay,
}: {
  groups: OptionGroup[];
  /** Comparisons already paid for (or sent): their cards are done. */
  paid?: string[];
  /** The latest turn, with the chat free: cards can be picked. */
  active: boolean;
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
      // One card per comparison unless "several" is on: two cards for the same work are two payments.
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
    const choices: Choice[] = chosen.map((p) => ({
      group: p.group.id,
      work: p.group.work,
      input: p.group.input,
      max_price_usdc: p.group.maxPriceUsdc,
      provider: p.rec.provider,
      // The provider is the person's choice; the way only names how the card was offered.
      strategy: p.ways.includes("balanced") || p.ways.length === 0 ? "balanced" : (p.ways[0] as Choice["strategy"]),
      label: `${p.ways.length ? p.ways.map((w) => pickLabel(w)).join(" · ") : "Chosen"}: ${p.group.title}`,
    }));
    const text = `Pay for ${chosen
      .map((p) => `${p.group.title.toLowerCase()} from ${providerName(p.rec.provider)} (${p.ways.map((w) => pickLabel(w)).join(", ") || "chosen"}, ${priceText(p.rec)})`)
      .join("; ")}.`;
    setPicked({});
    onPay(choices, text);
  }

  return (
    <div className="space-y-4" role="group" aria-label="Choose what to pay for">
      {groups.map((g) => (
        <GroupView key={g.id} g={g} paid={done.has(g.id)} active={active} picked={picked} onToggle={toggle} />
      ))}

      {open.length > 0 && (
        <div className="sticky bottom-0 z-10 flex flex-wrap items-center gap-3 rounded-2xl border border-border-strong bg-surface px-4 py-3 shadow-[0_10px_30px_-18px_rgba(11,16,32,0.45)]">
          <label className="inline-flex cursor-pointer items-center gap-2 text-xs text-muted">
            <input type="checkbox" checked={several} onChange={(e) => setSeveral(e.target.checked)} disabled={!active} className="accent-[var(--color-primary)]" />
            Pick several providers for the same work
          </label>
          <span className="ml-auto text-sm text-muted">
            {chosen.length === 0 ? (
              open.length > 1 ? "Pick a card for each thing you want" : "Pick a card to pay"
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
            className="inline-flex h-9 items-center gap-2 rounded-xl bg-primary px-4 text-sm font-semibold text-primary-tint transition-[transform,opacity] active:scale-[0.98] disabled:opacity-40"
          >
            {chosen.length > 1 ? `Pay for ${chosen.length}` : "Pay"}
          </button>
          <p className="basis-full text-[0.6875rem] leading-relaxed text-muted">
            Nothing is paid until you press Pay. Pressing it is your approval; Algebra still checks each payment against your Spend Pass and refuses a price above
            the limit, a changed payee or a blocked provider.
          </p>
        </div>
      )}
    </div>
  );
}

function GroupView({
  g,
  paid,
  active,
  picked,
  onToggle,
}: {
  g: OptionGroup;
  paid: boolean;
  active: boolean;
  picked: Record<string, Picked>;
  onToggle: (g: OptionGroup, rec: Recommendation, ways: string[]) => void;
}) {
  const cards = cardsOf(g);
  // With one offer there is nothing to rank: the three ways would all name it.
  const only = g.compared.length <= 1;
  const [showAll, setShowAll] = useState(false);
  return (
    <section className={`rounded-2xl border border-border bg-surface ${paid ? "opacity-70" : ""}`} aria-label={g.title}>
      <header className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-border px-4 py-3">
        <h3 className="text-sm font-semibold text-foreground">{g.title}</h3>
        <span className="text-xs text-muted">
          {g.compared.length} compared live · up to {g.maxPriceUsdc} USDC · {networkLabel(g.network)}
        </span>
        {paid && <span className="ml-auto rounded-full bg-primary-tint px-2 py-0.5 text-[0.6875rem] font-medium text-foreground">Sent to pay</span>}
      </header>

      <div className="grid gap-2.5 p-3 sm:grid-cols-3">
        {cards.map(({ rec, ways }) => {
          const key = `${g.id}|${rec.candidate_id}`;
          const on = !!picked[key];
          const style = DECISION_STYLE[rec.decision];
          const recommended = ways.includes("balanced");
          return (
            <button
              key={rec.candidate_id}
              type="button"
              aria-pressed={on}
              disabled={!active || paid || rec.decision === "BLOCK"}
              onClick={() => onToggle(g, rec, ways)}
              className={`group relative flex min-w-0 flex-col gap-2 rounded-xl border px-3.5 py-3 text-left transition-[border-color,box-shadow,background-color] disabled:cursor-not-allowed ${
                on ? "border-primary bg-primary-tint/50 shadow-[0_0_0_3px_color-mix(in_srgb,var(--color-primary)_14%,transparent)]" : "border-border-strong hover:border-primary/60"
              } ${rec.decision === "BLOCK" ? "opacity-60" : ""}`}
            >
              <span className="flex flex-wrap items-center gap-1.5">
                {(only ? ["Only offer"] : ways.length ? ways.map((w) => pickLabel(w)) : ["Next best"]).map((label) => (
                  <span key={label} className="text-[0.6875rem] font-semibold tracking-wide text-muted uppercase">
                    {label}
                  </span>
                ))}
                {recommended && !only && <span className="rounded-full bg-accent-tint px-1.5 py-px text-[0.625rem] font-semibold text-accent">Recommended</span>}
                {on && <IconCheck size={14} strokeWidth={3} className="ml-auto text-primary" />}
              </span>
              <span className="truncate font-mono text-[0.8125rem] text-foreground" title={rec.provider}>
                {providerName(rec.provider)}
              </span>
              <span className="font-display text-lg leading-none font-semibold text-foreground tabular-nums">{priceText(rec)}</span>
              <dl className="grid gap-0.5 text-xs text-muted">
                <div>
                  <dt className="sr-only">Speed</dt>
                  <dd>
                    <span className="font-medium text-foreground tabular-nums">{speedText(rec)}</span> · {latencySourceText(rec.latency_source, rec.calls)}
                  </dd>
                </div>
                <div>
                  <dt className="sr-only">Reliability</dt>
                  <dd>{reliabilityText(rec)}</dd>
                </div>
              </dl>
              <SellerChips seller={rec.seller} />
              <span className={`mt-auto inline-flex items-center gap-1 text-xs font-medium ${style.cls}`}>
                {style.icon} {style.text}
              </span>
              {rec.decision !== "ALLOW" && rec.reasons?.length ? (
                <span className="text-[0.6875rem] leading-snug text-muted">{rec.reasons.map((r) => reasonLine(r).text).join("; ")}</span>
              ) : null}
              {rec.test && <span className="text-[0.6875rem] text-muted">Test money: no real value moves.</span>}
            </button>
          );
        })}
      </div>

      {g.compared.length > 0 && (
        <div className="border-t border-border px-4 py-2.5">
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
                          <SellerChips seller={rec.seller} />
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
