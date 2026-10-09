"use client";

import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { motion, useReducedMotion } from "motion/react";
import type { StepHint } from "@/lib/agent/events";
import { IconCheck, IconLock } from "@/components/icons";
import { ProviderLogo } from "@/components/provider-logo";
import { useNow } from "@/lib/use-now";

// Waiting states for the agent: while a step runs, show what it is doing
// (the sites a search goes through, the checks policy runs, the stages of an
// order) instead of a bare spinner. Every loop here lives only as long as the
// step does — StepRow unmounts it the moment the result lands.

const EASE = [0.16, 1, 0.3, 1] as const;

/** Advances every `ms` while the tab is visible. */
function useTick(ms: number) {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") setTick((t) => t + 1);
    }, ms);
    return () => window.clearInterval(id);
  }, [ms]);
  return tick;
}

/** True once `ms` has passed — so a step that finishes fast never flashes a panel. */
function useAfter(ms: number) {
  const [ready, setReady] = useState(false);
  useEffect(() => {
    const id = window.setTimeout(() => setReady(true), ms);
    return () => window.clearTimeout(id);
  }, [ms]);
  return ready;
}

/** A live dot: the agent is working right now. */
export function Pulse({ size = 22 }: { size?: number }) {
  return (
    <span className="relative flex shrink-0 items-center justify-center" style={{ width: size, height: size }} aria-hidden="true">
      <span className="agent-ping absolute inset-[3px] rounded-full bg-primary/35" />
      <span className="relative h-2 w-2 rounded-full bg-primary" />
    </span>
  );
}

/**
 * One status line that swaps in place as the work moves on. Enter-only CSS
 * (a keyed remount), so nothing lingers in the DOM when frames are throttled.
 */
function Rotating({ text, className = "" }: { text: string; className?: string }) {
  return (
    <span className={`block min-w-0 flex-1 overflow-hidden ${className}`}>
      <span key={text} className="agent-swap block truncate">
        {text}
      </span>
    </span>
  );
}

function Elapsed({ since }: { since: number }) {
  const now = useNow(100);
  return <span className="shrink-0 font-mono text-[0.7rem] text-muted/80 tabular-nums">{(Math.max(0, now - since) / 1000).toFixed(1)}s</span>;
}

function Panel({ children, line, since, badge }: { children: ReactNode; line: string; since: number; badge?: string }) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 6, scale: 0.99 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      transition={{ duration: 0.35, ease: EASE }}
      className="mt-2.5 overflow-hidden rounded-xl border border-border bg-background/60 shadow-[0_8px_24px_-20px_rgba(11,16,32,0.55)]"
    >
      {children}
      <div className="flex items-center gap-2 border-t border-border px-3 py-2 text-xs text-muted" aria-live="polite">
        <Pulse size={14} />
        <Rotating text={`${line}…`} />
        {badge && <span className="shrink-0 rounded-full bg-primary-tint px-2 py-0.5 text-[0.7rem] font-medium text-primary">{badge}</span>}
        <Elapsed since={since} />
      </div>
    </motion.div>
  );
}

function SkeletonRows({ rows = 3, thumb = true }: { rows?: number; thumb?: boolean }) {
  const widths = [
    ["72%", "44%"],
    ["58%", "36%"],
    ["66%", "30%"],
  ];
  return (
    <ul className="divide-y divide-border" aria-hidden="true">
      {Array.from({ length: rows }, (_, i) => {
        const delay = { "--sweep-delay": `${i * 0.14}s` } as CSSProperties;
        return (
          <li key={i} className="flex items-center gap-3 px-3 py-2.5">
            {thumb && <span className="agent-skeleton h-9 w-9 shrink-0 rounded-lg" style={delay} />}
            <span className="min-w-0 flex-1 space-y-1.5">
              <span className="agent-skeleton block h-2.5 rounded-full" style={{ ...delay, width: widths[i % 3][0] }} />
              <span className="agent-skeleton block h-2 rounded-full" style={{ ...delay, width: widths[i % 3][1] }} />
            </span>
            <span className="agent-skeleton h-3 w-11 shrink-0 rounded-full" style={delay} />
          </li>
        );
      })}
    </ul>
  );
}

