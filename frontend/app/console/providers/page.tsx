"use client";

import { useEffect, useMemo, useState } from "react";
import * as api from "@/lib/api-client";
import type { ProviderDetail, ProviderEndpoint, ProviderListing, ProviderSummary } from "@/lib/types";
import { CATALOGS, catalogName, categoryLabel } from "@/lib/paysh";
import { formatEndpointPrice, formatPriceRange, formatUSDC } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { Button, ErrorNote, Input, PageHeader, Panel, Skeleton } from "@/components/console/ui";
import { Copy, Snippet } from "@/components/console/snippet";
import { ProviderLogo } from "@/components/provider-logo";
import { IconChevronDown, IconExternal, IconSearch, Spinner } from "@/components/icons";

const SOURCES = [{ id: "", name: "All catalogs" }, ...CATALOGS.map((c) => ({ id: c.id, name: c.name }))];
const PAGE = 100;

export default function ProvidersPage() {
  const { network } = useNetwork();
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [source, setSource] = useState("");
  const [category, setCategory] = useState("");
  const [listing, setListing] = useState<ProviderListing | null>(null);
  const [more, setMore] = useState<ProviderSummary[]>([]);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);

  // Search as you type, without a request per keystroke.
  useEffect(() => {
    const id = window.setTimeout(() => setQuery(q.trim()), 250);
    return () => window.clearTimeout(id);
  }, [q]);

  useEffect(() => {
    let live = true;
    api
      .listProviders({ q: query, source, category, network, limit: PAGE })
      .then((l) => {
        if (!live) return;
        setListing(l);
        setMore([]);
        setError(null);
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "Couldn't load the catalogs"));
    return () => {
      live = false;
    };
  }, [query, source, category, network]);

  const providers = useMemo(() => [...(listing?.providers ?? []), ...more], [listing, more]);
  const canLoadMore = listing !== null && providers.length < listing.total;

  async function loadMore() {
    setLoadingMore(true);
    try {
      const l = await api.listProviders({ q: query, source, category, network, limit: PAGE, offset: providers.length });
      setMore((m) => [...m, ...l.providers]);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't load more");
    } finally {
      setLoadingMore(false);
    }
  }

  const categories = listing?.categories ?? [];

  return (
    <div className="mx-auto max-w-5xl">
      <PageHeader
        title="Providers"
        description={`Find your agent's next capability. Explore live catalogs payable in USDC on Solana ${networkLabel(network).toLowerCase()}. Algebra checks the actual quote before a payment.`}
      />

      <div className="mt-8 flex flex-col gap-3 md:flex-row md:items-center">
        <label className="relative flex-1">
          <span className="sr-only">Search providers</span>
          <IconSearch size={16} className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search: token security, OCR, web search, wallet PnL…" className="pl-9" />
        </label>
        <div className="flex shrink-0 flex-wrap gap-1 rounded-xl bg-primary-tint/50 p-1" role="tablist" aria-label="Catalog">
          {SOURCES.map((s) => (
            <button
              key={s.id || "all"}
              type="button"
              role="tab"
              aria-selected={source === s.id}
              onClick={() => setSource(s.id)}
              className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-colors ${source === s.id ? "bg-surface text-foreground shadow-[0_1px_3px_rgb(0_0_0/0.08)]" : "text-muted hover:text-foreground"}`}
            >
              {s.name}
            </button>
          ))}
        </div>
      </div>

      {categories.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-1.5">
          <CategoryChip on={category === ""} onClick={() => setCategory("")}>
            All categories
          </CategoryChip>
          {categories.map((c) => (
            <CategoryChip key={c.name} on={category === c.name} onClick={() => setCategory(category === c.name ? "" : c.name)}>
              {categoryLabel(c.name)} <span className="text-muted">{c.count}</span>
            </CategoryChip>
          ))}
        </div>
      )}

      <CatalogStatusLine listing={listing} network={network} />

      <div className="mt-5 space-y-3">
        {error && <ErrorNote>{error}</ErrorNote>}
        {!listing && !error && [0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-20" />)}
        {listing?.total === 0 && (
          <p className="rounded-2xl border border-dashed border-border-strong px-6 py-12 text-center text-sm text-muted">
            {network === "solana-devnet"
              ? "Few paid APIs take devnet USDC, and none match. Try other words, or switch to mainnet at the top."
              : "Nothing matches. Try other words, or another catalog."}
          </p>
        )}
        {providers.map((p) => (
          <ProviderRow key={p.id} provider={p} open={open === p.id} onToggle={() => setOpen(open === p.id ? null : p.id)} />
        ))}
        {canLoadMore && (
          <div className="flex justify-center pt-2">
            <Button variant="secondary" onClick={loadMore} disabled={loadingMore}>
              {loadingMore && <Spinner size={13} />} Show more ({listing!.total - providers.length} left)
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

function CategoryChip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={`inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-xs transition-colors ${
        on ? "border-primary bg-primary-tint text-foreground" : "border-border-strong text-foreground hover:border-foreground/35"
      }`}
    >
      {children}
    </button>
  );
}

function CatalogStatusLine({ listing, network }: { listing: ProviderListing | null; network: string }) {
  if (!listing?.sources?.length) return null;
  return (
    <p className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
      <span className="text-foreground">
        {listing.total} provider{listing.total === 1 ? "" : "s"} on {networkLabel(network).toLowerCase()}
      </span>
      {listing.sources.map((s) => (
        <span key={s.name} className="inline-flex items-center gap-1.5">
          <span className={`h-1.5 w-1.5 rounded-full ${s.error ? "bg-danger" : s.stale ? "bg-accent" : "bg-primary"}`} />
          {catalogName(s.name)}: {s.error ? "couldn't be reached" : `${s.total}${s.stale ? " (older copy)" : ""}`}
        </span>
      ))}
    </p>
  );
}

function SourceBadge({ source }: { source: string }) {
  return (
    <span className="rounded-full border border-border px-2 py-0.5 text-[0.6875rem] font-medium whitespace-nowrap text-muted">
      {catalogName(source)}
    </span>
  );
}

function NetworkBadges({ networks }: { networks: string[] }) {
  return (
    <>
      {networks.map((n) => (
        <span
          key={n}
          className={`rounded-full px-2 py-0.5 text-[0.6875rem] font-medium whitespace-nowrap ${n === "solana" ? "bg-primary-tint text-primary" : "bg-accent-tint text-accent"}`}
        >
          {networkLabel(n)}
        </span>
      ))}
    </>
  );
}

function ProviderRow({ provider: p, open, onToggle }: { provider: ProviderSummary; open: boolean; onToggle: () => void }) {
  return (
    <div className={`rounded-2xl border bg-surface transition-colors ${open ? "border-primary/40" : "border-border"}`}>
      <button type="button" onClick={onToggle} aria-expanded={open} className="flex w-full items-start gap-4 px-5 py-4 text-left">
        <ProviderLogo name={p.name} logo={p.logo_url} website={p.website} host={p.host} fqn={p.fqn} size={36} />
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-2">
            <span className="font-display text-[0.95rem] font-semibold text-foreground">{p.name}</span>
            <SourceBadge source={p.source} />
            <NetworkBadges networks={p.networks ?? []} />
            {p.billing === "monid-balance" && (
              <span
                className="rounded-full bg-border px-2 py-0.5 text-[0.6875rem] font-medium whitespace-nowrap text-muted"
                title="Billed to a prepaid Monid balance, not x402: listed for comparison, Algebra can't pay it yet"
              >
                Monid balance · not payable yet
              </span>
            )}
            <span className="text-xs text-muted">{categoryLabel(p.category)}</span>
          </span>
          {p.description && <span className="mt-1 line-clamp-2 block text-sm leading-relaxed text-muted">{p.description}</span>}
          <span className="mt-1.5 block font-mono text-[0.6875rem] text-muted/80">
            {p.id} · {p.host}
          </span>
        </span>
        <span className="hidden shrink-0 text-right sm:block">
          <span className="block font-mono text-xs text-foreground tabular-nums">{formatPriceRange(p.min_price_minor, p.max_price_minor)}</span>
          <span className="mt-0.5 block text-xs text-muted">
            {p.endpoint_count} endpoint{p.endpoint_count === 1 ? "" : "s"}
          </span>
          {(p.payers_30d ?? 0) > 0 && (
            <span className="mt-0.5 block text-xs text-muted" title="What the directory reports: payments in the last 30 days, and the most distinct payers one endpoint had">
              {p.payers_30d} payer{p.payers_30d === 1 ? "" : "s"} · 30 d
            </span>
          )}
        </span>
        <IconChevronDown size={18} className={`mt-1 shrink-0 text-muted transition-transform ${open ? "rotate-180" : ""}`} />
      </button>
      {open && <ProviderEndpoints provider={p} />}
    </div>
  );
}

function ProviderEndpoints({ provider }: { provider: ProviderSummary }) {
  const { network } = useNetwork();
  const [detail, setDetail] = useState<ProviderDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    let live = true;
    api
      .getProvider(provider.id)
      .then((d) => live && setDetail(d))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Couldn't load the endpoints"));
    return () => {
      live = false;
    };
  }, [provider.id]);

  const payableHere = (e: ProviderEndpoint) => (e.payments?.length ? e.payments.some((p) => p.network === network) : network === "solana");
  const endpoints = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (detail?.endpoints ?? []).filter((e) => !f || `${e.method} ${e.path} ${e.description}`.toLowerCase().includes(f));
  }, [detail, filter]);
  const chosen = detail?.endpoints.find((e) => e.capability === picked) ?? null;

  return (
    <div className="border-t border-border px-5 pt-4 pb-5">
      {error && <ErrorNote>{error}</ErrorNote>}
      {!detail && !error && (
        <p className="flex items-center gap-2 text-sm text-muted">
          <Spinner size={14} /> Reading the endpoints from {catalogName(provider.source)}…
        </p>
      )}
      {detail && (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-xs text-muted">
              {detail.endpoints.length} endpoint{detail.endpoints.length === 1 ? "" : "s"}, {detail.endpoints.filter((e) => e.callable && payableHere(e)).length} callable on{" "}
              {networkLabel(network).toLowerCase()} through Algebra today.
            </p>
            <div className="flex items-center gap-3">
              {detail.endpoints.length > 8 && (
                <div className="w-48">
                  <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter endpoints" />
                </div>
              )}
              <a href={detail.page_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline">
                {catalogName(detail.source)} <IconExternal size={12} />
              </a>
            </div>
          </div>
          <ul className="mt-3 max-h-[420px] divide-y divide-border overflow-y-auto rounded-xl border border-border">
            {endpoints.map((e) => (
              <EndpointRow
                key={`${e.method} ${e.path}`}
                e={e}
                here={payableHere(e)}
                selected={picked === e.capability}
                onPick={() => setPicked(picked === e.capability ? null : e.capability)}
              />
            ))}
          </ul>
          {chosen && <UseIt provider={detail} e={chosen} network={network} />}
        </>
      )}
    </div>
  );
}

function MethodBadge({ method }: { method: string }) {
  const tone = method === "GET" ? "bg-primary-tint text-primary" : method === "POST" ? "bg-accent-tint text-accent" : "bg-border text-muted";
  return <span className={`w-14 shrink-0 rounded-md px-1.5 py-0.5 text-center font-mono text-[0.6875rem] font-medium ${tone}`}>{method}</span>;
}

function EndpointRow({ e, here, selected, onPick }: { e: ProviderEndpoint; here: boolean; selected: boolean; onPick: () => void }) {
  const prices = e.payments?.length ? e.payments : [{ network: "solana", price_minor: e.price_minor }];
  return (
    <li className={`flex items-start gap-3 px-3.5 py-2.5 ${selected ? "bg-primary-tint/50" : ""} ${here ? "" : "opacity-60"}`}>
      <MethodBadge method={e.method} />
      <div className="min-w-0 flex-1">
        <p className="truncate font-mono text-xs text-foreground" title={e.path}>
          /{e.path}
        </p>
        {e.description && <p className="mt-0.5 line-clamp-2 text-xs text-muted">{e.description}</p>}
        {e.callable && e.path_params && e.path_params.length > 0 && (
          <p className="mt-0.5 text-xs text-muted">
            Takes {e.path_params.map((p) => `{${p}}`).join(", ")} in the path: give each in the input.
          </p>
        )}
        {!e.callable && e.not_callable_reason && <p className="mt-0.5 text-xs text-accent">Listed only: {e.not_callable_reason}.</p>}
        {e.callable && !here && <p className="mt-0.5 text-xs text-muted">Not payable on this network: switch at the top.</p>}
      </div>
      <span className="shrink-0 text-right font-mono text-xs whitespace-nowrap text-foreground tabular-nums">
        {prices.length === 1
          ? formatEndpointPrice(prices[0].price_minor, e.free)
          : prices.map((p) => (
              <span key={p.network} className="block">
                {formatUSDC(p.price_minor)} <span className="text-muted">{networkLabel(p.network).toLowerCase()}</span>
              </span>
            ))}
      </span>
      {e.callable && here && (
        <button
          type="button"
          onClick={onPick}
          className={`shrink-0 rounded-md px-2 py-1 text-xs font-medium ${selected ? "bg-primary text-primary-tint" : "border border-border-strong text-foreground hover:bg-primary-tint"}`}
        >
          {selected ? "Hide" : "Use"}
        </button>
      )}
    </li>
  );
}

function UseIt({ provider, e, network }: { provider: ProviderDetail; e: ProviderEndpoint; network: string }) {
  const price = e.payments?.find((p) => p.network === network)?.price_minor ?? e.price_minor;
  const budget = Math.max(price * 2, 10_000);
  // A templated path takes its parameters from the input: one field for each.
  const input = Object.fromEntries((e.path_params ?? []).map((p) => [p, `<${p}>`]));
  const body = {
    capability: e.capability,
    providers: [provider.id],
    input,
    budget_max_minor: budget,
    constraints: { allowed_networks: [network] },
  };
  const curl = `curl -X POST "$ALGEBRA_API/execute" \\
  -H "Authorization: Bearer $SPEND_PASS_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '${JSON.stringify(body)}'`;
  const mcp = `algebra.execute({
  capability: "${e.capability}",
  providers: ["${provider.id}"],
  input: ${JSON.stringify(input).replace(/"([A-Za-z_]+)":/g, "$1: ").replace(/^{}$/, "{ ... }")},
  max_price_usdc: "${(budget / 1_000_000).toString()}"
})`;
  return (
    <Panel className="mt-4 bg-background">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm font-medium text-foreground">Call it through Algebra on {networkLabel(network).toLowerCase()}</p>
        <span className="flex items-center gap-2 font-mono text-xs text-muted">
          {e.capability} <Copy text={e.capability} label="Copy ID" />
        </span>
      </div>
      <p className="mt-1 text-xs leading-relaxed text-muted">
        Put the request&apos;s parameters in <code className="font-mono">input</code>: query parameters for a GET, the JSON body for a POST.
        Algebra prices the call first, checks it against the agent&apos;s Spend Pass, pays in USDC on Solana {networkLabel(network).toLowerCase()}, and
        returns the result with a signed receipt. Or just ask in Agent chat.
      </p>
      <div className="mt-4 space-y-4">
        <Snippet title="REST" code={curl} />
        <Snippet title="MCP tool call" code={mcp} />
        {e.input_schema != null && <Snippet title="Input, as the catalog describes it" code={JSON.stringify(e.input_schema, null, 2)} />}
      </div>
    </Panel>
  );
}
