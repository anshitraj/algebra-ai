"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import * as api from "@/lib/api-client";
import type { IntelChange, IntelEndpoint, IntelEndpointDetail, IntelQuery, IntelStatus, Trust } from "@/lib/platform-types";
import { catalogName } from "@/lib/paysh";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { Button, ErrorNote, Input, PageHeader, Panel, Skeleton, timeAgo } from "@/components/console/ui";
import { IconBan, IconCheck, IconChevronDown, IconRefresh, IconSearch, IconShield, Spinner } from "@/components/icons";

const PAGE = 50;

const NETWORKS = [
  { id: "", label: "Any network" },
  { id: "solana", label: "Solana mainnet" },
  { id: "solana-devnet", label: "Solana devnet" },
  { id: "base", label: "Base" },
  { id: "sandbox", label: "Sandbox" },
];
const LEVELS = [
  { id: "", label: "All" },
  { id: "new", label: "New this week" },
  { id: "quoted", label: "Quoted" },
  { id: "tested", label: "Tested" },
  { id: "verified", label: "Verified" },
  { id: "executable", label: "Executable" },
  { id: "removed", label: "Removed" },
];
const SORTS = [
  { id: "", label: "Name" },
  { id: "cheapest", label: "Cheapest" },
  { id: "fastest", label: "Fastest (paid calls)" },
  { id: "newest", label: "Newest" },
  { id: "changed", label: "Recently changed" },
];
const LEVEL_COLUMNS: { key: keyof IntelEndpoint["levels"]; label: string; hint: string }[] = [
  { key: "listed", label: "Listed", hint: "A catalog lists it now" },
  { key: "quoted", label: "Quoted", hint: "An unpaid request returned payment terms" },
  { key: "tested", label: "Tested", hint: "Algebra paid it and it delivered" },
  { key: "verified", label: "Verified", hint: "Tested, paid to its pinned payee, never blocked since" },
  { key: "executable", label: "Executable", hint: "Paid over x402, callable, not blocked: the router can pay it where its network's rail runs" },
];

const sourceName = (s: string) => (s === "sandbox" ? "Algebra sandbox" : catalogName(s));

const select =
  "h-10 rounded-lg border border-border-strong bg-surface px-3 text-sm text-foreground focus-visible:border-primary";

