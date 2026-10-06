"use client";

import { useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import type { ProviderCard, StepDetail, StepHint, StepStatus } from "@/lib/agent/events";
import { IconBan, IconCheck, IconChevronDown, IconClock, IconExternal, IconX } from "@/components/icons";
import { LiveActivity, Pulse, ThinkingLine } from "./live-activity";
import { StoreLogo } from "@/components/store-logo";
import { ProviderLogo } from "@/components/provider-logo";
import { networkLabel } from "@/lib/network";

export type Step = {
  id: string;
  tool: string;
  title: string;
  status: StepStatus;
  summary?: string;
  detail?: StepDetail;
  hint?: StepHint;
  startedAt: number;
  endedAt?: number;
};

const EASE = [0.16, 1, 0.3, 1] as const;

function Node({ status }: { status: StepStatus }) {
  const base = "relative z-10 flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full";
  switch (status) {
    case "running":
      return (
        <span className={`${base} bg-background`}>
          <span className="absolute inset-0 animate-spin rounded-full border-2 border-primary/20 border-t-primary" />
        </span>
      );
    case "done":
      return (
        <motion.span initial={{ scale: 0.6 }} animate={{ scale: 1 }} transition={{ type: "spring", stiffness: 500, damping: 22 }} className={`${base} bg-primary text-primary-tint`}>
          <IconCheck size={12} strokeWidth={2.8} />
        </motion.span>
      );
    case "waiting":
      return (
        <span className={`${base} bg-accent text-accent-tint`}>
          <IconClock size={13} strokeWidth={2.2} />
        </span>
      );
    case "blocked":
      return (
        <span className={`${base} bg-danger text-danger-tint`}>
          <IconBan size={12} strokeWidth={2.4} />
        </span>
      );
    default:
      return (
        <span className={`${base} border border-danger/50 bg-danger-tint text-danger`}>
          <IconX size={11} strokeWidth={2.6} />
        </span>
      );
  }
}

const PILL: Partial<Record<StepStatus, { label: string; cls: string }>> = {
  waiting: { label: "Needs you", cls: "bg-accent-tint text-accent" },
  blocked: { label: "Blocked", cls: "bg-danger-tint text-danger" },
  error: { label: "Failed", cls: "bg-danger-tint text-danger" },
};

/** One web listing, as chats saved before the Solana pivot show it. */
export type Listing = NonNullable<StepDetail["products"]>[number];

/** A follow-up the person can send with one tap: "use this provider", "call this endpoint". */
export type Ask = (text: string) => void;

export function StepRow({ step, last, onAsk }: { step: Step; last: boolean; onAsk?: Ask }) {
  // Open by default when there's something the user came for (listings) or
  // must act on (waiting/blocked); their own toggle wins after that.
  const [toggled, setToggled] = useState<boolean | null>(null);
  const hasDetail = !!step.detail && Object.values(step.detail).some((v) => (Array.isArray(v) ? v.length > 0 : typeof v === "string" && v.length > 0));
  const autoOpen =
    step.status === "waiting" ||
    step.status === "blocked" ||
    (step.detail?.providers?.length ?? 0) > 0 ||
    (step.detail?.products?.length ?? 0) > 0 ||
    !!step.detail?.response;
  const open = toggled ?? autoOpen;
  const pill = PILL[step.status];
  const secs = step.endedAt ? ((step.endedAt - step.startedAt) / 1000).toFixed(1) : null;

  return (
    <motion.li
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, ease: EASE }}
      className="relative flex gap-3.5 pb-4 last:pb-0"
    >
      {!last && <span aria-hidden="true" className="absolute top-[26px] bottom-0 left-[10.5px] w-px bg-border-strong" />}
      <Node status={step.status} />
      <div className="min-w-0 flex-1 pt-px">
        <button
          type="button"
          onClick={() => hasDetail && setToggled(!open)}
          aria-expanded={hasDetail ? open : undefined}
          className={`flex w-full items-center gap-2 text-left ${hasDetail ? "cursor-pointer" : "cursor-default"}`}
        >
          <span className={`text-[0.925rem] ${step.status === "running" ? "text-foreground" : "font-medium text-foreground"}`}>{step.title}</span>
          {pill && <span className={`rounded-full px-2 py-0.5 text-[0.7rem] font-medium ${pill.cls}`}>{pill.label}</span>}
          {hasDetail && <IconChevronDown size={15} className={`ml-auto shrink-0 text-muted transition-transform ${open ? "rotate-180" : ""}`} />}
        </button>
        {step.summary && step.status !== "running" && (
          <p className="mt-0.5 flex items-center gap-2 text-sm text-muted">
            <span className="truncate">{step.summary}</span>
            {secs && <span className="shrink-0 font-mono text-[0.7rem] text-muted/70 tabular-nums">{secs}s</span>}
          </p>
        )}
        {/* Unmounts the instant the result lands; the result rows animate in on their own. */}
        {step.status === "running" && <LiveActivity tool={step.tool} hint={step.hint} since={step.startedAt} />}
        <AnimatePresence initial={false}>
          {open && hasDetail && step.detail && (
            <motion.div
              initial={{ height: 0, opacity: 0 }}
              animate={{ height: "auto", opacity: 1 }}
              exit={{ height: 0, opacity: 0 }}
              transition={{ duration: 0.25, ease: EASE }}
              className="overflow-hidden"
            >
              <Detail detail={step.detail} onAsk={onAsk} />
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </motion.li>
  );
}

function Detail({ detail, onAsk }: { detail: StepDetail; onAsk?: Ask }) {
  const onPick = onAsk
    ? (p: Listing) => onAsk(`I'll take this one: "${p.title || p.name}" from ${p.merchant}${p.price ? `, listed at ${p.price}` : ""}.`)
    : undefined;
  return (
    <div className="mt-2.5 overflow-hidden rounded-xl border border-border bg-background/60 text-sm">
      {detail.providers && detail.providers.length > 0 && (
        <ul className="divide-y divide-border">
          {detail.providers.map((p) => (
            <ProviderRow key={p.id} p={p} onAsk={onAsk} />
          ))}
        </ul>
      )}
      {detail.endpoints && detail.endpoints.length > 0 && (
        <ul className="divide-y divide-border">
          {detail.endpoints.map((e) => (
            <li key={e.capability} className={`flex items-start gap-3 px-3.5 py-2.5 ${e.callable ? "" : "opacity-60"}`}>
              <span className="w-12 shrink-0 rounded-md bg-primary-tint px-1.5 py-0.5 text-center font-mono text-[0.68rem] font-medium text-primary">{e.method}</span>
              <div className="min-w-0 flex-1">
                <p className="truncate font-mono text-xs text-foreground" title={e.path}>
                  /{e.path}
                </p>
                {e.description && <p className="mt-0.5 line-clamp-2 text-xs text-muted">{e.description}</p>}
              </div>
              {e.price && <span className="shrink-0 font-mono text-xs whitespace-nowrap text-foreground tabular-nums">{e.price}</span>}
            </li>
          ))}
        </ul>
      )}
      {detail.response && (
        <div className="border-b border-border last:border-b-0">
          <p className="px-3.5 pt-2.5 text-xs font-medium text-muted">Response</p>
          <pre className="max-h-72 overflow-auto px-3.5 py-2 font-mono text-[0.72rem] leading-relaxed whitespace-pre-wrap text-foreground">{detail.response}</pre>
        </div>
      )}
      {detail.quotes && detail.quotes.length > 0 && (
        <ul className="divide-y divide-border">
          {detail.quotes.map((q, i) => (
            <li key={i} className="flex items-center justify-between gap-4 px-3.5 py-2.5">
              <StoreLogo store={q.merchant} size={32} />
              <div className="min-w-0 flex-1">
                <p className="font-medium text-foreground">{q.merchant}</p>
                <p className="truncate text-xs text-muted">{q.items}</p>
              </div>
              <div className="shrink-0 text-right">
                <p className="font-mono text-foreground tabular-nums">{q.total}</p>
                {q.eta && <p className="text-xs text-muted">{formatEta(q.eta)}</p>}
              </div>
            </li>
          ))}
        </ul>
      )}
      {detail.products && detail.products.length > 0 && (
        <ul className="divide-y divide-border">
          {detail.products.map((p, i) => (
            <ProductRow key={i} p={p} onPick={onPick} />
          ))}
        </ul>
      )}
      {detail.links && detail.links.length > 0 && (
        <ul className="divide-y divide-border">
          {detail.links.map((l, i) => (
            <li key={i}>
              <a href={l.url} target={l.url.startsWith("/") ? undefined : "_blank"} rel="noopener noreferrer" className="flex items-center justify-between gap-3 px-3.5 py-2.5 text-foreground hover:bg-primary-tint/50">
                <span className="truncate">{l.title}</span>
                <IconExternal size={14} className="shrink-0 text-muted" />
              </a>
            </li>
          ))}
        </ul>
      )}
      {detail.reasons && detail.reasons.length > 0 && (
        <ul className="space-y-1 px-3.5 py-2.5">
          {detail.reasons.map((r, i) => (
            <li key={i} className="text-foreground">
              {r}
            </li>
          ))}
        </ul>
      )}
      {detail.note && <p className="border-t border-border px-3.5 py-2 text-xs text-muted">{detail.note}</p>}
      {detail.rows && detail.rows.length > 0 && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 px-3.5 py-2.5">
          {detail.rows.map((r, i) => (
            <div key={i} className="contents">
              <dt className="text-muted">{r.label}</dt>
              <dd className="truncate font-mono text-foreground">{r.value}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

/** A provider from the catalogs: logo, what it does, listed price, and one tap to use it. */
function ProviderRow({ p, onAsk }: { p: ProviderCard; onAsk?: Ask }) {
  return (
    <li className="flex items-start gap-3 px-3.5 py-3 transition-colors hover:bg-primary-tint/30">
      <ProviderLogo name={p.name} logo={p.logo} website={p.website} host={p.host} fqn={p.fqn} size={36} className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <span className="font-medium text-foreground">{p.name}</span>
          <span className="text-xs text-muted">{p.catalog}</span>
          {p.networks?.map((n) => (
            <span key={n} className={`rounded-full px-1.5 py-px text-[0.65rem] font-medium ${n === "solana" ? "bg-primary-tint text-primary" : "bg-accent-tint text-accent"}`}>
              {networkLabel(n)}
            </span>
          ))}
        </p>
        {p.description && <p className="mt-0.5 line-clamp-2 text-xs leading-relaxed text-muted">{p.description}</p>}
        <p className="mt-1 truncate font-mono text-[0.68rem] text-muted/80">{p.id}</p>
      </div>
      <div className="flex shrink-0 flex-col items-end gap-1.5">
        {p.price && <span className="font-mono text-xs whitespace-nowrap text-foreground tabular-nums">{p.price}</span>}
        <div className="flex items-center gap-1.5">
          {onAsk && (
            <button
              type="button"
              onClick={() => onAsk(`Use ${p.name} (${p.id}).`)}
              className="inline-flex h-7 items-center rounded-lg bg-primary px-2.5 text-xs font-medium text-primary-tint transition-transform active:scale-95"
            >
              Use
            </button>
          )}
          {p.url && (
            <a
              href={p.url}
              target="_blank"
              rel="noopener noreferrer nofollow"
              aria-label={`${p.name} in its catalog`}
              className="grid h-7 w-7 place-items-center rounded-lg border border-border text-muted transition-colors hover:border-primary/50 hover:text-primary"
            >
              <IconExternal size={13} />
            </a>
          )}
        </div>
      </div>
    </li>
  );
}

/** A community code, one click to copy. Unverified, so it never looks like a price. */
function CodeChip({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard?.writeText(code).then(
          () => {
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1500);
          },
          () => {}
        );
      }}
      title="Copy code — it may have expired"
      className="inline-flex items-center gap-1 rounded-md border border-dashed border-accent/60 bg-accent-tint px-1.5 py-px font-mono text-[0.7rem] font-medium text-accent"
    >
      {copied ? "Copied" : code}
    </button>
  );
}

/** Where a community post lives, as an icon: reddit.com for r/…, the site's own otherwise. */
function tipIcon(merchant: string) {
  if (merchant.startsWith("r/") || merchant === "Reddit") return "reddit.com";
  if (merchant === "DesiDime") return "desidime.com";
  return merchant;
}

/** The product's own photo when the store publishes one, else the store's icon. */
function Thumb({ p }: { p: Listing }) {
  const [broken, setBroken] = useState(false);
  if (!p.image || broken) return <StoreLogo store={p.posted || p.code ? tipIcon(p.merchant) : p.merchant} size={44} />;
  return (
    // Store-published photo from hosts that vary, so a plain img that
    // falls back to the store's icon rather than next/image.
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src={p.image}
      alt=""
      loading="lazy"
      referrerPolicy="no-referrer"
      onError={() => setBroken(true)}
      className="h-11 w-11 shrink-0 rounded-xl border border-border bg-white object-contain p-0.5"
    />
  );
}

function ProductRow({ p, onPick }: { p: Listing; onPick?: (p: Listing) => void }) {
  // Not a single item, or likely a scam listing: nothing to pick.
  const pickable = !!onPick && !p.storePage && !p.warning;
  return (
    <li className="flex items-center gap-3 px-3.5 py-3 transition-colors hover:bg-primary-tint/30">
      <Thumb p={p} />
      <div className="min-w-0 flex-1">
        {p.url ? (
          <a href={p.url} target="_blank" rel="noopener noreferrer nofollow" className="line-clamp-2 text-foreground decoration-border-strong underline-offset-2 hover:underline">
            {p.name}
          </a>
        ) : (
          <p className="line-clamp-2 text-foreground">{p.name}</p>
        )}
        <p className="mt-1 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted">
          <span className="font-medium text-foreground/80">{p.merchant}</span>
          {p.eta && (
            <span
              className="inline-flex items-center gap-1"
              title={p.etaTypical ? "The store's usual delivery time — yours depends on your pincode" : "Delivery time the listing showed"}
            >
              <IconClock size={12} />
              {p.etaTypical ? `Typically ${p.eta}` : p.eta}
            </span>
          )}
          {p.posted && <span>{p.posted}</span>}
          {p.storePage && !p.code && !p.posted && <span className="rounded-md bg-background px-1.5 py-px">Store page</span>}
          {p.code && <CodeChip code={p.code} />}
        </p>
        {p.warning && <p className="mt-1 text-xs font-medium text-danger">{p.warning}</p>}
      </div>
      <div className="flex shrink-0 flex-col items-end gap-1.5">
        {p.price ? (
          <span className="font-mono text-foreground tabular-nums">{p.price}</span>
        ) : (
          <span className="text-xs text-muted">{p.storePage ? "Browse" : "See price"}</span>
        )}
        <div className="flex items-center gap-1.5">
          {pickable && (
            <button
              type="button"
              onClick={() => onPick?.(p)}
              className="inline-flex h-7 items-center rounded-lg bg-primary px-2.5 text-xs font-medium text-primary-tint transition-transform active:scale-95"
            >
              Select
            </button>
          )}
          {p.url && (
            <a
              href={p.url}
              target="_blank"
              rel="noopener noreferrer nofollow"
              aria-label={`Open on ${p.merchant}`}
              className="grid h-7 w-7 place-items-center rounded-lg border border-border text-muted transition-colors hover:border-primary/50 hover:text-primary"
            >
              <IconExternal size={13} />
            </a>
          )}
        </div>
      </div>
    </li>
  );
}

function formatEta(eta: string) {
  const d = new Date(eta);
  if (Number.isNaN(d.getTime())) return eta;
  const mins = Math.round((d.getTime() - Date.now()) / 60000);
  if (mins > 0 && mins < 120) return `~${mins} min`;
  return d.toLocaleString(undefined, { weekday: "short", hour: "numeric", minute: "2-digit" });
}

export function Timeline({
  steps,
  running,
  defaultOpen,
  onAsk,
}: {
  steps: Step[];
  running: boolean;
  defaultOpen: boolean;
  /** Present only while the user can act on this turn's results. */
  onAsk?: Ask;
}) {
  const [open, setOpen] = useState(defaultOpen);
  if (steps.length === 0) return null;
  const expanded = open || running;
  const done = steps.filter((s) => s.status === "done").length;
  const first = steps[0].startedAt;
  const last = steps[steps.length - 1].endedAt ?? steps[steps.length - 1].startedAt;
  // What the run came to: something waiting on the person, else the last step that found something.
  const outcome =
    steps.find((s) => s.status === "waiting" || s.status === "blocked") ??
    [...steps].reverse().find((s) => s.status !== "done" || (s.detail && Object.keys(s.detail).length > 0)) ??
    steps[steps.length - 1];
  const current = steps.find((s) => s.status === "running");

  return (
    <div className="rounded-2xl border border-border bg-surface">
      <button
        type="button"
        onClick={() => !running && setOpen((o) => !o)}
        aria-expanded={expanded}
        disabled={running}
        className="flex w-full items-center gap-2.5 px-4 py-3 text-left disabled:cursor-default"
      >
        {running && <Pulse size={16} />}
        <span className={`min-w-0 truncate text-sm font-medium text-foreground ${running ? "agent-shimmer-text" : "shrink-0"}`}>
          {running ? (current?.title ?? "Thinking") : `${steps.length} step${steps.length === 1 ? "" : "s"}`}
        </span>
        <span className={`truncate text-sm text-muted ${running ? "shrink-0" : ""}`}>
          {running ? `· ${done} done` : `· ${outcome.summary ?? outcome.title} · ${((last - first) / 1000).toFixed(1)}s`}
        </span>
        {!running && <IconChevronDown size={15} className={`ml-auto shrink-0 text-muted transition-transform ${expanded ? "rotate-180" : ""}`} />}
      </button>
      <AnimatePresence initial={false}>
        {expanded && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.28, ease: EASE }}
            className="overflow-hidden"
          >
            <ol className="border-t border-border px-4 pt-4 pb-4">
              {steps.map((s, i) => (
                <StepRow key={s.id} step={s} last={i === steps.length - 1 && !(running && !current)} onAsk={onAsk} />
              ))}
              {running && !current && (
                // Between steps the model is reading results — show that, not a frozen list.
                <motion.li initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.3, ease: EASE }} className="relative">
                  <ThinkingLine lines={["Going through what came back", "Checking it against what you asked for", "Deciding the next step"]} />
                </motion.li>
              )}
            </ol>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