// --- search_providers: a small browser working through the catalogs ---

const plus = (q: string) => encodeURIComponent(q).replace(/%20/g, "+");

// The catalogs Algebra reads, at the addresses it reads them from. The panel
// walks the same path: each catalog, then the matching and the network filter.
const CATALOG_SITES: { host: string; name: string; path: (q: string, net: string) => string }[] = [
  { host: "pay.sh", name: "Pay.sh", path: (q) => `/api/catalog?q=${plus(q)}` },
  {
    host: "api.circle.com",
    name: "Circle Agent Marketplace",
    path: (q, net) => `/v2/x402/discovery/resources?q=${plus(q)}&network=${net}`,
  },
  {
    host: "facilitator.payai.network",
    name: "PayAI",
    path: (q, net) => `/discovery/resources?q=${plus(q)}&network=${net}`,
  },
];

function CatalogBrowser({ hint, since }: { hint?: StepHint; since: number }) {
  const reduce = useReducedMotion();
  const tick = useTick(reduce ? 2400 : 900);
  const q = hint?.query ?? "";
  const devnet = hint?.network === "solana-devnet";
  const net = devnet ? "solana-devnet" : "solana";
  const site = CATALOG_SITES[tick % CATALOG_SITES.length];
  const afterPass = [
    q ? `Matching “${q}” across ${CATALOG_SITES.length} catalogs` : "Matching your request",
    `Keeping what can be paid on ${devnet ? "devnet" : "mainnet"}`,
    "Reading listed prices in USDC",
  ];
  const line = tick < CATALOG_SITES.length ? `Searching ${site.name}` : afterPass[(tick - CATALOG_SITES.length) % afterPass.length];

  return (
    <Panel line={line} since={since} badge={devnet ? "devnet" : "mainnet"}>
      <div className="flex items-center gap-2.5 px-3 py-2">
        <span className="flex gap-1" aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <span key={i} className="h-1.5 w-1.5 rounded-full bg-border-strong" />
          ))}
        </span>
        <div className="flex h-7 min-w-0 flex-1 items-center gap-2 rounded-lg border border-border bg-surface px-2.5">
          <IconLock size={12} className="shrink-0 text-muted" />
          <span className="block min-w-0 flex-1 overflow-hidden">
            <span key={site.host} className="agent-swap flex min-w-0 items-center gap-1.5">
              <ProviderLogo name={site.name} host={site.host} size={14} className="!border-0 !p-0" />
              <span className="truncate font-mono text-[0.72rem]">
                <span className="text-foreground">{site.host}</span>
                <span className="text-muted">{site.path(q, net)}</span>
              </span>
            </span>
          </span>
        </div>
      </div>
      {/* Restarts on every "navigation", like a real browser's bar. */}
      <div key={site.host + tick} className="agent-loadbar" aria-hidden="true" />
      <CatalogRail at={tick % CATALOG_SITES.length} visited={tick} />
      <SkeletonRows rows={3} thumb />
    </Panel>
  );
}

/**
 * Every catalog the search reads, as its own icon: the one being read lifts
 * and gets a ring, the ones already read carry a check, the rest wait dimmed.
 */
function CatalogRail({ at, visited }: { at: number; visited: number }) {
  return (
    <ul className="flex items-center gap-2 border-b border-border px-3 py-2.5" aria-hidden="true">
      {CATALOG_SITES.map((c, i) => {
        const current = i === at;
        const seen = visited >= CATALOG_SITES.length || i < at;
        return (
          <li
            key={c.host}
            className={`relative flex items-center gap-1.5 rounded-[9px] transition-[transform,opacity,filter] duration-300 ${
              current ? "-translate-y-0.5 scale-105 opacity-100" : seen ? "opacity-100" : "opacity-35 grayscale"
            }`}
          >
            <span className={`relative rounded-[9px] ${current ? "ring-2 ring-primary ring-offset-2 ring-offset-background" : ""}`}>
              <ProviderLogo name={c.name} host={c.host} size={26} />
              {seen && !current && (
                <span className="absolute -right-1 -bottom-1 flex h-3.5 w-3.5 items-center justify-center rounded-full border-2 border-background bg-success text-white">
                  <IconCheck size={7} strokeWidth={4} />
                </span>
              )}
            </span>
            <span className={`hidden text-[0.7rem] sm:inline ${current ? "text-foreground" : "text-muted"}`}>{c.name}</span>
          </li>
        );
      })}
    </ul>
  );
}

