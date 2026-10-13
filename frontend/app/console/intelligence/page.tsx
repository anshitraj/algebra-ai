"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import * as api from "@/lib/api-client";
import type { IntelChange, IntelEndpoint, IntelEndpointDetail, IntelQuery, IntelStatus, Trust } from "@/lib/platform-types";
import { catalogName } from "@/lib/paysh";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { Button, ErrorNote, Input, Skeleton, timeAgo } from "@/components/console/ui";
import { IconBan, IconCheck, IconChevronDown, IconRefresh, IconSearch, IconShield, IconSettings, IconX, Spinner } from "@/components/icons";
import styles from "./intelligence.module.css";

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
  { key: "tested", label: "Tested", hint: "Payment tested: Algebra paid it and it delivered. One transaction worked; it says nothing about who the seller is" },
  { key: "verified", label: "Verified", hint: "Identity verified: every check passed and a person reviewed evidence of who runs it. Never automatic" },
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
  const [showFilters, setShowFilters] = useState(false);

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
  const filterCount = [filter.network, filter.source, filter.protocol, filter.trust, filter.level].filter(Boolean).length;
  const hasFilters = Boolean(q || filterCount || filter.sort);
  const clearFilters = () => { setQ(""); setFilter({}); };

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>DIRECTORY</p>
          <h1>Provider intelligence</h1>
          <p className={styles.description}>Discover paid APIs. See what Algebra knows before your agent calls.</p>
        </div>
        {canManage && (
          <Button variant="secondary" onClick={refresh} disabled={refreshing}>
            {refreshing ? <Spinner size={14} /> : <IconRefresh size={15} />} {refreshing ? "Refreshing…" : "Refresh catalogs"}
          </Button>
        )}
      </header>

      {error && <div className="mt-6"><ErrorNote>{error}</ErrorNote></div>}
      <Summary status={status} />
      <RefreshLine status={status} />
      {suspicious && suspicious.length > 0 && <Suspicious changes={suspicious} />}

      <section className={styles.directory} aria-label="Endpoint directory">
      <div className={styles.toolbar}>
        <label className={styles.search}>
          <span className="sr-only">Search the registry</span>
          <IconSearch size={17} />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search providers, capabilities or URLs…" />
          {q && <button type="button" aria-label="Clear search" onClick={() => setQ("")}><IconX size={15} /></button>}
        </label>
        <button type="button" className={styles.filterButton} aria-expanded={showFilters} aria-controls="directory-filters" onClick={() => setShowFilters(!showFilters)}>
          <IconSettings size={15} /> Filters {filterCount > 0 && <span>{filterCount}</span>}
        </button>
        <label className={styles.sort}>
          <span>Sort by</span>
          <select aria-label="Sort" value={filter.sort ?? ""} onChange={(e) => set({ sort: e.target.value || undefined })}>
            {SORTS.map((s) => <option key={s.id} value={s.id}>{s.label}</option>)}
          </select>
        </label>
      </div>
      {showFilters && <div id="directory-filters" className={styles.filters}>
        <label><span>Network</span>
          <select aria-label="Network" className={select} value={filter.network ?? ""} onChange={(e) => set({ network: e.target.value || undefined })}>
            {NETWORKS.map((n) => <option key={n.id} value={n.id}>{n.label}</option>)}
          </select>
        </label>
        <label><span>Catalog</span>
          <select aria-label="Catalog" className={select} value={filter.source ?? ""} onChange={(e) => set({ source: e.target.value || undefined })}>
            <option value="">Every catalog</option>
            {sources.map((s) => <option key={s} value={s}>{sourceName(s)}</option>)}
          </select>
        </label>
        <label><span>Payment protocol</span>
          <select aria-label="Payment protocol" className={select} value={filter.protocol ?? ""} onChange={(e) => set({ protocol: e.target.value || undefined })}>
            <option value="">Any protocol</option>
            <option value="x402">x402</option>
            <option value="mpp">MPP</option>
            <option value="monid-balance">Monid balance</option>
          </select>
        </label>
        <label><span>Identity</span>
          <select aria-label="Trust" className={select} value={filter.trust ?? ""} onChange={(e) => set({ trust: e.target.value || undefined })}>
            <option value="">Any trust</option>
            <option value="verified">Verified</option>
            <option value="unverified">Unverified</option>
            <option value="blocked">Blocked</option>
          </select>
        </label>
        <label><span>Evidence</span>
          <select aria-label="Evidence" className={select} value={filter.level ?? ""} onChange={(e) => set({ level: e.target.value || undefined })}>
            {LEVELS.map((l) => <option key={l.id} value={l.id}>{l.label}</option>)}
          </select>
        </label>
      </div>}
      <div className={styles.results}>
        <p aria-live="polite">{rows ? <><strong>{total.toLocaleString()}</strong> endpoint{total === 1 ? "" : "s"}{filterCount || q ? " matching your search" : " across the catalogs"}</> : "Loading endpoints…"}</p>
        {hasFilters && <button type="button" onClick={clearFilters}>Reset filters <IconX size={12} /></button>}
      </div>
      <div className={styles.tableWrap}>
        <table className={styles.table}>
          <caption className="sr-only">Paid endpoints. Expand an endpoint to see its evidence and history.</caption>
          <thead>
            <tr>
              <th scope="col">Provider / endpoint</th>
              <th scope="col" className={styles.networkColumn}>Network</th>
              <th scope="col" className={styles.priceColumn}>Listed price</th>
              <th scope="col" className={styles.availabilityColumn}>Availability</th>
              <th scope="col">Identity</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {!rows && [0, 1, 2, 3, 4].map((i) => (
              <tr key={i}><td colSpan={5} className="px-4 py-3"><Skeleton className="h-8" /></td></tr>
            ))}
            {rows?.length === 0 && (
              <tr><td colSpan={5} className={styles.empty}>
                <IconSearch size={22} className="mx-auto mb-3 text-muted" />
                <p className="font-medium text-foreground">{status && status.summary.endpoints === 0 ? "No endpoints yet" : "No matching endpoints"}</p>
                <p className="mt-1 text-sm text-muted">{status && status.summary.endpoints === 0 ? "Endpoints will appear after the first catalog refresh." : "Try another search or adjust your filters."}</p>
                {hasFilters && <button type="button" className="mt-4 text-sm underline" onClick={clearFilters}>Clear search and filters</button>}
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
        <div className={styles.pagination}>
          <span>Showing {rows.length.toLocaleString()} of {total.toLocaleString()}</span>
          <Button variant="secondary" onClick={loadMore} disabled={loadingMore}>
            {loadingMore && <Spinner size={13} />} {loadingMore ? "Loading…" : "Load more endpoints"}
          </Button>
        </div>
      )}
      </section>
      <details className={styles.guide}>
        <summary><IconShield size={14} /> A listing is not a recommendation <IconChevronDown size={13} /></summary>
        <div>
          <p>Algebra pays only executable endpoints and refuses any payee other than the one pinned to an endpoint. Mainnet payments stay off unless the operator enables them.</p>
          <dl>
            <div><dt>Payment tested</dt><dd>Algebra paid the endpoint and it delivered. This proves one transaction worked; it does not verify the seller.</dd></div>
            <div><dt>Identity verified</dt><dd>Every automatic check passed and a person reviewed evidence of who runs the endpoint. Listings, quotes and payments never grant verification.</dd></div>
            <div><dt>Execution approved</dt><dd>Your Spend Pass and the router&apos;s guards decide approval for every request. Executable means the router could pay it, not that your request is approved.</dd></div>
          </dl>
          <p>Listed, quoted, tested, verified and executable are independent facts. None is a guarantee about a provider.</p>
          {status?.discovery?.parallel_ai && <p>Open-web discovery (Parallel AI): {status.discovery.parallel_ai}.</p>}
        </div>
      </details>
    </div>
  );
}

