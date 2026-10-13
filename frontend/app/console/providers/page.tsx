"use client";

import Link from "next/link";
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { motion, useInView, useReducedMotion } from "motion/react";
import * as api from "@/lib/api-client";
import type {
  ProviderListing,
  ProviderSummary,
  CatalogStatus,
} from "@/lib/types";
import { CATALOGS, catalogName, categoryLabel } from "@/lib/paysh";
import { formatPriceRange } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import {
  compactHost,
  providerSummary,
  providerTitle,
  safeExternalUrl,
} from "@/lib/provider-presentation";
import { Button, ErrorNote, Skeleton } from "@/components/console/ui";
import { Copy } from "@/components/console/snippet";
import { ProviderEndpoints } from "@/components/console/provider-endpoints";
import { ProviderLogo } from "@/components/provider-logo";
import { Logo } from "@/components/logo";
import {
  IconArrowRight,
  IconChat,
  IconCheck,
  IconClock,
  IconExternal,
  IconGrid,
  IconList,
  IconMail,
  IconPlug,
  IconRefresh,
  IconSearch,
  IconShield,
  IconStore,
  IconTag,
  IconWallet,
  IconX,
  Spinner,
} from "@/components/icons";
import styles from "./providers.module.css";

const PAGE = 48;
const QUICK_CATEGORIES = ["ai_ml", "finance", "search", "data", "media"];
const CATALOG_DOMAINS: Record<string, string> = {
  "pay.sh": "pay.sh",
  circle: "circle.com",
  payai: "payai.network",
  cdp: "coinbase.com",
  monid: "monid.ai",
};

function CategoryIcon({
  category,
  size = 18,
}: {
  category: string;
  size?: number;
}) {
  if (category === "finance") return <IconWallet size={size} />;
  if (category === "ai_ml" || category === "translation")
    return <IconChat size={size} />;
  if (category === "search") return <IconSearch size={size} />;
  if (category === "security" || category === "identity")
    return <IconShield size={size} />;
  if (category === "messaging") return <IconMail size={size} />;
  if (
    category === "compute" ||
    category === "infrastructure" ||
    category === "devtools"
  )
    return <IconPlug size={size} />;
  return <IconGrid size={size} />;
}

function CatalogLogo({ source, size = 24 }: { source: string; size?: number }) {
  return (
    <ProviderLogo
      name={catalogName(source)}
      website={CATALOG_DOMAINS[source]}
      size={size}
    />
  );
}

function subscribeVisibility(callback: () => void) {
  document.addEventListener("visibilitychange", callback);
  return () => document.removeEventListener("visibilitychange", callback);
}
function pageIsVisible() {
  return document.visibilityState === "visible";
}
function serverIsVisible() {
  return false;
}

/** Decorative discovery diagram. Looping motion stops off-screen and in hidden tabs. */
function CatalogNetwork() {
  const ref = useRef<HTMLDivElement>(null);
  const inView = useInView(ref);
  const reducedMotion = useReducedMotion();
  const visible = useSyncExternalStore(
    subscribeVisibility,
    pageIsVisible,
    serverIsVisible,
  );
  const animate = inView && visible && !reducedMotion;
  return (
    <div className={styles.networkSketch} ref={ref} aria-hidden="true">
      <svg className={styles.networkLines} viewBox="0 0 290 110" fill="none">
        <path
          d="M38 29H92L145 55 198 29h54M38 81H92L145 55 198 81h54"
          stroke="currentColor"
          strokeWidth="1"
        />
        {[29, 81].map((y, i) => (
          <motion.g
            key={y}
            animate={
              animate
                ? {
                    x: [0, 54, 107],
                    y: [0, 0, y === 29 ? 26 : -26],
                    opacity: [0, 1, 0],
                  }
                : { x: 0, y: 0, opacity: 0 }
            }
            transition={
              animate
                ? {
                    duration: 3,
                    repeat: Infinity,
                    delay: i * 1.5,
                    ease: "linear",
                    times: [0, 0.5, 1],
                  }
                : { duration: 0 }
            }
          >
            <circle cx="38" cy={y} r="2.5" fill="currentColor" />
          </motion.g>
        ))}
      </svg>
      <span className={styles.networkCore}>
        <Logo size={25} />
      </span>
      {["pay.sh", "circle", "payai", "cdp"].map((source, i) => (
        <span key={source} className={styles.networkNode} data-position={i}>
          <CatalogLogo source={source} size={29} />
        </span>
      ))}
      <span className={styles.networkCaption}>
        DISCOVER · COMPARE · CONNECT
      </span>
    </div>
  );
}

