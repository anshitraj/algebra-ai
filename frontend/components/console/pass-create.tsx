"use client";

// Making a Spend Pass, and the one-time card that hands its token to the agent.
// Lives on the Spend firewall page: a pass and its rules are one thing.

import Link from "next/link";
import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { AgentKind, BudgetPeriod, IssuedPass, PassConnect, ProviderSummary } from "@/lib/types";
import { mcpSetup } from "@/lib/mcp-clients";
import { catalogName } from "@/lib/paysh";
import { IconCheck, IconX, Spinner } from "@/components/icons";
import { Button, ErrorNote, Field, Input, Panel } from "@/components/console/ui";
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

type Ask = "every" | "above" | "never";

const ASKS: { value: Ask; label: string; hint: string }[] = [
  { value: "every", label: "Every payment", hint: "Nothing is paid until you say yes." },
  { value: "above", label: "Above an amount", hint: "Small calls run on their own; bigger ones wait for you." },
  { value: "never", label: "Never", hint: "Payments within the rules run on their own." },
];

/** "0.05" USDC as typed, in micro-USDC; NaN when it isn't an amount. */
const micro = (v: string) => (v.trim() === "" ? NaN : Math.round(Number(v) * 1_000_000));

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

/** A new pass: who it is for, how much, and when it must ask. Everything else is behind "More options". */
export function CreatePass({ onCancel, onCreated }: { onCancel: () => void; onCreated: (p: IssuedPass) => void }) {
  const [kind, setKind] = useState<AgentKind>("claude");
  const [label, setLabel] = useState("");
  const [budget, setBudget] = useState("1");
  const [period, setPeriod] = useState<BudgetPeriod>("week");
  const [ask, setAsk] = useState<Ask>("every");
  const [askAbove, setAskAbove] = useState("0.05");
  const [perCall, setPerCall] = useState("");
  const [providers, setProviders] = useState<ProviderSummary[]>([]);
  const [days, setDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const agentName = AGENTS.find((a) => a.value === kind)?.label ?? "Agent";
  const suggested = `${agentName === "Other agent" ? "My agent" : agentName} — ${providers.length === 1 ? providers[0].name : providers.length ? "selected providers" : "paid APIs"}`;
  const budgetMicro = micro(budget);
  const valid = budgetMicro > 0 && (perCall === "" || micro(perCall) > 0) && (ask !== "above" || micro(askAbove) > 0);

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
        // 0 asks every time; left out never asks.
        approve_above_minor_units: ask === "every" ? 0 : ask === "above" ? micro(askAbove) : undefined,
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

        <div>
          <p className="text-sm font-medium text-foreground">Ask me before paying</p>
          <div className="mt-2 grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Ask me before paying">
            {ASKS.map((a) => (
              <button
                key={a.value}
                type="button"
                role="radio"
                aria-checked={ask === a.value}
                onClick={() => setAsk(a.value)}
                className={`rounded-xl border px-3.5 py-3 text-left transition-colors ${
                  ask === a.value ? "border-primary bg-primary-tint" : "border-border-strong hover:border-foreground/35"
                }`}
              >
                <span className="block text-sm font-medium text-foreground">{a.label}</span>
                <span className="block text-xs text-muted">{a.hint}</span>
              </button>
            ))}
          </div>
          {ask === "above" && (
            <div className="mt-3 max-w-xs">
              <Field label="Ask me at or above (USDC)">
                <Input type="number" inputMode="decimal" min={0} step="any" value={askAbove} onChange={(e) => setAskAbove(e.target.value)} />
              </Field>
            </div>
          )}
        </div>

        <details className="group rounded-xl border border-border px-4 py-3">
          <summary className="cursor-pointer text-sm font-medium text-foreground">More options</summary>
          <div className="mt-4 space-y-6">
            <Field label="Name">
              <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder={suggested} maxLength={80} />
            </Field>
            <Field label="Most one call may cost (USDC)" hint="Optional. The payment rail has its own hard ceiling too.">
              <Input type="number" inputMode="decimal" min={0} step="any" value={perCall} onChange={(e) => setPerCall(e.target.value)} placeholder="No limit" />
            </Field>
            <ProviderPicker picked={providers} onChange={setProviders} />
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
        </details>

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

export function IssuedCard({ pass, connect, onDone }: { pass: IssuedPass; connect: PassConnect; onDone: () => void }) {
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
              This server hasn&apos;t published an MCP address. Use the API tab.
            </p>
          )}
          {tab === "claude" && mcp && (
            <Snippet
              {...mcpSetup("claude-code", mcp, t)}
              note="Then ask Claude for something that costs money, for example a token risk score. It can only spend within this pass."
            />
          )}
          {tab === "desktop" && mcp && (
            <Snippet {...mcpSetup("claude-desktop", mcp, t)} title="Add to your MCP config (claude_desktop_config.json or .cursor/mcp.json)" />
          )}
          {tab === "openai" && mcp && (
            <Snippet {...mcpSetup("openai-agents", mcp, t)} title="Python — OpenAI Agents SDK" />
          )}
          {tab !== "rest" && mcp && (
            <p className="text-xs text-muted">
              Codex, Antigravity or Cursor without npx: each has its own format on the{" "}
              <Link href="/console/connect" className="text-primary hover:underline">Connect page</Link>.
            </p>
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