function RefreshLine({ status }: { status: IntelStatus | null }) {
  if (!status) return <Skeleton className="mt-4 h-4 w-48" />;
  const last = status.runs?.find((r) => r.status !== "running");
  const running = status.runs?.find((r) => r.status === "running");
  const issues = status.sources.filter((s) => s.last_error || s.stale).length;
  return (
    <details className={styles.sync}>
      <summary>
        {running ? <Spinner size={12} /> : <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${issues || last?.status === "failed" || last?.status === "partial" ? "bg-accent" : last ? "bg-success" : "bg-muted"}`} />}
        <span>{running ? "Refreshing catalogs…" : last ? `Updated ${timeAgo(last.finished_at ?? last.started_at)}` : "Waiting for the first refresh"}</span>
        <span className={styles.syncMeta}>{issues ? `${issues} catalog${issues === 1 ? "" : "s"} need attention` : `${status.sources.length} catalogs · every 6 hours`}</span>
        <IconChevronDown size={12} />
      </summary>
      <div className={styles.syncDetails}>
        <div className={styles.syncStats}>
          {last && <p>Last refresh: {last.status} · {last.seen.toLocaleString()} seen · {last.added} new · {last.changed} changed · {last.removed} removed</p>}
          {status.job?.next_run_at && <p>Next refresh: {new Date(status.job.next_run_at).toUTCString()} · Schedule: {status.schedule} (UTC)</p>}
          {last?.error && <p className="text-danger">{last.error}</p>}
        </div>
        <ul>
          {status.sources.map((s) => <li key={s.name}>
            <span>{sourceName(s.name)}</span>
            <span className={s.last_error ? "text-danger" : s.stale ? "text-accent" : "text-muted"}>{s.last_error || (s.last_success_at ? `${s.stale ? "Stale · " : ""}Read ${timeAgo(s.last_success_at)}` : "Not read yet")}</span>
          </li>)}
        </ul>
        <dl className={styles.evidenceStats}>
          {[["New this week", status.summary.new_this_week], ["Quoted", status.summary.quoted], ["Payment tested", status.summary.tested], ["Identity verified", status.summary.by_trust.verified ?? 0], ["Blocked", status.summary.by_trust.blocked ?? 0]].map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{Number(value).toLocaleString()}</dd></div>)}
        </dl>
      </div>
    </details>
  );
}

function Summary({ status }: { status: IntelStatus | null }) {
  const s = status?.summary;
  const tiles = [
    { label: "Cataloged endpoints", value: s?.endpoints, hint: "Across connected catalogs" },
    { label: "Providers", value: s?.providers, hint: "Distinct providers in the directory" },
    { label: "Executable endpoints", value: s?.executable, hint: "Subject to your Spend Pass" },
  ];
  return (
    <dl className={styles.summary}>
      {tiles.map((t) => (
        <div key={t.label}>
          <dt>{t.label}</dt>
          <dd>
            {t.value === undefined ? "—" : t.value.toLocaleString()}
          </dd>
          <p>{t.hint}</p>
        </div>
      ))}
    </dl>
  );
}

function Suspicious({ changes }: { changes: IntelChange[] }) {
  return (
    <details className={styles.security}>
      <summary>
        <IconShield size={17} className="shrink-0 text-danger" />
        <span><strong>Suspicious changes detected</strong><small>Review recent payee and trust changes.</small></span>
        <span className={styles.review}>Review <IconChevronDown size={14} /></span>
      </summary>
      <div className={styles.securityDetails}>
      <p className="mb-3 text-xs text-muted">Latest {changes.length} flagged change{changes.length === 1 ? "" : "s"}. A changed payee cannot receive payment while the endpoint is blocked.</p>
      <ul>
        {changes.map((c) => (
          <li key={c.id}>
            <span className="font-mono text-foreground">{c.provider_id}</span>
            <span>{describe(c)}</span>
            <span>· {timeAgo(c.created_at)}</span>
          </li>
        ))}
      </ul>
      </div>
    </details>
  );
}

function TrustBadge({ e }: { e: IntelEndpoint }) {
  const tone = e.trust === "verified" ? "bg-primary-tint text-primary" : e.trust === "blocked" ? "bg-danger-tint text-danger" : "text-muted";
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap ${tone}`} title={e.trust_reason || undefined}>
      {e.trust === "verified" && <IconCheck size={12} />}
      {e.trust === "blocked" && <IconBan size={12} />}
      {e.trust === "unverified" ? "Unverified" : e.trust === "verified" ? "Verified" : "Blocked"}
    </span>
  );
}

