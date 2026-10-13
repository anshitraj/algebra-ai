"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { ProviderDetail, ProviderEndpoint } from "@/lib/types";
import { EndpointTrustChips, ProviderTrustChips } from "./trust-badges";
import { catalogName, categoryLabel } from "@/lib/paysh";
import { formatEndpointPrice, formatPriceRange } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { IconArrowLeft, IconExternal, IconSearch } from "@/components/icons";
import { Button, ErrorNote, Input, Skeleton } from "./ui";
import { Copy, Snippet } from "./snippet";
import { AgentQuickstart } from "./agent-quickstart";
import { ProviderLogo } from "@/components/provider-logo";

function externalURL(value: string | undefined) {
  try { const u = new URL(value ?? ""); return ["https:", "http:"].includes(u.protocol) ? u.href : undefined; } catch { return undefined; }
}
function paymentHere(e: ProviderEndpoint, network: string) {
  return e.payments?.length ? e.payments.find((p) => p.network === network) : (e.network ?? "solana") === network ? { network, price_minor: e.price_minor } : undefined;
}

export function ProviderDetailPage({ id }: { id: string }) {
  const { network } = useNetwork();
  const [result, setResult] = useState<{ id: string; detail: ProviderDetail } | null>(null);
  const [failure, setFailure] = useState<{ id: string; message: string } | null>(null);
  const [filter, setFilter] = useState("");
  const [shown, setShown] = useState(30);
  const [picked, setPicked] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let live = true;
    api.getProvider(id).then((detail) => { if (live) setResult({ id, detail }); }).catch((e) => { if (live) setFailure({ id, message: e instanceof Error ? e.message : "Couldn't read this provider" }); });
    return () => { live = false; };
  }, [id, retry]);
  const detail = result?.id === id ? result.detail : null;
  const error = failure?.id === id && !detail ? failure.message : null;
  const endpoints = (detail?.endpoints ?? []).filter((e) => `${e.method} ${e.path} ${e.description} ${e.capability}`.toLowerCase().includes(filter.trim().toLowerCase()));
  const selected = detail?.endpoints.find((e) => e.capability === picked);
  const website = externalURL(detail?.website ?? detail?.service_url);
  const catalog = externalURL(detail?.page_url);
  return <div className="mx-auto max-w-6xl">
    <Link href="/console/providers" className="inline-flex min-h-10 items-center gap-2 text-xs font-medium text-primary hover:underline"><IconArrowLeft size={14} /> All providers</Link>
    {error && <div className="mt-6 space-y-4"><ErrorNote>{error}</ErrorNote><Button variant="secondary" onClick={() => { setFailure(null); setRetry((n) => n + 1); }}>Try again</Button></div>}
    {!detail && !error && <div className="mt-8 space-y-5"><Skeleton className="h-24" /><Skeleton className="h-72" /></div>}
    {detail && <div className="mt-6 grid items-start gap-8 xl:grid-cols-[minmax(0,1fr)_340px]">
      <main className="min-w-0">
        <p className="mb-4 font-mono text-[0.6rem] tracking-[0.2em] text-muted uppercase">{catalogName(detail.source)} / {categoryLabel(detail.category)}</p>
        <div className="flex items-center gap-4"><ProviderLogo name={detail.name} logo={detail.logo_url} website={detail.website} host={detail.host} fqn={detail.fqn} size={44} /><h1 className="font-display min-w-0 break-words text-3xl font-semibold tracking-tight text-foreground sm:text-4xl">{detail.name}</h1></div>
        <p className="mt-5 max-w-2xl text-sm leading-relaxed text-muted sm:text-base">{detail.description || detail.use_case || `API endpoints served at ${detail.host}.`}</p>
        <div className="mt-4 flex flex-wrap items-center gap-2"><ProviderTrustChips trust={detail.trust} /><span className="text-xs text-muted">Verified means a person reviewed who runs it; payment tested means one paid call delivered. Every payment is still checked against your Spend Pass.</span></div>
        <div className="mt-5 flex flex-wrap items-center gap-3 text-xs">
          {website && <a href={website} target="_blank" rel="noopener noreferrer" className="inline-flex min-h-9 items-center gap-1 text-primary hover:underline">Website <IconExternal size={12} /></a>}
          {catalog && <a href={catalog} target="_blank" rel="noopener noreferrer" className="inline-flex min-h-9 items-center gap-1 text-primary hover:underline">Catalog &amp; docs <IconExternal size={12} /></a>}
          {(detail.networks ?? []).map((n) => <span key={n} className="rounded-full border border-border px-2 py-1 text-muted">{networkLabel(n)}</span>)}
          <Copy text={detail.id} label="Copy provider ID" />
        </div>
        {detail.billing === "monid-balance" && <p className="mt-4 text-sm text-accent">This provider uses a Monid balance. You can browse its endpoints; Algebra can&apos;t pay it yet.</p>}
        {detail.stale && <p className="mt-4 text-xs text-accent">Showing a cached catalog. Algebra checks a fresh quote before payment.</p>}
        <section className="mt-9" aria-label="Provider endpoints">
          <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="text-sm font-semibold text-foreground">Endpoints <span className="ml-1 font-mono text-xs font-normal text-muted">{detail.endpoints.length}</span></h2><label className="relative min-w-0 w-full sm:w-64"><span className="sr-only">Filter endpoints</span><IconSearch size={14} className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" /><Input value={filter} onChange={(e) => { setFilter(e.target.value); setShown(30); }} placeholder="Filter method, path or capability…" className="pl-9" /></label></div>
          <p className="mt-3 text-xs leading-relaxed text-muted">Listed prices are estimates. Algebra checks the live quote and your Spend Pass before paying.</p>
          <div className="mt-4 overflow-hidden rounded-xl border border-border bg-surface">
            <div className="hidden grid-cols-[58px_minmax(0,1fr)_130px] gap-3 border-b border-border bg-background px-4 py-3 font-mono text-[0.65rem] tracking-wider text-muted uppercase sm:grid"><span>Method</span><span>Endpoint</span><span className="text-right">Listed price</span></div>
            {endpoints.slice(0, shown).map((e) => {
              const pay = paymentHere(e, network);
              const canUse = e.callable && !!pay && detail.billing !== "monid-balance";
              return <article key={e.capability} className="border-b border-border px-4 py-4 last:border-b-0">
                <div className="grid gap-3 sm:grid-cols-[58px_minmax(0,1fr)_130px]">
                  <span className={`h-fit w-fit rounded-md px-2 py-1 font-mono text-[0.65rem] ${e.method === "GET" ? "bg-primary-tint text-primary" : "bg-accent-tint text-accent"}`}>{e.method}</span>
                  <div className="min-w-0"><p className="break-words font-mono text-xs text-foreground">/{e.path.replace(/^\/+/, "")}</p>{e.description && <p className="mt-1.5 text-xs leading-relaxed text-muted">{e.description}</p>}<p className="mt-2 break-all font-mono text-[0.6rem] text-muted">{e.capability}</p><div className="mt-2"><EndpointTrustChips rows={detail.endpoint_trust?.[e.capability]} /></div></div>
                  <div className="text-xs text-foreground sm:text-right"><p className="font-mono">{formatEndpointPrice(pay?.price_minor ?? e.price_minor, e.free)}</p><p className="mt-1 text-[0.65rem] text-muted">{pay ? networkLabel(network) : "Other network"}</p></div>
                </div>
                <div className="mt-3 flex flex-wrap items-center justify-between gap-3 sm:pl-[70px]">
                  <div className="flex flex-wrap gap-2"><Copy text={e.capability} label="Copy capability" /><Copy text={e.url} label="Copy URL" /></div>
                  {canUse ? <Button variant="secondary" className="min-h-9 text-xs" onClick={() => setPicked(picked === e.capability ? null : e.capability)}>{picked === e.capability ? "Hide example" : "Request example"}</Button> : <span className="text-xs text-muted">{!pay ? `Not payable on ${networkLabel(network).toLowerCase()}` : e.not_callable_reason || "Catalog only"}</span>}
                </div>
                {selected?.capability === e.capability && <EndpointExample key={`${e.capability}:${network}`} provider={detail} endpoint={e} network={network} />}
              </article>;
            })}
            {endpoints.length === 0 && <p className="p-8 text-center text-sm text-muted">{detail.endpoints.length ? "No endpoints match this filter." : "The catalog hasn't published endpoint details for this provider."}</p>}
          </div>
          {endpoints.length > shown && <div className="mt-4 flex justify-center"><Button variant="secondary" onClick={() => setShown((n) => n + 30)}>Show more endpoints ({endpoints.length - shown} left)</Button></div>}
        </section>
      </main>
      <aside className="min-w-0 space-y-5 xl:sticky xl:top-4">
        <AgentQuickstart provider={detail} compact />
        <dl className="divide-y divide-border rounded-xl border border-border bg-surface px-5 text-xs">
          {[{ label: "Endpoints", value: String(detail.endpoints.length) }, { label: "Listed price range", value: formatPriceRange(detail.min_price_minor, detail.max_price_minor) }, { label: `Callable on ${networkLabel(network)}`, value: String(detail.endpoints.filter((e) => e.callable && paymentHere(e, network) && detail.billing !== "monid-balance").length) }].map((s) => <div key={s.label} className="flex justify-between gap-3 py-4"><dt className="text-muted">{s.label}</dt><dd className="font-mono text-foreground">{s.value}</dd></div>)}
        </dl>
        <Link href={`/console/agent?prompt=${encodeURIComponent(`Help me inspect the endpoints of provider ${JSON.stringify(detail.id)} on ${networkLabel(network)}. Don't pay yet.`)}`} className="flex min-h-11 items-center justify-center rounded-lg bg-primary px-4 text-sm text-primary-tint">Explore in Agent chat</Link>
      </aside>
    </div>}
  </div>;
}

