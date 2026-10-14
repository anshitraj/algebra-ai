"use client";

import Link from "next/link";
import { useEffect, useMemo, useRef, useState } from "react";
import * as api from "@/lib/api-client";
import type {
  ProviderDetail,
  ProviderEndpoint,
  ProviderSummary,
  EndpointTrust,
} from "@/lib/types";
import { EndpointTrustChips } from "./trust-badges";
import { catalogName } from "@/lib/paysh";
import { formatEndpointPrice, formatUSDC } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { Button, ErrorNote, Input } from "@/components/console/ui";
import { Copy, Snippet } from "@/components/console/snippet";
import { IconExternal, Spinner } from "@/components/icons";
import { safeExternalUrl } from "@/lib/provider-presentation";
import styles from "@/app/console/providers/providers.module.css";
export function ProviderEndpoints({ provider }: { provider: ProviderSummary }) {
  // Demo mode (and a demo account, which only practises) reads these and can't pay them.
  const { network, practice: demo } = useNetwork();
  const [detail, setDetail] = useState<ProviderDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    let live = true;
    api
      .getProvider(provider.id)
      .then((d) => live && setDetail(d))
      .catch(
        (e) =>
          live &&
          setError(
            e instanceof Error ? e.message : "Couldn't load the endpoints",
          ),
      );
    return () => {
      live = false;
    };
  }, [provider.id, retry]);

  const payableHere = (e: ProviderEndpoint) =>
    !demo &&
    provider.billing !== "monid-balance" &&
    (e.payments?.length
      ? e.payments.some((p) => p.network === network)
      : network === "solana");
  const endpoints = useMemo(() => {
    const f = filter.trim().toLowerCase();
    return (detail?.endpoints ?? []).filter(
      (e) =>
        !f ||
        `${e.method} ${e.path} ${e.description}`.toLowerCase().includes(f),
    );
  }, [detail, filter]);
  const chosen = detail?.endpoints.find((e) => e.capability === picked) ?? null;

  return (
    <div className={styles.endpointDetails}>
      {error && (
        <div className={styles.error}>
          <ErrorNote>{error}</ErrorNote>
          <Button
            variant="secondary"
            onClick={() => {
              setError(null);
              setRetry((v) => v + 1);
            }}
          >
            Try again
          </Button>
        </div>
      )}
      {!detail && !error && (
        <p className="flex items-center gap-2 text-sm text-muted">
          <Spinner size={14} /> Reading the endpoints from{" "}
          {catalogName(provider.source)}…
        </p>
      )}
      {detail && (
        <>
          <div className={styles.endpointMeta}>
            <p>
              {detail.endpoints.length} API
              {detail.endpoints.length === 1 ? "" : "s"} ·{" "}
              {demo
                ? "Browse only in demo mode"
                : `${detail.endpoints.filter((e) => e.callable && payableHere(e)).length} callable on ${networkLabel(network)}`}
            </p>
            <div className="flex items-center gap-3">
              {detail.endpoints.length > 8 && (
                <div className="w-48">
                  <Input
                    aria-label="Filter endpoints"
                    value={filter}
                    onChange={(e) => setFilter(e.target.value)}
                    placeholder="Filter endpoints"
                  />
                </div>
              )}
              <Link
                href={`/console/providers/${encodeURIComponent(provider.id)}`}
                className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
              >
                Full details
              </Link>
              {safeExternalUrl(detail.page_url) && (
                <a
                  href={safeExternalUrl(detail.page_url)}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
                >
                  {catalogName(detail.source)} <IconExternal size={12} />
                </a>
              )}
            </div>
          </div>
          <ul className={styles.endpointList}>
            {endpoints.length === 0 && (
              <li className="flex flex-wrap items-center gap-3 px-4 py-6 text-xs text-muted">
                No matching endpoints.
                {filter && (
                  <button
                    type="button"
                    className="text-foreground"
                    onClick={() => setFilter("")}
                  >
                    Clear endpoint filter
                  </button>
                )}
              </li>
            )}
            {endpoints.map((e) => (
              <EndpointRow
                key={`${e.method} ${e.path}`}
                e={e}
                here={payableHere(e)}
                trust={detail.endpoint_trust?.[e.capability]}
                selected={picked === e.capability}
                onPick={() =>
                  setPicked(picked === e.capability ? null : e.capability)
                }
              />
            ))}
          </ul>
          {chosen && (
            <UseIt
              key={chosen.capability}
              provider={detail}
              e={chosen}
              network={network}
            />
          )}
        </>
      )}
    </div>
  );
}

function MethodBadge({ method }: { method: string }) {
  const tone =
    method === "GET"
      ? "bg-primary-tint text-primary"
      : method === "POST"
        ? "bg-accent-tint text-accent"
        : "bg-border text-muted";
  return (
    <span
      className={`w-14 shrink-0 rounded-md px-1.5 py-0.5 text-center font-mono text-[0.6875rem] font-medium ${tone}`}
    >
      {method}
    </span>
  );
}

