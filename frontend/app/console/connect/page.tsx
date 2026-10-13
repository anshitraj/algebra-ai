"use client";

import Link from "next/link";
import Image from "next/image";
import { useEffect, useRef, useState } from "react";
import * as api from "@/lib/api-client";
import type { PassConnect } from "@/lib/types";
import { Snippet } from "@/components/console/snippet";
import {
  IconArrowRight,
  IconChevronDown,
  IconExternal,
  IconPlug,
  IconPlus,
  IconSearch,
  IconShield,
  IconX,
} from "@/components/icons";
import { Logo } from "@/components/logo";
import styles from "./connect.module.css";

const APPS = [
  {
    id: "claude",
    name: "Claude",
    publisher: "Anthropic",
    category: "Desktop apps",
    description:
      "Give Claude access to paid APIs, with your spending rules built in.",
    tag: "Desktop & Code",
    guide:
      "Add this to Claude Desktop’s configuration file under mcpServers. Restart Claude to load the tools.",
    docs: "https://modelcontextprotocol.io/docs/develop/connect-local-servers",
  },
  {
    id: "cursor",
    name: "Cursor",
    publisher: "Cursor",
    category: "Developer tools",
    description:
      "Bring paid API tools into your editor and keep building without switching tabs.",
    tag: "Code editor",
    guide:
      "Add this to .cursor/mcp.json in your project, or ~/.cursor/mcp.json for all projects. Enable Algebra in Cursor’s MCP settings.",
    docs: "https://cursor.com/docs/context/mcp",
  },
  {
    id: "openai",
    name: "OpenAI Agents",
    publisher: "OpenAI",
    category: "Developer tools",
    description:
      "Let your agent discover, price, and call APIs through the Agents SDK.",
    tag: "Agents SDK",
    guide:
      "Use the Python Agents SDK to attach Algebra’s MCP server to your agent.",
    docs: "https://openai.github.io/openai-agents-python/mcp/",
  },
  {
    id: "custom",
    name: "Custom agent",
    publisher: "Your own stack",
    category: "Developer tools",
    description:
      "Connect any MCP-compatible client, or build directly with the REST API.",
    tag: "MCP & REST",
    guide:
      "Add the server URL and Authorization header to a client that supports Streamable HTTP. For stdio clients, use the npx adapter below.",
    docs: "https://modelcontextprotocol.io/docs/develop/connect-remote-servers",
  },
] as const;
type Connector = (typeof APPS)[number];
const FILTERS = ["All apps", "Desktop apps", "Developer tools"];
const TOKEN = "<SPEND_PASS_TOKEN>";
const TOOLS = [
  [
    "algebra.discover",
    "Browse paid APIs and endpoint capability IDs across catalogs.",
  ],
  [
    "algebra.classes",
    "Find the kinds of work Algebra can route, with a shared input shape.",
  ],
  ["algebra.quote", "Compare real prices with a free, unpaid request."],
  [
    "algebra.execute",
    "Run a task within your budget and get a signed receipt.",
  ],
  [
    "algebra.simulate",
    "Check a request against your rules without making a payment.",
  ],
  ["algebra.status", "Read a request’s result, provider, cost, and receipt."],
  ["algebra.receipt", "Retrieve and verify a signed receipt."],
  ["algebra.spend_pass", "Read your pass’s limits and remaining budget."],
];
const ROUTES = [
  ["GET /providers?q=&category=&source=", "Search the combined catalogs."],
  ["GET /providers/{id}", "Read a provider’s endpoints and capability IDs."],
  ["POST /execute", "Run a task and receive a result and receipt."],
  ["GET /economic-intents/{id}", "Check a request and its receipt."],
  ["GET /pass", "Read spending limits and remaining budget."],
];

function AppMark({ app }: { app: Connector }) {
  return (
    <span className={`${styles.appMark} ${styles[app.id]}`} aria-hidden="true">
      {app.id === "custom" ? (
        <IconPlug size={27} />
      ) : (
        <Image
          src={`/connectors/${app.id}.svg`}
          width={32}
          height={32}
          alt=""
        />
      )}
    </span>
  );
}