function availabilityLabel(e: IntelEndpoint) {
  if (e.trust === "blocked") return "Blocked";
  if (e.status === "removed") return "Removed";
  if (e.status === "unreachable") return "Unreachable";
  return e.levels.executable ? "Executable" : "Not executable";
}

function Row({ e, open, onToggle, canManage, onChanged }: { e: IntelEndpoint; open: boolean; onToggle: () => void; canManage: boolean; onChanged: (e: IntelEndpoint) => void }) {
  return (
    <>
      <tr className={`${styles.endpointRow} ${open ? styles.expanded : ""} ${e.status === "removed" ? "opacity-60" : ""}`}>
        <td>
          <button type="button" className={styles.endpointButton} onClick={onToggle} aria-expanded={open} aria-controls={`endpoint-${e.id}`}>
            <IconChevronDown size={14} className={`shrink-0 text-muted ${open ? "rotate-180" : ""}`} />
            <span>
              <strong title={e.provider_name || e.provider_id}>{e.provider_name || e.provider_id}</strong>
              <small title={`${e.method} ${e.url}`}>{sourceName(e.source)} <span>·</span> {e.capability || e.url.replace(/^https?:\/\//, "")}</small>
              <span className={styles.mobileMeta}>{networkLabel(e.network) || "—"} · {e.listed_price_minor > 0 ? formatUSDC(e.listed_price_minor) : "Price not listed"}<span className="block mt-1">{availabilityLabel(e)}</span></span>
            </span>
          </button>
        </td>
        <td className={styles.networkColumn}>{networkLabel(e.network) || "—"}<span className={styles.cellMeta}>{e.protocol}</span></td>
        <td className={styles.priceColumn}>
          {e.listed_price_minor > 0 ? formatUSDC(e.listed_price_minor) : "—"}
          {e.latency_ms ? <span className={styles.cellMeta}>{e.latency_ms} ms</span> : null}
        </td>
        <td className={styles.availabilityColumn}>
          <span className={styles.availability}><span className={`h-1.5 w-1.5 rounded-full ${e.trust === "blocked" ? "bg-danger" : e.status === "active" && e.levels.executable ? "bg-success" : "bg-border-strong"}`} />{availabilityLabel(e)}</span>
        </td>
        <td><TrustBadge e={e} /></td>
      </tr>
      {open && (
        <tr id={`endpoint-${e.id}`}><td colSpan={5} className={styles.detail}><Detail id={e.id} canManage={canManage} onChanged={onChanged} /></td></tr>
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
    ["Provider", d.provider_name || d.provider_id],
    ["Endpoint", `${d.method} ${d.url}`],
    ["Network / protocol", `${networkLabel(d.network) || d.network} · ${d.protocol}`],
    ["Listed price", d.listed_price_minor > 0 ? formatUSDC(d.listed_price_minor) : "Not listed"],
    ["Capability", d.capability],
    ["Pinned payee", d.pinned_pay_to || "not pinned yet (pinned on its first paid delivery)"],
    ["Payee it last named", d.pay_to],
    ["Trust reason", d.trust_reason],
    ["First seen", new Date(d.first_seen_at).toLocaleString()],
    ["Last seen in its catalog", new Date(d.last_seen_at).toLocaleString()],
    ["Quoted", d.quoted_at && new Date(d.quoted_at).toLocaleString()],
    ["Payment tested", d.tested_at && new Date(d.tested_at).toLocaleString()],
    ["Identity verified", d.verified_at && new Date(d.verified_at).toLocaleString()],
  ];
  const checks = d.verification ?? [];
  const unmet = checks.filter((c) => !c.passed);
  const evidenceOk = note.trim().length >= 20;
  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_1.2fr]">
      <div>
        <p className="mb-3 text-xs font-medium text-foreground">Endpoint evidence</p>
        <ul className={styles.evidence}>
          {LEVEL_COLUMNS.map((c) => <li key={c.key} title={c.hint}>
            {d.levels[c.key] ? <IconCheck size={13} className="text-success" /> : <span className="text-muted">–</span>}
            <span>{c.label}<span className="sr-only">: {d.levels[c.key] ? "yes" : "no"}. {c.hint}</span></span>
          </li>)}
        </ul>
        <dl className="space-y-1.5 text-xs">
          {facts.filter(([, v]) => v).map(([k, v]) => (
            <div key={k} className="flex flex-col gap-1 sm:flex-row sm:gap-3"><dt className="shrink-0 text-muted sm:w-40">{k}</dt><dd className="min-w-0 break-all text-foreground">{v}</dd></div>
          ))}
        </dl>
        {checks.length > 0 && (
          <div className="mt-4">
            <p className="text-xs font-medium text-foreground">Verification standard</p>
            <p className="mt-0.5 text-[0.6875rem] leading-relaxed text-muted">
              {d.trust === "verified" ? "Every check passed and a person reviewed who runs it." : unmet.length === 0 ? "Every automatic check passes. It is still unverified until a person reviews who runs it." : `${unmet.length} of ${checks.length} checks not met. It can't be verified yet.`}
            </p>
            <ul className="mt-2 space-y-1 text-xs">
              {checks.map((c) => (
                <li key={c.id} className="flex gap-2">
                  {c.passed ? <IconCheck size={13} className="mt-0.5 shrink-0 text-success" /> : <IconBan size={13} className="mt-0.5 shrink-0 text-muted" />}
                  <span className={c.passed ? "text-foreground" : "text-muted"}>
                    {c.label}
                    {c.detail && <span className="text-muted">: {c.detail}</span>}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}
        {canManage ? (
          <div className="mt-4 rounded-lg border border-border bg-surface p-3">
            <p className="text-xs font-medium text-foreground">Your decision</p>
            <p className="mt-0.5 text-[0.6875rem] leading-relaxed text-muted">Recorded with your name. Unblocking an endpoint blocked for a new payee accepts that payee, and it must be tested again. To verify, write the evidence you reviewed: who runs this endpoint and how you know.</p>
            <Input value={note} onChange={(ev) => setNote(ev.target.value)} placeholder="Why, or the evidence you reviewed (required to verify)" aria-label="Reason or evidence" className="mt-2" maxLength={1000} />
            <div className="mt-2 flex flex-wrap gap-2">
              {d.trust !== "blocked" && <Button variant="danger" onClick={() => decide("blocked")} disabled={busy !== null}>{busy === "blocked" && <Spinner size={13} />} Block</Button>}
              {d.trust !== "unverified" && <Button variant="secondary" onClick={() => decide("unverified")} disabled={busy !== null}>{busy === "unverified" && <Spinner size={13} />} {d.trust === "blocked" ? "Unblock" : "Mark unverified"}</Button>}
              {d.trust !== "verified" && d.trust !== "blocked" && <Button variant="secondary" onClick={() => decide("verified")} disabled={busy !== null || unmet.length > 0 || !evidenceOk} title={unmet.length > 0 ? "Every check of the standard must pass first" : !evidenceOk ? "Write the evidence you reviewed (at least 20 characters)" : undefined}>{busy === "verified" && <Spinner size={13} />} Mark verified</Button>}
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
