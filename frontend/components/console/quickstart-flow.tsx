"use client";

import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import * as api from "@/lib/api-client";
import type { IssuedKey, Project } from "@/lib/platform-types";
import type { Simulation } from "@/lib/routing-types";
import type { PassConnect, SpendPass } from "@/lib/types";
import { mcpSetup } from "@/lib/mcp-clients";
import { formatUSDC } from "@/lib/money";
import { Button, ErrorNote, Skeleton } from "./ui";
import { Copy, Snippet } from "./snippet";
import { DevnetAirdropCard } from "./airdrop-action";
import {
  IconArrowLeft,
  IconArrowRight,
  IconCheck,
  IconEye,
  IconGauge,
  IconLock,
  IconPlug,
  IconReceipt,
  IconShield,
  Spinner,
} from "@/components/icons";
import styles from "./quickstart.module.css";

const STEPS = [
  {
    label: "Set your limits",
    hint: "You're in control",
    title: "A little freedom. Clear limits.",
    body: "Give your agent a budget and a limit for each call.",
    icon: IconShield,
  },
  {
    label: "Get your key",
    hint: "Your server's connection",
    title: "Your connection to Algebra.",
    body: "Create a sandbox key for your server or agent.",
    icon: IconLock,
  },
  {
    label: "Preview a request",
    hint: "See before you spend",
    title: "What would you like to try?",
    body: "Choose a task. See the provider and price before you run it.",
    icon: IconEye,
  },
  {
    label: "Run & get a receipt",
    hint: "Watch it come together",
    title: "Make your first call.",
    body: "Algebra checks your limits, runs the task and signs a receipt.",
    icon: IconReceipt,
  },
  {
    label: "Connect your code",
    hint: "Make it yours",
    title: "Take it into your own project.",
    body: "Choose how you want to connect. Your limits come with you.",
    icon: IconPlug,
  },
];
const TASKS = [
  {
    capability: "token.price",
    label: "Token price",
    detail: "Check the price of SOL",
    input: { mint: "So11111111111111111111111111111111111111112" },
    icon: IconGauge,
  },
  {
    capability: "weather.forecast",
    label: "Weather",
    detail: "Forecast for Bengaluru",
    input: { city: "Bengaluru" },
    icon: IconEye,
  },
  {
    capability: "web.search",
    label: "Web search",
    detail: "Find x402 payment resources",
    input: { query: "x402 payments on Solana" },
    icon: IconPlug,
  },
];
const MODES = [
  { id: "cheapest", label: "Cheapest", detail: "Save on each call" },
  { id: "fastest", label: "Fastest", detail: "Prioritize speed" },
  { id: "balanced", label: "Balanced", detail: "Balance cost & speed" },
];
type ExecuteOut = {
  delivered?: boolean;
  summary?: string;
  response?: unknown;
  response_is_untrusted_provider_data?: boolean;
  receipt?: string;
  attempts?: {
    result?: {
      provider?: string;
      actual_cost_minor?: number;
      latency_ms?: number;
      test?: boolean;
    };
  }[];
  intent?: { id: string; state: string; committed_minor?: number };
};
type Preview = Pick<
  Simulation,
  "verdict" | "would_pay" | "rejected" | "reasons"
>;
type Limits = Pick<
  SpendPass,
  | "id"
  | "budget_minor_units"
  | "max_per_purchase_minor_units"
  | "remaining_minor_units"
  | "budget_period"
>;
const DEMO_PROJECT: Project = {
  id: "demo-project",
  org_id: "demo-workspace",
  name: "Your first project",
  default_strategy: "cheapest",
  created_at: "",
};
const DEMO_PASS: Limits = {
  id: "demo-pass",
  budget_minor_units: 1_000_000,
  max_per_purchase_minor_units: 50_000,
  remaining_minor_units: 1_000_000,
  budget_period: "month",
};
const demoWait = () => new Promise<void>((resolve) => setTimeout(resolve, 450));

