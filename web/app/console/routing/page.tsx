"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import * as api from "@/lib/api-client";
import type { ClassDetail, ClassMember, ClassRow, EndpointHealth } from "@/lib/routing-types";
import { formatUSDC } from "@/lib/money";
import { catalogName } from "@/lib/paysh";
import { networkLabel } from "@/lib/network";
import { Button, ErrorNote, PageHeader, Panel, Skeleton } from "@/components/console/ui";
import { ProviderLogo } from "@/components/provider-logo";
import { IconArrowRight, IconGauge, IconRefresh, Spinner } from "@/components/icons";

/**
 * Routing: every catalog endpoint grouped by the work it does, so an agent asks
 * for "token.price" and Algebra compares every provider of it, live.
 */
export default function RoutingPage() {
  const [classes, setClasses] = useState<ClassRow[] | null>(null);
  const [selected, setSelected] = useState<string>("token.price");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .listClasses()
      .then((r) => setClasses(r.classes))
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load the classes"));
  }, []);

  const totals = useMemo(() => {
    const c = classes ?? [];
    return { classes: c.length, providers: c.reduce((n, x) => n + x.providers, 0), routable: c.reduce((n, x) => n + x.routable, 0) };
  }, [classes]);

  return (
    <div className="mx-auto max-w-5xl">
      <PageHeader
        title="Routing"
        description="Algebra groups every endpoint in Pay.sh, Circle and PayAI by the work it does. An agent asks for the work (token.price), not a provider: the router prices every provider of it live, skips the ones that are down or overcharge, and ranks the rest."
      />

      {error && <ErrorNote>{error}</ErrorNote>}

      <dl className="mt-6 grid grid-cols-3 gap-3">
        {[
          { label: "Classes of work", value: classes ? String(totals.classes) : "…" },
          { label: "Endpoints classified", value: classes ? String(totals.providers) : "…" },
          { label: "Routable with one input", value: classes ? String(totals.routable) : "…" },
        ].map((s) => (
          <div key={s.label} className="rounded-xl border border-border bg-surface px-4 py-3">
            <dt className="text-xs text-muted">{s.label}</dt>
            <dd className="font-display mt-1 text-lg font-semibold text-foreground tabular-nums">{s.value}</dd>
          </div>
        ))}
      </dl>

      <div className="mt-6 grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
        {!classes && !error && [0, 1, 2, 3, 4, 5].map((i) => <Skeleton key={i} className="h-24" />)}
        {classes?.map((c) => {
          const on = c.id === selected;
          return (
            <button
              key={c.id}
              type="button"
              onClick={() => setSelected(c.id)}
              aria-pressed={on}
              className={`rounded-xl border px-4 py-3 text-left transition-colors ${
                on ? "border-primary bg-primary-tint/50" : "border-border bg-surface hover:border-border-strong"
              }`}
            >
              <span className="flex items-baseline justify-between gap-2">
                <span className="truncate text-sm font-medium text-foreground">{c.title}</span>
                <span className="shrink-0 font-mono text-[0.7rem] text-muted">{c.id}</span>
              </span>
              <span className="mt-2 flex items-center gap-3 text-xs text-muted">
                <span>
                  <span className="font-medium text-foreground tabular-nums">{c.routable}</span> routable of {c.providers}
                </span>
                {c.median_price_minor > 0 && <span>usually {formatUSDC(c.median_price_minor)}</span>}
              </span>
            </button>
          );
        })}
      </div>

      {selected && <ClassPanel key={selected} id={selected} />}
    </div>
  );
}

function memberKey(provider: string, url: string) {
  return `${provider}|${url.replace(/\/+$/, "")}`;
}

