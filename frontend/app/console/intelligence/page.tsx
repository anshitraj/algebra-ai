"use client";

import Link from "next/link";
import { useCallback, useEffect, useId, useMemo, useState } from "react";
import * as api from "@/lib/api-client";
import type { IntelChange, IntelEndpoint, IntelEndpointDetail, IntelProviderFace, IntelQuery, IntelStatus, Trust } from "@/lib/platform-types";
import { catalogName } from "@/lib/paysh";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { ChainMark, chainName, hasChainMark } from "@/components/chain-logos";
import { providerTitle, shortHost } from "@/lib/provider-presentation";
import { ProviderLogo } from "@/components/provider-logo";
import { Button, ErrorNote, Input, Skeleton, timeAgo } from "@/components/console/ui";
import {
  IconArrowRight, IconArrowUp, IconBadgeCheck, IconBan, IconBolt, IconBook, IconCheck, IconChevronDown, IconCopy, IconInfo,
  IconPlus, IconRefresh, IconSearch, IconShield, IconShieldOutline, IconSliders, IconUsers, IconX, Spinner,
} from "@/components/icons";
import styles from "./intelligence.module.css";

const PAGE = 50;

const NETWORKS = [
  { id: "", label: "Any network" },
  { id: "solana", label: "Solana mainnet" },
  { id: "solana-devnet", label: "Solana devnet" },
  { id: "base", label: "Base" },
  { id: "base-sepolia", label: "Base Sepolia" },
  { id: "arc", label: "Arc" },
  { id: "arc-testnet", label: "Arc testnet" },
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
const PROTOCOLS = [
  { id: "", label: "Any protocol" },
  { id: "x402", label: "x402" },
  { id: "mpp", label: "MPP" },
  { id: "monid-balance", label: "Monid balance" },
];
const TRUSTS = [
  { id: "", label: "Any trust" },
  { id: "verified", label: "Verified" },
  { id: "unverified", label: "Unverified" },
  { id: "blocked", label: "Blocked" },
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
const networkName = (n: string) => (/^(base|arc)/.test(n) ? chainName(n) : networkLabel(n) || "—");
const labelOf = (list: { id: string; label: string }[], id: string) => list.find((x) => x.id === id)?.label ?? id;

const select =
  "h-10 rounded-lg border border-border-strong bg-surface px-3 text-sm text-foreground focus-visible:border-primary";

/** What a row shows for an endpoint: a readable provider name, where it answers, and its path. */
function present(e: IntelEndpoint, face?: IntelProviderFace) {
  let host = "";
  let path = "";
  try {
    const u = new URL(e.url);
    host = u.hostname;
    path = u.pathname === "/" ? "" : decodeURI(u.pathname);
  } catch {
    // A listing whose URL doesn't parse still shows by name.
  }
  const name = face?.name || e.provider_name || e.provider_id;
  const title = e.source === "sandbox" ? name : providerTitle({ name, host, description: face?.description ?? "" });
  return { title, host: e.source === "sandbox" ? "" : host, where: path || shortHost(host) };
}

export default function IntelligencePage() {
  const [status, setStatus] = useState<IntelStatus | null>(null);
  const [canManage, setCanManage] = useState(false);
  const [suspicious, setSuspicious] = useState<IntelChange[] | null>(null);
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<IntelQuery>({});
  const [rows, setRows] = useState<IntelEndpoint[] | null>(null);
  const [faces, setFaces] = useState<Record<string, IntelProviderFace>>({});
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
        setFaces((f) => ({ ...f, ...r.providers }));
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
      setFaces((f) => ({ ...f, ...r.providers }));
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
  // The filters in force, as chips: the order the filter panel lists them.
  const chips: { key: "level" | "trust" | "network" | "protocol" | "source"; label: string }[] = [];
  if (filter.level) chips.push({ key: "level", label: labelOf(LEVELS, filter.level) });
  if (filter.trust) chips.push({ key: "trust", label: labelOf(TRUSTS, filter.trust) });
  if (filter.network) chips.push({ key: "network", label: labelOf(NETWORKS, filter.network) });
  if (filter.protocol) chips.push({ key: "protocol", label: labelOf(PROTOCOLS, filter.protocol) });
  if (filter.source) chips.push({ key: "source", label: sourceName(filter.source) });
  const hasFilters = Boolean(q || chips.length || filter.sort);
  const clearFilters = () => { setQ(""); setFilter({}); };

  return (
    <div className={styles.page}>
      <h1 className="sr-only">Provider intelligence</h1>
      {error && <div className="mb-5"><ErrorNote>{error}</ErrorNote></div>}
      <Summary status={status} />
      <div className={styles.statusRow}>
        <RefreshLine status={status} />
        {canManage && (
          <button type="button" className={styles.ghostButton} onClick={refresh} disabled={refreshing}>
            {refreshing ? <Spinner size={13} /> : <IconRefresh size={14} />} {refreshing ? "Refreshing…" : "Refresh catalogs"}
          </button>
        )}
      </div>
      {suspicious && suspicious.length > 0 && <Suspicious changes={suspicious} />}

      <section className={styles.directory} aria-label="Endpoint directory">
        <div className={styles.toolbar}>
          <label className={styles.search}>
            <span className="sr-only">Search the registry</span>
            <IconSearch size={16} />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search providers, capabilities or URLs…" />
            {q && <button type="button" aria-label="Clear search" onClick={() => setQ("")}><IconX size={14} /></button>}
          </label>
          <button type="button" className={styles.filterButton} aria-expanded={showFilters} aria-controls="directory-filters" onClick={() => setShowFilters(!showFilters)}>
            <IconSliders size={16} /> Filters {chips.length > 0 && <span>{chips.length}</span>}
          </button>
          <label className={styles.sort}>
            <span className={styles.sortLabel}>Sort by</span>
            <select aria-label="Sort" value={filter.sort ?? ""} onChange={(e) => set({ sort: e.target.value || undefined })}>
              {SORTS.map((s) => <option key={s.id} value={s.id}>{s.label}</option>)}
            </select>
            <IconChevronDown size={14} aria-hidden="true" />
          </label>
        </div>

        <div className={styles.chipRow}>
          <ul className={styles.chips} aria-label="Active filters">
            {chips.map((c) => (
              <li key={c.key} className={styles.chip}>
                <span>{c.label}</span>
                <button type="button" aria-label={`Remove filter: ${c.label}`} onClick={() => set({ [c.key]: undefined })}><IconX size={12} /></button>
              </li>
            ))}
            <li>
              <button type="button" className={styles.addChip} aria-expanded={showFilters} aria-controls="directory-filters" onClick={() => setShowFilters(true)}>
                <IconPlus size={13} /> Add filter
              </button>
            </li>
            {hasFilters && <li><button type="button" className={styles.resetChip} onClick={clearFilters}>Reset</button></li>}
          </ul>
          <p aria-live="polite" className={styles.count}>
            {rows ? <><strong>{total.toLocaleString()}</strong> endpoint{total === 1 ? "" : "s"}{chips.length || q ? " match" : " across the catalogs"}</> : "Loading endpoints…"}
          </p>
        </div>

        {showFilters && <div id="directory-filters" className={styles.filters}>
          <label><span>Evidence</span>
            <select aria-label="Evidence" className={select} value={filter.level ?? ""} onChange={(e) => set({ level: e.target.value || undefined })}>
              {LEVELS.map((l) => <option key={l.id} value={l.id}>{l.label}</option>)}
            </select>
          </label>
          <label><span>Identity</span>
            <select aria-label="Trust" className={select} value={filter.trust ?? ""} onChange={(e) => set({ trust: e.target.value || undefined })}>
              {TRUSTS.map((t) => <option key={t.id} value={t.id}>{t.label}</option>)}
            </select>
          </label>
          <label><span>Network</span>
            <select aria-label="Network" className={select} value={filter.network ?? ""} onChange={(e) => set({ network: e.target.value || undefined })}>
              {NETWORKS.map((n) => <option key={n.id} value={n.id}>{n.label}</option>)}
            </select>
          </label>
          <label><span>Payment protocol</span>
            <select aria-label="Payment protocol" className={select} value={filter.protocol ?? ""} onChange={(e) => set({ protocol: e.target.value || undefined })}>
              {PROTOCOLS.map((p) => <option key={p.id} value={p.id}>{p.label}</option>)}
            </select>
          </label>
          <label><span>Catalog</span>
            <select aria-label="Catalog" className={select} value={filter.source ?? ""} onChange={(e) => set({ source: e.target.value || undefined })}>
              <option value="">Every catalog</option>
              {sources.map((s) => <option key={s} value={s}>{sourceName(s)}</option>)}
            </select>
          </label>
        </div>}

        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <caption className="sr-only">Paid endpoints. Expand an endpoint to see its evidence and history.</caption>
            <thead>
              <tr>
                <th scope="col">Provider / endpoint</th>
                <th scope="col" className={styles.wideOnly}>Network</th>
                <th scope="col" className={styles.wideOnly}>Price</th>
                <th scope="col" className={styles.wideOnly}>Availability</th>
                <th scope="col">Identity</th>
                <th scope="col" className={styles.wideOnly}>
                  <span className={styles.thHint}>
                    Evidence
                    <span tabIndex={0} title="Five separate facts: listed, quoted, payment tested, identity verified, executable. Hover a row's bars for which ones." className={styles.info}>
                      <IconInfo size={13} />
                      <span className="sr-only">Five separate facts: listed, quoted, payment tested, identity verified, executable.</span>
                    </span>
                  </span>
                </th>
                <th scope="col" className={styles.actionColumn}><span className="sr-only">Provider page</span></th>
              </tr>
            </thead>
            <tbody>
              {!rows && [0, 1, 2, 3, 4].map((i) => (
                <tr key={i}><td colSpan={7} className="px-5 py-4"><Skeleton className="h-9" /></td></tr>
              ))}
              {rows?.length === 0 && (
                <tr><td colSpan={7} className={styles.empty}>
                  <IconSearch size={22} className="mx-auto mb-3 text-muted" />
                  <p className="font-medium text-foreground">{status && status.summary.endpoints === 0 ? "No endpoints yet" : "No matching endpoints"}</p>
                  <p className="mt-1 text-sm text-muted">{status && status.summary.endpoints === 0 ? "Endpoints will appear after the first catalog refresh." : "Try another search or adjust your filters."}</p>
                  {hasFilters && <button type="button" className="mt-4 text-sm underline underline-offset-4" onClick={clearFilters}>Clear search and filters</button>}
                </td></tr>
              )}
              {rows?.map((e) => (
                <Row key={e.id} e={e} face={faces[e.provider_id]} open={open === e.id} onToggle={() => setOpen(open === e.id ? null : e.id)} canManage={canManage}
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
          <p>Listed, quoted, tested, verified and executable are independent facts. None is a guarantee about a provider. Names and logos come from the catalogs, or from the endpoint&apos;s address when a catalog gives none; they imply no affiliation.</p>
          {status?.discovery?.parallel_ai && <p>Open-web discovery (Parallel AI): {status.discovery.parallel_ai}.</p>}
        </div>
      </details>
    </div>
  );
}

function RefreshLine({ status }: { status: IntelStatus | null }) {
  if (!status) return <Skeleton className="h-4 w-48" />;
  const last = status.runs?.find((r) => r.status !== "running");
  const running = status.runs?.find((r) => r.status === "running");
  const issues = status.sources.filter((s) => s.last_error || s.stale).length;
  const tone = issues || last?.status === "failed" || last?.status === "partial" ? styles.dotWarn : last ? styles.dotOk : styles.dotIdle;
  return (
    <details className={styles.sync}>
      <summary>
        {running ? <Spinner size={12} /> : <span className={`${styles.dot} ${tone}`} />}
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
            <span className={s.last_error ? "text-danger" : "text-muted"}>{s.last_error || (s.last_success_at ? `${s.stale ? "Stale · " : ""}Read ${timeAgo(s.last_success_at)}` : "Not read yet")}</span>
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
  // Each finished refresh read this many listings: the catalogs' size over time.
  const history = (status?.runs ?? []).filter((r) => r.status !== "running" && r.seen > 0).slice().reverse().map((r) => r.seen);
  const share = s && s.endpoints > 0 ? s.executable / s.endpoints : 0;
  const catalogs = status?.sources.length ?? 0;
  const bySource = Object.entries(s?.by_source ?? {}).sort((a, b) => b[1] - a[1]);
  return (
    <div className={styles.summary}>
      <Stat icon={<IconBook size={19} />} tone="violet" label="Cataloged endpoints" value={s?.endpoints}
        delta={s && s.new_this_week > 0 ? `${s.new_this_week.toLocaleString()} new` : undefined}
        hint={catalogs ? `Across ${catalogs} catalogs` : "Across the catalogs"}
        visual={history.length > 1 ? <Sparkline values={history} label={`Listings read by the last ${history.length} refreshes`} /> : null} />
      <Stat icon={<IconUsers size={19} />} tone="blue" label="Providers" value={s?.providers}
        hint="Distinct in the directory"
        visual={bySource.length > 1 ? <SourceBars entries={bySource} /> : null} />
      <Stat icon={<IconBolt size={19} />} tone="green" label="Executable endpoints" value={s?.executable}
        note={s && s.endpoints > 0 ? `${Math.round(share * 100)}% of all` : undefined}
        hint="Subject to your Spend Pass"
        visual={s && s.endpoints > 0 ? <ShareTicks share={share} /> : null} />
    </div>
  );
}

function Stat({ icon, tone, label, value, delta, note, hint, visual }: { icon: React.ReactNode; tone: "violet" | "blue" | "green"; label: string; value?: number; delta?: string; note?: string; hint: string; visual: React.ReactNode }) {
  return (
    <div className={styles.stat}>
      <span className={`${styles.statIcon} ${styles[tone]}`} aria-hidden="true">{icon}</span>
      <p className={styles.statLabel}>{label}</p>
      <div className={styles.statBody}>
        <div className={styles.statLine}>
          {value === undefined ? <Skeleton className="h-8 w-24" /> : <span className={styles.statValue}>{value.toLocaleString()}</span>}
          {delta && <span className={styles.delta}><IconArrowUp size={12} />{delta}</span>}
          {note && <span className={styles.delta}>{note}</span>}
        </div>
        <p className={styles.statHint}>{hint}</p>
      </div>
      {visual && <div className={styles.statVisual}>{visual}</div>}
    </div>
  );
}

function Sparkline({ values, label }: { values: number[]; label: string }) {
  const id = useId();
  const [w, h, pad] = [120, 44, 4];
  const min = Math.min(...values);
  const span = Math.max(...values) - min || 1;
  const pts = values.map((v, i) => [(i / (values.length - 1)) * w, pad + (1 - (v - min) / span) * (h - pad * 2)] as const);
  const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
  const [lx, ly] = pts[pts.length - 1];
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width={w} height={h} role="img" aria-label={`${label}: ${values.map((v) => v.toLocaleString()).join(", ")}`} className={styles.spark}>
      <defs>
        <linearGradient id={id} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor="currentColor" stopOpacity=".2" />
          <stop offset="1" stopColor="currentColor" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={`${line} L${w} ${h} L0 ${h}Z`} fill={`url(#${id})`} />
      <path d={line} fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
      <circle cx={lx} cy={ly} r="2.6" fill="currentColor" />
    </svg>
  );
}

function SourceBars({ entries }: { entries: [string, number][] }) {
  const max = Math.max(...entries.map(([, n]) => n)) || 1;
  return (
    <div className={styles.sourceBars} role="img" aria-label={`Endpoints by catalog: ${entries.map(([s, n]) => `${sourceName(s)} ${n.toLocaleString()}`).join(", ")}`}>
      {entries.slice(0, 7).map(([s, n]) => (
        <span key={s} title={`${sourceName(s)}: ${n.toLocaleString()} endpoints`} style={{ height: `${Math.max(8, (n / max) * 100)}%` }} />
      ))}
    </div>
  );
}

function ShareTicks({ share }: { share: number }) {
  const lit = Math.round(share * 20);
  return (
    <div className={styles.ticks} role="img" aria-label={`${Math.round(share * 100)}% of cataloged endpoints are executable`}>
      {Array.from({ length: 20 }, (_, i) => <span key={i} data-on={i < lit || undefined} />)}
    </div>
  );
}

function Suspicious({ changes }: { changes: IntelChange[] }) {
  return (
    <details className={styles.security}>
      <summary>
        <span className={styles.securityIcon} aria-hidden="true"><IconShield size={19} /></span>
        <span className={styles.securityText}><strong>Suspicious changes detected</strong><small>Review recent payee and trust changes across your connected catalogs.</small></span>
        <span className={styles.flagCount}>{changes.length} flagged</span>
        <span className={styles.review}>Review <IconArrowRight size={14} /></span>
      </summary>
      <div className={styles.securityDetails}>
        <p>Latest {changes.length} flagged change{changes.length === 1 ? "" : "s"}. A changed payee cannot receive payment while the endpoint is blocked.</p>
        <ul>
          {changes.map((c) => (
            <li key={c.id}>
              <span className={styles.mono} title={c.provider_id}>{shortID(c.provider_id)}</span>
              <span>{describe(c)}</span>
              <span>{timeAgo(c.created_at)}</span>
            </li>
          ))}
        </ul>
      </div>
    </details>
  );
}

function AvailabilityPill({ e }: { e: IntelEndpoint }) {
  const [label, tone] =
    e.trust === "blocked" ? ["Blocked", styles.red]
    : e.status === "removed" ? ["Removed", styles.neutral]
    : e.status === "unreachable" ? ["Unreachable", styles.amber]
    : e.levels.executable ? ["Executable", styles.green]
    : ["Not executable", styles.neutral];
  return <span className={`${styles.pill} ${tone}`}><span className={styles.pillDot} />{label}</span>;
}

function IdentityPill({ e }: { e: IntelEndpoint }) {
  if (e.trust === "verified") return <span className={`${styles.pill} ${styles.green}`} title={e.trust_reason || undefined}><IconBadgeCheck size={14} />Verified</span>;
  if (e.trust === "blocked") return <span className={`${styles.pill} ${styles.red}`} title={e.trust_reason || undefined}><IconBan size={13} />Blocked</span>;
  return <span className={`${styles.pill} ${styles.quiet}`} title={e.trust_reason || "No person has reviewed who runs it"}><IconShieldOutline size={13} />Unverified</span>;
}

function EvidenceMeter({ levels }: { levels: IntelEndpoint["levels"] }) {
  const met = LEVEL_COLUMNS.filter((c) => levels[c.key]);
  const title = LEVEL_COLUMNS.map((c) => `${c.label}: ${levels[c.key] ? "yes" : "no"}`).join("\n");
  return (
    <span className={styles.meter} title={title}>
      <span className={styles.meterBars} aria-hidden="true">
        {LEVEL_COLUMNS.map((c, i) => <i key={c.key} data-on={levels[c.key] || undefined} style={{ height: 7 + i * 2.5 }} />)}
      </span>
      <span className="tabular-nums">{met.length}/5</span>
      <span className="sr-only">: {met.length ? met.map((c) => c.label.toLowerCase()).join(", ") : "nothing known yet"}</span>
    </span>
  );
}

function CopyURL({ url }: { url: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const id = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(id);
  }, [copied]);
  return (
    <button type="button" className={styles.copy} aria-label={copied ? "Endpoint URL copied" : "Copy endpoint URL"} title={copied ? "Copied" : "Copy endpoint URL"}
      onClick={() => navigator.clipboard?.writeText(url).then(() => setCopied(true), () => {})}>
      {copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
    </button>
  );
}

function Row({ e, face, open, onToggle, canManage, onChanged }: { e: IntelEndpoint; face?: IntelProviderFace; open: boolean; onToggle: () => void; canManage: boolean; onChanged: (e: IntelEndpoint) => void }) {
  const p = present(e, face);
  return (
    <>
      <tr className={`${styles.endpointRow} ${open ? styles.expanded : ""} ${e.status === "removed" ? styles.removed : ""}`}>
        <td>
          <div className={styles.endpointCell}>
            <button type="button" className={styles.endpointButton} onClick={onToggle} aria-expanded={open} aria-controls={`endpoint-${e.id}`}>
              <IconChevronDown size={15} className={`${styles.chevron} ${open ? styles.chevronOpen : ""}`} />
              <ProviderLogo name={p.title} logo={face?.logo_url} website={face?.website} host={p.host || undefined} fqn={face?.fqn} size={36} />
              <span className={styles.endpointText}>
                <strong title={p.title === (face?.name || e.provider_name) ? undefined : `Listed as ${face?.name || e.provider_name || e.provider_id}`}>{p.title}</strong>
                <small title={`${e.method} ${e.url}`}>{sourceName(e.source)}<span aria-hidden="true">·</span><span className={styles.mono}>{p.where}</span></small>
                <span className={styles.mobileMeta}>{networkName(e.network)} · {e.listed_price_minor > 0 ? formatUSDC(e.listed_price_minor) : "Price not listed"}</span>
              </span>
            </button>
            <CopyURL url={e.url} />
          </div>
        </td>
        <td className={styles.wideOnly}>
          <span className={styles.network}>{hasChainMark(e.network) ? <ChainMark network={e.network} size={13} /> : <span className={`${styles.dot} ${styles[`net_${e.network.replace(/[^a-z]/g, "")}`] ?? styles.dotIdle}`} />}{networkName(e.network)}</span>
          <span className={styles.cellMeta}>{e.protocol}</span>
        </td>
        <td className={styles.wideOnly}>
          <span className={styles.price}>{e.listed_price_minor > 0 ? formatUSDC(e.listed_price_minor) : "—"}</span>
          <span className={styles.cellMeta}>{e.latency_ms ? `${e.latency_ms} ms` : "per call"}</span>
        </td>
        <td className={styles.wideOnly}><AvailabilityPill e={e} /></td>
        <td><IdentityPill e={e} /></td>
        <td className={styles.wideOnly}><EvidenceMeter levels={e.levels} /></td>
        <td className={styles.actionColumn}>
          {face && (
            <Link href={`/console/providers/${encodeURIComponent(e.provider_id)}`} className={styles.rowLink} aria-label={`Open ${p.title}'s provider page`} title="Provider page">
              <IconArrowRight size={15} />
            </Link>
          )}
        </td>
      </tr>
      {open && (
        <tr id={`endpoint-${e.id}`}><td colSpan={7} className={styles.detail}><Detail id={e.id} canManage={canManage} onChanged={onChanged} /></td></tr>
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
    ["Network / protocol", `${networkName(d.network)} · ${d.protocol}`],
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
    <div className="grid gap-8 lg:grid-cols-[1fr_1.1fr]">
      <div>
        <p className={styles.detailHeading}>Endpoint evidence</p>
        <ul className={styles.evidence}>
          {LEVEL_COLUMNS.map((c) => <li key={c.key} title={c.hint} data-on={d.levels[c.key] || undefined}>
            {d.levels[c.key] ? <IconCheck size={13} /> : <span aria-hidden="true" className={styles.evidenceOff} />}
            <span>{c.label}<span className="sr-only">: {d.levels[c.key] ? "yes" : "no"}. {c.hint}</span></span>
          </li>)}
        </ul>
        <dl className={styles.facts}>
          {facts.filter(([, v]) => v).map(([k, v]) => (
            <div key={k}><dt>{k}</dt><dd>{v}</dd></div>
          ))}
        </dl>
        {checks.length > 0 && (
          <div className="mt-5">
            <p className={styles.detailHeading}>Verification standard</p>
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
          <div className="mt-5 rounded-xl border border-border bg-surface p-4">
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
          <p className="mt-5 text-[0.6875rem] text-muted">Only the operator can change an endpoint&apos;s trust on this server.</p>
        )}
      </div>
      <div>
        <p className={styles.detailHeading}>History</p>
        <ul className={styles.history}>
          {d.changes.length === 0 && <li className="text-muted">Nothing has changed since it was first listed.</li>}
          {d.changes.map((c) => (
            <li key={c.id} data-suspicious={c.suspicious || undefined}>
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

/** "payai:01a046b2-…-xai-org.vercel.app" → "payai:01a046…vercel.app": IDs stay recognisable without filling a line. */
function shortID(id: string): string {
  return id.length > 36 ? `${id.slice(0, 14)}…${id.slice(-14)}` : id;
}
