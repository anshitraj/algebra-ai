"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { IssuedKey, Project } from "@/lib/platform-types";
import type { Simulation } from "@/lib/routing-types";
import type { PassConnect, SpendPass } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { Button, ErrorNote, PageHeader, Panel, Skeleton } from "@/components/console/ui";
import { Copy, Snippet } from "@/components/console/snippet";
import { EnvBadge } from "@/components/console/environment";
import { IconCheck, Spinner } from "@/components/icons";

// Demo A: signup → project → sandbox pass (the policy) → API key → a dry run
// → a sandbox execution → its response and signed receipt. Sandbox only: no
// wallet, no chain, no money. The run here uses the signed-in session acting
// under the project's sandbox pass; the API key is for the developer's server.

const TASKS = [
  { capability: "token.price", label: "Token price", input: { mint: "So11111111111111111111111111111111111111112" } },
  { capability: "weather.forecast", label: "Weather forecast", input: { city: "Bengaluru" } },
  { capability: "web.search", label: "Web search", input: { query: "x402 payments on Solana" } },
];
const MODES = [
  { id: "cheapest", label: "Cheapest", detail: "Lowest live price that passes every guard" },
  { id: "fastest", label: "Fastest", detail: "Lowest measured latency" },
  { id: "balanced", label: "Balanced", detail: "Price, speed and reliability together" },
];

type ExecuteOut = {
  delivered?: boolean;
  summary?: string;
  response?: unknown;
  response_is_untrusted_provider_data?: boolean;
  receipt?: string;
  rejected?: { provider?: string; code: string; detail?: string }[];
  attempts?: { result?: { provider?: string; actual_cost_minor?: number; latency_ms?: number; network?: string; test?: boolean } }[];
  intent?: { id: string; state: string; committed_minor?: number };
};