function configuration(app: Connector, mcp: string, mode: string) {
  if (app.id === "claude" && mode === "code")
    return `claude mcp add --transport http algebra "${mcp}" \\\n  --header "Authorization: Bearer ${TOKEN}"`;
  if (app.id === "openai")
    return `from agents import Agent\nfrom agents.mcp import MCPServerStreamableHttp\n\nalgebra = MCPServerStreamableHttp(params={\n    "url": "${mcp}",\n    "headers": {"Authorization": "Bearer ${TOKEN}"},\n})\nagent = Agent(name="Researcher", mcp_servers=[algebra])`;
  if (app.id === "custom")
    return `npx -y mcp-remote "${mcp}" \\\n  --header "Authorization: Bearer ${TOKEN}"`;
  return JSON.stringify(
    {
      mcpServers: {
        algebra: {
          command: "npx",
          args: [
            "-y",
            "mcp-remote",
            mcp,
            "--header",
            `Authorization: Bearer ${TOKEN}`,
          ],
        },
      },
    },
    null,
    2,
  );
}

export default function ConnectPage() {
  const [connect, setConnect] = useState<PassConnect | null>(null);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("All apps");
  const [selected, setSelected] = useState<Connector | null>(null);
  const [mode, setMode] = useState("desktop");
  const [loadError, setLoadError] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    let live = true;
    api
      .listPasses()
      .then((r) => {
        if (live) setConnect(r.connect);
      })
      .catch(() => {
        if (live) setLoadError(true);
      });
    return () => {
      live = false;
    };
  }, []);
  useEffect(() => {
    if (!selected) return;
    const element = dialog.current;
    const previousFocus = document.activeElement as HTMLElement | null;
    element?.showModal();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      element?.close();
      document.body.style.overflow = previousOverflow;
      previousFocus?.focus();
    };
  }, [selected]);
  const open = (app: Connector) => {
    setMode("desktop");
    setSelected(app);
  };
  const visible = APPS.filter(
    (app) =>
      (filter === "All apps" || app.category === filter) &&
      `${app.name} ${app.publisher} ${app.description} ${app.tag}`
        .toLowerCase()
        .includes(query.toLowerCase().trim()),
  );
  const mcp = connect?.mcp_url;
  const curl = connect
    ? `curl -X POST "${connect.api_base}/execute" \\\n  -H "Authorization: Bearer ${TOKEN}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"capability":"circle.birdeye.get.x402-defi-price","providers":["circle:birdeye"],"input":{"address":"So11111111111111111111111111111111111111112"},"budget_max_minor":10000}'`
    : "";

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>YOUR AGENT, CONNECTED</p>
          <h1>Connect your apps</h1>
          <p className={styles.subtitle}>
            The tools your agent needs. The spending control you keep.
          </p>
        </div>
        <button
          type="button"
          className={styles.primaryButton}
          onClick={() => open(APPS[3])}
        >
          <IconPlus size={17} /> Add custom agent
        </button>
      </header>
      <section className={styles.bridge} aria-label="How connections work">
        <div className={styles.bridgeArt} aria-hidden="true">
          <span className={styles.algebraMark}>
            <Logo size={26} />
          </span>
          <span className={styles.connectionLine} />
          <span className={styles.brandCluster}>
            {APPS.slice(0, 3).map((app) => (
              <AppMark key={app.id} app={app} />
            ))}
          </span>
        </div>
        <div className={styles.bridgeCopy}>
          <h2>One connection. A world of APIs.</h2>
          <p>
            Let your favorite agent discover and use paid APIs through Algebra.
          </p>
          <span>
            <IconShield size={13} /> Every request follows your Spend Pass
            limits.
          </span>
        </div>
        <Link href="/console/passes" className={styles.textLink}>
          Manage Spend Passes <IconArrowRight size={15} />
        </Link>
      </section>
      <section className={styles.directory} aria-labelledby="directory-title">
        <div className={styles.directoryHeading}>
          <div>
            <h2 id="directory-title">
              Choose your app <span>{APPS.length}</span>
            </h2>
            <p>Add Algebra to the place you already work.</p>
          </div>
          <label className={styles.search}>
            <IconSearch size={16} />
            <input
              type="search"
              aria-label="Search apps"
              placeholder="Search apps…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </label>
        </div>
        <div className={styles.filters} role="group" aria-label="Filter apps">
          {FILTERS.map((item) => (
            <button
              type="button"
              key={item}
              aria-pressed={filter === item}
              onClick={() => setFilter(item)}
            >
              {item}
            </button>
          ))}
        </div>
        <div className={styles.grid}>
          {visible.map((app) => (
            <article key={app.id} className={styles.card}>
              <div className={styles.cardTop}>
                <AppMark app={app} />
                <button
                  type="button"
                  className={styles.addButton}
                  aria-label={`Add ${app.name}`}
                  onClick={() => open(app)}
                >
                  <IconPlus size={19} />
                </button>
              </div>
              <h3>{app.name}</h3>
              <p className={styles.publisher}>By {app.publisher}</p>
              <p className={styles.cardDescription}>{app.description}</p>
              <div className={styles.cardFooter}>
                <span>{app.tag}</span>
                <button type="button" onClick={() => open(app)}>
                  Set up <IconArrowRight size={13} />
                </button>
              </div>
            </article>
          ))}
        </div>
        {visible.length === 0 && (
          <div className={styles.empty}>
            <IconSearch size={24} />
            <h3>No apps found</h3>
            <p>Try another name or browse all apps.</p>
            <button
              type="button"
              className={styles.secondaryButton}
              onClick={() => {
                setQuery("");
                setFilter("All apps");
              }}
            >
              Clear filters
            </button>
          </div>
        )}
        <p className={styles.directoryNote}>
          <IconPlug size={14} /> Works with MCP. Choose an app to get its
          connection instructions.
        </p>
      </section>
      <details className={styles.reference}>
        <summary>
          <span>
            <span className={styles.referenceIcon} aria-hidden="true">
              &lt;/&gt;
            </span>
            <span>
              <strong>Building something of your own?</strong>
              <small>MCP tools, REST endpoints, and examples</small>
            </span>
          </span>
          <IconChevronDown size={17} />
        </summary>
        <div className={styles.referenceBody}>
          <h3>MCP tools</h3>
          <p>
            Listed providers are not verified. Your Spend Pass controls what an
            agent can pay.
          </p>
          <ul className={styles.referenceList}>
            {TOOLS.map(([name, what]) => (
              <li key={name}>
                <code>{name}</code>
                <span>{what}</span>
              </li>
            ))}
          </ul>
          <h3>REST API</h3>
          <p>
            Authenticate with <code>Authorization: Bearer {TOKEN}</code>.
          </p>
          {connect && <Snippet title="Base URL" code={connect.api_base} />}
          <ul className={styles.referenceList}>
            {ROUTES.map(([path, what]) => (
              <li key={path}>
                <code>{path}</code>
                <span>{what}</span>
              </li>
            ))}
          </ul>
          {connect && (
            <Snippet
              title="Ask for an outcome"
              code={curl}
              note="budget_max_minor is micro-USDC: 10000 is 0.01 USDC. Find capability IDs on the Providers page."
            />
          )}
          {loadError && (
            <p role="status">
              Connection details could not be loaded. Reload this page to try
              again.
            </p>
          )}
          <h3>Explore the ecosystem</h3>
          <p>
            Algebra already reads these catalogs. Direct payments through other
            servers use their own controls, outside your Algebra Spend Pass.
          </p>
          <div className={styles.ecosystem}>
            <a
              href="https://agents.circle.com"
              target="_blank"
              rel="noreferrer"
            >
              Circle Agent Marketplace <IconExternal size={13} />
              <small>Read-only discovery · Streamable HTTP</small>
            </a>
            <a href="https://pay.sh" target="_blank" rel="noreferrer">
              Pay.sh <IconExternal size={13} />
              <small>Local wallet payments · pay mcp</small>
            </a>
          </div>
        </div>
      </details>
      <footer className={styles.pageFooter}>
        <IconShield size={14} />
        <span>
          Your budget. Your rules. Every payment comes with a receipt.
        </span>
        <Link href="/console/firewall">
          Review your firewall <IconArrowRight size={13} />
        </Link>
      </footer>
      {selected && (
        <dialog
          ref={dialog}
          className={styles.dialog}
          aria-labelledby="connection-title"
          onCancel={() => setSelected(null)}
          onClick={(e) => {
            if (e.target !== e.currentTarget) return;
            const r = e.currentTarget.getBoundingClientRect();
            if (
              e.clientX < r.left ||
              e.clientX > r.right ||
              e.clientY < r.top ||
              e.clientY > r.bottom
            )
              setSelected(null);
          }}
        >
          <div className={styles.dialogHeader}>
            <AppMark app={selected} />
            <button
              type="button"
              className={styles.closeButton}
              aria-label="Close setup"
              onClick={() => setSelected(null)}
            >
              <IconX size={19} />
            </button>
          </div>
          <p className={styles.eyebrow}>CONNECT TO ALGEBRA</p>
          <h2 id="connection-title">Set up {selected.name}</h2>
          <p className={styles.dialogIntro}>
            Bring Algebra’s tools into{" "}
            {selected.id === "custom" ? "your agent" : selected.name}. Your
            Spend Pass sets the limits.
          </p>
          <div className={styles.step}>
            <span className={styles.stepNumber}>1</span>
            <div>
              <h3>Get a Spend Pass</h3>
              <p>Issue a pass for this agent and keep its token ready.</p>
              <Link href="/console/passes" className={styles.textLink}>
                Go to Spend Passes <IconArrowRight size={13} />
              </Link>
            </div>
          </div>
          <div className={styles.step}>
            <span className={styles.stepNumber}>2</span>
            <div className={styles.stepContent}>
              <h3>
                Add Algebra to{" "}
                {selected.id === "custom" ? "your client" : selected.name}
              </h3>
              {selected.id === "claude" && (
                <div
                  className={styles.modeSwitch}
                  role="group"
                  aria-label="Claude client"
                >
                  <button
                    type="button"
                    aria-pressed={mode === "desktop"}
                    onClick={() => setMode("desktop")}
                  >
                    Claude Desktop
                  </button>
                  <button
                    type="button"
                    aria-pressed={mode === "code"}
                    onClick={() => setMode("code")}
                  >
                    Claude Code
                  </button>
                </div>
              )}
              <p>
                {selected.id === "claude" && mode === "code"
                  ? "Run this command in your terminal to add Algebra to Claude Code."
                  : selected.guide}
              </p>
              {mcp ? (
                <>
                  {selected.id === "custom" && (
                    <Snippet
                      title="Server URL · Streamable HTTP"
                      code={mcp}
                      note={`Header: Authorization: Bearer ${TOKEN}`}
                    />
                  )}
                  <Snippet
                    title={
                      selected.id === "openai"
                        ? "Python configuration"
                        : selected.id === "custom" || mode === "code"
                          ? "Connection command"
                          : "MCP configuration"
                    }
                    code={configuration(selected, mcp, mode)}
                    note="Replace <SPEND_PASS_TOKEN> with the token issued for this agent."
                  />
                </>
              ) : (
                <p className={styles.setupNotice} role="status">
                  {loadError
                    ? "We couldn’t load the server address. Reload this page to try again."
                    : connect
                      ? "This deployment hasn’t published an MCP address. Use the REST example below to connect your agent."
                      : "Loading connection details…"}
                </p>
              )}
              {!mcp && connect && (
                <Snippet
                  title="REST connection"
                  code={curl}
                  note="Replace the Spend Pass token placeholder before making a request."
                />
              )}
            </div>
          </div>
          <div className={styles.dialogFooter}>
            <a
              href={selected.docs}
              target="_blank"
              rel="noreferrer"
              className={styles.textLink}
            >
              Client documentation <IconExternal size={13} />
            </a>
            <button
              type="button"
              className={styles.primaryButton}
              onClick={() => setSelected(null)}
            >
              Done
            </button>
          </div>
        </dialog>
      )}
    </div>
  );
}