function ClassPanel({ id }: { id: string }) {
  const [detail, setDetail] = useState<ClassDetail | null>(null);
  const [health, setHealth] = useState<EndpointHealth[]>([]);
  const [probing, setProbing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const r = await api.getClass(id);
      setDetail(r.class);
      setHealth(r.health ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't load the class");
    }
  }, [id]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetching from the REST API on mount
    load();
  }, [load]);

  async function probe() {
    setProbing(true);
    try {
      const r = await api.probeClass(id);
      setHealth(r.health);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The probe failed");
    } finally {
      setProbing(false);
    }
  }

  const byMember = useMemo(() => {
    const m = new Map<string, EndpointHealth>();
    for (const h of health) {
      const k = memberKey(h.provider, h.endpoint);
      const prev = m.get(k);
      if (!prev || prev.checked_at < h.checked_at) m.set(k, h);
    }
    return m;
  }, [health]);

  const sample = detail ? JSON.stringify(detail.sample) : "";

  return (
    <Panel className="mt-6">
      {error && <ErrorNote>{error}</ErrorNote>}
      {!detail && !error && <Skeleton className="h-40" />}
      {detail && (
        <>
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="min-w-0">
              <h2 className="font-display text-lg font-semibold text-foreground">{detail.title}</h2>
              <p className="mt-1 max-w-2xl text-sm text-muted">{detail.description}</p>
              <p className="mt-2 text-xs text-muted">
                Ask for it as <code className="rounded bg-primary-tint px-1.5 py-0.5 font-mono text-foreground">{detail.id}</code> with input{" "}
                <code className="rounded bg-primary-tint px-1.5 py-0.5 font-mono text-foreground">{sample}</code>
              </p>
            </div>
            <div className="flex shrink-0 gap-2">
              <Button variant="secondary" onClick={probe} disabled={probing} title="An unpaid request to each provider: no money moves">
                {probing ? <Spinner size={14} /> : <IconRefresh size={14} />} Probe now
              </Button>
              <Link
                href={`/console/firewall?class=${encodeURIComponent(detail.id)}`}
                className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-tint hover:opacity-90"
              >
                Would it pass? <IconArrowRight size={14} />
              </Link>
            </div>
          </div>

          <div className="mt-5 overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted">
                  <th className="py-2 pr-3 font-medium">Provider</th>
                  <th className="py-2 pr-3 font-medium">Listed</th>
                  <th className="py-2 pr-3 font-medium">Live 402</th>
                  <th className="py-2 pr-3 font-medium">Health</th>
                  <th className="py-2 pr-3 font-medium">Network</th>
                  <th className="py-2 font-medium">Router</th>
                </tr>
              </thead>
              <tbody>
                {detail.members.map((m, i) => (
                  <MemberRow key={`${m.provider}-${m.url}-${i}`} m={m} h={byMember.get(memberKey(m.provider, m.url))} median={detail.median_price_minor} />
                ))}
              </tbody>
            </table>
          </div>
          {detail.members.length === 0 && <p className="mt-4 text-sm text-muted">No catalog lists this kind of work yet.</p>}
          <p className="mt-4 flex items-center gap-1.5 text-xs text-muted">
            <IconGauge size={14} /> Health is a free probe every 15 minutes: the same unpaid request a quote is. A provider that asks more than its
            listing, or many times what its peers ask, is never paid.
          </p>
        </>
      )}
    </Panel>
  );
}

function MemberRow({ m, h, median }: { m: ClassMember; h?: EndpointHealth; median: number }) {
  const outlier = median > 0 && m.price_minor > 50_000 && m.price_minor > median * 10;
  return (
    <tr className="border-b border-border/60 align-top last:border-0">
      <td className="py-2.5 pr-3">
        <span className="flex items-center gap-2.5">
          <ProviderLogo name={m.provider_name} logo={m.logo_url} host={hostOf(m.url)} size={24} />
          <span className="min-w-0">
            <span className="block max-w-[220px] truncate text-foreground">{m.provider_name}</span>
            <span className="block max-w-[220px] truncate font-mono text-[0.7rem] text-muted">
              {catalogName(m.source)} · {m.method} {pathOf(m.url)}
            </span>
          </span>
        </span>
      </td>
      <td className="py-2.5 pr-3 tabular-nums text-foreground">
        {m.price_minor > 0 ? formatUSDC(m.price_minor) : <span className="text-muted">not stated</span>}
        {outlier && <Tag tone="danger">trap price</Tag>}
      </td>
      <td className="py-2.5 pr-3 tabular-nums">
        {h?.live_price_minor ? (
          <span className={h.overcharges ? "text-danger" : "text-foreground"}>
            {formatUSDC(h.live_price_minor)}
            {h.overcharges && <Tag tone="danger">over listing</Tag>}
          </span>
        ) : (
          <span className="text-muted">—</span>
        )}
      </td>
      <td className="py-2.5 pr-3">{h ? <HealthTag h={h} /> : <span className="text-xs text-muted">not probed</span>}</td>
      <td className="py-2.5 pr-3 text-xs text-muted">{(m.networks ?? []).map(networkLabel).join(", ") || "—"}</td>
      <td className="py-2.5 text-xs">
        {m.routable ? <span className="text-primary">routable</span> : <span className="text-muted" title={m.not_routable_reason}>{m.not_routable_reason || "listed only"}</span>}
      </td>
    </tr>
  );
}

function HealthTag({ h }: { h: EndpointHealth }) {
  const tone = h.status === "up" ? "ok" : h.status === "down" ? "danger" : "warn";
  const label = { up: "up", down: "down", input_rejected: "rejects input", unpayable: "can't pay" }[h.status];
  const uptime = h.checks > 0 ? Math.round((h.ups / h.checks) * 100) : 0;
  return (
    <span className="flex flex-col gap-0.5" title={h.error}>
      <Tag tone={tone}>{label}</Tag>
      <span className="text-[0.7rem] text-muted tabular-nums">
        {h.latency_ms} ms · {uptime}% of {h.checks}
      </span>
    </span>
  );
}

function Tag({ tone, children }: { tone: "ok" | "warn" | "danger"; children: React.ReactNode }) {
  const cls = { ok: "bg-primary-tint text-primary", warn: "bg-accent-tint text-accent", danger: "bg-danger-tint text-danger" }[tone];
  return <span className={`ml-1.5 inline-flex rounded-full px-2 py-0.5 text-[0.7rem] font-medium ${cls}`}>{children}</span>;
}

function hostOf(u: string) {
  try {
    return new URL(u).hostname;
  } catch {
    return undefined;
  }
}

function pathOf(u: string) {
  try {
    return new URL(u).pathname;
  } catch {
    return u;
  }
}
