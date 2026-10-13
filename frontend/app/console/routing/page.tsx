"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import * as api from "@/lib/api-client";
import type {
  ClassDetail,
  ClassMember,
  ClassRow,
  EndpointHealth,
} from "@/lib/routing-types";
import { formatUSDC } from "@/lib/money";
import { catalogName } from "@/lib/paysh";
import { networkLabel } from "@/lib/network";
import { Button, ErrorNote, Skeleton } from "@/components/console/ui";
import { ProviderLogo } from "@/components/provider-logo";
import {
  CompareStrategies,
  StrategySettings,
} from "@/components/console/strategy-settings";
import {
  IconArrowRight,
  IconCheck,
  IconChat,
  IconClock,
  IconGauge,
  IconGrid,
  IconMail,
  IconRefresh,
  IconSearch,
  IconShield,
  IconTag,
  IconWallet,
  IconX,
  Spinner,
} from "@/components/icons";
import { Logo } from "@/components/logo";
import styles from "./routing.module.css";

const GROUPS = ["All", "Onchain", "Web & data", "AI & media"] as const;
type Group = (typeof GROUPS)[number];

function groupOf(id: string): Group {
  if (/^(solana|token|wallet)\./.test(id)) return "Onchain";
  if (/^(llm|image|text)\./.test(id)) return "AI & media";
  return "Web & data";
}

function CapabilityIcon({ id, size = 21 }: { id: string; size?: number }) {
  if (id.includes("risk")) return <IconShield size={size} />;
  if (id.startsWith("wallet")) return <IconWallet size={size} />;
  if (id.startsWith("token")) return <IconTag size={size} />;
  if (id.startsWith("llm") || id.startsWith("text"))
    return <IconChat size={size} />;
  if (id.startsWith("email")) return <IconMail size={size} />;
  if (id.startsWith("weather"))
    return (
      <svg
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        aria-hidden="true"
      >
        <path d="M8 15a4 4 0 1 1 4-5 3.5 3.5 0 1 1 2 6H8" />
        <path d="M5 3v2M1 7h2M2 3l1.5 1.5M7 20v1M12 20v1M17 20v1" />
      </svg>
    );
  if (id.startsWith("image"))
    return (
      <svg
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <rect x="3" y="3" width="18" height="18" rx="4" />
        <circle cx="8" cy="8" r="1.5" />
        <path d="m3 17 5-5 4 4 4-6 5 7" />
      </svg>
    );
  if (id.startsWith("ip") || id.startsWith("domain"))
    return (
      <svg
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        aria-hidden="true"
      >
        <circle cx="12" cy="12" r="9" />
        <ellipse cx="12" cy="12" rx="4" ry="9" />
        <path d="M3 12h18M5 6h14M5 18h14" />
      </svg>
    );
  return <IconSearch size={size} />;
}

