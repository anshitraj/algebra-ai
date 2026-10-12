"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { APIKey, Environment, IssuedKey, Organization, PlatformEvent, Project, ProjectUsage, Scope, Strategy } from "@/lib/platform-types";
import type { SpendPass } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { Button, EmptyState, ErrorNote, Field, Input, PageHeader, Panel, Skeleton, timeAgo } from "@/components/console/ui";
import { Copy, Snippet } from "@/components/console/snippet";
import { ENVIRONMENTS, EnvBadge } from "@/components/console/environment";
import { IconPlus, Spinner } from "@/components/icons";

const SCOPES: { id: Scope; label: string; detail: string }[] = [
  { id: "read", label: "Read", detail: "Its pass, intents and receipts" },
  { id: "simulate", label: "Simulate", detail: "Dry runs: who would be paid, nothing spent" },
  { id: "execute", label: "Execute", detail: "Pay within its project's Spend Pass" },
];
const STRATEGIES: { id: Strategy; label: string }[] = [
  { id: "", label: "None: balanced unless a request says otherwise" },
  { id: "cheapest", label: "Cheapest" },
  { id: "balanced", label: "Balanced" },
  { id: "fastest", label: "Fastest" },
];
const select = "h-10 w-full rounded-lg border border-border-strong bg-surface px-3 text-sm text-foreground focus-visible:border-primary";