export default function IntelligencePage() {
  const [status, setStatus] = useState<IntelStatus | null>(null);
  const [canManage, setCanManage] = useState(false);
  const [suspicious, setSuspicious] = useState<IntelChange[] | null>(null);
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<IntelQuery>({});
  const [rows, setRows] = useState<IntelEndpoint[] | null>(null);
  const [total, setTotal] = useState(0);
  const [open, setOpen] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);

  const loadStatus = useCallback(async () => {
    const [s, c] = await Promise.all([api.getIntelStatus(), api.listIntelChanges({ suspicious: true, limit: 6 })]);
    setStatus(s.status);
    setCanManage(s.can_manage);
    setSuspicious(c.changes);
    return s.status;
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetching from the REST API on mount
    loadStatus().catch((e) => setError(e instanceof Error ? e.message : "Couldn't load the registry"));
  }, [loadStatus]);

  useEffect(() => {
    const id = window.setTimeout(() => setFilter((f) => ({ ...f, q: q.trim() || undefined })), 250);
    return () => window.clearTimeout(id);
  }, [q]);

  useEffect(() => {
    let live = true;
    api
      .listIntelEndpoints({ ...filter, limit: PAGE })
      .then((r) => {
        if (!live) return;
        setRows(r.endpoints);
        setTotal(r.total);
        setError(null);
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "Couldn't load the registry"));
    return () => {
      live = false;
    };
  }, [filter]);

  async function loadMore() {
    setLoadingMore(true);
    try {
      const r = await api.listIntelEndpoints({ ...filter, limit: PAGE, offset: rows?.length ?? 0 });
      setRows((x) => [...(x ?? []), ...r.endpoints]);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't load more");
    } finally {
      setLoadingMore(false);
    }
  }

  // A refresh reads every catalog (a minute or more): poll until the job's lease is released.
  async function refresh() {
    setRefreshing(true);
    setError(null);
    try {
      await api.refreshIntel();
      const started = Date.now();
      for (;;) {
        await new Promise((r) => setTimeout(r, 4000));
        const s = await loadStatus();
        const leased = s.job?.lease_until && new Date(s.job.lease_until).getTime() > Date.now() && s.job.lease_owner;
        if ((!leased && Date.now() - started > 5000) || Date.now() - started > 10 * 60_000) break;
      }
      setFilter((f) => ({ ...f }));
    } catch (e) {
      setError(e instanceof Error ? e.message : "The refresh couldn't start");
    } finally {
      setRefreshing(false);
    }
  }

  const set = (patch: IntelQuery) => setFilter((f) => ({ ...f, ...patch }));
  const sources = useMemo(() => status?.sources.map((s) => s.name) ?? [], [status]);

  return (
    <div className="mx-auto max-w-6xl">
      <PageHeader
        title="Provider intelligence"
        description="Every paid endpoint the catalogs list, refreshed every six hours. A listing is not a recommendation: Algebra pays only endpoints that are executable, and refuses any payee other than the one it pinned for an endpoint."
        actions={
          canManage ? (
            <Button variant="secondary" onClick={refresh} disabled={refreshing}>
              {refreshing ? <Spinner size={14} /> : <IconRefresh size={15} />} {refreshing ? "Refreshing…" : "Refresh now"}
            </Button>
          ) : undefined
        }
      />

      {error && <div className="mt-6"><ErrorNote>{error}</ErrorNote></div>}
      <RefreshLine status={status} />
      <Summary status={status} />
      {suspicious && suspicious.length > 0 && <Suspicious changes={suspicious} />}

      <div className="mt-8 flex flex-col gap-3 lg:flex-row lg:items-center">
        <label className="relative flex-1">
          <span className="sr-only">Search the registry</span>
          <IconSearch size={16} className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Provider, capability or URL" className="pl-9" />
        </label>
        <div className="flex flex-wrap gap-2">
          <select aria-label="Network" className={select} value={filter.network ?? ""} onChange={(e) => set({ network: e.target.value || undefined })}>
            {NETWORKS.map((n) => <option key={n.id} value={n.id}>{n.label}</option>)}
          </select>
          <select aria-label="Catalog" className={select} value={filter.source ?? ""} onChange={(e) => set({ source: e.target.value || undefined })}>
            <option value="">Every catalog</option>
            {sources.map((s) => <option key={s} value={s}>{sourceName(s)}</option>)}
          </select>
          <select aria-label="Payment protocol" className={select} value={filter.protocol ?? ""} onChange={(e) => set({ protocol: e.target.value || undefined })}>
            <option value="">Any protocol</option>
            <option value="x402">x402</option>
            <option value="mpp">MPP</option>
            <option value="monid-balance">Monid balance</option>
          </select>
          <select aria-label="Trust" className={select} value={filter.trust ?? ""} onChange={(e) => set({ trust: e.target.value || undefined })}>
            <option value="">Any trust</option>
            <option value="verified">Verified</option>
            <option value="unverified">Unverified</option>
            <option value="blocked">Blocked</option>
          </select>
          <select aria-label="Sort" className={select} value={filter.sort ?? ""} onChange={(e) => set({ sort: e.target.value || undefined })}>
            {SORTS.map((s) => <option key={s.id} value={s.id}>{s.label}</option>)}
          </select>
        </div>
      </div>
      <div className="mt-3 flex flex-wrap gap-1.5" role="group" aria-label="What is known">
        {LEVELS.map((l) => (
          <button
            key={l.id || "all"}
            type="button"
            aria-pressed={(filter.level ?? "") === l.id}
            onClick={() => set({ level: l.id || undefined })}
            className={`inline-flex h-8 items-center rounded-full border px-3 text-xs transition-colors ${
              (filter.level ?? "") === l.id ? "border-primary bg-primary-tint text-foreground" : "border-border-strong text-foreground hover:border-foreground/35"
            }`}
          >
            {l.label}
          </button>
        ))}
      </div>

      <p className="mt-4 text-xs text-muted">
        {rows ? `${total.toLocaleString()} endpoint${total === 1 ? "" : "s"}` : "Loading…"} · Listed, Quoted, Tested, Verified and Executable are separate facts, each recorded from Algebra&apos;s own calls.
      </p>

      <div className="mt-3 overflow-x-auto rounded-xl border border-border bg-surface">
        <table className="w-full min-w-[880px] text-left text-sm">
          <thead className="border-b border-border text-xs text-muted">
            <tr>
              <th className="px-4 py-2.5 font-medium">Endpoint</th>
              <th className="px-3 py-2.5 font-medium">Network</th>
              <th className="px-3 py-2.5 text-right font-medium">Listed price</th>
              {LEVEL_COLUMNS.map((c) => (
                <th key={c.key} className="px-2 py-2.5 text-center font-medium" title={c.hint}>{c.label}</th>
              ))}
              <th className="px-3 py-2.5 font-medium">Trust</th>
              <th className="w-8" />
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {!rows && [0, 1, 2, 3, 4].map((i) => (
              <tr key={i}><td colSpan={10} className="px-4 py-3"><Skeleton className="h-8" /></td></tr>
            ))}
            {rows?.length === 0 && (
              <tr><td colSpan={10} className="px-4 py-12 text-center text-sm text-muted">
                {status && status.summary.endpoints === 0 ? "The registry is empty: the first refresh runs a minute after the API starts." : "Nothing matches these filters."}
              </td></tr>
            )}
            {rows?.map((e) => (
              <Row key={e.id} e={e} open={open === e.id} onToggle={() => setOpen(open === e.id ? null : e.id)} canManage={canManage}
                onChanged={(n) => { setRows((x) => x?.map((r) => (r.id === n.id ? n : r)) ?? null); loadStatus().catch(() => {}); }} />
            ))}
          </tbody>
        </table>
      </div>
      {rows && rows.length < total && (
        <div className="mt-4 flex justify-center">
          <Button variant="secondary" onClick={loadMore} disabled={loadingMore}>
            {loadingMore && <Spinner size={13} />} Show more ({(total - rows.length).toLocaleString()} left)
          </Button>
        </div>
      )}
      <p className="mt-6 text-xs leading-relaxed text-muted">
        {status?.discovery?.parallel_ai ? `Open-web discovery (Parallel AI): ${status.discovery.parallel_ai}.` : ""} A verified check means Algebra&apos;s checks passed on its own paid calls; it is not a guarantee about the provider. Executable says the router could pay it; mainnet payments stay off unless the operator enables them.
      </p>
    </div>
  );
}