export default function RoutingPage() {
  const [classes, setClasses] = useState<ClassRow[] | null>(null);
  const [selected, setSelected] = useState("token.price");
  const [query, setQuery] = useState("");
  const [group, setGroup] = useState<Group>("All");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api
      .listClasses()
      .then((r) => {
        if (!live) return;
        setClasses(r.classes);
        if (!r.classes.some((c) => c.id === "token.price"))
          setSelected(r.classes[0]?.id ?? "");
      })
      .catch(
        (e) =>
          live &&
          setError(
            e instanceof Error ? e.message : "Couldn't load capabilities",
          ),
      );
    return () => {
      live = false;
    };
  }, []);

  const totals = useMemo(
    () =>
      (classes ?? []).reduce(
        (t, c) => ({
          endpoints: t.endpoints + c.providers,
          routable: t.routable + c.routable,
        }),
        { endpoints: 0, routable: 0 },
      ),
    [classes],
  );
  const visible = (classes ?? []).filter(
    (c) =>
      (group === "All" || groupOf(c.id) === group) &&
      `${c.title} ${c.id} ${c.description}`
        .toLowerCase()
        .includes(query.trim().toLowerCase()),
  );

  function selectCapability(id: string) {
    setSelected(id);
    if (window.matchMedia("(max-width: 850px)").matches) {
      document.getElementById("capability-detail")?.scrollIntoView({
        behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
          ? "instant"
          : "smooth",
        block: "start",
      });
    }
  }

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>
            <IconGauge size={13} /> ROUTING WORKSPACE
          </p>
          <h1>
            One request. <span>The right provider.</span>
          </h1>
          <p className={styles.subtitle}>
            Explore a capability. Compare providers. Find your route.
          </p>
        </div>
        <Link href="/console/connect" className={styles.headerLink}>
          Connect your agent <IconArrowRight size={15} />
        </Link>
      </header>

      <div className={styles.overview}>
        <dl className={styles.metrics}>
          <div>
            <dt>Capabilities</dt>
            <dd>{classes ? classes.length : "—"}</dd>
          </div>
          <div>
            <dt>Catalog endpoints</dt>
            <dd>{classes ? totals.endpoints.toLocaleString() : "—"}</dd>
          </div>
          <div>
            <dt>Routable endpoints</dt>
            <dd>
              {classes ? totals.routable.toLocaleString() : "—"}
              <span className={styles.dot} />
            </dd>
          </div>
        </dl>
        <div className={styles.guardNote}>
          <IconShield size={18} />
          <div>
            <strong>Your rules, every route</strong>
            <span>Spend Pass limits always apply</span>
          </div>
        </div>
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className={styles.workspace}>
        <section
          className={styles.browser}
          aria-labelledby="capabilities-title"
        >
          <div className={styles.sectionHeading}>
            <h2 id="capabilities-title">What do you want to do?</h2>
            <span>
              {classes ? `${classes.length} capabilities` : "Loading…"}
            </span>
          </div>
          <div className={styles.search}>
            <IconSearch size={17} />
            <input
              aria-label="Search capabilities"
              placeholder="Search capabilities…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            {query && (
              <button
                type="button"
                aria-label="Clear search"
                onClick={() => setQuery("")}
              >
                <IconX size={14} />
              </button>
            )}
            <span className={styles.searchHint}>Explore</span>
          </div>
          <div className={styles.filters} aria-label="Capability categories">
            {GROUPS.map((g) => (
              <button
                key={g}
                type="button"
                aria-pressed={group === g}
                onClick={() => setGroup(g)}
                className={group === g ? styles.activeFilter : ""}
              >
                {g}
                {g === "All" && <span>{classes?.length ?? "—"}</span>}
              </button>
            ))}
          </div>
          <div className={styles.cards}>
            {!classes &&
              !error &&
              Array.from({ length: 6 }, (_, i) => (
                <Skeleton key={i} className="h-32" />
              ))}
            {visible.map((c) => {
              const on = selected === c.id;
              return (
                <button
                  key={c.id}
                  type="button"
                  aria-pressed={on}
                  aria-controls="capability-detail"
                  onClick={() => selectCapability(c.id)}
                  className={`${styles.card} ${on ? styles.selectedCard : ""}`}
                >
                  <span className={styles.cardTop}>
                    <span
                      className={styles.capabilityIcon}
                      data-group={groupOf(c.id)}
                    >
                      <CapabilityIcon id={c.id} />
                    </span>
                    <span className={styles.cardArrow}>
                      {on ? (
                        <IconCheck size={15} />
                      ) : (
                        <IconArrowRight size={15} />
                      )}
                    </span>
                  </span>
                  <strong>{c.title}</strong>
                  <code>{c.id}</code>
                  <span className={styles.cardBottom}>
                    <span>
                      <i className={styles.dot} />
                      {c.routable} <span>routable</span>
                    </span>
                    <span>
                      {c.median_price_minor > 0 ? (
                        <>
                          <b>
                            {formatUSDC(c.median_price_minor).replace(
                              " USDC",
                              "",
                            )}
                          </b>{" "}
                          <small>USDC</small>
                        </>
                      ) : (
                        <small>Price varies</small>
                      )}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
          {classes && visible.length === 0 && (
            <div className={styles.empty}>
              <IconSearch size={25} />
              <h3>No capabilities found</h3>
              <p>Try another search or category.</p>
              <button
                type="button"
                onClick={() => {
                  setQuery("");
                  setGroup("All");
                }}
              >
                Reset filters <IconArrowRight size={14} />
              </button>
            </div>
          )}
          <p className={styles.catalogNote}>
            <IconGrid size={13} /> Prices shown are catalog medians. Test a
            route for live quotes.
          </p>
        </section>
        {selected ? (
          <ClassPanel key={selected} id={selected} />
        ) : (
          classes && (
            <div className={styles.empty}>No capabilities are listed yet.</div>
          )
        )}
      </div>
      <StrategySettings />
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
  const [view, setView] = useState<"providers" | "test">("providers");
  const [routableOnly, setRoutableOnly] = useState(false);
  const [sort, setSort] = useState("routable");
  const [expanded, setExpanded] = useState(false);
  const [input, setInput] = useState("");

  useEffect(() => {
    let live = true;
    api
      .getClass(id)
      .then((r) => {
        if (!live) return;
        setDetail(r.class);
        setHealth(r.health ?? []);
        setInput(JSON.stringify(r.class.sample, null, 2));
      })
      .catch(
        (e) =>
          live &&
          setError(e instanceof Error ? e.message : "Couldn't load providers"),
      );
    return () => {
      live = false;
    };
  }, [id]);

  async function probe() {
    setProbing(true);
    setError(null);
    try {
      const r = await api.probeClass(id);
      setHealth(r.health);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The health check failed");
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
  const members = [...(detail?.members ?? [])]
    .filter((m) => !routableOnly || m.routable)
    .sort((a, b) =>
      sort === "price"
        ? (a.price_minor || Infinity) - (b.price_minor || Infinity)
        : sort === "name"
          ? a.provider_name.localeCompare(b.provider_name)
          : Number(b.routable) - Number(a.routable),
    );
  const brands = (detail?.members ?? [])
    .filter(
      (m, i, all) => all.findIndex((x) => x.provider === m.provider) === i,
    )
    .slice(0, 4);
  let sample: Record<string, unknown> | null = null;
  try {
    const parsed: unknown = JSON.parse(input);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed))
      sample = parsed as Record<string, unknown>;
  } catch {
    /* The editor displays validation below. */
  }

  return (
    <aside
      className={styles.inspector}
      id="capability-detail"
      aria-label="Selected capability"
    >
      {error && (
        <div className={styles.panelPadding}>
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}
      {!detail && !error && (
        <div className={styles.panelPadding}>
          <Skeleton className="h-96" />
        </div>
      )}
      {detail && (
        <>
          <div className={styles.detailHeader}>
            <div className={styles.detailEyebrow}>
              <span>CAPABILITY INSPECTOR</span>
              <span>
                <i className={styles.dot} />
                {detail.routable} routable
              </span>
            </div>
            <div className={styles.detailTitle}>
              <span className={styles.capabilityIcon} data-group={groupOf(id)}>
                <CapabilityIcon id={id} size={24} />
              </span>
              <div>
                <h2 aria-live="polite">{detail.title}</h2>
                <code>{id}</code>
              </div>
            </div>
            <div className={styles.providerStack}>
              <div>
                {brands.map((m) => (
                  <ProviderLogo
                    key={m.provider}
                    name={m.provider_name}
                    logo={m.logo_url}
                    host={hostOf(m.url)}
                    fqn={m.provider}
                    size={29}
                  />
                ))}
              </div>
              <span>
                <strong>{detail.members.length}</strong> catalog endpoints
              </span>
            </div>
            <div className={styles.routeFlow} aria-label="Routing process">
              <span>
                <IconChat size={17} />
                <small>Request</small>
              </span>
              <IconArrowRight size={13} />
              <span className={styles.flowCore}>
                <Logo size={21} />
                <small>Algebra</small>
              </span>
              <IconArrowRight size={13} />
              <span>
                <IconShield size={17} />
                <small>Your rules</small>
              </span>
              <IconArrowRight size={13} />
              <span>
                <IconCheck size={17} />
                <small>Best route</small>
              </span>
            </div>
          </div>
          <div className={styles.detailViews} aria-label="Inspector view">
            <button
              type="button"
              aria-pressed={view === "providers"}
              onClick={() => setView("providers")}
              className={view === "providers" ? styles.activeView : ""}
            >
              Providers <span>{detail.members.length}</span>
            </button>
            <button
              type="button"
              aria-pressed={view === "test"}
              onClick={() => setView("test")}
              className={view === "test" ? styles.activeView : ""}
            >
              <IconGauge size={14} />
              Test a route
            </button>
          </div>
          {view === "providers" ? (
            <>
              <div className={styles.providerToolbar}>
                <label>
                  <input
                    type="checkbox"
                    checked={routableOnly}
                    onChange={(e) => {
                      setRoutableOnly(e.target.checked);
                      setExpanded(false);
                    }}
                  />
                  Routable only
                </label>
                <select
                  aria-label="Sort providers"
                  value={sort}
                  onChange={(e) => setSort(e.target.value)}
                >
                  <option value="routable">Routable first</option>
                  <option value="price">Lowest listed price</option>
                  <option value="name">Name A–Z</option>
                </select>
              </div>
              <div className={styles.providerList}>
                {members.slice(0, expanded ? members.length : 5).map((m, i) => (
                  <MemberRow
                    key={`${m.provider}-${m.url}-${i}`}
                    m={m}
                    h={byMember.get(memberKey(m.provider, m.url))}
                    median={detail.median_price_minor}
                  />
                ))}
              </div>
              {members.length === 0 && (
                <p className={styles.panelEmpty}>
                  No {routableOnly ? "routable " : ""}providers for this
                  capability.
                </p>
              )}
              {members.length > 5 && (
                <button
                  type="button"
                  className={styles.showMore}
                  onClick={() => setExpanded(!expanded)}
                >
                  {expanded
                    ? "Show fewer providers"
                    : `View all ${members.length} endpoints`}
                  <IconArrowRight size={14} />
                </button>
              )}
              <div className={styles.detailFooter}>
                <Button
                  variant="secondary"
                  onClick={probe}
                  disabled={probing}
                  title="Checks providers with unpaid requests"
                >
                  {probing ? <Spinner size={14} /> : <IconRefresh size={14} />}
                  {probing ? "Checking…" : "Check health"}
                </Button>
                <span>
                  <IconClock size={12} />
                  Unpaid checks · every 15 min
                </span>
              </div>
            </>
          ) : (
            <div className={styles.testPanel}>
              <div className={styles.testHeading}>
                <h3>Preview before you pay</h3>
                <span className={styles.freeBadge}>No payment</span>
              </div>
              <p className={styles.testDescription}>{detail.description}</p>
              <label className={styles.inputLabel} htmlFor="route-input">
                Request input <span>JSON</span>
              </label>
              <textarea
                id="route-input"
                spellCheck={false}
                value={input}
                onChange={(e) => setInput(e.target.value)}
                rows={6}
                className={styles.inputEditor}
                aria-invalid={!sample}
                aria-describedby={!sample ? "input-error" : undefined}
              />
              {!sample && (
                <p id="input-error" className={styles.inputError}>
                  Enter a valid JSON object to compare routes.
                </p>
              )}
              <button
                type="button"
                className={styles.resetInput}
                onClick={() => setInput(JSON.stringify(detail.sample, null, 2))}
              >
                <IconRefresh size={12} />
                Reset sample
              </button>
              {sample && (
                <CompareStrategies
                  capability={detail.id}
                  sample={sample}
                  providers={detail.members}
                />
              )}
              <Link
                href={`/console/firewall?class=${encodeURIComponent(detail.id)}`}
                className={styles.firewallLink}
              >
                <IconShield size={15} />
                Open in Spend Firewall <IconArrowRight size={14} />
              </Link>
            </div>
          )}
        </>
      )}
    </aside>
  );
}

function MemberRow({
  m,
  h,
  median,
}: {
  m: ClassMember;
  h?: EndpointHealth;
  median: number;
}) {
  const outlier =
    median > 0 && m.price_minor > 50_000 && m.price_minor > median * 10;
  return (
    <div className={styles.providerRow}>
      <ProviderLogo
        name={m.provider_name}
        logo={m.logo_url}
        host={hostOf(m.url)}
        fqn={m.provider}
        size={36}
      />
      <div className={styles.providerIdentity}>
        <strong title={m.provider_name}>{m.provider_name}</strong>
        <span title={`${m.method} ${pathOf(m.url)}`}>
          {catalogName(m.source)} · {m.method} {pathOf(m.url)}
        </span>
        <div className={styles.providerBadges}>
          <span
            className={m.routable ? styles.routableBadge : styles.listedBadge}
            title={m.not_routable_reason}
          >
            {m.routable ? (
              <>
                <IconCheck size={10} />
                Routable
              </>
            ) : (
              "Listed only"
            )}
          </span>
          {h ? (
            <span
              className={
                h.status === "up" ? styles.healthUp : styles.healthOther
              }
              title={`${h.error || ""} · ${h.checks > 0 ? Math.round((h.ups / h.checks) * 100) : 0}% uptime across ${h.checks} checks`}
            >
              {
                {
                  up: "Healthy",
                  down: "Down",
                  input_rejected: "Input rejected",
                  unpayable: "Unpayable",
                }[h.status]
              }{" "}
              · {h.latency_ms} ms
            </span>
          ) : (
            <span className={styles.unchecked}>Not checked</span>
          )}
        </div>
      </div>
      <div className={styles.providerPrice}>
        <strong>
          {m.price_minor > 0
            ? formatUSDC(m.price_minor).replace(" USDC", "")
            : "—"}
        </strong>
        <small>USDC · listed</small>
        {h?.live_price_minor ? (
          <span className={h.overcharges ? styles.priceWarning : ""} title={`Last quote checked ${h.checked_at}`}>
            Quote {formatUSDC(h.live_price_minor)}
          </span>
        ) : null}
        {(outlier || h?.overcharges) && (
          <span className={styles.priceWarning}>
            {outlier ? "Price outlier" : "Over listing"}
          </span>
        )}
        <span title={(m.networks ?? []).map(networkLabel).join(", ")}>
          {(m.networks ?? []).map(networkLabel).join(", ") ||
            "Network unstated"}
        </span>
      </div>
    </div>
  );
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