export default function QuickstartPage() {
  const [project, setProject] = useState<Project | null>(null);
  const [pass, setPass] = useState<SpendPass | null | undefined>(undefined);
  const [connect, setConnect] = useState<PassConnect | null>(null);
  const [key, setKey] = useState<IssuedKey | null>(null);
  const [hasKey, setHasKey] = useState(false);
  const [task, setTask] = useState(TASKS[0]);
  const [mode, setMode] = useState("cheapest");
  const [sim, setSim] = useState<Simulation | null>(null);
  const [out, setOut] = useState<ExecuteOut | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const orgs = await api.listOrgs();
    const org = orgs.organizations[0];
    if (!org) return;
    const p = (await api.listProjects(org.id)).projects[0] ?? null;
    setProject(p);
    if (!p) return;
    const [passes, keys] = await Promise.all([api.listProjectPasses(p.id, "sandbox"), api.listKeys(p.id)]);
    setPass(passes.passes.find((x) => x.active) ?? null);
    setConnect(keys.connect);
    setHasKey(keys.keys.some((k) => k.environment === "sandbox" && !k.revoked_at));
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetching from the REST API on mount
    load().catch((e) => setError(e instanceof Error ? e.message : "Couldn't load your workspace"));
  }, [load]);

  async function step(name: string, fn: () => Promise<void>) {
    setBusy(name);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That didn't work");
    } finally {
      setBusy(null);
    }
  }

  const createPass = () =>
    step("pass", async () => {
      await api.createPass({
        label: "Sandbox pass", agent_kind: "custom", currency: "USDC", budget_minor_units: 1_000_000, budget_period: "month",
        max_per_purchase_minor_units: 50_000, allowed_categories: ["digital_services"], expires_in_days: 30, project_id: project!.id, environment: "sandbox",
      });
      await load();
    });
  const issueKey = () =>
    step("key", async () => {
      setKey((await api.createKey(project!.id, { name: "quickstart", environment: "sandbox", scopes: ["read", "simulate", "execute"] })).key);
      setHasKey(true);
    });
  const body = () => ({ capability: task.capability, input: task.input, budget_max_minor: 50_000, constraints: { allowed_networks: ["sandbox"] }, provider_policy: { strategy: mode } });
  const dryRun = () =>
    step("sim", async () => {
      setOut(null);
      setSim(await api.simulatePolicy({ ...body(), spend_pass_id: pass!.id, live_quotes: true }));
    });
  const run = () =>
    step("run", async () => {
      setOut(await api.executeAsPass<ExecuteOut>(pass!.id, { ...body(), window: `quickstart-${Date.now()}` }));
      await load();
    });

  const apiBase = connect?.api_base ?? "https://YOUR_ALGEBRA_HOST/api/v1";
  const rest = `curl -X POST "${apiBase}/execute" \\
  -H "Authorization: Bearer $ALGEBRA_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '${JSON.stringify({ ...body(), window: "my-first-call" })}'`;
  const ts = `const res = await fetch("${apiBase}/execute", {
  method: "POST",
  headers: { Authorization: \`Bearer \${process.env.ALGEBRA_API_KEY}\`, "Content-Type": "application/json" },
  body: JSON.stringify(${JSON.stringify(body())}),
});
const { summary, response, receipt } = await res.json();`;
  const mcp = `{
  "mcpServers": {
    "algebra": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "${connect?.mcp_url ?? "https://YOUR_ALGEBRA_HOST/mcp"}", "--header", "Authorization: Bearer \${ALGEBRA_API_KEY}"]
    }
  }
}`;
  const last = out?.attempts?.[out.attempts.length - 1]?.result;

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader
        title="Quickstart"
        description="From an empty account to a paid API call with a signed receipt, in the sandbox: simulated providers and simulated USDC, no wallet and no money. Devnet comes after."
      />
      {error && <div className="mt-6"><ErrorNote>{error}</ErrorNote></div>}
      {pass === undefined && !error && <Skeleton className="mt-8 h-40" />}

      {pass !== undefined && project && (
        <ol className="mt-8 space-y-4">
          <Step n={1} done title="Your project" body={<>Every account starts with a workspace and a project: <strong className="text-foreground">{project.name}</strong>. <Link href="/console/projects" className="text-primary hover:underline">Manage projects</Link></>} />

          <Step n={2} done={!!pass} title="A sandbox Spend Pass: the policy" body={
            pass ? (
              <>Budget {formatUSDC(pass.budget_minor_units)} a month, at most {formatUSDC(pass.max_per_purchase_minor_units ?? 0)} a call, {formatUSDC(pass.remaining_minor_units)} left. Algebra checks it on every request; the agent can&apos;t change it.</>
            ) : (
              <>The pass is what your keys may spend: a budget and a cap per call. Nothing is paid without one.</>
            )
          } action={!pass && <Button onClick={createPass} disabled={busy !== null}>{busy === "pass" && <Spinner size={13} />} Create a sandbox pass (1 USDC, 0.05 a call)</Button>} />

          <Step n={3} done={hasKey} title="A sandbox API key" body={<>Your server calls Algebra with it. It works only in the sandbox, through the pass above.</>}
            action={!key && <Button variant={hasKey ? "secondary" : "primary"} onClick={issueKey} disabled={busy !== null}>{busy === "key" && <Spinner size={13} />} {hasKey ? "Issue another" : "Issue a key"}</Button>}>
            {key && (
              <div className="mt-3 rounded-lg border border-primary/40 bg-background p-3">
                <p className="text-xs font-medium text-foreground">Copy it now: it is shown once.</p>
                <div className="mt-2 flex items-center gap-2"><code className="min-w-0 flex-1 font-mono text-xs break-all">{key.secret}</code><Copy text={key.secret} label="Copy" /></div>
              </div>
            )}
          </Step>

          <Step n={4} done={!!sim} title="Ask for work, not a provider" body={<>Pick a task and how to choose among the providers that can do it. A dry run shows who would be paid and why the others were refused. Nothing is spent.</>}>
            <div className="mt-3 flex flex-wrap gap-2">
              {TASKS.map((t) => (
                <button key={t.capability} type="button" aria-pressed={task === t} onClick={() => { setTask(t); setSim(null); setOut(null); }}
                  className={`rounded-full border px-3 py-1.5 text-xs ${task === t ? "border-primary bg-primary-tint text-foreground" : "border-border-strong text-foreground"}`}>{t.label}</button>
              ))}
            </div>
            <div className="mt-3 grid gap-2 sm:grid-cols-3">
              {MODES.map((m) => (
                <label key={m.id} className={`cursor-pointer rounded-lg border px-3 py-2 text-xs ${mode === m.id ? "border-primary bg-primary-tint/50" : "border-border"}`}>
                  <input type="radio" name="mode" className="sr-only" checked={mode === m.id} onChange={() => { setMode(m.id); setSim(null); }} />
                  <span className="font-medium text-foreground">{m.label}</span><span className="mt-0.5 block text-muted">{m.detail}</span>
                </label>
              ))}
            </div>
            <div className="mt-3"><Button variant="secondary" onClick={dryRun} disabled={!pass || busy !== null}>{busy === "sim" && <Spinner size={13} />} Dry run</Button></div>
            {sim && (
              <div className="mt-3 rounded-lg border border-border bg-background p-3 text-xs">
                <p className="text-foreground"><strong>{sim.verdict}</strong>{sim.would_pay ? ` · would pay ${sim.would_pay.provider} ${formatUSDC(sim.would_pay.price_minor)} (${sim.would_pay.price_source} price)` : ""}</p>
                {(sim.rejected ?? []).length > 0 && (
                  <ul className="mt-2 space-y-1 text-muted">
                    {sim.rejected!.map((r, i) => <li key={i}><span className="font-mono text-foreground">{r.provider}</span> refused: {r.code}{r.detail ? ` (${r.detail})` : ""}</li>)}
                  </ul>
                )}
              </div>
            )}
          </Step>

          <Step n={5} done={!!out?.delivered} title="Run it" body={<>Algebra reserves the budget, pays the chosen provider in the sandbox, checks the answer and signs a receipt. Asking again with the same request returns the kept answer and pays nothing.</>}
            action={<Button onClick={run} disabled={!pass || busy !== null}>{busy === "run" && <Spinner size={13} />} Execute in the sandbox</Button>}>
            {out && (
              <div className="mt-3 space-y-3">
                <p className="text-sm text-foreground">{out.summary}</p>
                {last && (
                  <p className="flex flex-wrap items-center gap-2 text-xs text-muted">
                    <EnvBadge env="sandbox" /> {last.provider} · {formatUSDC(last.actual_cost_minor ?? 0)} · {last.latency_ms} ms{last.test ? " · test payment, no money moved" : ""}
                  </p>
                )}
                {out.response !== undefined && (
                  <Snippet title={out.response_is_untrusted_provider_data ? "The provider's answer (untrusted data)" : "The provider's answer"} code={JSON.stringify(out.response, null, 2)} />
                )}
                {out.receipt && (
                  <div className="rounded-lg border border-border bg-background p-3">
                    <p className="text-xs font-medium text-foreground">Signed receipt</p>
                    <p className="mt-1 line-clamp-2 font-mono text-[0.6875rem] break-all text-muted">{out.receipt}</p>
                    <div className="mt-2 flex gap-3">
                      <Copy text={out.receipt} label="Copy receipt" />
                      <Link href={`/verify?r=${encodeURIComponent(out.receipt)}`} className="text-xs font-medium text-primary hover:underline">Verify it</Link>
                    </div>
                  </div>
                )}
              </div>
            )}
          </Step>

          <Step n={6} title="Call it from your code" body={<>The same request with your API key, over REST, from TypeScript, or as MCP tools for an agent. Keep the key on your server.</>}>
            <div className="mt-3 space-y-3">
              <Snippet title="REST" code={rest} />
              <Snippet title="TypeScript (fetch)" code={ts} />
              <Snippet title="MCP client configuration" code={mcp} />
            </div>
          </Step>

          <Step n={7} title="Then: real devnet payments" body={<>Make a devnet key and pass on <Link href="/console/projects" className="text-primary hover:underline">Projects</Link>, put the pass on Solana from your wallet on <Link href="/console/passes" className="text-primary hover:underline">Spend passes</Link>, fund it with devnet USDC, and the same call pays on chain with an explorer link.</>} />
        </ol>
      )}
    </div>
  );
}

function Step({ n, title, body, done, action, children }: { n: number; title: string; body: React.ReactNode; done?: boolean; action?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <li>
      <Panel className="py-5">
        <div className="flex gap-4">
          <span className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold ${done ? "bg-primary text-primary-tint" : "bg-border text-muted"}`}>
            {done ? <IconCheck size={14} /> : n}
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="text-sm font-semibold text-foreground">{title}</h2>
            <p className="mt-1 text-sm leading-relaxed text-muted">{body}</p>
            {action && <div className="mt-3">{action}</div>}
            {children}
          </div>
        </div>
      </Panel>
    </li>
  );
}
