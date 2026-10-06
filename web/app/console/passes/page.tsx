"use client";

import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { AgentKind, BudgetPeriod, IssuedPass, PassConnect, ProviderSummary, SpendPass } from "@/lib/types";
import { formatMoney } from "@/lib/money";
import { catalogName } from "@/lib/paysh";
import { IconCheck, IconPlus, IconShield, IconX, Spinner } from "@/components/icons";
import { Button, ErrorNote, Field, Input, PageHeader, Panel, Skeleton } from "@/components/console/ui";
import { Copy, Snippet } from "@/components/console/snippet";
import { ProviderLogo } from "@/components/provider-logo";

const AGENTS: { value: AgentKind; label: string; hint: string }[] = [
  { value: "claude", label: "Claude", hint: "Claude Code, Claude Desktop" },
  { value: "chatgpt", label: "ChatGPT", hint: "OpenAI agents and apps" },
  { value: "custom", label: "Other agent", hint: "Your own bot, Cursor, any MCP or API client" },
];

const PERIODS: { value: BudgetPeriod; label: string }[] = [
  { value: "week", label: "per week" },
  { value: "month", label: "per month" },
  { value: "total", label: "in total" },
];

const LIFETIMES = [7, 30, 90];

const periodText = (p: BudgetPeriod) => (p === "week" ? "this week" : p === "month" ? "this month" : "in total");

/** "0.05" USDC as typed, in micro-USDC; NaN when it isn't an amount. */
const micro = (v: string) => (v.trim() === "" ? NaN : Math.round(Number(v) * 1_000_000));

export default function PassesPage() {
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [connect, setConnect] = useState<PassConnect | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<IssuedPass | null>(null);

  function load() {
    api
      .listPasses()
      .then((r) => {
        setPasses(r.passes);
        setConnect(r.connect);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load your passes"));
  }
  useEffect(load, []);
  // Arriving from onboarding (?new=1): open the form straight away.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reading the URL once on arrival
    if (new URLSearchParams(window.location.search).get("new") === "1") setCreating(true);
  }, []);

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader
        title="Spend passes"
        description="A pass is an agent's whole authority to spend: a USDC budget, the most one call may cost, when to ask you, and which providers it may pay. The agent gets a token, never a key. Every payment clears the pass first, ends in a signed receipt, and you can revoke the pass at any time."
        actions={
          !creating && !issued ? (
            <Button onClick={() => setCreating(true)}>
              <IconPlus size={15} /> New pass
            </Button>
          ) : undefined
        }
      />

      <div className="mt-8 space-y-4">
        {error && <ErrorNote>{error}</ErrorNote>}
        {issued && connect && (
          <IssuedCard
            pass={issued}
            connect={issued.connect ?? connect}
            onDone={() => {
              setIssued(null);
              load();
            }}
          />
        )}
        {creating && (
          <CreatePass
            onCancel={() => setCreating(false)}
            onCreated={(p) => {
              setCreating(false);
              setIssued(p);
            }}
          />
        )}
        {passes === null && !error && (
          <>
            <Skeleton className="h-28" />
            <Skeleton className="h-28" />
          </>
        )}
        {passes?.length === 0 && !creating && !issued && <Empty onCreate={() => setCreating(true)} />}
        {passes?.map((p) => <PassCard key={p.id} pass={p} onRevoked={load} />)}
      </div>
    </div>
  );
}

function Empty({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="rounded-2xl border border-dashed border-border-strong px-6 py-14 text-center">
      <span className="mx-auto flex h-11 w-11 items-center justify-center rounded-xl bg-primary-tint text-primary">
        <IconShield size={20} />
      </span>
      <p className="font-display mt-4 text-lg font-semibold text-foreground">No spend passes yet</p>
      <p className="mx-auto mt-1.5 max-w-md text-sm text-muted">
        Give Claude 1 USDC a week for market data, that asks you before any call above 0.05 USDC and pays only Birdeye and
        Allium. Revoke it with one click.
      </p>
      <div className="mt-5">
        <Button onClick={onCreate}>
          <IconPlus size={15} /> Create your first pass
        </Button>
      </div>
    </div>
  );
}

function Chip({ on, onClick, children }: { on: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={on}
      onClick={onClick}
      className={`inline-flex h-9 items-center gap-1.5 rounded-full border px-3.5 text-sm transition-colors ${
        on ? "border-primary bg-primary text-primary-tint" : "border-border-strong bg-surface text-foreground hover:border-foreground/35"
      }`}
    >
      {on && <IconCheck size={13} strokeWidth={2.4} />}
      {children}
    </button>
  );
}