/** The public demo and authenticated sandbox share the same guide and controls. */
export function QuickstartFlow({ demo = false }: { demo?: boolean }) {
  const reduce = useReducedMotion();
  const heading = useRef<HTMLHeadingElement>(null);
  const submitting = useRef(false);
  const requestWindow = useRef<string | null>(null);
  const [active, setActive] = useState(0);
  const [direction, setDirection] = useState(1);
  const [finished, setFinished] = useState(false);
  const [project, setProject] = useState<Project | null>(
    demo ? DEMO_PROJECT : null,
  );
  const [pass, setPass] = useState<Limits | null>(null);
  const [loading, setLoading] = useState(!demo);
  const [connect, setConnect] = useState<PassConnect | null>(null);
  const [key, setKey] = useState<IssuedKey | null>(null);
  const [hasKey, setHasKey] = useState(false);
  const [savedKey, setSavedKey] = useState(false);
  const [task, setTask] = useState(TASKS[0]);
  const [mode, setMode] = useState("cheapest");
  const [sim, setSim] = useState<Preview | null>(null);
  const [out, setOut] = useState<ExecuteOut | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [integration, setIntegration] = useState("rest");

  const load = useCallback(async () => {
    if (demo) return;
    const org = (await api.listOrgs()).organizations[0];
    if (!org)
      throw new Error(
        "Create a workspace in Projects & API keys to start this guide.",
      );
    const p = (await api.listProjects(org.id)).projects[0];
    if (!p)
      throw new Error(
        "Create a project in Projects & API keys to start this guide.",
      );
    const [passes, keys] = await Promise.all([
      api.listProjectPasses(p.id, "sandbox"),
      api.listKeys(p.id),
    ]);
    setProject(p);
    setPass(passes.passes.find((x) => x.active) ?? null);
    setConnect(keys.connect);
    setHasKey(
      keys.keys.some((k) => k.environment === "sandbox" && !k.revoked_at),
    );
  }, [demo]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- hydrate existing sandbox resources from the API
    load()
      .catch((e) =>
        setError(
          e instanceof Error ? e.message : "Couldn't load your workspace",
        ),
      )
      .finally(() => setLoading(false));
  }, [load]);

  async function action(name: string, fn: () => Promise<void>) {
    if (submitting.current) return;
    submitting.current = true;
    setBusy(name);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "That didn't work. Please try again.",
      );
    } finally {
      submitting.current = false;
      setBusy(null);
    }
  }

  const readyToRun = sim?.verdict === "ALLOW" && !!sim.would_pay;
  const keyReady = hasKey && (!key || savedKey);
  const completed = [!!pass, keyReady, readyToRun, !!out?.delivered, finished];
  const unlocked = !pass
    ? 0
    : !keyReady
      ? 1
      : !readyToRun
        ? 2
        : !out?.delivered
          ? 3
          : 4;
  function go(next: number) {
    if (busy || next > unlocked) return;
    setDirection(next > active ? 1 : -1);
    setActive(next);
    setError(null);
  }
  function resetRequest() {
    requestWindow.current = null;
    setSim(null);
    setOut(null);
    setFinished(false);
  }

  const createPass = () =>
    action("pass", async () => {
      if (demo) {
        await demoWait();
        setPass({ ...DEMO_PASS });
        return;
      }
      await api.createPass({
        // The name demo mode gives its pass too (lib/practice.ts): one pass, whichever way in.
        label: "Demo pass",
        agent_kind: "custom",
        currency: "USDC",
        budget_minor_units: 1_000_000,
        budget_period: "month",
        max_per_purchase_minor_units: 50_000,
        allowed_categories: ["digital_services"],
        expires_in_days: 30,
        project_id: project!.id,
        environment: "sandbox",
      });
      await load();
    });
  const issueKey = () =>
    action("key", async () => {
      if (demo) {
        await demoWait();
        setHasKey(true);
        return;
      }
      const issued = await api.createKey(project!.id, {
        name: "quickstart",
        environment: "sandbox",
        scopes: ["read", "simulate", "execute"],
      });
      setKey(issued.key);
      setConnect(issued.connect);
      setSavedKey(false);
      setHasKey(true);
    });
  const body = () => ({
    capability: task.capability,
    input: task.input,
    budget_max_minor: 50_000,
    constraints: { allowed_networks: ["sandbox"] },
    provider_policy: { strategy: mode },
  });
  const dryRun = () =>
    action("sim", async () => {
      resetRequest();
      if (demo) {
        await demoWait();
        const price = mode === "fastest" ? 4000 : 2000;
        setSim({
          verdict: "ALLOW",
          reasons: [],
          would_pay: {
            provider: mode === "fastest" ? "sandbox:beta" : "sandbox:alpha",
            price_minor: price,
            total_minor: price,
            price_source: "live",
            verdict: "ALLOW",
            decision: "ALLOW",
            seller: { known: true, payment_tested: false, payee_pinned: false },
          },
        });
        return;
      }
      setSim(
        await api.simulatePolicy({
          ...body(),
          spend_pass_id: pass!.id,
          live_quotes: true,
        }),
      );
    });
  const run = () =>
    action("run", async () => {
      if (!readyToRun || !pass || out?.delivered) return;
      if (demo) {
        await demoWait();
        const price = sim!.would_pay!.price_minor;
        setOut({
          delivered: true,
          summary: "Your example request is complete.",
          response:
            task.capability === "token.price"
              ? { token: "SOL", price_usd: 150, illustrative: true }
              : task.capability === "weather.forecast"
                ? {
                    city: "Bengaluru",
                    forecast: "Partly cloudy",
                    illustrative: true,
                  }
                : {
                    results: [
                      {
                        title: "Example x402 payment resource",
                        illustrative: true,
                      },
                    ],
                  },
          attempts: [
            {
              result: {
                provider: sim!.would_pay!.provider,
                actual_cost_minor: price,
                latency_ms: 120,
                test: true,
              },
            },
          ],
        });
        setPass({
          ...pass,
          remaining_minor_units: pass.remaining_minor_units - price,
        });
        return;
      }
      // A retry after a dropped response reuses the request rather than paying twice.
      requestWindow.current ??= `quickstart-${crypto.randomUUID()}`;
      setOut(
        await api.executeAsPass<ExecuteOut>(pass.id, {
          ...body(),
          window: requestWindow.current,
        }),
      );
      await load();
    });

  const apiBase = connect?.api_base ?? "https://YOUR_ALGEBRA_HOST/api/v1";
  const rest = `curl -X POST "${apiBase}/execute" \\\n  -H "Authorization: Bearer $ALGEBRA_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify({ ...body(), window: "my-first-call" })}'`;
  const ts = `const res = await fetch("${apiBase}/execute", {\n  method: "POST",\n  headers: {\n    Authorization: \`Bearer \${process.env.ALGEBRA_API_KEY}\`,\n    "Content-Type": "application/json",\n  },\n  body: JSON.stringify(${JSON.stringify({ ...body(), window: "my-first-call" })}),\n});\nif (!res.ok) throw new Error(await res.text());\nconst { summary, response, receipt } = await res.json();`;
  const mcp = mcpSetup(
    "claude-desktop",
    connect?.mcp_url ?? "https://YOUR_ALGEBRA_HOST/mcp",
  );
  const last = out?.attempts?.[out.attempts.length - 1]?.result;
  const count = completed.filter(Boolean).length;
  const current = STEPS[active];
  const CurrentIcon = current.icon;
  const refusals = Array.from(
    new Map(
      (sim?.rejected ?? [])
        .filter((r) => r.code !== "network_not_allowed")
        .map((r) => [`${r.provider}|${r.code}`, r]),
    ).values(),
  );

  // The console page's h1. The landing demo sits under its own page title, so there it is an h3.
  const Title = demo ? "h3" : "h1";
  return (
    <section
      className={`${styles.guide} ${demo ? styles.demo : ""}`}
      aria-label={demo ? "Interactive quickstart demo" : "Quickstart guide"}
    >
      <header className={styles.header}>
        <div>
          <span className={styles.eyebrow}>YOUR FIRST ALGEBRA CALL</span>
          <Title>From idea to receipt.</Title>
          <p>Five small steps. One working connection.</p>
        </div>
        <span className={styles.environment}>
          <span /> {demo ? "Interactive demo" : "Sandbox guide"}
        </span>
      </header>
      <div className={styles.safety}>
        <IconShield size={15} />
        <span>
          {demo
            ? "Try the whole flow with example data."
            : "A safe place to start."}{" "}
          Simulated USDC. No wallet needed.
        </span>
      </div>
      {loading ? (
        <div aria-label="Loading your workspace" aria-busy="true">
          <Skeleton className="mt-6 h-80" />
        </div>
      ) : !project ? (
        <div className={styles.empty}>
          {error && <ErrorNote>{error}</ErrorNote>}
          <div className="mt-4 flex gap-3">
            <Button
              variant="secondary"
              disabled={!!busy}
              onClick={() => action("load", load)}
            >
              Try again
            </Button>
            <Link href="/console/projects">
              Projects & API keys <IconArrowRight size={14} />
            </Link>
          </div>
        </div>
      ) : (
        <div className={styles.layout}>
          <aside className={styles.rail}>
            <div className={styles.progressLabel}>
              <span>Your quickstart</span>
              <span>{count} / 5</span>
            </div>
            <div
              className={styles.progress}
              role="progressbar"
              aria-label="Quickstart completion"
              aria-valuemin={0}
              aria-valuemax={5}
              aria-valuenow={count}
            >
              <span style={{ width: `${count * 20}%` }} />
            </div>
            <nav aria-label="Quickstart steps">
              <ol>
                {STEPS.map((s, i) => (
                  <li key={s.label}>
                    <button
                      type="button"
                      disabled={i > unlocked || !!busy}
                      aria-current={active === i ? "step" : undefined}
                      onClick={() => go(i)}
                      className={active === i ? styles.activeStep : ""}
                    >
                      <span
                        className={`${styles.stepNumber} ${completed[i] ? styles.doneNumber : ""}`}
                      >
                        {completed[i] ? <IconCheck size={13} /> : `0${i + 1}`}
                      </span>
                      <span>
                        <strong>{s.label}</strong>
                        <small>{s.hint}</small>
                      </span>
                    </button>
                  </li>
                ))}
              </ol>
            </nav>
            <div className={styles.project}>
              <span className={styles.projectMark}>A</span>
              <div>
                <small>YOUR PROJECT</small>
                <strong>{project.name}</strong>
              </div>
            </div>
            <p className={styles.railNote}>
              Your progress stays here as you move between steps.
            </p>
          </aside>
          <div className={styles.stage}>
            <AnimatePresence mode="wait" initial={false}>
              <motion.div
                key={`${active}-${finished && active === 4}`}
                initial={{
                  opacity: reduce ? 1 : 0,
                  x: reduce ? 0 : direction * 12,
                }}
                animate={{ opacity: 1, x: 0 }}
                exit={{
                  opacity: reduce ? 1 : 0,
                  x: reduce ? 0 : direction * -8,
                }}
                transition={{ duration: reduce ? 0 : 0.18 }}
                onAnimationComplete={() => {
                  heading.current?.focus({ preventScroll: true });
                  heading.current?.scrollIntoView({
                    block: "nearest",
                    behavior: reduce ? "instant" : "smooth",
                  });
                }}
              >
                <div className={styles.stageTop}>
                  <span className={styles.stageIcon}>
                    {finished && active === 4 ? (
                      <IconCheck size={22} />
                    ) : (
                      <CurrentIcon size={22} />
                    )}
                  </span>
                  <span className={styles.eyebrow}>
                    STEP 0{active + 1} / 05
                  </span>
                </div>
                <h2 ref={heading} tabIndex={-1}>
                  {finished && active === 4
                    ? "You're ready to build."
                    : current.title}
                </h2>
                <p className={styles.description}>
                  {finished && active === 4
                    ? "Your first call is complete. Bring these limits to your agent next."
                    : current.body}
                </p>
                {error && (
                  <div className="mt-5">
                    <ErrorNote>{error}</ErrorNote>
                  </div>
                )}
                <div className={styles.content} aria-busy={!!busy}>
                  {active === 0 && (
                    <>
                      <div className={styles.passCard}>
                        <div className={styles.passTop}>
                          <IconShield size={17} />
                          <span>SANDBOX SPEND PASS</span>
                          <span>{pass ? "Active" : "Ready to create"}</span>
                        </div>
                        <div className={styles.limitGrid}>
                          <div>
                            <small>
                              {!pass || pass.budget_period === "month"
                                ? "Monthly budget"
                                : pass.budget_period === "week"
                                  ? "Weekly budget"
                                  : "Total budget"}
                            </small>
                            <strong>
                              {formatUSDC(
                                pass?.budget_minor_units ?? 1_000_000,
                              ).replace(" USDC", "")}
                              <em>USDC</em>
                            </strong>
                          </div>
                          <div>
                            <small>Maximum per call</small>
                            <strong>
                              {pass &&
                              pass.max_per_purchase_minor_units ===
                                undefined ? (
                                "Budget only"
                              ) : (
                                <>
                                  {formatUSDC(
                                    pass?.max_per_purchase_minor_units ??
                                      50_000,
                                  ).replace(" USDC", "")}
                                  <em>USDC</em>
                                </>
                              )}
                            </strong>
                          </div>
                        </div>
                        <div className={styles.passBottom}>
                          <IconLock size={13} /> Checked on every request. Your
                          agent can&apos;t change these limits.
                        </div>
                      </div>
                      {pass && (
                        <p className={styles.success} role="status">
                          <IconCheck size={14} /> Your pass is ready.{" "}
                          {formatUSDC(pass.remaining_minor_units)} remaining.
                        </p>
                      )}
                      <details className={styles.details}>
                        <summary>What is a Spend Pass?</summary>
                        <p>
                          A Spend Pass is your agent&apos;s permission to spend.
                          It sets the budget, per-call ceiling and allowed
                          providers. You can manage it in Spend passes.
                          {pass && (
                            <>
                              {" "}
                              This pass uses a {pass.budget_period} budget
                              period.
                            </>
                          )}
                        </p>
                      </details>
                      {!pass && (
                        <Button onClick={createPass} disabled={!!busy}>
                          {busy === "pass" ? (
                            <Spinner size={14} />
                          ) : (
                            <IconShield size={15} />
                          )}{" "}
                          Create sandbox pass
                        </Button>
                      )}
                    </>
                  )}
                  {active === 1 && (
                    <>
                      <div className={styles.connection}>
                        <span className={styles.connectionNode}>
                          <IconPlug size={23} />
                          <small>Your agent</small>
                        </span>
                        <span className={styles.connectionLine}>
                          <IconLock size={15} />
                        </span>
                        <span className={styles.connectionNode}>
                          <strong>a</strong>
                          <small>Algebra</small>
                        </span>
                      </div>
                      {demo ? (
                        hasKey && (
                          <div className={styles.result} role="status">
                            <IconCheck size={17} />
                            <div>
                              <strong>Example key ready</strong>
                              <p>
                                Your real key will be shown once after you
                                create an account.
                              </p>
                            </div>
                          </div>
                        )
                      ) : key ? (
                        <div className={styles.keyCard}>
                          <span className={styles.eyebrow}>
                            SHOWN ONCE · SAVE IT NOW
                          </span>
                          <div className={styles.secret}>
                            <code>{key.secret}</code>
                            <Copy text={key.secret} label="Copy key" />
                          </div>
                          <label className={styles.confirm}>
                            <input
                              type="checkbox"
                              checked={savedKey}
                              onChange={(e) => setSavedKey(e.target.checked)}
                            />{" "}
                            I&apos;ve saved my key somewhere private.
                          </label>
                        </div>
                      ) : (
                        hasKey && (
                          <div className={styles.result}>
                            <IconCheck size={17} />
                            <div>
                              <strong>You already have a sandbox key.</strong>
                              <p>
                                Use the key you saved, or create another below.
                              </p>
                            </div>
                          </div>
                        )
                      )}
                      {!key && !(demo && hasKey) && (
                        <Button
                          variant={hasKey ? "secondary" : "primary"}
                          onClick={issueKey}
                          disabled={!!busy}
                        >
                          {busy === "key" ? (
                            <Spinner size={14} />
                          ) : (
                            <IconLock size={15} />
                          )}
                          {hasKey
                            ? "Create another key"
                            : demo
                              ? "Try creating a key"
                              : "Create sandbox key"}
                        </Button>
                      )}
                      <p className={styles.note}>
                        Keep your key on your server. This key works only in the
                        sandbox.
                      </p>
                    </>
                  )}
                  {active === 2 && (
                    <>
                      <fieldset className={styles.fieldset} disabled={!!busy}>
                        <legend>Choose a task</legend>
                        <div className={styles.choices}>
                          {TASKS.map((t) => (
                            <button
                              type="button"
                              key={t.capability}
                              aria-pressed={task === t}
                              className={task === t ? styles.selected : ""}
                              onClick={() => {
                                setTask(t);
                                resetRequest();
                              }}
                            >
                              <t.icon size={19} />
                              <strong>{t.label}</strong>
                              <small>{t.detail}</small>
                            </button>
                          ))}
                        </div>
                      </fieldset>
                      <fieldset className={styles.fieldset} disabled={!!busy}>
                        <legend>What matters most?</legend>
                        <div className={styles.modes}>
                          {MODES.map((m) => (
                            <label
                              key={m.id}
                              className={mode === m.id ? styles.selected : ""}
                            >
                              <input
                                type="radio"
                                name={
                                  demo ? "demo-routing-mode" : "routing-mode"
                                }
                                checked={mode === m.id}
                                onChange={() => {
                                  setMode(m.id);
                                  resetRequest();
                                }}
                              />
                              <span>
                                <strong>{m.label}</strong>
                                <small>{m.detail}</small>
                              </span>
                            </label>
                          ))}
                        </div>
                      </fieldset>
                      <Button
                        variant="secondary"
                        onClick={dryRun}
                        disabled={!!busy || !pass}
                      >
                        {busy === "sim" ? (
                          <Spinner size={14} />
                        ) : (
                          <IconEye size={15} />
                        )}
                        {busy === "sim"
                          ? "Checking providers…"
                          : sim
                            ? "Preview again"
                            : "Preview request"}
                      </Button>
                      <span className={styles.inlineNote}>No USDC spent</span>
                      {sim && (
                        <>
                          <div className={styles.result} role="status">
                            {readyToRun ? (
                              <IconCheck size={18} />
                            ) : (
                              <IconShield size={18} />
                            )}
                            <div>
                              <strong>
                                {readyToRun
                                  ? "Within your limits. Ready to run."
                                  : sim.verdict === "REQUIRE_APPROVAL"
                                    ? "This request needs approval."
                                    : "This request can't run yet."}
                              </strong>
                              {sim.would_pay && (
                                <p>
                                  {sim.would_pay.provider} ·{" "}
                                  {formatUSDC(sim.would_pay.price_minor)}
                                </p>
                              )}
                              {!readyToRun && (
                                <p>
                                  {sim.reasons?.join(" · ") ||
                                    "Choose another task or review your Spend Pass limits."}
                                </p>
                              )}
                            </div>
                          </div>
                          {refusals.length > 0 && (
                            <details className={styles.details}>
                              <summary>
                                Why were other providers skipped?
                              </summary>
                              <ul>
                                {refusals.map((r) => (
                                  <li key={`${r.provider}|${r.code}`}>
                                    <strong>{r.provider}</strong>:{" "}
                                    {r.code.replace(/_/g, " ")}
                                    {r.detail ? ` (${r.detail})` : ""}
                                  </li>
                                ))}
                              </ul>
                            </details>
                          )}
                        </>
                      )}
                    </>
                  )}
                  {active === 3 && (
                    <>
                      <div className={styles.requestSummary}>
                        <span>
                          <small>YOUR TASK</small>
                          <strong>{task.label}</strong>
                        </span>
                        <span>
                          <small>
                            {out?.delivered ? "SPENT" : "PREVIEW PRICE"}
                          </small>
                          <strong>
                            {formatUSDC(
                              out?.delivered
                                ? (last?.actual_cost_minor ?? 0)
                                : (sim?.would_pay?.price_minor ?? 0),
                            )}
                          </strong>
                        </span>
                      </div>
                      <ol
                        className={styles.execution}
                        aria-label="Request lifecycle"
                      >
                        {["Check limits", "Run task", "Get receipt"].map(
                          (label, i) => (
                            <li
                              key={label}
                              className={
                                out?.delivered
                                  ? styles.executionDone
                                  : busy === "run"
                                    ? styles.executionBusy
                                    : ""
                              }
                            >
                              <span>
                                {out?.delivered ? (
                                  <IconCheck size={13} />
                                ) : (
                                  i + 1
                                )}
                              </span>
                              {label}
                            </li>
                          ),
                        )}
                      </ol>
                      <p className={styles.note} role="status">
                        {busy === "run"
                          ? "Waiting for Algebra to return your result and receipt…"
                          : "Sandbox only. All payments here are simulated."}
                      </p>
                      {!out?.delivered && (
                        <Button onClick={run} disabled={!!busy || !readyToRun}>
                          {busy === "run" ? (
                            <Spinner size={14} />
                          ) : (
                            <IconArrowRight size={15} />
                          )}
                          {busy === "run"
                            ? "Running your request…"
                            : "Run in sandbox"}
                        </Button>
                      )}
                      {out && (
                        <>
                          <div className={styles.result} role="status">
                            {out.delivered ? (
                              <IconCheck size={18} />
                            ) : (
                              <IconShield size={18} />
                            )}
                            <div>
                              <strong>
                                {out.delivered
                                  ? "Your first call, delivered."
                                  : "Your call wasn't delivered."}
                              </strong>
                              <p>
                                {out.summary ||
                                  (out.delivered
                                    ? "The provider returned an answer."
                                    : "Review your limits or preview another task.")}
                              </p>
                              {last && (
                                <p>
                                  {last.provider} · {last.latency_ms} ms ·
                                  simulated payment
                                </p>
                              )}
                            </div>
                          </div>
                          {out.response !== undefined && (
                            <details className={styles.details}>
                              <summary>View the provider&apos;s answer</summary>
                              <div className="mt-3">
                                <Snippet
                                  title="Provider response (untrusted data)"
                                  code={JSON.stringify(out.response, null, 2)}
                                />
                              </div>
                            </details>
                          )}
                          {demo && out.delivered && (
                            <div className={styles.receipt}>
                              <IconReceipt size={20} />
                              <div>
                                <strong>Example receipt</strong>
                                <p>
                                  {last?.provider} ·{" "}
                                  {formatUSDC(last?.actual_cost_minor ?? 0)} ·
                                  sandbox
                                </p>
                                <small>
                                  A real account gets a signed, verifiable
                                  receipt.
                                </small>
                              </div>
                            </div>
                          )}
                          {out.receipt && (
                            <div className={styles.receipt}>
                              <IconReceipt size={21} />
                              <div>
                                <strong>Signed receipt</strong>
                                <p>Your proof of what ran and what it cost.</p>
                                <div className={styles.receiptActions}>
                                  <Copy
                                    text={out.receipt}
                                    label="Copy receipt"
                                  />
                                  <Link
                                    href={`/verify?r=${encodeURIComponent(out.receipt)}`}
                                  >
                                    Verify receipt <IconArrowRight size={13} />
                                  </Link>
                                </div>
                              </div>
                            </div>
                          )}
                        </>
                      )}
                    </>
                  )}
                  {active === 4 &&
                    (finished ? (
                      <div className={styles.finish}>
                        <div className={styles.finishChecks}>
                          {["Limits set", "Key ready", "Request delivered"].map(
                            (label) => (
                              <span key={label}>
                                <IconCheck size={14} />
                                {label}
                              </span>
                            ),
                          )}
                        </div>
                        <Link
                          className={styles.linkButton}
                          href={demo ? "/signup" : "/console/connect"}
                        >
                          {demo ? "Create your workspace" : "Connect an agent"}
                          <IconArrowRight size={15} />
                        </Link>
                        {demo ? (
                          <>
                            <Link
                              href="/signup"
                              className={styles.secondaryLink}
                            >
                              Continue with real sandbox calls
                              <IconArrowRight size={14} />
                            </Link>
                            <p className={styles.note}>
                              Your account starts with the same guided sandbox setup.
                            </p>
                          </>
                        ) : (
                          <DevnetAirdropCard />
                        )}
                      </div>
                    ) : (
                      <>
                        <div
                          className={styles.tabs}
                          role="tablist"
                          aria-label="Connection method"
                        >
                          {[
                            { id: "rest", label: "REST" },
                            { id: "ts", label: "TypeScript" },
                            { id: "mcp", label: "MCP agent" },
                          ].map((t, i, tabs) => (
                            <button
                              key={t.id}
                              id={`quickstart-${demo ? "demo-" : ""}tab-${t.id}`}
                              type="button"
                              role="tab"
                              aria-selected={integration === t.id}
                              aria-controls={`quickstart-${demo ? "demo-" : ""}code`}
                              tabIndex={integration === t.id ? 0 : -1}
                              onClick={() => setIntegration(t.id)}
                              onKeyDown={(e) => {
                                const next =
                                  e.key === "ArrowRight"
                                    ? (i + 1) % tabs.length
                                    : e.key === "ArrowLeft"
                                      ? (i + tabs.length - 1) % tabs.length
                                      : e.key === "Home"
                                        ? 0
                                        : e.key === "End"
                                          ? tabs.length - 1
                                          : -1;
                                if (next < 0) return;
                                e.preventDefault();
                                setIntegration(tabs[next].id);
                                document
                                  .getElementById(
                                    `quickstart-${demo ? "demo-" : ""}tab-${tabs[next].id}`,
                                  )
                                  ?.focus();
                              }}
                            >
                              {t.label}
                            </button>
                          ))}
                        </div>
                        <div
                          role="tabpanel"
                          id={`quickstart-${demo ? "demo-" : ""}code`}
                          aria-labelledby={`quickstart-${demo ? "demo-" : ""}tab-${integration}`}
                          className={styles.code}
                        >
                          <Snippet
                            title={
                              integration === "rest"
                                ? "Run from your server"
                                : integration === "ts"
                                  ? "Server-side TypeScript"
                                  : "Claude Desktop configuration"
                            }
                            code={
                              integration === "rest"
                                ? rest
                                : integration === "ts"
                                  ? ts
                                  : mcp.code
                            }
                          />
                        </div>
                        <p className={styles.note}>
                          {integration === "mcp"
                            ? mcp.note
                            : "Set ALGEBRA_API_KEY to your saved key in your server's environment."}
                          {demo &&
                            " Example URLs: replace them with your workspace's addresses."}
                        </p>
                        {!demo && (
                          <Link
                            href="/console/connect"
                            className={styles.secondaryLink}
                          >
                            More agent setup options{" "}
                            <IconArrowRight size={14} />
                          </Link>
                        )}
                      </>
                    ))}
                </div>
                <footer className={styles.footer}>
                  <Button
                    variant="secondary"
                    disabled={active === 0 || !!busy}
                    onClick={() => go(active - 1)}
                  >
                    <IconArrowLeft size={14} />
                    Back
                  </Button>
                  <span>
                    {active === 1 && key && !savedKey
                      ? "Save your key to continue"
                      : `Step ${active + 1} of 5`}
                  </span>
                  {active < 4 ? (
                    <Button
                      disabled={!completed[active] || !!busy}
                      onClick={() => go(active + 1)}
                    >
                      {active === 2
                        ? "Continue to run"
                        : active === 3
                          ? "Connect your code"
                          : "Continue"}
                      <IconArrowRight size={14} />
                    </Button>
                  ) : (
                    <Button
                      disabled={finished}
                      onClick={() => setFinished(true)}
                    >
                      {finished ? "Complete" : "Finish quickstart"}
                      <IconCheck size={14} />
                    </Button>
                  )}
                </footer>
              </motion.div>
            </AnimatePresence>
          </div>
        </div>
      )}
    </section>
  );
}