export default function ProjectsPage() {
  const [orgs, setOrgs] = useState<Organization[] | null>(null);
  const [org, setOrg] = useState<Organization | null>(null);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [projectId, setProjectId] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [startingTrial, setStartingTrial] = useState(false);

  useEffect(() => {
    api
      .listOrgs()
      .then((r) => {
        setOrgs(r.organizations);
        setOrg(r.organizations[0] ?? null);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load your workspace"));
  }, []);

  useEffect(() => {
    if (!org) return;
    api
      .listProjects(org.id)
      .then((r) => {
        setProjects(r.projects);
        setProjectId((id) => (r.projects.some((p) => p.id === id) ? id : (r.projects[0]?.id ?? "")));
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load the projects"));
  }, [org]);

  const manage = org?.role === "owner" || org?.role === "admin";
  const project = projects?.find((p) => p.id === projectId) ?? null;

  async function create() {
    if (!org || !newName.trim()) return;
    setCreating(true);
    setError(null);
    try {
      const p = await api.createProject(org.id, newName.trim());
      setProjects((x) => [...(x ?? []), p]);
      setProjectId(p.id);
      setNewName("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create the project");
    } finally {
      setCreating(false);
    }
  }

  async function trial() {
    if (!org) return;
    setStartingTrial(true);
    setError(null);
    try {
      const updated = await api.startTrial(org.id);
      setOrg(updated);
      setOrgs((x) => x?.map((o) => (o.id === updated.id ? updated : o)) ?? null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't start the trial");
    } finally {
      setStartingTrial(false);
    }
  }

  const live = projects?.filter((p) => !p.archived_at).length ?? 0;
  const projectLimit = org?.entitlements?.projects ?? 1;
  const atProjectLimit = projectLimit >= 0 && live >= projectLimit;

  return (
    <div className="mx-auto max-w-5xl">
      <PageHeader
        title="Projects and API keys"
        description="An API key authenticates your application. It spends only through a Spend Pass of its own project and environment, so a sandbox key can never move devnet or mainnet money."
        actions={<Link href="/console/quickstart" className="inline-flex min-h-10 items-center rounded-md border border-border-strong px-4 text-sm font-medium text-foreground hover:bg-primary-tint">Quickstart</Link>}
      />
      {error && <div className="mt-6"><ErrorNote>{error}</ErrorNote></div>}
      {!orgs && !error && <Skeleton className="mt-8 h-28" />}
      {org && (
        <Panel className="mt-8 flex flex-wrap items-center justify-between gap-4 py-4">
          <div>
            <p className="text-xs text-muted">Organization</p>
            {orgs && orgs.length > 1 ? (
              <select aria-label="Organization" className={`${select} mt-1 w-64`} value={org.id} onChange={(e) => setOrg(orgs.find((o) => o.id === e.target.value) ?? null)}>
                {orgs.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
              </select>
            ) : (
              <p className="mt-0.5 font-display text-base font-semibold text-foreground">{org.name}</p>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="rounded-full bg-primary-tint px-2.5 py-0.5 font-medium text-primary capitalize">{org.plan} plan</span>
            {org.trial_active && org.trial_ends_at && (
              <span className="rounded-full bg-accent-tint px-2.5 py-0.5 font-medium text-accent">
                <span className="capitalize">{org.trial_plan}</span> trial until {new Date(org.trial_ends_at).toLocaleDateString()}
              </span>
            )}
            <span className="text-muted">
              {projectLimit < 0 ? `${live} projects` : `${live} of ${projectLimit} project${projectLimit === 1 ? "" : "s"}`} · You are {org.role}
            </span>
            {manage && org.trial_available && (
              <Button variant="secondary" onClick={trial} disabled={startingTrial} className="min-h-8 px-3 py-1 text-xs">
                {startingTrial && <Spinner size={12} />} Start 7-day Startup trial
              </Button>
            )}
            <Link href="/pricing" className="text-muted underline hover:text-foreground">Plans</Link>
          </div>
        </Panel>
      )}
      {org && manage && org.trial_available && (
        <p className="mt-2 text-xs text-muted">The trial gives this organization the Startup plan&apos;s limits for seven days. No card, nothing is charged, and it returns to Free on its own.</p>
      )}

      {projects && (
        <div className="mt-6 grid gap-6 md:grid-cols-[220px_1fr]">
          <nav aria-label="Projects" className="space-y-1">
            {projects.map((p) => (
              <button
                key={p.id}
                type="button"
                onClick={() => setProjectId(p.id)}
                aria-current={p.id === projectId ? "page" : undefined}
                className={`block w-full truncate rounded-lg px-3 py-2 text-left text-sm ${p.id === projectId ? "bg-primary-tint font-medium text-foreground" : "text-muted hover:bg-primary-tint/50 hover:text-foreground"}`}
              >
                {p.name}
              </button>
            ))}
            {manage && (
              <form className="mt-3 space-y-2" onSubmit={(e) => { e.preventDefault(); create(); }}>
                <Input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="New project name" maxLength={80} aria-label="New project name" disabled={atProjectLimit} />
                <Button type="submit" variant="secondary" disabled={creating || !newName.trim() || atProjectLimit} className="w-full">
                  {creating ? <Spinner size={13} /> : <IconPlus size={14} />} Add project
                </Button>
                {atProjectLimit && (
                  <p className="text-xs leading-snug text-muted">
                    Your plan includes {projectLimit} project{projectLimit === 1 ? "" : "s"}.{org?.trial_available ? " Start the trial above for more." : ""}
                  </p>
                )}
              </form>
            )}
          </nav>
          {project ? <ProjectPanel key={`${project.id}:${org?.effective_plan ?? ""}`} project={project} manage={manage} onUpdated={(p) => setProjects((x) => x?.map((y) => (y.id === p.id ? p : y)) ?? null)} /> : <EmptyState title="No project" body="Create a project to issue API keys." />}
        </div>
      )}
    </div>
  );
}

// This month's metered use, from GET /api/v1/projects/{id}/usage. Spend is
// what providers were paid from the project's passes, never an Algebra fee.
function UsagePanel({ usage }: { usage: ProjectUsage | null }) {
  if (!usage) return <Skeleton className="h-24" />;
  const included = usage.included_executions;
  const month = new Date(usage.since).toLocaleDateString(undefined, { month: "long", year: "numeric", timeZone: "UTC" });
  return (
    <Panel>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="text-sm font-semibold text-foreground">Usage in {month}</h3>
        <p className="text-xs text-muted">
          {usage.managed_executions.toLocaleString()} {included < 0 ? "on-chain executions" : `of ${included.toLocaleString()} on-chain executions included`} · sandbox runs are free
        </p>
      </div>
      {usage.requests === 0 ? (
        <p className="mt-2 text-sm text-muted">No requests yet this month. Run the <Link href="/console/quickstart" className="underline">quickstart</Link> to make the first one.</p>
      ) : (
        <div className="mt-3 overflow-x-auto">
          <table className="w-full min-w-[420px] text-left text-sm">
            <thead className="text-xs text-muted">
              <tr><th className="py-1.5 pr-4 font-medium">Environment</th><th className="py-1.5 pr-4 font-medium">Committed executions</th><th className="py-1.5 font-medium">Paid to providers</th></tr>
            </thead>
            <tbody className="divide-y divide-border">
              {usage.by_network.map((n) => (
                <tr key={n.network}>
                  <td className="py-2 pr-4">{n.environment ? <EnvBadge env={n.environment} /> : <span className="font-mono text-xs">{n.network}</span>}</td>
                  <td className="py-2 pr-4 tabular-nums">{n.executions.toLocaleString()}</td>
                  <td className="py-2 tabular-nums">{formatUSDC(n.spent_minor)}{n.environment === "sandbox" ? <span className="ml-1.5 text-xs text-muted">simulated</span> : n.environment === "devnet" ? <span className="ml-1.5 text-xs text-muted">test USDC</span> : null}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="mt-2 text-xs text-muted">
            {usage.requests.toLocaleString()} request{usage.requests === 1 ? "" : "s"} in all: {Object.entries(usage.by_state).map(([state, n]) => `${n} ${state.toLowerCase().replace(/_/g, " ")}`).join(", ")}.
          </p>
        </div>
      )}
    </Panel>
  );
}

function ProjectPanel({ project, manage, onUpdated }: { project: Project; manage: boolean; onUpdated: (p: Project) => void }) {
  const [keys, setKeys] = useState<APIKey[] | null>(null);
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [events, setEvents] = useState<PlatformEvent[] | null>(null);
  const [apiBase, setApiBase] = useState("");
  const [issued, setIssued] = useState<IssuedKey | null>(null);
  const [usage, setUsage] = useState<ProjectUsage | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [k, p, ev, u] = await Promise.all([api.listKeys(project.id), api.listProjectPasses(project.id), api.listProjectEvents(project.id, 20), api.projectUsage(project.id)]);
    setKeys(k.keys);
    setApiBase(k.connect.api_base);
    setPasses(p.passes);
    setEvents(ev.events);
    setUsage(u);
  }, [project.id]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetching from the REST API on mount
    load().catch((e) => setError(e instanceof Error ? e.message : "Couldn't load the project"));
  }, [load]);

  async function strategy(s: Strategy) {
    try {
      onUpdated(await api.updateProject(project.id, { default_strategy: s }));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't save");
    }
  }

  async function act(fn: () => Promise<unknown>) {
    setError(null);
    try {
      await fn();
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That didn't work");
    }
  }

  return (
    <div className="min-w-0 space-y-6">
      {error && <ErrorNote>{error}</ErrorNote>}
      <Panel>
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <h2 className="font-display text-lg font-semibold text-foreground">{project.name}</h2>
            <p className="mt-0.5 font-mono text-[0.6875rem] text-muted">{project.id}</p>
          </div>
          <label className="w-56">
            <span className="text-xs text-muted">Default routing when a request names none</span>
            <select className={`${select} mt-1`} value={project.default_strategy} disabled={!manage} onChange={(e) => strategy(e.target.value as Strategy)}>
              {STRATEGIES.map((s) => <option key={s.id || "ask"} value={s.id}>{s.label}</option>)}
            </select>
          </label>
        </div>
      </Panel>

      <UsagePanel usage={usage} />

      {issued && <IssuedSecret issued={issued} apiBase={apiBase} onDone={() => setIssued(null)} />}

      <Panel>
        <h3 className="text-sm font-semibold text-foreground">API keys</h3>
        {!keys ? <Skeleton className="mt-3 h-16" /> : keys.length === 0 ? (
          <p className="mt-2 text-sm text-muted">No keys yet.</p>
        ) : (
          <ul className="mt-3 divide-y divide-border rounded-lg border border-border">
            {keys.map((k) => {
              const state = k.revoked_at ? "revoked" : k.expires_at && new Date(k.expires_at) < new Date() ? "expired" : "active";
              return (
                <li key={k.id} className={`flex flex-wrap items-center gap-3 px-3.5 py-2.5 text-sm ${state !== "active" ? "opacity-60" : ""}`}>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium text-foreground">{k.name}</span>
                    <span className="block font-mono text-[0.6875rem] text-muted">{k.prefix}… · {k.scopes.join(", ")} · created {timeAgo(k.created_at)} · {k.last_used_at ? `used ${timeAgo(k.last_used_at)}` : "never used"}</span>
                  </span>
                  <EnvBadge env={k.environment} />
                  {state !== "active" ? (
                    <span className="text-xs text-muted capitalize">{state}</span>
                  ) : manage ? (
                    <span className="flex gap-2">
                      <Button variant="secondary" className="min-h-8 px-3 text-xs" onClick={() => act(async () => setIssued((await api.rotateKey(k.id)).key))}>Rotate</Button>
                      <Button variant="secondary" className="min-h-8 px-3 text-xs text-danger" onClick={() => window.confirm(`Revoke "${k.name}"? Calls with it stop at once.`) && act(() => api.revokeKey(k.id))}>Revoke</Button>
                    </span>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
        {manage && <NewKeyForm projectId={project.id} onIssued={(k) => { setIssued(k); load().catch(() => {}); }} />}
      </Panel>

      <Panel>
        <h3 className="text-sm font-semibold text-foreground">Spend Passes</h3>
        <p className="mt-1 text-xs text-muted">A key pays only through a pass of this project in the key&apos;s environment. Without one, its calls are refused.</p>
        {!passes ? <Skeleton className="mt-3 h-12" /> : (
          <ul className="mt-3 space-y-2">
            {passes.length === 0 && <li className="text-sm text-muted">No passes for this project yet.</li>}
            {passes.map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-3 rounded-lg border border-border px-3.5 py-2.5 text-sm">
                <span className="min-w-0 flex-1 truncate text-foreground">{p.label}</span>
                <EnvBadge env={p.environment} />
                <span className="font-mono text-xs text-muted">{formatUSDC(p.remaining_minor_units)} left of {formatUSDC(p.budget_minor_units)}</span>
                {!p.active && <span className="text-xs text-muted">inactive</span>}
              </li>
            ))}
          </ul>
        )}
        {manage && <NewPassForm projectId={project.id} onCreated={() => load().catch(() => {})} />}
      </Panel>

      <Panel>
        <h3 className="text-sm font-semibold text-foreground">Activity</h3>
        {!events ? <Skeleton className="mt-3 h-12" /> : events.length === 0 ? <p className="mt-2 text-sm text-muted">Nothing yet.</p> : (
          <ul className="mt-2 space-y-1 text-xs">
            {events.map((e) => (
              <li key={e.id} className="flex gap-2 text-muted"><span className="font-mono text-foreground">{e.event}</span><span>{timeAgo(e.created_at)}</span></li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}

function IssuedSecret({ issued, apiBase, onDone }: { issued: IssuedKey; apiBase: string; onDone: () => void }) {
  const curl = `export ALGEBRA_API_KEY="<the key above>"
curl -X POST "${apiBase || "https://YOUR_ALGEBRA_HOST/api/v1"}/execute" \\
  -H "Authorization: Bearer $ALGEBRA_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"capability":"token.price","input":{"mint":"So11111111111111111111111111111111111111112"},"budget_max_minor":50000}'`;
  return (
    <Panel className="border-primary/40">
      <p className="text-sm font-semibold text-foreground">Copy this key now: it is shown once</p>
      <p className="mt-1 text-xs text-muted">Algebra stores only its hash. Keep it on your server; never put it in browser code or an LLM prompt.</p>
      <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-background px-3 py-2">
        <code className="min-w-0 flex-1 font-mono text-xs break-all text-foreground">{issued.secret}</code>
        <Copy text={issued.secret} label="Copy key" />
      </div>
      <div className="mt-3"><Snippet title={`Call Algebra with it (${issued.environment})`} code={curl} /></div>
      <div className="mt-3"><Button variant="secondary" onClick={onDone}>I&apos;ve saved it</Button></div>
    </Panel>
  );
}

function NewKeyForm({ projectId, onIssued }: { projectId: string; onIssued: (k: IssuedKey) => void }) {
  const [name, setName] = useState("");
  const [env, setEnv] = useState<Environment>("sandbox");
  const [scopes, setScopes] = useState<Scope[]>(["read", "simulate", "execute"]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      onIssued((await api.createKey(projectId, { name: name.trim() || `${env} key`, environment: env, scopes })).key);
      setName("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't issue the key");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="mt-5 grid gap-3 border-t border-border pt-4 sm:grid-cols-[1fr_180px]" onSubmit={(e) => { e.preventDefault(); submit(); }}>
      <Field label="New key"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. backend, staging" maxLength={80} /></Field>
      <Field label="Environment" hint={ENVIRONMENTS.find((x) => x.id === env)?.detail}>
        <select className={select} value={env} onChange={(e) => setEnv(e.target.value as Environment)}>
          {ENVIRONMENTS.map((x) => <option key={x.id} value={x.id}>{x.label}</option>)}
        </select>
      </Field>
      <fieldset className="sm:col-span-2">
        <legend className="text-sm font-medium text-foreground">Scopes</legend>
        <div className="mt-1.5 flex flex-wrap gap-4">
          {SCOPES.map((s) => (
            <label key={s.id} className="flex items-start gap-2 text-sm" title={s.detail}>
              <input type="checkbox" className="mt-1" checked={scopes.includes(s.id)} onChange={(e) => setScopes((x) => (e.target.checked ? [...x, s.id] : x.filter((y) => y !== s.id)))} />
              <span><span className="text-foreground">{s.label}</span><span className="block text-xs text-muted">{s.detail}</span></span>
            </label>
          ))}
        </div>
      </fieldset>
      {error && <div className="sm:col-span-2"><ErrorNote>{error}</ErrorNote></div>}
      <div className="sm:col-span-2"><Button type="submit" disabled={busy || scopes.length === 0}>{busy && <Spinner size={13} />} Issue key</Button></div>
    </form>
  );
}

function NewPassForm({ projectId, onCreated }: { projectId: string; onCreated: () => void }) {
  const [env, setEnv] = useState<Environment>("sandbox");
  const [budget, setBudget] = useState("1");
  const [perCall, setPerCall] = useState("0.05");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const micro = (s: string) => Math.round(Number(s) * 1_000_000);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      await api.createPass({
        label: `${ENVIRONMENTS.find((x) => x.id === env)?.label} pass`, agent_kind: "custom", currency: "USDC",
        budget_minor_units: micro(budget), budget_period: "month", max_per_purchase_minor_units: micro(perCall),
        allowed_categories: ["digital_services"], expires_in_days: 30, project_id: projectId, environment: env,
      });
      onCreated();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create the pass");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="mt-5 grid gap-3 border-t border-border pt-4 sm:grid-cols-3" onSubmit={(e) => { e.preventDefault(); submit(); }}>
      <Field label="Environment">
        <select className={select} value={env} onChange={(e) => setEnv(e.target.value as Environment)}>
          {ENVIRONMENTS.map((x) => <option key={x.id} value={x.id}>{x.label}</option>)}
        </select>
      </Field>
      <Field label="Monthly budget (USDC)"><Input inputMode="decimal" value={budget} onChange={(e) => setBudget(e.target.value)} /></Field>
      <Field label="Most per call (USDC)"><Input inputMode="decimal" value={perCall} onChange={(e) => setPerCall(e.target.value)} /></Field>
      {error && <div className="sm:col-span-3"><ErrorNote>{error}</ErrorNote></div>}
      <div className="sm:col-span-3">
        <Button type="submit" variant="secondary" disabled={busy || !(micro(budget) > 0) || !(micro(perCall) > 0)}>{busy && <Spinner size={13} />} Add a pass for this project</Button>
        <p className="mt-1.5 text-xs text-muted">Devnet and mainnet passes can also be put on Solana from your wallet on the Spend passes page.</p>
      </div>
    </form>
  );
}
