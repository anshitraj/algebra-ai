"use client";

import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { AgentKind, BudgetPeriod, IssuedPass, PassConnect, SpendPass } from "@/lib/types";
import { IconCheck, IconPlus, IconShield, Spinner } from "@/components/icons";
import { Button, ErrorNote, Field, Input, PageHeader, Panel, Skeleton } from "@/components/console/ui";

const rupees = (minor: number) => `₹${(minor / 100).toLocaleString("en-IN", { maximumFractionDigits: 0 })}`;

const AGENTS: { value: AgentKind; label: string; hint: string }[] = [
  { value: "claude", label: "Claude", hint: "Claude Code, Claude Desktop" },
  { value: "chatgpt", label: "ChatGPT", hint: "OpenAI agents and apps" },
  { value: "custom", label: "Other agent", hint: "Your own bot, Cursor, any MCP or API client" },
];

const CATEGORIES: { value: string; label: string }[] = [
  { value: "groceries", label: "Groceries" },
  { value: "food_delivery", label: "Food delivery" },
  { value: "electronics", label: "Electronics" },
  { value: "fashion", label: "Fashion" },
  { value: "home", label: "Home" },
  { value: "beauty", label: "Beauty" },
  { value: "pharmacy", label: "Pharmacy" },
  { value: "subscriptions", label: "Subscriptions" },
  { value: "travel", label: "Travel" },
];

const PERIODS: { value: BudgetPeriod; label: string }[] = [
  { value: "week", label: "per week" },
  { value: "month", label: "per month" },
  { value: "total", label: "in total" },
];

const LIFETIMES = [7, 30, 90];