function RefreshLine({ status }: { status: IntelStatus | null }) {
  if (!status) return <Skeleton className="mt-6 h-5 w-2/3" />;
  const last = status.runs?.find((r) => r.status !== "running");
  const running = status.runs?.find((r) => r.status === "running");
  return (
    <div className="mt-6 flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-muted">
      <span className="text-foreground">
        {running ? "Refreshing now…" : last ? `Last refresh ${timeAgo(last.finished_at ?? last.started_at)}: ${last.status}` : "Not refreshed yet"}
        {last && ` (${last.seen.toLocaleString()} seen, ${last.added} new, ${last.changed} changed, ${last.removed} removed)`}
      </span>
      {status.job?.next_run_at && <span>Next: {new Date(status.job.next_run_at).toUTCString().slice(17, 22)} UTC · schedule {status.schedule}</span>}
      {status.sources.map((s) => (
        <span key={s.name} className="inline-flex items-center gap-1.5" title={s.last_error || undefined}>
          <span className={`h-1.5 w-1.5 rounded-full ${s.last_error ? "bg-danger" : s.stale ? "bg-accent" : "bg-primary"}`} />
          {sourceName(s.name)}: {s.last_success_at ? `read ${timeAgo(s.last_success_at)}` : "not read yet"}
          {s.stale && s.last_success_at ? " (stale)" : ""}
        </span>
      ))}
    </div>
  );
}

function Summary({ status }: { status: IntelStatus | null }) {
  const s = status?.summary;
  const tiles = [
    { label: "Endpoints", value: s?.endpoints },
    { label: "Providers", value: s?.providers },
    { label: "New this week", value: s?.new_this_week },
    { label: "Quoted", value: s?.quoted },
    { label: "Tested", value: s?.tested },
    { label: "Verified", value: s?.by_trust?.verified ?? 0 },
    { label: "Executable", value: s?.executable },
    { label: "Blocked", value: s?.by_trust?.blocked ?? 0, danger: true },
  ];
  return (
    <div className="mt-5 grid grid-cols-2 gap-2.5 sm:grid-cols-4 lg:grid-cols-8">
      {tiles.map((t) => (
        <div key={t.label} className="rounded-lg border border-border bg-surface px-3.5 py-3">
          <p className="text-[0.6875rem] text-muted">{t.label}</p>
          <p className={`mt-1 font-mono text-lg tabular-nums ${t.danger && (t.value ?? 0) > 0 ? "text-danger" : "text-foreground"}`}>
            {t.value === undefined ? "—" : t.value.toLocaleString()}
          </p>
        </div>
      ))}
    </div>
  );
}

