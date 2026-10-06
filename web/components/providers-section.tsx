"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { Container } from "./container";
import { ProviderMark } from "./provider-mark";
import { CATALOGS, FEATURED_PROVIDERS, catalogName, categoryLabel } from "@/lib/paysh";
import { formatPriceRange } from "@/lib/money";
import type { ProviderListing } from "@/lib/types";

/**
 * The paid APIs an agent can reach, from the live catalogs (Pay.sh and
 * Circle's Agent Marketplace) through GET /api/v1/providers, with a snapshot
 * to show before they arrive or when the API can't be reached. Prices are the
 * catalogs' listings, never quotes.
 */
export function Providers() {
  const [listing, setListing] = useState<ProviderListing | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 8000);
    // Same-origin: /api/v1 is proxied to the Go API (next.config.ts).
    fetch("/api/v1/providers?limit=200", { signal: controller.signal })
      .then((res) => (res.ok ? res.json() : Promise.reject()))
      .then((data: ProviderListing) => {
        if (data.providers?.length) setListing(data);
      })
      .catch(() => {})
      .finally(() => clearTimeout(timeout));
    return () => controller.abort();
  }, []);

  const rows = useMemo(() => {
    const live = new Map((listing?.providers ?? []).map((p) => [p.id, p]));
    return FEATURED_PROVIDERS.map((f) => live.get(f.id) ?? f);
  }, [listing]);

  const total = listing?.total;

  return (
    <section id="providers" className="pt-20 pb-20 md:pt-28 md:pb-28">
      <Container>
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <h2 className="font-display max-w-lg text-3xl font-semibold tracking-tight text-foreground md:text-4xl">
              Every provider, as its catalog lists it.
            </h2>
            <p className="mt-4 max-w-xl text-[1.0625rem] leading-relaxed text-muted">
              Algebra reads two catalogs of pay-per-call APIs, Pay.sh and Circle&apos;s Agent Marketplace, and keeps what can
              be paid in USDC on Solana. A listing is not an endorsement and a listed price is not a quote: Algebra asks
              the endpoint for its real terms before it pays.
            </p>
          </div>
          <span className="flex items-center gap-2 font-mono text-xs text-muted">
            <span className={`h-1.5 w-1.5 rounded-full ${listing ? "bg-primary" : "bg-border-strong"}`} />
            {listing
              ? listing.stale
                ? "older copy: a catalog didn't answer"
                : `live: ${(listing.sources ?? []).map((s) => `${catalogName(s.name)} ${s.total}`).join(" · ")}`
              : "snapshot: API not reached yet"}
          </span>
        </div>

        <div className="mt-12 overflow-x-auto rounded-2xl border border-border">
          <table className="w-full min-w-[720px] border-collapse text-left">
            <thead>
              <tr className="border-b border-border text-xs text-muted">
                <th className="px-5 py-3 font-medium">Provider</th>
                <th className="px-5 py-3 font-medium">Catalog</th>
                <th className="px-5 py-3 font-medium">Category</th>
                <th className="px-5 py-3 font-medium">Listed price</th>
                <th className="px-5 py-3 font-medium">Endpoints</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => (
                <tr key={p.id} className="border-b border-border last:border-b-0">
                  <td className="px-5 py-4">
                    <span className="flex items-start gap-3">
                      <ProviderMark name={p.name} size={28} />
                      <span className="min-w-0">
                        <span className="font-display block text-sm font-semibold text-foreground">{p.name}</span>
                        <span className="mt-0.5 block max-w-md text-xs leading-relaxed text-muted">{p.description}</span>
                        <span className="mt-1 block font-mono text-[0.6875rem] text-muted/80">{p.host}</span>
                      </span>
                    </span>
                  </td>
                  <td className="px-5 py-4 text-sm whitespace-nowrap text-muted">{catalogName(p.source)}</td>
                  <td className="px-5 py-4 text-sm text-muted">{categoryLabel(p.category)}</td>
                  <td className="px-5 py-4 font-mono text-xs whitespace-nowrap text-foreground tabular-nums">
                    {formatPriceRange(p.min_price_minor, p.max_price_minor)}
                  </td>
                  <td className="px-5 py-4 font-mono text-xs text-muted tabular-nums">{p.endpoint_count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="mt-6 flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
          <Link href="/console/providers" className="font-medium text-primary underline decoration-primary/30 underline-offset-4 hover:decoration-primary">
            {total ? `Browse all ${total} providers in the console` : "Browse every provider in the console"}
          </Link>
          {CATALOGS.map((c) => (
            <a
              key={c.id}
              href={c.url}
              rel="noreferrer"
              className="text-muted underline decoration-border-strong underline-offset-4 hover:text-foreground hover:decoration-foreground"
            >
              {c.name}
            </a>
          ))}
        </div>
      </Container>
    </section>
  );
}
