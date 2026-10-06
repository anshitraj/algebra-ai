"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { PassConnect } from "@/lib/types";
import { PageHeader, Panel, Skeleton } from "@/components/console/ui";
import { Snippet } from "@/components/console/snippet";
import { IconExternal, IconPlug, IconShield } from "@/components/icons";

const TOOLS = [
  { name: "algebra.classes", what: "The kinds of work Algebra routes across every catalog (token price, token risk and more), each with one input shape. Ask for a class and Algebra pays the best provider of it." },
  { name: "algebra.discover_providers", what: "Browse the paid APIs in Pay.sh, Circle's Agent Marketplace and PayAI, and an endpoint's capability ID." },
  { name: "algebra.execute", what: "Ask for an outcome with a USDC ceiling. Algebra prices it, checks the Spend Pass, pays, verifies and returns the result with a signed receipt." },
  { name: "algebra.simulate", what: "A dry run: would this be allowed, and who would be paid? Nothing is reserved or paid." },
  { name: "algebra.execution_status", what: "Check on a request: committed or not, what it cost, which provider, the receipt, and the answer while Algebra keeps it. Never moves money." },
  { name: "algebra.spend_pass", what: "Read the pass's own limits and the budget it has left." },
];

const ROUTES = [
  { method: "GET", path: "/providers?q=&category=&source=", what: "The catalogs, combined and searchable." },
  { method: "GET", path: "/providers/{id}", what: "One provider's endpoints, each with its capability ID." },
  { method: "POST", path: "/execute", what: "Ask for an outcome; Algebra pays and returns the result and receipt." },
  { method: "GET", path: "/economic-intents/{id}", what: "What happened to a request, with its receipt." },
  { method: "GET", path: "/pass", what: "The pass's limits and remaining budget." },
];

const ECOSYSTEM = [
  {
    name: "Circle Agent Marketplace",
    url: "https://agents.circle.com",
    transport: "Streamable HTTP · https://agents.circle.com/api/mcp · no sign-in",
    tools: "search_agent_marketplace_services, get_agent_marketplace_service",
    note: "Read-only search over Circle's catalog of x402 services.",
  },
  {
    name: "Pay.sh",
    url: "https://pay.sh",
    transport: "stdio · the pay CLI (pay mcp)",
    tools: "search_skills, get_skill_endpoints, curl, get_balance",
    note: "Pays from a local wallet you approve call by call.",
  },
];

