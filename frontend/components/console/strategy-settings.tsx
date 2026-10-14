"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { Project } from "@/lib/platform-types";
import type { ClassMember, Simulation } from "@/lib/routing-types";
import type { SpendPass } from "@/lib/types";
import { PICKS, pickLabel, type Pick } from "@/lib/strategy";
import { decisionOf, leadCandidate } from "@/lib/decision";
import { formatUSDC } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { Button, ErrorNote } from "@/components/console/ui";
import { ProviderLogo } from "@/components/provider-logo";
import {
  IconArrowRight,
  IconCheck,
  IconChevronDown,
  IconClock,
  IconGauge,
  IconSettings,
  IconShield,
  IconTag,
  Spinner,
} from "@/components/icons";
import styles from "./strategy-settings.module.css";

type Row = { project: Project; orgName: string; manage: boolean };
const STRATEGIES = {
  cheapest: { icon: IconTag, hint: "Lowest total cost", focus: "Price first" },
  balanced: {
    icon: IconGauge,
    hint: "Cost, speed & reliability",
    focus: "Best overall fit",
  },
  fastest: {
    icon: IconClock,
    hint: "Shortest time to a result",
    focus: "Speed first",
  },
};

/** Defaults persist per project; the chips describe each strategy's focus. */
export function StrategySettings() {
  const [rows, setRows] = useState<Row[] | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState("");
  const [saved, setSaved] = useState("");

  useEffect(() => {
    let live = true;
    (async () => {
      try {
        const out: Row[] = [];
        for (const org of (await api.listOrgs()).organizations) {
          for (const project of (await api.listProjects(org.id)).projects) {
            out.push({
              project,
              orgName: org.name,
              manage: org.role === "owner" || org.role === "admin",
            });
          }
        }
        if (live) setRows(out);
      } catch (e) {
        if (live)
          setError(
            e instanceof Error ? e.message : "Couldn't load your projects",
          );
      }
    })();
    return () => {
      live = false;
    };
  }, []);

  async function setDefault(id: string, value: string) {
    setSaving(id);
    setSaved("");
    setError("");
    try {
      const updated = await api.updateProject(id, {
        default_strategy: value as Project["default_strategy"],
      });
      setRows(
        (rs) =>
          rs?.map((r) =>
            r.project.id === id
              ? {
                  ...r,
                  project: {
                    ...r.project,
                    default_strategy: updated.default_strategy,
                  },
                }
              : r,
          ) ?? null,
      );
      setSaved(id);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't save the default");
    } finally {
      setSaving("");
    }
  }

  return (
    <details className={styles.settings}>
      <summary className={styles.summary}>
        <span className={styles.settingsIcon}>
          <IconSettings size={19} />
        </span>
        <span className={styles.summaryText}>
          <strong>Routing preferences</strong>
          <span>Set the default strategy for each project</span>
        </span>
        <span className={styles.strategyPills}>
          {PICKS.map((p) => {
            const Icon = STRATEGIES[p.id].icon;
            return (
              <span key={p.id}>
                <Icon size={12} />
                {p.label}
              </span>
            );
          })}
        </span>
        <span className={styles.configure}>
          Configure <IconChevronDown size={14} />
        </span>
      </summary>
      <div className={styles.settingsBody}>
        <div className={styles.strategyCards}>
          {PICKS.map((p) => {
            const strategy = STRATEGIES[p.id];
            const Icon = strategy.icon;
            return (
              <div key={p.id} className={styles.strategyCard}>
                <div>
                  <Icon size={19} />
                  <span>{strategy.focus}</span>
                </div>
                <h3>{p.label}</h3>
                <p title={p.hint}>{strategy.hint}</p>
                <div
                  className={styles.signals}
                  aria-label={`Focus of ${p.label}`}
                >
                  {["Cost", "Speed", "Reliability"].map((label, i) => (
                    <span
                      key={label}
                      data-emphasis={
                        p.id === "balanced" ||
                        (p.id === "cheapest" && i === 0) ||
                        (p.id === "fastest" && i === 1)
                      }
                    >
                      {label}
                    </span>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
        <p className={styles.guardNote}>
          <IconShield size={14} />
          Unhealthy, overpriced and blocked providers are filtered first. Your
          Spend Pass still sets the limits.
        </p>
        <div className={styles.defaultsHeading}>
          <h3>Project defaults</h3>
          <span>A strategy on the request overrides this default.</span>
        </div>
        {error && <ErrorNote>{error}</ErrorNote>}
        <ul className={styles.projectList}>
          {rows === null && !error && (
            <li className={styles.loading}>Loading projects…</li>
          )}
          {rows?.length === 0 && (
            <li className={styles.loading}>
              No projects yet.{" "}
              <Link href="/console/projects">
                Create a project <IconArrowRight size={12} />
              </Link>
            </li>
          )}
          {rows?.map(({ project, orgName, manage }) => (
            <li key={project.id}>
              <span className={styles.projectIcon}>
                <IconGauge size={17} />
              </span>
              <span className={styles.projectName}>
                <strong>{project.name}</strong>
                <span>{orgName}</span>
              </span>
              <span className={styles.saveStatus} role="status">
                {saving === project.id ? (
                  <Spinner size={13} />
                ) : saved === project.id ? (
                  <>
                    <IconCheck size={12} />
                    Saved
                  </>
                ) : null}
              </span>
              <select
                value={project.default_strategy}
                disabled={!manage || Boolean(saving)}
                onChange={(e) => setDefault(project.id, e.target.value)}
                aria-label={`Default strategy for ${project.name}`}
              >
                <option value="">Ask in chat · Balanced elsewhere</option>
                {PICKS.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.label}
                  </option>
                ))}
              </select>
              {!manage && (
                <span className={styles.readOnly}>Owner or admin only</span>
              )}
            </li>
          ))}
        </ul>
      </div>
    </details>
  );
}

/** Unpaid policy simulations: changing the request hides results for the previous input. */
export function CompareStrategies({
  capability,
  sample,
  providers = [],
}: {
  capability: string;
  sample: Record<string, unknown>;
  providers?: ClassMember[];
}) {
  const { network } = useNetwork();
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [passId, setPassId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<{
    key: string;
    rows: { pick: Pick; sim: Simulation }[];
  } | null>(null);
  const requestKey = JSON.stringify([network, passId, capability, sample]);

  useEffect(() => {
    let live = true;
    api
      .listPasses()
      .then((r) => {
        if (!live) return;
        // A pass of this network's environment first: a demo pass can't be asked about a chain, nor a test one about practice.
        const env =
          network === "sandbox" ? "sandbox" : network === "solana-devnet" ? "devnet" : "mainnet";
        const fits = (p: SpendPass) => !p.environment || p.environment === env;
        const usable = (r.passes ?? [])
          .filter((p) => p.active && p.currency === "USDC")
          .sort((a, b) => Number(fits(b)) - Number(fits(a)));
        setPasses(usable);
        setPassId(usable[0]?.id || "");
      })
      .catch((e) => {
        if (live) {
          setPasses([]);
          setError(
            e instanceof Error ? e.message : "Couldn't load Spend Passes",
          );
        }
      });
    return () => {
      live = false;
    };
  }, [network]);

  async function run() {
    setBusy(true);
    setError("");
    setResult(null);
    try {
      const out: { pick: Pick; sim: Simulation }[] = [];
      for (const p of PICKS) {
        const sim = await api.simulatePolicy({
          capability,
          input: sample,
          budget_max_minor: 50_000,
          spend_pass_id: passId,
          constraints: { allowed_networks: [network] },
          provider_policy: { strategy: p.id },
          live_quotes: true,
        });
        out.push({ pick: p.id, sim });
      }
      setResult({ key: requestKey, rows: out });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The dry run failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className={styles.compare}>
      {error && <ErrorNote>{error}</ErrorNote>}
      {passes !== null && passes.length === 0 ? (
        <div className={styles.noPass}>
          <IconShield size={20} />
          <p>A USDC Spend Pass is needed to test your route.</p>
          <Link href="/console/passes">
            Create a Spend Pass <IconArrowRight size={13} />
          </Link>
        </div>
      ) : (
        <>
          <label className={styles.passLabel}>
            Spend Pass
            <select
              value={passId}
              disabled={busy || !passes}
              onChange={(e) => setPassId(e.target.value)}
              aria-label="Spend Pass"
            >
              {!passes && <option value="">Loading…</option>}
              {passes?.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label}
                </option>
              ))}
            </select>
          </label>
          <div className={styles.quoteMeta}>
            <span>{networkLabel(network)}</span>
            <span>Max {formatUSDC(50_000)}</span>
          </div>
          <Button
            className={styles.compareButton}
            onClick={run}
            disabled={busy || !passId}
          >
            {busy ? <Spinner size={14} /> : <IconGauge size={15} />}
            {busy ? "Comparing live quotes…" : "Compare all 3 strategies"}
            {!busy && <IconArrowRight size={14} />}
          </Button>
          <p className={styles.unpaidNote}>
            Unpaid quotes only. No funds reserved or spent.
          </p>
          {result?.key === requestKey && (
            <div className={styles.results} aria-live="polite">
              {result.rows.map(({ pick, sim }) => {
                const lead = leadCandidate(sim);
                const d = decisionOf(sim.decision ?? sim.verdict);
                const Icon = STRATEGIES[pick].icon;
                const provider = providers.find(
                  (m) => m.provider === lead?.provider,
                );
                return (
                  <div
                    key={pick}
                    className={styles.result}
                    data-strategy={pick}
                  >
                    <div className={styles.resultTitle}>
                      <Icon size={14} />
                      <strong>
                        {pickLabel(sim.strategy) || pickLabel(pick)}
                      </strong>
                      <span data-decision={d}>
                        {d === "ALLOW" && <IconCheck size={10} />}
                        {d}
                      </span>
                    </div>
                    <div className={styles.resultProvider}>
                      {lead && (
                        <ProviderLogo
                          name={provider?.provider_name || lead.provider}
                          logo={provider?.logo_url}
                          host={provider ? hostOf(provider.url) : undefined}
                          fqn={lead.provider}
                          size={27}
                        />
                      )}
                      <span>
                        {lead
                          ? provider?.provider_name || lead.provider
                          : "No eligible provider"}
                      </span>
                      <strong>
                        {lead ? formatUSDC(lead.total_minor) : "—"}
                      </strong>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </>
      )}
    </div>
  );
}

function hostOf(url: string) {
  try {
    return new URL(url).hostname;
  } catch {
    return undefined;
  }
}