function CreatePass({ onCancel, onCreated }: { onCancel: () => void; onCreated: (p: IssuedPass) => void }) {
  const [kind, setKind] = useState<AgentKind>("claude");
  const [label, setLabel] = useState("");
  const [budget, setBudget] = useState("1");
  const [period, setPeriod] = useState<BudgetPeriod>("week");
  const [perCall, setPerCall] = useState("0.10");
  const [askAbove, setAskAbove] = useState("0.05");
  const [providers, setProviders] = useState<ProviderSummary[]>([]);
  const [days, setDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const agentName = AGENTS.find((a) => a.value === kind)?.label ?? "Agent";
  const suggested = `${agentName === "Other agent" ? "My agent" : agentName} — ${providers.length === 1 ? providers[0].name : providers.length ? "selected providers" : "paid APIs"}`;
  const budgetMicro = micro(budget);
  const valid = budgetMicro > 0 && (perCall === "" || micro(perCall) > 0) && (askAbove === "" || micro(askAbove) >= 0);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const created = await api.createPass({
        label: label.trim() || suggested,
        agent_kind: kind,
        currency: "USDC",
        budget_minor_units: budgetMicro,
        budget_period: period,
        max_per_purchase_minor_units: perCall !== "" ? micro(perCall) : undefined,
        approve_above_minor_units: askAbove !== "" ? micro(askAbove) : undefined,
        // Paid API calls are digital services: the category every execution is checked under.
        allowed_categories: ["digital_services"],
        allowed_merchants: providers.map((p) => p.id),
        expires_in_days: days,
      });
      onCreated(created);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't create the pass");
      setBusy(false);
    }
  }

  return (
    <Panel>
      <form onSubmit={submit} className="space-y-6">
        <div>
          <p className="text-sm font-medium text-foreground">Which agent is it for?</p>
          <div className="mt-2 grid gap-2 sm:grid-cols-3" role="radiogroup">
            {AGENTS.map((a) => (
              <button
                key={a.value}
                type="button"
                role="radio"
                aria-checked={kind === a.value}
                onClick={() => setKind(a.value)}
                className={`rounded-xl border px-3.5 py-3 text-left transition-colors ${
                  kind === a.value ? "border-primary bg-primary-tint" : "border-border-strong hover:border-foreground/35"
                }`}
              >
                <span className="block text-sm font-medium text-foreground">{a.label}</span>
                <span className="block text-xs text-muted">{a.hint}</span>
              </button>
            ))}
          </div>
        </div>

        <div className="grid gap-4 sm:grid-cols-[1fr_auto]">
          <Field label="Budget (USDC)" hint="Algebra refuses any payment past this.">
            <Input type="number" inputMode="decimal" min={0} step="any" required value={budget} onChange={(e) => setBudget(e.target.value)} />
          </Field>
          <Field label="Renews">
            <div className="flex gap-1.5">
              {PERIODS.map((p) => (
                <Chip key={p.value} on={period === p.value} onClick={() => setPeriod(p.value)}>
                  {p.label}
                </Chip>
              ))}
            </div>
          </Field>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Ask me before any call above (USDC)" hint="0 = ask every time. Leave empty to never ask.">
            <Input type="number" inputMode="decimal" min={0} step="any" value={askAbove} onChange={(e) => setAskAbove(e.target.value)} />
          </Field>
          <Field label="Most one call may cost (USDC)" hint="Optional. The payment rail has its own hard ceiling too.">
            <Input type="number" inputMode="decimal" min={0} step="any" value={perCall} onChange={(e) => setPerCall(e.target.value)} placeholder="No limit" />
          </Field>
        </div>

        <ProviderPicker picked={providers} onChange={setProviders} />

        <div className="grid gap-4 sm:grid-cols-[1fr_auto]">
          <Field label="Name">
            <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={suggested} maxLength={80} />
          </Field>
          <Field label="Expires in">
            <div className="flex gap-1.5">
              {LIFETIMES.map((d) => (
                <Chip key={d} on={days === d} onClick={() => setDays(d)}>
                  {d} days
                </Chip>
              ))}
            </div>
          </Field>
        </div>

        {error && <ErrorNote>{error}</ErrorNote>}
        <div className="flex items-center justify-end gap-2 border-t border-border pt-5">
          <Button type="button" variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" disabled={busy || !valid}>
            {busy && <Spinner size={14} />} Create pass
          </Button>
        </div>
      </form>
    </Panel>
  );
}