function Suspicious({ changes }: { changes: IntelChange[] }) {
  return (
    <Panel className="mt-5 border-danger/30 py-4">
      <p className="flex items-center gap-2 text-sm font-medium text-foreground"><IconShield size={16} className="text-danger" /> Suspicious changes</p>
      <ul className="mt-2 space-y-1.5 text-xs text-muted">
        {changes.map((c) => (
          <li key={c.id} className="flex flex-wrap gap-x-2">
            <span className="font-mono text-foreground">{c.provider_id}</span>
            <span>{describe(c)}</span>
            <span>· {timeAgo(c.created_at)}</span>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

function Tick({ on }: { on: boolean }) {
  return on ? <IconCheck size={15} className="mx-auto text-primary" aria-label="yes" /> : <span className="block text-center text-muted/60" aria-label="no">–</span>;
}

function TrustBadge({ e }: { e: IntelEndpoint }) {
  const tone = e.trust === "verified" ? "bg-primary-tint text-primary" : e.trust === "blocked" ? "bg-danger-tint text-danger" : "bg-border text-muted";
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap ${tone}`} title={e.trust_reason || undefined}>
      {e.trust === "verified" && <IconCheck size={12} />}
      {e.trust === "blocked" && <IconBan size={12} />}
      {e.trust}
    </span>
  );
}

function Row({ e, open, onToggle, canManage, onChanged }: { e: IntelEndpoint; open: boolean; onToggle: () => void; canManage: boolean; onChanged: (e: IntelEndpoint) => void }) {
  return (
    <>
      <tr className={`cursor-pointer align-top hover:bg-primary-tint/30 ${e.status === "removed" ? "opacity-60" : ""}`} onClick={onToggle}>
        <td className="max-w-[360px] px-4 py-3">
          <p className="truncate font-medium text-foreground">{e.provider_name || e.provider_id}</p>
          <p className="truncate font-mono text-[0.6875rem] text-muted" title={e.url}>{e.method} {e.url.replace(/^https?:\/\//, "")}</p>
          <p className="mt-0.5 text-[0.6875rem] text-muted">{sourceName(e.source)}{e.status !== "active" ? ` · ${e.status}` : ""}</p>
        </td>
        <td className="px-3 py-3 text-xs whitespace-nowrap text-foreground">{networkLabel(e.network) || "—"}<span className="block text-muted">{e.protocol}</span></td>
        <td className="px-3 py-3 text-right font-mono text-xs whitespace-nowrap text-foreground tabular-nums">
          {e.listed_price_minor > 0 ? formatUSDC(e.listed_price_minor) : "—"}
          {e.latency_ms ? <span className="block text-muted">{e.latency_ms} ms</span> : null}
        </td>
        {LEVEL_COLUMNS.map((c) => <td key={c.key} className="px-2 py-3"><Tick on={e.levels[c.key]} /></td>)}
        <td className="px-3 py-3"><TrustBadge e={e} /></td>
        <td className="px-2 py-3"><IconChevronDown size={16} className={`text-muted transition-transform ${open ? "rotate-180" : ""}`} /></td>
      </tr>
      {open && (
        <tr><td colSpan={10} className="bg-background px-4 py-4"><Detail id={e.id} canManage={canManage} onChanged={onChanged} /></td></tr>
      )}
    </>
  );
}

function Detail({ id, canManage, onChanged }: { id: string; canManage: boolean; onChanged: (e: IntelEndpoint) => void }) {
  const [d, setD] = useState<IntelEndpointDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState<Trust | null>(null);

  const load = useCallback(() => api.getIntelEndpoint(id).then(setD), [id]);
  useEffect(() => {
    load().catch((e) => setError(e instanceof Error ? e.message : "Couldn't load the endpoint"));
  }, [load]);

  async function decide(t: Trust) {
    setBusy(t);
    setError(null);
    try {
      const e = await api.setIntelTrust(id, t, note);
      onChanged(e);
      setNote("");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't record the decision");
    } finally {
      setBusy(null);
    }
  }

  if (error && !d) return <ErrorNote>{error}</ErrorNote>;
  if (!d) return <p className="flex items-center gap-2 text-xs text-muted"><Spinner size={13} /> Loading its history…</p>;
  const facts: [string, string | undefined][] = [
    ["Capability", d.capability],
    ["Pinned payee", d.pinned_pay_to || "not pinned yet (pinned on its first paid delivery)"],
    ["Payee it last named", d.pay_to],
    ["Trust reason", d.trust_reason],
    ["First seen", new Date(d.first_seen_at).toLocaleString()],
    ["Last seen in its catalog", new Date(d.last_seen_at).toLocaleString()],
    ["Quoted", d.quoted_at && new Date(d.quoted_at).toLocaleString()],
    ["Tested", d.tested_at && new Date(d.tested_at).toLocaleString()],
    ["Verified", d.verified_at && new Date(d.verified_at).toLocaleString()],
  ];
  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_1.2fr]">
      <div>
        <dl className="space-y-1.5 text-xs">
          {facts.filter(([, v]) => v).map(([k, v]) => (
            <div key={k} className="flex gap-3"><dt className="w-40 shrink-0 text-muted">{k}</dt><dd className="min-w-0 font-mono break-all text-foreground">{v}</dd></div>
          ))}
        </dl>
        {canManage ? (
          <div className="mt-4 rounded-lg border border-border bg-surface p-3">
            <p className="text-xs font-medium text-foreground">Your decision</p>
            <p className="mt-0.5 text-[0.6875rem] text-muted">Recorded with your name. Unblocking an endpoint blocked for a new payee accepts that payee.</p>
            <Input value={note} onChange={(ev) => setNote(ev.target.value)} placeholder="Why (optional)" className="mt-2" maxLength={300} />
            <div className="mt-2 flex flex-wrap gap-2">
              {d.trust !== "blocked" && <Button variant="danger" onClick={() => decide("blocked")} disabled={busy !== null}>{busy === "blocked" && <Spinner size={13} />} Block</Button>}
              {d.trust !== "unverified" && <Button variant="secondary" onClick={() => decide("unverified")} disabled={busy !== null}>{busy === "unverified" && <Spinner size={13} />} {d.trust === "blocked" ? "Unblock" : "Mark unverified"}</Button>}
              {d.trust !== "verified" && d.trust !== "blocked" && <Button variant="secondary" onClick={() => decide("verified")} disabled={busy !== null}>{busy === "verified" && <Spinner size={13} />} Mark verified</Button>}
            </div>
            {error && <div className="mt-2"><ErrorNote>{error}</ErrorNote></div>}
          </div>
        ) : (
          <p className="mt-4 text-[0.6875rem] text-muted">Only the operator can change an endpoint&apos;s trust on this server.</p>
        )}
      </div>
      <div>
        <p className="text-xs font-medium text-foreground">History</p>
        <ul className="mt-2 max-h-72 space-y-1.5 overflow-y-auto text-xs">
          {d.changes.length === 0 && <li className="text-muted">Nothing has changed since it was first listed.</li>}
          {d.changes.map((c) => (
            <li key={c.id} className={`rounded-md px-2.5 py-1.5 ${c.suspicious ? "bg-danger-tint text-danger" : "bg-surface text-foreground"}`}>
              <span className="font-medium">{describe(c)}</span>
              <span className="text-muted"> · {c.actor === "system" ? "Algebra" : c.actor} · {timeAgo(c.created_at)}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

function describe(c: IntelChange): string {
  const a = c.after ?? {};
  const b = c.before ?? {};
  switch (c.kind) {
    case "added": return "listed for the first time";
    case "removed": return "no longer listed by its catalog";
    case "restored": return "listed again";
    case "price_changed": return `listed price ${fmt(b.price_minor)} → ${fmt(a.price_minor)}`;
    case "payee_changed": {
      if (a.pinned) return `payee pinned: ${short(a.pay_to)}`;
      let [asked, pinned] = [short(a.pay_to), short(b.pinned_pay_to ?? b.pay_to)];
      if (asked === pinned) [asked, pinned] = [String(a.pay_to ?? "—"), String(b.pinned_pay_to ?? b.pay_to ?? "—")];
      return `asked to be paid at ${asked} (pinned ${pinned})`;
    }
    case "callable_changed": return a.callable ? "now callable" : "no longer callable";
    case "unreachable": return "couldn't be reached";
    case "trust_changed": return `trust → ${String(a.trust)}${a.reason ? ` (${String(a.reason)})` : ""}${a.note ? `: “${String(a.note)}”` : ""}`;
    default: return c.kind.replace(/_/g, " ");
  }
}

function fmt(v: unknown): string {
  return typeof v === "number" && v > 0 ? formatUSDC(v) : "—";
}

function short(v: unknown): string {
  const s = typeof v === "string" ? v : "";
  return s.length > 16 ? `${s.slice(0, 6)}…${s.slice(-6)}` : s || "—";
}