function EndpointRow({
  e,
  here,
  trust,
  selected,
  onPick,
}: {
  e: ProviderEndpoint;
  here: boolean;
  trust?: EndpointTrust[];
  selected: boolean;
  onPick: () => void;
}) {
  const { practice: demo } = useNetwork();
  const prices = e.payments?.length
    ? e.payments
    : [{ network: "solana", price_minor: e.price_minor }];
  return (
    <li
      className={`flex items-start gap-3 px-3.5 py-2.5 ${selected ? "bg-primary-tint/50" : ""} ${here ? "" : "opacity-60"}`}
    >
      <MethodBadge method={e.method} />
      <div className="min-w-0 flex-1">
        <p
          className="truncate font-mono text-xs text-foreground"
          title={e.path}
        >
          /{e.path}
        </p>
        {e.description && (
          <p className="mt-0.5 line-clamp-2 text-xs text-muted">
            {e.description}
          </p>
        )}
        {e.callable && e.path_params && e.path_params.length > 0 && (
          <p className="mt-0.5 text-xs text-muted">
            Takes {e.path_params.map((p) => `{${p}}`).join(", ")} in the path:
            give each in the input.
          </p>
        )}
        <span className="mt-1 block"><EndpointTrustChips rows={trust} /></span>
        {!e.callable && e.not_callable_reason && (
          <p className="mt-0.5 text-xs text-accent">
            Listed only: {e.not_callable_reason}.
          </p>
        )}
        {e.callable && !here && (
          <p className="mt-0.5 text-xs text-muted">
            {demo
              ? "Demo mode can't pay this one: it uses play money. Switch to Devnet at the top to pay it."
              : "Not payable on this network: switch at the top."}
          </p>
        )}
      </div>
      <span className="shrink-0 text-right font-mono text-xs whitespace-nowrap text-foreground tabular-nums">
        {prices.length === 1
          ? formatEndpointPrice(prices[0].price_minor, e.free)
          : prices.map((p) => (
              <span key={p.network} className="block">
                {formatUSDC(p.price_minor)}{" "}
                <span className="text-muted">
                  {networkLabel(p.network).toLowerCase()}
                </span>
              </span>
            ))}
      </span>
      {e.callable && here && (
        <button
          type="button"
          onClick={onPick}
          aria-expanded={selected}
          className={`shrink-0 rounded-md px-2 py-1 text-xs font-medium ${selected ? "bg-primary text-primary-tint" : "border border-border-strong text-foreground hover:bg-primary-tint"}`}
        >
          {selected ? "Hide code" : "Get code"}
        </button>
      )}
    </li>
  );
}

function UseIt({
  provider,
  e,
  network,
}: {
  provider: ProviderDetail;
  e: ProviderEndpoint;
  network: string;
}) {
  const [tab, setTab] = useState<"rest" | "mcp" | "schema">("rest");
  const codeRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    codeRef.current?.scrollIntoView({
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
        ? "instant"
        : "smooth",
      block: "nearest",
    });
  }, []);
  const price =
    e.payments?.find((p) => p.network === network)?.price_minor ??
    e.price_minor ??
    0;
  const budget = Math.max(price * 2, 10_000);
  // A templated path takes its parameters from the input: one field for each.
  const input = Object.fromEntries(
    (e.path_params ?? []).map((p) => [p, `<${p}>`]),
  );
  const body = {
    capability: e.capability,
    providers: [provider.id],
    input,
    budget_max_minor: budget,
    constraints: { allowed_networks: [network] },
  };
  const curl = `curl -X POST "$ALGEBRA_API/execute" \\
  -H "Authorization: Bearer $ALGEBRA_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '${JSON.stringify(body)}'`;
  const mcp = `algebra_execute({
  capability: "${e.capability}",
  providers: ["${provider.id}"],
  input: ${JSON.stringify(input)
    .replace(/"([A-Za-z_]+)":/g, "$1: ")
    .replace(/^{}$/, "{ ... }")},
  max_price_usdc: "${(budget / 1_000_000).toString()}"
})`;
  return (
    <div className={styles.useIt} ref={codeRef}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm font-medium text-foreground">
          Use this API · {networkLabel(network)}
        </p>
        <span className="flex min-w-0 flex-wrap items-center gap-2 font-mono text-xs text-muted">
          <span className="min-w-0 break-all">{e.capability}</span>{" "}
          <Copy text={e.capability} label="Copy ID" />
        </span>
      </div>
      <p className="mt-1 text-xs leading-relaxed text-muted">
        Put query fields (GET) or JSON fields (POST) in{" "}
        <code className="font-mono">input</code>. Your Spend Pass applies to
        every call.
      </p>
      <div className={styles.snippetTabs} aria-label="Integration example">
        <button
          type="button"
          aria-pressed={tab === "rest"}
          onClick={() => setTab("rest")}
        >
          REST
        </button>
        <button
          type="button"
          aria-pressed={tab === "mcp"}
          onClick={() => setTab("mcp")}
        >
          MCP
        </button>
        {e.input_schema != null && (
          <button
            type="button"
            aria-pressed={tab === "schema"}
            onClick={() => setTab("schema")}
          >
            Input schema
          </button>
        )}
      </div>
      <div className={styles.snippetContent}>
        {tab === "rest" && <Snippet title="REST request" code={curl} />}
        {tab === "mcp" && <Snippet title="MCP tool call" code={mcp} />}
        {tab === "schema" && (
          <Snippet
            title="Catalog input schema"
            code={JSON.stringify(e.input_schema, null, 2)}
          />
        )}
      </div>
    </div>
  );
}