const periodText = (p: BudgetPeriod) => (p === "week" ? "this week" : p === "month" ? "this month" : "in total");

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

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader
        title="Spend passes"
        description="Let an AI agent — Claude, ChatGPT or your own — buy things for you within limits you set. It gets a pass, never your card. Every purchase clears the pass and your guardrails, comes with a signed receipt, and you can revoke it any time."
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
        Give Claude a ₹2,000-a-week grocery pass that asks you above ₹500, instead of your card. Revoke it with one click.
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
  const [budget, setBudget] = useState("2000");
  const [period, setPeriod] = useState<BudgetPeriod>("week");
  const [perPurchase, setPerPurchase] = useState("");
  const [askAbove, setAskAbove] = useState("500");
  const [cats, setCats] = useState<string[]>(["groceries"]);
  const [days, setDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const agentName = AGENTS.find((a) => a.value === kind)?.label ?? "Agent";
  const suggested = `${agentName === "Other agent" ? "My agent" : agentName} — ${cats.length === 1 ? CATEGORIES.find((c) => c.value === cats[0])?.label.toLowerCase() : cats.length ? "shopping" : "anything"}`;
  const paise = (v: string) => Math.round(Number(v) * 100);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const created = await api.createPass({
        label: label.trim() || suggested,
        agent_kind: kind,
        budget_minor_units: paise(budget),
        budget_period: period,
        max_per_purchase_minor_units: perPurchase ? paise(perPurchase) : undefined,
        approve_above_minor_units: askAbove !== "" ? paise(askAbove) : undefined,
        allowed_categories: cats,
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
          <Field label="Budget (₹)" hint="Algebra refuses anything past this.">
            <Input type="number" inputMode="numeric" min={1} required value={budget} onChange={(e) => setBudget(e.target.value)} />
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
          <Field label="Ask me before any purchase above (₹)" hint="0 = ask every time. Leave empty to use only your guardrails.">
            <Input type="number" inputMode="numeric" min={0} value={askAbove} onChange={(e) => setAskAbove(e.target.value)} />
          </Field>
          <Field label="Most one purchase can cost (₹)" hint="Optional.">
            <Input type="number" inputMode="numeric" min={1} value={perPurchase} onChange={(e) => setPerPurchase(e.target.value)} placeholder="No limit" />
          </Field>
        </div>

        <div>
          <p className="text-sm font-medium text-foreground">What it may buy</p>
          <p className="mt-0.5 text-xs text-muted">Nothing selected = anything your guardrails allow.</p>
          <div className="mt-2 flex flex-wrap gap-2">
            {CATEGORIES.map((c) => (
              <Chip key={c.value} on={cats.includes(c.value)} onClick={() => setCats((s) => (s.includes(c.value) ? s.filter((x) => x !== c.value) : [...s, c.value]))}>
                {c.label}
              </Chip>
            ))}
          </div>
        </div>

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
          <Button type="submit" disabled={busy || !(Number(budget) > 0)}>
            {busy && <Spinner size={14} />} Create pass
          </Button>
        </div>
      </form>
    </Panel>
  );
}

function Copy({ text, label = "Copy" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      onClick={() =>
        navigator.clipboard?.writeText(text).then(
          () => {
            setDone(true);
            window.setTimeout(() => setDone(false), 1500);
          },
          () => {}
        )
      }
      className="shrink-0 rounded-md border border-border-strong px-2 py-1 text-xs font-medium text-foreground hover:bg-primary-tint"
    >
      {done ? "Copied" : label}
    </button>
  );
}

function Snippet({ title, code, note }: { title: string; code: string; note?: string }) {
  return (
    <div>
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm font-medium text-foreground">{title}</p>
        <Copy text={code} />
      </div>
      <pre className="mt-1.5 overflow-x-auto rounded-lg border border-border bg-background px-3 py-2.5 font-mono text-xs leading-relaxed text-foreground">{code}</pre>
      {note && <p className="mt-1 text-xs text-muted">{note}</p>}
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
            Give this token to the agent. It&apos;s shown <strong className="text-foreground">only now</strong> — Algebra keeps just a fingerprint of it.
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
            <Snippet title="Run in your terminal" code={`claude mcp add --transport http algebra ${mcp} \\\n  --header "Authorization: Bearer ${t}"`} note="Then ask Claude to shop — it can only spend within this pass." />
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
              code={`from agents import Agent\nfrom agents.mcp import MCPServerStreamableHttp\n\nalgebra = MCPServerStreamableHttp(params={\n    "url": "${mcp}",\n    "headers": {"Authorization": "Bearer ${t}"},\n})\nagent = Agent(name="Shopper", mcp_servers=[algebra])`}
              note="One-click connection inside the ChatGPT app needs OAuth sign-in, which is next on Algebra's list."
            />
          )}
          {tab === "rest" && (
            <>
              <Snippet title="Check the pass (limits and budget left)" code={`curl ${connect.api_base}/pass \\\n  -H "Authorization: Bearer ${t}"`} />
              <Snippet
                title="Then shop: search → intent → discover → select → request purchase → execute"
                code={`curl "${connect.api_base}/web-search?q=wireless+mouse" -H "Authorization: Bearer ${t}"`}
                note="Every purchase is checked against this pass and your guardrails; big ones wait for your approval. The execute response includes a signed receipt."
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
  const used = Math.min(p.spent_minor_units / p.budget_minor_units, 1);
  const rules = [
    p.approve_above_minor_units !== undefined && p.approve_above_minor_units !== null
      ? p.approve_above_minor_units === 0
        ? "Asks you every time"
        : `Asks you above ${rupees(p.approve_above_minor_units)}`
      : "Asks you per your guardrails",
    p.max_per_purchase_minor_units ? `Max ${rupees(p.max_per_purchase_minor_units)} a purchase` : "",
    p.allowed_categories.length ? p.allowed_categories.map((c) => CATEGORIES.find((x) => x.value === c)?.label ?? c).join(", ") + " only" : "Anything your guardrails allow",
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
          </div>
          <p className="mt-1 text-sm text-muted">{rules.join(" · ")}</p>
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
            <span className="font-mono text-foreground tabular-nums">{rupees(p.spent_minor_units)}</span> spent {periodText(p.budget_period)}
          </span>
          <span className="text-muted">
            <span className="font-mono text-foreground tabular-nums">{rupees(p.remaining_minor_units)}</span> left of {rupees(p.budget_minor_units)}
          </span>
        </div>
        <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-border" role="progressbar" aria-valuenow={Math.round(used * 100)} aria-valuemin={0} aria-valuemax={100}>
          <div className={`h-full rounded-full ${used >= 0.9 ? "bg-danger" : "bg-primary"}`} style={{ width: `${used * 100}%` }} />
        </div>
      </div>
    </Panel>
  );
}