// --- checks and stages: a short list that works down itself ---

function Checklist({ items, every, since, verb }: { items: string[]; every: number; since: number; verb?: string }) {
  const tick = useTick(every);
  // Everything before the last item settles; the last stays in progress
  // until the real result replaces the whole panel.
  const at = Math.min(tick, items.length - 1);
  return (
    <Panel line={verb ? `${verb} ${items[at].charAt(0).toLowerCase()}${items[at].slice(1)}` : items[at]} since={since}>
      <ul className="space-y-2 px-3 py-3">
        {items.map((item, i) => {
          const state = i < at ? "done" : i === at ? "active" : "todo";
          return (
            <li key={item} className="flex items-center gap-2.5 text-sm">
              <span
                className={`flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded-full transition-colors duration-300 ${
                  state === "done" ? "bg-primary-tint text-primary" : state === "active" ? "text-primary" : "border border-border-strong"
                }`}
              >
                {state === "done" ? (
                  <motion.span initial={{ scale: 0.5, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} transition={{ duration: 0.25, ease: EASE }}>
                    <IconCheck size={11} strokeWidth={2.8} />
                  </motion.span>
                ) : state === "active" ? (
                  <span className="h-[14px] w-[14px] animate-spin rounded-full border-2 border-primary/20 border-t-primary motion-reduce:animate-none" />
                ) : null}
              </span>
              <span className={state === "todo" ? "text-muted" : state === "active" ? "agent-shimmer-text text-foreground" : "text-foreground"}>
                {item}
              </span>
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}

// --- everything else with a wait worth showing ---

/** What paying for a call goes through, in order. */
function payStages(network?: string) {
  return [
    "Asking the endpoint for its real price",
    "Checking it against your Spend Pass",
    `Paying in USDC on ${network === "solana-devnet" ? "devnet" : "mainnet"}`,
    "Calling the API with proof of payment",
    "Confirming the payment on-chain",
  ];
}

/**
 * The live panel under a running step. Fast steps show a shimmering line
 * only; the panel fades in if the step is still going after a beat.
 */
export function LiveActivity({ tool, hint, since }: { tool: string; hint?: StepHint; since: number }) {
  const settled = useAfter(350);
  const panel =
    tool === "search_providers" ? (
      <CatalogBrowser hint={hint} since={since} />
    ) : tool === "pay_and_call" || tool === "run_approved_intent" ? (
      <Checklist items={payStages(hint?.network)} every={1100} since={since} />
    ) : tool === "get_provider_endpoints" ? (
      <Checklist items={["Opening the provider's listing", "Reading its endpoints and prices", "Reading what each one expects"]} every={700} since={since} />
    ) : null;

  if (panel && settled) return panel;
  return <p className="agent-shimmer-text mt-0.5 text-sm text-muted">Working…</p>;
}

/** The agent is thinking between (or before) steps. */
export function ThinkingLine({ lines, className = "" }: { lines: string[]; className?: string }) {
  const reduce = useReducedMotion();
  const tick = useTick(reduce ? 3000 : 2200);
  return (
    <div className={`flex items-center gap-2.5 text-sm ${className}`} aria-live="polite">
      <Pulse />
      <span className="agent-shimmer-text shrink-0 font-medium text-foreground">Thinking</span>
      <Rotating text={lines[tick % lines.length]} className="text-muted" />
    </div>
  );
}