/** Choose which catalog providers the pass may pay; none chosen means any. */
function ProviderPicker({ picked, onChange }: { picked: ProviderSummary[]; onChange: (p: ProviderSummary[]) => void }) {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<ProviderSummary[]>([]);

  useEffect(() => {
    const query = q.trim();
    if (query.length < 2) return;
    let live = true;
    const id = window.setTimeout(() => {
      api
        .listProviders({ q: query, limit: 8 })
        .then((l) => live && setResults(l.providers))
        .catch(() => live && setResults([]));
    }, 250);
    return () => {
      live = false;
      window.clearTimeout(id);
    };
  }, [q]);

  const has = (id: string) => picked.some((p) => p.id === id);
  // Results belong to the last query of two or more characters; a shorter one shows none.
  const shown = q.trim().length >= 2 ? results : [];
  return (
    <div>
      <p className="text-sm font-medium text-foreground">Providers it may pay</p>
      <p className="mt-0.5 text-xs text-muted">None chosen = any provider in the catalogs. Choosing some is safer.</p>
      {picked.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-2">
          {picked.map((p) => (
            <span key={p.id} className="inline-flex h-8 items-center gap-1.5 rounded-full bg-primary-tint pr-1.5 pl-3 text-sm text-foreground">
              {p.name}
              <span className="text-xs text-muted">{catalogName(p.source)}</span>
              <button
                type="button"
                aria-label={`Remove ${p.name}`}
                onClick={() => onChange(picked.filter((x) => x.id !== p.id))}
                className="grid h-5 w-5 place-items-center rounded-full text-muted hover:bg-surface hover:text-foreground"
              >
                <IconX size={12} />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="relative mt-2">
        <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search providers: birdeye, exa, vision…" />
        {shown.length > 0 && (
          <ul className="absolute z-10 mt-1 max-h-64 w-full overflow-y-auto rounded-xl border border-border bg-surface p-1 shadow-[0_16px_40px_-16px_rgba(11,16,32,0.35)]">
            {shown.map((p) => (
              <li key={p.id}>
                <button
                  type="button"
                  disabled={has(p.id)}
                  onClick={() => {
                    onChange([...picked, p]);
                    setQ("");
                  }}
                  className="flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-primary-tint disabled:opacity-50"
                >
                  <ProviderLogo name={p.name} logo={p.logo_url} website={p.website} host={p.host} fqn={p.fqn} size={24} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-foreground">{p.name}</span>
                    <span className="block truncate font-mono text-[0.6875rem] text-muted">{p.id}</span>
                  </span>
                  <span className="shrink-0 text-xs text-muted">{catalogName(p.source)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function IssuedCard({ pass, connect, onDone }: { pass: IssuedPass; connect: PassConnect; onDone: () => void }) {
  const [tab, setTab] = useState<"claude" | "desktop" | "openai" | "rest">(pass.agent_kind === "chatgpt" ? "openai" : pass.agent_kind === "custom" ? "rest" : "claude");
  const mcp = connect.mcp_url;
  const t = pass.token;
  const tabs = [
    { id: "claude" as const, label: "Claude Code" },
    { id: "desktop" as const, label: "Claude Desktop / Cursor" },
    { id: "openai" as const, label: "OpenAI Agents SDK" },
    { id: "rest" as const, label: "Any agent (API)" },
  ];
  return (
    <Panel className="border-primary/50">
      <div className="flex items-start gap-3">
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-xl bg-primary text-primary-tint">
          <IconCheck size={17} strokeWidth={2.6} />
        </span>
        <div className="min-w-0">
          <h2 className="font-display text-lg font-semibold text-foreground">{pass.label} is ready</h2>
          <p className="mt-0.5 text-sm text-muted">
            Give this token to the agent. It&apos;s shown <strong className="text-foreground">only now</strong>: Algebra keeps just a fingerprint of it.
          </p>
        </div>
      </div>

      <div className="mt-4 flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-2">
        <code className="min-w-0 flex-1 truncate font-mono text-xs text-foreground">{t}</code>
        <Copy text={t} label="Copy token" />
      </div>

      <div className="mt-6">
        <p className="text-sm font-medium text-foreground">Connect it</p>
        <div className="mt-2 flex flex-wrap gap-1.5" role="tablist">
          {tabs.map((x) => (
            <button
              key={x.id}
              type="button"
              role="tab"
              aria-selected={tab === x.id}
              onClick={() => setTab(x.id)}
              className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-colors ${tab === x.id ? "bg-primary text-primary-tint" : "text-muted hover:bg-primary-tint hover:text-foreground"}`}
            >
              {x.label}
            </button>
          ))}
        </div>
        <div className="mt-4 space-y-3">
          {tab !== "rest" && !mcp && (
            <p className="rounded-lg bg-accent-tint px-3 py-2 text-xs text-accent">
              This server&apos;s MCP endpoint isn&apos;t published yet (set MCP_PUBLIC_URL and run cmd/mcp -http). Until then, use the API tab.
            </p>
          )}
          {tab === "claude" && mcp && (
            <Snippet
              title="Run in your terminal"
              code={`claude mcp add --transport http algebra ${mcp} \\\n  --header "Authorization: Bearer ${t}"`}
              note="Then ask Claude for something that costs money, for example a token risk score. It can only spend within this pass."
            />
          )}
          {tab === "desktop" && mcp && (
            <Snippet
              title="Add to your MCP config (claude_desktop_config.json or .cursor/mcp.json)"
              code={JSON.stringify({ mcpServers: { algebra: { command: "npx", args: ["-y", "mcp-remote", mcp, "--header", `Authorization: Bearer ${t}`] } } }, null, 2)}
            />
          )}
          {tab === "openai" && mcp && (
            <Snippet
              title="Python — OpenAI Agents SDK"
              code={`from agents import Agent\nfrom agents.mcp import MCPServerStreamableHttp\n\nalgebra = MCPServerStreamableHttp(params={\n    "url": "${mcp}",\n    "headers": {"Authorization": "Bearer ${t}"},\n})\nagent = Agent(name="Researcher", mcp_servers=[algebra])`}
            />
          )}
          {tab === "rest" && (
            <>
              <Snippet title="Check the pass (limits and budget left)" code={`curl ${connect.api_base}/pass \\\n  -H "Authorization: Bearer ${t}"`} />
              <Snippet
                title="Ask for an outcome"
                code={`curl -X POST ${connect.api_base}/execute \\\n  -H "Authorization: Bearer ${t}" -H "Content-Type: application/json" \\\n  -d '{"capability":"circle.birdeye.get.x402-defi-price","providers":["circle:birdeye"],"input":{"address":"So11111111111111111111111111111111111111112"},"budget_max_minor":10000}'`}
                note="Find capability IDs on the Providers page. Every call is checked against this pass, and the response carries a signed receipt."
              />
            </>
          )}
        </div>
      </div>

      <div className="mt-6 flex justify-end border-t border-border pt-5">
        <Button onClick={onDone}>I&apos;ve saved the token</Button>
      </div>
    </Panel>
  );
}

function PassCard({ pass: p, onRevoked }: { pass: SpendPass; onRevoked: () => void }) {
  const [busy, setBusy] = useState(false);
  // The server says whether the pass can still spend (not revoked, not expired).
  const status = p.revoked_at ? "Revoked" : p.active ? "Active" : "Expired";
  const used = p.budget_minor_units > 0 ? Math.min(p.spent_minor_units / p.budget_minor_units, 1) : 0;
  const m = (minor: number) => formatMoney(minor, p.currency);
  const rules = [
    p.approve_above_minor_units !== undefined && p.approve_above_minor_units !== null
      ? p.approve_above_minor_units === 0
        ? "Asks you every time"
        : `Asks you above ${m(p.approve_above_minor_units)}`
      : "Never asks",
    p.max_per_purchase_minor_units ? `Max ${m(p.max_per_purchase_minor_units)} a call` : "",
    p.allowed_merchants?.length ? `Pays only ${p.allowed_merchants.join(", ")}` : "Any provider",
    `${status === "Active" ? "Expires" : "Ran until"} ${new Date(p.revoked_at ?? p.expires_at).toLocaleDateString(undefined, { day: "numeric", month: "short" })}`,
  ].filter(Boolean);

  async function revoke() {
    if (!window.confirm(`Revoke "${p.label}"? The agent loses access immediately.`)) return;
    setBusy(true);
    try {
      await api.revokePass(p.id);
      onRevoked();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel className={status === "Active" ? "" : "opacity-70"}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="font-display truncate text-base font-semibold text-foreground">{p.label}</h3>
            <span
              className={`rounded-full px-2 py-0.5 text-[0.7rem] font-medium ${
                status === "Active" ? "bg-primary-tint text-primary" : "bg-border text-muted"
              }`}
            >
              {status}
            </span>
            {p.currency !== "USDC" && <span className="rounded-full bg-border px-2 py-0.5 text-[0.7rem] font-medium text-muted">{p.currency}</span>}
          </div>
          <p className="mt-1 text-sm break-words text-muted">{rules.join(" · ")}</p>
        </div>
        {status === "Active" && (
          <Button variant="secondary" onClick={revoke} disabled={busy}>
            {busy && <Spinner size={13} />} Revoke
          </Button>
        )}
      </div>
      <div className="mt-4">
        <div className="flex items-baseline justify-between text-sm">
          <span className="text-muted">
            <span className="font-mono text-foreground tabular-nums">{m(p.spent_minor_units)}</span> spent {periodText(p.budget_period)}
          </span>
          <span className="text-muted">
            <span className="font-mono text-foreground tabular-nums">{m(p.remaining_minor_units)}</span> left of {m(p.budget_minor_units)}
          </span>
        </div>
        <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-border" role="progressbar" aria-valuenow={Math.round(used * 100)} aria-valuemin={0} aria-valuemax={100}>
          <div className={`h-full rounded-full ${used >= 0.9 ? "bg-danger" : "bg-primary"}`} style={{ width: `${used * 100}%` }} />
        </div>
      </div>
    </Panel>
  );
}