function EndpointExample({ provider, endpoint: e, network }: { provider: ProviderDetail; endpoint: ProviderEndpoint; network: string }) {
  const price = paymentHere(e, network)?.price_minor ?? e.price_minor;
  const [ceiling, setCeiling] = useState(String(Math.max(price, 10_000) / 1_000_000));
  const budget = Math.round(Number(ceiling) * 1_000_000);
  const valid = ceiling.trim() !== "" && Number.isSafeInteger(budget) && budget > 0;
  const input = Object.fromEntries((e.path_params ?? []).map((p) => [p, `<${p}>`]));
  const body = { capability: e.capability, providers: [provider.id], input, budget_max_minor: valid ? budget : "<BUDGET_IN_MICRO_USDC>", constraints: { allowed_networks: [network] } };
  const mcp = { capability: e.capability, providers: [provider.id], input, max_price_usdc: valid ? ceiling : "<MAX_USDC>", allowed_networks: [network] };
  const curl = `curl -X POST "$ALGEBRA_API/execute" \\\n  -H "Authorization: Bearer $ALGEBRA_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify(body).replace(/'/g, "'\\''")}'`;
  return <div className="mt-4 space-y-4 rounded-lg border border-border bg-background p-4">
    <label className="block max-w-xs text-xs text-muted">Request spending ceiling (USDC)<Input value={ceiling} onChange={(ev) => setCeiling(ev.target.value)} inputMode="decimal" className="mt-1.5" /></label>
    {!valid && <ErrorNote>Enter a positive USDC ceiling.</ErrorNote>}
    <p className="text-xs leading-relaxed text-muted">Add the endpoint&apos;s required parameters to <code>input</code>. GET uses query parameters; POST uses a JSON body. Set <code>ALGEBRA_API</code> to your API base URL and <code>ALGEBRA_API_KEY</code> to a key from Projects &amp; API keys. Copying an example doesn&apos;t make a payment.</p>
    <Snippet title="REST request" code={curl} />
    <Snippet title="MCP tool arguments · algebra.execute" code={JSON.stringify(mcp, null, 2)} />
    {e.input_schema != null && <Snippet title="Input schema from the catalog" code={JSON.stringify(e.input_schema, null, 2)} />}
    <Link href="/console/connect" className="inline-flex min-h-9 items-center gap-1 text-xs text-primary hover:underline">Find your API base URL in the setup guide</Link>
  </div>;
}