export default function ConnectPage() {
  const [connect, setConnect] = useState<PassConnect | null>(null);

  useEffect(() => {
    api
      .listPasses()
      .then((r) => setConnect(r.connect))
      .catch(() => setConnect({ api_base: `${window.location.origin}/api/v1` }));
  }, []);

  const mcp = connect?.mcp_url;
  const token = "<SPEND_PASS_TOKEN>";

  return (
    <div className="mx-auto max-w-4xl">
      <PageHeader
        title="Connect an agent"
        description="Any agent that speaks MCP or can make an HTTP call can use Algebra. It authenticates with a Spend Pass token, and the pass is its whole authority: a USDC budget, a per-call ceiling and the providers it may pay."
        actions={
          <Link href="/console/passes" className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-tint hover:opacity-90">
            <IconShield size={15} /> Issue a Spend Pass
          </Link>
        }
      />

      <div className="mt-8 space-y-6">
        <Panel>
          <div className="flex items-start gap-3">
            <span className="grid h-9 w-9 shrink-0 place-items-center rounded-xl bg-primary-tint text-primary">
              <IconPlug size={17} />
            </span>
            <div className="min-w-0">
              <h2 className="font-display text-lg font-semibold text-foreground">Algebra&apos;s MCP server</h2>
              <p className="mt-0.5 text-sm text-muted">
                {mcp ? (
                  <>
                    Streamable HTTP at <code className="font-mono text-foreground">{mcp}</code>, with the pass token as a bearer token.
                  </>
                ) : (
                  <>This server hasn&apos;t published an MCP address. The tools are the same over REST below.</>
                )}
              </p>
            </div>
          </div>

          <ul className="mt-5 divide-y divide-border rounded-xl border border-border">
            {TOOLS.map((t) => (
              <li key={t.name} className="grid gap-1 px-4 py-3 sm:grid-cols-[14rem_1fr] sm:gap-4">
                <code className="font-mono text-xs text-foreground">{t.name}</code>
                <span className="text-sm text-muted">{t.what}</span>
              </li>
            ))}
          </ul>

          {connect === null ? (
            <Skeleton className="mt-5 h-24" />
          ) : (
            mcp && (
              <div className="mt-5 space-y-4">
                <Snippet title="Claude Code" code={`claude mcp add --transport http algebra ${mcp} \\\n  --header "Authorization: Bearer ${token}"`} />
                <Snippet
                  title="Claude Desktop or Cursor (MCP config)"
                  code={JSON.stringify({ mcpServers: { algebra: { command: "npx", args: ["-y", "mcp-remote", mcp, "--header", `Authorization: Bearer ${token}`] } } }, null, 2)}
                />
                <Snippet
                  title="OpenAI Agents SDK (Python)"
                  code={`from agents import Agent\nfrom agents.mcp import MCPServerStreamableHttp\n\nalgebra = MCPServerStreamableHttp(params={\n    "url": "${mcp}",\n    "headers": {"Authorization": "Bearer ${token}"},\n})\nagent = Agent(name="Researcher", mcp_servers=[algebra])`}
                />
              </div>
            )
          )}
        </Panel>

        <Panel>
          <h2 className="font-display text-lg font-semibold text-foreground">REST</h2>
          <p className="mt-0.5 text-sm text-muted">
            Base URL <code className="font-mono text-foreground">{connect?.api_base ?? "…"}</code>, with{" "}
            <code className="font-mono text-foreground">Authorization: Bearer {token}</code>.
          </p>
          <ul className="mt-4 divide-y divide-border rounded-xl border border-border">
            {ROUTES.map((r) => (
              <li key={r.path} className="grid gap-1 px-4 py-3 sm:grid-cols-[4rem_17rem_1fr] sm:items-center sm:gap-4">
                <span className="font-mono text-xs text-muted">{r.method}</span>
                <code className="font-mono text-xs text-foreground">{r.path}</code>
                <span className="text-sm text-muted">{r.what}</span>
              </li>
            ))}
          </ul>
          {connect && (
            <div className="mt-5">
              <Snippet
                title="Ask for an outcome"
                code={`curl -X POST ${connect.api_base}/execute \\\n  -H "Authorization: Bearer ${token}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"capability":"circle.birdeye.get.x402-defi-price","providers":["circle:birdeye"],\n       "input":{"address":"So11111111111111111111111111111111111111112"},"budget_max_minor":10000}'`}
                note="budget_max_minor is micro-USDC: 10000 is 0.01 USDC. Find capability IDs on the Providers page."
              />
            </div>
          )}
        </Panel>

        <Panel>
          <h2 className="font-display text-lg font-semibold text-foreground">Other MCP servers in the ecosystem</h2>
          <p className="mt-0.5 text-sm text-muted">
            Algebra already reads these catalogs for your agents. These servers let an agent browse them directly; paying through
            them instead of algebra.execute leaves out your Spend Pass, its approvals and its receipts.
          </p>
          <ul className="mt-4 grid gap-3 md:grid-cols-2">
            {ECOSYSTEM.map((s) => (
              <li key={s.name} className="rounded-xl border border-border px-4 py-3">
                <a href={s.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm font-medium text-foreground hover:text-primary">
                  {s.name} <IconExternal size={12} />
                </a>
                <p className="mt-1 text-xs text-muted">{s.note}</p>
                <p className="mt-2 font-mono text-[0.6875rem] text-muted">{s.transport}</p>
                <p className="mt-1 font-mono text-[0.6875rem] text-muted">tools: {s.tools}</p>
              </li>
            ))}
          </ul>
        </Panel>
      </div>
    </div>
  );
}