export default function ProvidersPage() {
  const { network, demo } = useNetwork();
  const cluster = demo ? undefined : network;
  const reducedMotion = useReducedMotion();
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [source, setSource] = useState("");
  const [category, setCategory] = useState("");
  const [view, setView] = useState<"grid" | "list">("grid");
  const [revision, setRevision] = useState(0);
  const scope = JSON.stringify([query, source, category, cluster]);
  const requestKey = JSON.stringify([scope, revision]);
  const [result, setResult] = useState<{
    key: string;
    listing: ProviderListing;
    more: ProviderSummary[];
  } | null>(null);
  const [paginationKey, setPaginationKey] = useState<string | null>(null);
  const [failure, setFailure] = useState<{
    key: string;
    message: string;
  } | null>(null);
  const [selection, setSelection] = useState<{
    provider: ProviderSummary;
    scope: string;
  } | null>(null);
  const [recommendations, setRecommendations] = useState<{
    cluster: string | undefined;
    providers: ProviderSummary[];
  } | null>(null);
  const [catalogSnapshot, setCatalogSnapshot] = useState<{
    cluster: string | undefined;
    sources: CatalogStatus[];
  } | null>(null);
  const inspectorRef = useRef<HTMLElement>(null);

  useEffect(() => {
    const timeout = window.setTimeout(() => setQuery(q.trim()), 250);
    return () => window.clearTimeout(timeout);
  }, [q]);

  useEffect(() => {
    let live = true;
    api
      .listProviders({
        q: query,
        source,
        category,
        network: cluster,
        limit: PAGE,
      })
      .then((listing) => {
        if (live) {
          setResult({ key: requestKey, listing, more: [] });
          setFailure(null);
        }
      })
      .catch(
        (e) =>
          live &&
          setFailure({
            key: requestKey,
            message:
              e instanceof Error ? e.message : "Couldn't load the directory",
          }),
      );
    return () => {
      live = false;
    };
  }, [query, source, category, cluster, requestKey]);

  useEffect(() => {
    let live = true;
    api
      .listProviders({ network: cluster, limit: 1 })
      .then((listing) => {
        if (live)
          setCatalogSnapshot({ cluster, sources: listing.sources ?? [] });
      })
      .catch(() => {
        /* The primary listing still reports catalog errors. */
      });
    api
      .listProviders({ source: "circle", network: cluster, limit: 100 })
      .then((listing) => {
        const names = ["birdeye", "exa", "allium", "alchemy"];
        const providers = names.flatMap((name) => {
          const p = listing.providers.find((p) =>
            p.name.toLowerCase().includes(name),
          );
          return p ? [p] : [];
        });
        if (live) setRecommendations({ cluster, providers });
      })
      .catch(() => {
        if (live) setRecommendations({ cluster, providers: [] });
      });
    return () => {
      live = false;
    };
  }, [cluster, revision]);

  const error = failure?.key === requestKey ? failure.message : null;
  const loading = result?.key !== requestKey && !error;
  const listing = result?.listing;
  const providers =
    result?.key === requestKey
      ? [...result.listing.providers, ...result.more]
      : [];
  const selected = selection?.scope === scope ? selection.provider : null;
  const suggestions =
    recommendations && recommendations.cluster === cluster
      ? recommendations.providers
      : [];
  const categories = listing?.categories ?? [];
  const catalogStatuses =
    catalogSnapshot && catalogSnapshot.cluster === cluster
      ? catalogSnapshot.sources
      : result?.key === requestKey
        ? (listing?.sources ?? [])
        : [];
  const filtered = Boolean(q.trim() || source || category);
  // These are real entries from the same catalog set, kept at the front of the unfiltered view.
  // Pagination continues from the server's page offset; displayed IDs are deduplicated separately.
  const starters = !filtered && !loading && !error ? suggestions : [];
  const displayedProviders = Array.from(
    new Map([...starters, ...providers].map((p) => [p.id, p])).values(),
  );
  const loadingMore = paginationKey === requestKey;
  const canLoadMore = !loading && listing && providers.length < listing.total;

  async function loadMore() {
    if (loadingMore || loading) return;
    setPaginationKey(requestKey);
    try {
      const next = await api.listProviders({
        q: query,
        source,
        category,
        network: cluster,
        limit: PAGE,
        offset: providers.length,
      });
      setResult((current) =>
        current?.key === requestKey
          ? { ...current, more: [...current.more, ...next.providers] }
          : current,
      );
      setFailure((current) => (current?.key === requestKey ? null : current));
    } catch (e) {
      setFailure({
        key: requestKey,
        message:
          e instanceof Error ? e.message : "Couldn't load more providers",
      });
    } finally {
      setPaginationKey((key) => (key === requestKey ? null : key));
    }
  }

  function pick(provider: ProviderSummary) {
    if (selected?.id === provider.id) {
      setSelection(null);
      return;
    }
    setSelection({ provider, scope });
  }
  function clearFilters() {
    setQ("");
    setSource("");
    setCategory("");
    setSelection(null);
  }
  const activeCategory = categories.find((c) => c.name === category);

  useEffect(() => {
    if (selected)
      inspectorRef.current?.scrollIntoView({
        behavior: reducedMotion ? "instant" : "smooth",
        block: "start",
      });
  }, [selected, reducedMotion]);

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>
            <IconStore size={13} /> PROVIDER DIRECTORY
          </p>
          <h1>
            Your next capability. <span>One call away.</span>
          </h1>
          <p className={styles.subtitle}>
            Discover APIs across five catalogs. Find the right tool for your
            agent.
          </p>
        </div>
        <CatalogNetwork />
      </header>

      <div className={styles.overview}>
        <p className={styles.overviewSummary}>
          <span className={styles.statusDot} />
          <strong>
            {loading ? "—" : (listing?.total.toLocaleString() ?? "—")}
          </strong>
          {filtered ? "matching providers" : "providers"}
          <span className={styles.metaSeparator}>/</span>
          <IconPlug size={12} />
          {demo ? "All networks" : networkLabel(network)}
          <span className={styles.metaSeparator}>/</span>USDC
        </p>
        <Button
          variant="secondary"
          onClick={() => setRevision((v) => v + 1)}
          disabled={loading}
          className={styles.refreshButton}
          aria-label="Refresh directory"
        >
          {loading ? <Spinner size={14} /> : <IconRefresh size={14} />}
          <span className={styles.refreshLabel}>Refresh directory</span>
        </Button>
      </div>
      {demo && (
        <p className={styles.demoNote}>
          <IconShield size={14} />
          Demo mode: explore every catalog. Payments stay in the sandbox.
        </p>
      )}

      <section aria-label="Browse catalogs">
        <div className={styles.catalogHeading}>
          <h2>Choose your catalog</h2>
          <button
            type="button"
            onClick={() => setSource("")}
            aria-pressed={!source}
          >
            All catalogs <IconArrowRight size={12} />
          </button>
        </div>
        <div className={styles.catalogs}>
          {CATALOGS.map((catalog) => (
            <CatalogCard
              key={catalog.id}
              catalog={catalog}
              status={catalogStatuses.find((s) => s.name === catalog.id)}
              selected={source === catalog.id}
              onClick={() => {
                setSource(source === catalog.id ? "" : catalog.id);
                setSelection(null);
              }}
            />
          ))}
        </div>
      </section>

      <section className={styles.directory} aria-labelledby="directory-title">
        <div className={styles.searchRow}>
          <label className={styles.search}>
            <IconSearch size={18} />
            <span className="sr-only">Search providers</span>
            <input
              aria-label="Search providers"
              value={q}
              onChange={(e) => {
                setQ(e.target.value);
                setSelection(null);
              }}
              placeholder="Search providers, APIs, or capabilities…"
            />
            {q && (
              <button
                type="button"
                aria-label="Clear search"
                onClick={() => setQ("")}
              >
                <IconX size={14} />
              </button>
            )}
          </label>
          <label className={styles.categorySelect}>
            <span className="sr-only">Provider category</span>
            <select
              value={category}
              onChange={(e) => {
                setCategory(e.target.value);
                setSelection(null);
              }}
            >
              <option value="">All categories</option>
              {category && !activeCategory && (
                <option value={category}>{categoryLabel(category)}</option>
              )}
              {categories.map((c) => (
                <option value={c.name} key={c.name}>
                  {categoryLabel(c.name)} ({c.count})
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className={styles.quickCategories}>
          <button
            type="button"
            aria-pressed={!category}
            onClick={() => {
              setCategory("");
              setSelection(null);
            }}
          >
            <IconGrid size={13} />
            All capabilities
          </button>
          {QUICK_CATEGORIES.filter((id) =>
            categories.some((c) => c.name === id),
          ).map((id) => (
            <button
              key={id}
              type="button"
              aria-pressed={category === id}
              onClick={() => {
                setCategory(category === id ? "" : id);
                setSelection(null);
              }}
            >
              <CategoryIcon category={id} size={13} />
              {categoryLabel(id)}
            </button>
          ))}
        </div>
        <div className={styles.resultsBar}>
          <h2 id="directory-title">
            {source ? catalogName(source) : "Explore providers"}
            <span aria-live="polite">
              {loading
                ? "Updating…"
                : `${displayedProviders.length} of ${listing?.total.toLocaleString() ?? 0}`}
            </span>
          </h2>
          <div className={styles.resultActions}>
            {filtered && (
              <button
                type="button"
                onClick={clearFilters}
                className={styles.clearFilters}
              >
                <IconX size={12} />
                Clear filters
              </button>
            )}
            <div className={styles.viewToggle} aria-label="Provider layout">
              <button
                type="button"
                aria-label="Grid view"
                aria-pressed={view === "grid"}
                onClick={() => setView("grid")}
              >
                <IconGrid size={15} />
              </button>
              <button
                type="button"
                aria-label="List view"
                aria-pressed={view === "list"}
                onClick={() => setView("list")}
              >
                <IconList size={15} />
              </button>
            </div>
          </div>
        </div>
        {error && (
          <div className={styles.error}>
            <ErrorNote>{error}</ErrorNote>
            <Button
              variant="secondary"
              onClick={() => setRevision((v) => v + 1)}
            >
              Try again
            </Button>
          </div>
        )}

        {selected && (
          <motion.section
            ref={inspectorRef}
            id="provider-inspector"
            className={styles.inspector}
            initial={reducedMotion ? false : { opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.18, ease: "easeOut" }}
            aria-label="Selected provider"
          >
            <div className={styles.inspectorHeader}>
              <ProviderIdentity provider={selected} size={44} />
              <div>
                <span className={styles.detailEyebrow}>PROVIDER DETAILS</span>
                <h3>{providerTitle(selected)}</h3>
                <span className={styles.inspectorSource}>
                  {catalogName(selected.source)} · {selected.endpoint_count}{" "}
                  endpoints
                </span>
              </div>
              <button
                type="button"
                aria-label="Close provider details"
                onClick={() => setSelection(null)}
              >
                <IconX size={17} />
              </button>
            </div>
            <details className={styles.identityDetails}>
              <summary>
                Catalog identity &amp; description <IconExternal size={11} />
              </summary>
              <p>{selected.name}</p>
              <p>{selected.description}</p>
              <div>
                <code>{selected.id}</code>
                <Copy text={selected.id} label="Copy ID" />
              </div>
              {safeExternalUrl(selected.website) && (
                <a
                  href={safeExternalUrl(selected.website)}
                  target="_blank"
                  rel="noreferrer"
                >
                  Provider website <IconExternal size={11} />
                </a>
              )}
            </details>
            <ProviderEndpoints key={selected.id} provider={selected} />
          </motion.section>
        )}

        <div
          className={`${styles.providers} ${view === "list" ? styles.listView : ""}`}
          aria-busy={loading}
        >
          {loading &&
            Array.from({ length: 6 }, (_, i) => (
              <div className={styles.cardSkeleton} key={i}>
                <Skeleton className="size-11" />
                <Skeleton className="mt-4 h-4 w-2/3" />
                <Skeleton className="mt-2 h-3 w-5/6" />
                <Skeleton className="mt-6 h-4 w-full" />
              </div>
            ))}
          {displayedProviders.map((provider) => (
            <ProviderCard
              key={provider.id}
              provider={provider}
              selected={selected?.id === provider.id}
              onClick={() => pick(provider)}
              reducedMotion={Boolean(reducedMotion)}
            />
          ))}
        </div>
        {!loading && !error && listing?.total === 0 && (
          <div className={styles.empty}>
            <span>
              <IconSearch size={25} />
            </span>
            <h3>No providers found</h3>
            <p>
              {!demo && network === "solana-devnet"
                ? "Few APIs accept devnet USDC. Try another search or browse Mainnet at the top."
                : "Try another search, category, or catalog."}
            </p>
            <Button variant="secondary" onClick={clearFilters}>
              Reset filters <IconArrowRight size={14} />
            </Button>
          </div>
        )}
        {canLoadMore && (
          <div className={styles.loadMore}>
            <span>
              {displayedProviders.length} of {listing!.total.toLocaleString()}{" "}
              providers
            </span>
            <Button
              variant="secondary"
              onClick={loadMore}
              disabled={loadingMore}
            >
              {loadingMore ? <Spinner size={14} /> : <IconGrid size={14} />}
              {loadingMore ? "Loading providers…" : "Load more providers"}
            </Button>
          </div>
        )}
        <div className={styles.directoryFooter}>
          <span>
            <IconShield size={14} />
            Listed prices are estimates. Algebra checks the quote before
            payment.
          </span>
          <Link href="/console/routing">
            Explore routing <IconArrowRight size={13} />
          </Link>
        </div>
      </section>
    </div>
  );
}

function CatalogCard({
  catalog,
  status,
  selected,
  onClick,
}: {
  catalog: (typeof CATALOGS)[number];
  status?: CatalogStatus;
  selected: boolean;
  onClick: () => void;
}) {
  const label =
    catalog.id === "circle"
      ? "Circle"
      : catalog.id === "cdp"
        ? "Coinbase"
        : catalog.name;
  const subtitle =
    catalog.id === "circle"
      ? "Agent Marketplace"
      : catalog.id === "cdp"
        ? "Bazaar"
        : catalog.id === "monid"
          ? "Tool directory"
          : "API catalog";
  return (
    <button
      type="button"
      className={`${styles.catalogCard} ${selected ? styles.selectedCatalog : ""}`}
      aria-pressed={selected}
      onClick={onClick}
      title={catalog.blurb}
    >
      <span className={styles.catalogCardTop}>
        <CatalogLogo source={catalog.id} size={30} />
        <span className={styles.catalogCheck}>
          {selected ? <IconCheck size={12} /> : <IconArrowRight size={12} />}
        </span>
      </span>
      <strong>{label}</strong>
      <span>{subtitle}</span>
      <div
        className={styles.catalogStatus}
        data-state={status?.error ? "error" : status?.stale ? "stale" : "ready"}
      >
        <i />
        {status?.error
          ? "Unavailable"
          : status?.stale
            ? "Cached"
            : status
              ? `${status.total.toLocaleString()} provider${status.total === 1 ? "" : "s"}`
              : "Loading…"}
      </div>
    </button>
  );
}

function ProviderIdentity({
  provider,
  size = 40,
}: {
  provider: ProviderSummary;
  size?: number;
}) {
  const fallbackCategory =
    provider.category === "other" &&
    /inference|language model|llm/i.test(provider.description)
      ? "ai_ml"
      : provider.category;
  const fallback = (
    <span
      className={styles.providerGlyph}
      style={{ width: size, height: size }}
    >
      <CategoryIcon category={fallbackCategory} size={Math.round(size * 0.5)} />
    </span>
  );
  return (
    <ProviderLogo
      name={providerTitle(provider)}
      logo={provider.logo_url}
      website={provider.website}
      host={provider.host}
      fqn={provider.fqn}
      size={size}
      fallback={fallback}
    />
  );
}

function ProviderCard({
  provider: p,
  selected,
  onClick,
  reducedMotion,
}: {
  provider: ProviderSummary;
  selected: boolean;
  onClick: () => void;
  reducedMotion: boolean;
}) {
  const priceId = `provider-price-${encodeURIComponent(p.id)}`;
  return (
    <motion.button
      type="button"
      className={`${styles.providerCard} ${selected ? styles.selectedProvider : ""}`}
      onClick={onClick}
      aria-expanded={selected}
      aria-controls="provider-inspector"
      aria-label={`Inspect ${providerTitle(p)} from ${catalogName(p.source)} (${compactHost(p.host)})`}
      aria-describedby={priceId}
      initial={reducedMotion ? false : { opacity: 0, y: 8 }}
      whileInView={{ opacity: 1, y: 0 }}
      viewport={{ once: true, amount: 0.08 }}
      transition={{ duration: 0.18, ease: "easeOut" }}
    >
      <span className={styles.providerCardHeader}>
        <ProviderIdentity provider={p} />
        <span className={styles.sourceBadge}>
          <CatalogLogo source={p.source} size={15} />
          {p.source === "circle"
            ? "Circle"
            : p.source === "cdp"
              ? "Coinbase"
              : catalogName(p.source)}
        </span>
        <span className={styles.cardArrow}>
          {selected ? <IconX size={14} /> : <IconArrowRight size={14} />}
        </span>
      </span>
      <span className={styles.providerCardBody}>
        <strong title={p.name}>{providerTitle(p)}</strong>
        <span className={styles.providerHost} title={p.host}>
          {compactHost(p.host)}
        </span>
        <span className={styles.providerDescription} title={p.description}>
          {providerSummary(p)}
        </span>
        <span className={styles.tags}>
          <span>
            <CategoryIcon category={p.category} size={11} />
            {categoryLabel(p.category)}
          </span>
          {p.billing === "monid-balance" ? (
            <span title="Algebra cannot pay a prepaid Monid balance">
              Listed only
            </span>
          ) : (
            (p.networks ?? []).map((n) => (
              <span key={n}>
                <i />
                {networkLabel(n)}
              </span>
            ))
          )}
        </span>
      </span>
      <span className={styles.providerCardFooter} id={priceId}>
        <span>
          <IconTag size={12} />
          <strong>
            {formatPriceRange(p.min_price_minor, p.max_price_minor)}
          </strong>
        </span>
        <span>
          {p.endpoint_count} API{p.endpoint_count === 1 ? "" : "s"}
        </span>
      </span>
      {(p.payers_30d ?? 0) > 0 && (
        <span className={styles.usageNote}>
          <IconClock size={10} />
          {p.payers_30d} directory-reported payers · 30 days
        </span>
      )}
    </motion.button>
  );
}
