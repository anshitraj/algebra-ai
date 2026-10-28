"use client";

import Link from "next/link";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import * as api from "@/lib/api-client";
import type {
  APIKey,
  Environment,
  IssuedKey,
  Organization,
  PlatformEvent,
  Project,
  ProjectUsage,
  Scope,
  Strategy,
} from "@/lib/platform-types";
import type { SpendPass } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { useNetwork } from "@/lib/network";
import { mcpSetup, type McpClient } from "@/lib/mcp-clients";
import {
  Button,
  EmptyState,
  ErrorNote,
  Field,
  Input,
  Skeleton,
  timeAgo,
} from "@/components/console/ui";
import { Copy, Snippet } from "@/components/console/snippet";
import { ENVIRONMENTS, EnvBadge } from "@/components/console/environment";
import {
  IconArrowRight,
  IconCheck,
  IconChevronDown,
  IconEye,
  IconEyeOff,
  IconLock,
  IconPlug,
  IconPlus,
  IconShield,
  IconX,
  Spinner,
} from "@/components/icons";
import styles from "./projects.module.css";

const select = styles.select;
const CLIENTS: { id: McpClient; label: string; location: string }[] = [
  {
    id: "cursor",
    label: "Cursor",
    location: "Add to .cursor/mcp.json, then enable Algebra in MCP settings.",
  },
  {
    id: "claude-desktop",
    label: "Claude",
    location:
      "Add to Claude Desktop’s MCP configuration, then restart the app.",
  },
  {
    id: "codex",
    label: "Codex",
    location: "Add to ~/.codex/config.toml, then restart Codex.",
  },
  {
    id: "stdio",
    label: "Other apps",
    location: "Run this adapter from your MCP client’s environment.",
  },
];
const ENV_COPY: Record<Environment, string> = {
  sandbox: "Test with simulated providers and USDC. No real funds are used.",
  devnet: "Use Solana Devnet with test USDC. No real funds are used.",
  mainnet:
    "Use real USDC on Solana. Spending follows your project’s Spend Pass limits.",
};
function keyState(key: APIKey) {
  return key.revoked_at
    ? "Revoked"
    : key.expires_at && new Date(key.expires_at).getTime() <= Date.now()
      ? "Expired"
      : "Active";
}
function Section({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`${styles.section} ${className}`}>{children}</section>
  );
}
function Dialog({
  title,
  children,
  onClose,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const element = ref.current;
    const previousFocus = document.activeElement as HTMLElement | null;
    element?.showModal();
    element?.querySelector<HTMLInputElement>("input")?.focus();
    return () => {
      element?.close();
      previousFocus?.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={styles.dialog}
      aria-labelledby="project-dialog-title"
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
    >
      <div className={styles.dialogHeader}>
        <h2 id="project-dialog-title">{title}</h2>
        <button
          className={styles.iconButton}
          type="button"
          aria-label="Close dialog"
          onClick={onClose}
        >
          <IconX size={18} />
        </button>
      </div>
      {children}
    </dialog>
  );
}

export default function ProjectsPage() {
  const [orgs, setOrgs] = useState<Organization[] | null>(null);
  const [org, setOrg] = useState<Organization | null>(null);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [projectId, setProjectId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [startingTrial, setStartingTrial] = useState(false);
  useEffect(() => {
    api
      .listOrgs()
      .then((r) => {
        setOrgs(r.organizations);
        setOrg(r.organizations[0] ?? null);
      })
      .catch((e) =>
        setError(
          e instanceof Error ? e.message : "Couldn't load your workspace",
        ),
      );
  }, []);
  useEffect(() => {
    if (!org) return;
    let live = true;
    api
      .listProjects(org.id)
      .then((r) => {
        if (!live) return;
        setProjects(r.projects);
        setProjectId((id) =>
          r.projects.some((p) => p.id === id) ? id : (r.projects[0]?.id ?? ""),
        );
      })
      .catch((e) => {
        if (live)
          setError(e instanceof Error ? e.message : "Couldn't load projects");
      });
    return () => {
      live = false;
    };
  }, [org]);
  const manage = org?.role === "owner" || org?.role === "admin";
  const project = projects?.find((p) => p.id === projectId) ?? null;
  const live = projects?.filter((p) => !p.archived_at).length ?? 0;
  const limit = org?.entitlements?.projects ?? 1;
  const atLimit = limit >= 0 && live >= limit;
  async function create() {
    if (!org || !newName.trim() || creating) return;
    setCreating(true);
    setError(null);
    try {
      const p = await api.createProject(org.id, newName.trim());
      setProjects((x) => [...(x ?? []), p]);
      setProjectId(p.id);
      setNewName("");
      setShowCreate(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create project");
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
      setOrgs(
        (x) => x?.map((o) => (o.id === updated.id ? updated : o)) ?? null,
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't start trial");
    } finally {
      setStartingTrial(false);
    }
  }
  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>DEVELOPER SETTINGS</p>
          <h1>Projects & API keys</h1>
          <p>One API key for your apps, agents, and Algebra MCP.</p>
        </div>
        <Link className={styles.textLink} href="/console/quickstart">
          Quickstart <IconArrowRight size={15} />
        </Link>
      </header>
      {error && <ErrorNote>{error}</ErrorNote>}
      {!orgs && !error && <Skeleton className="h-48" />}
      {orgs?.length === 0 && (
        <EmptyState
          title="No workspace available"
          body="Ask your workspace owner to invite you."
        />
      )}
      {org && (
        <div className={styles.workspaceBar}>
          <div className={styles.projectPicker}>
            <span className={styles.projectMark}>
              <IconPlug size={18} />
            </span>
            <div>
              <label htmlFor="project-picker">Project</label>
              <select
                id="project-picker"
                value={projectId}
                onChange={(e) => setProjectId(e.target.value)}
                disabled={!projects?.length}
              >
                {projects?.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                    {p.archived_at ? " (archived)" : ""}
                  </option>
                ))}
              </select>
            </div>
            {manage && (
              <button
                type="button"
                className={styles.iconButton}
                aria-label="Add project"
                onClick={() => setShowCreate(true)}
              >
                <IconPlus size={17} />
              </button>
            )}
          </div>
          <div className={styles.workspaceMeta}>
            {orgs && orgs.length > 1 ? (
              <select
                aria-label="Workspace"
                className={select}
                value={org.id}
                onChange={(e) => {
                  setProjects(null);
                  setOrg(orgs.find((o) => o.id === e.target.value) ?? null);
                }}
              >
                {orgs.map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.name}
                  </option>
                ))}
              </select>
            ) : (
              <span>{org.name}</span>
            )}
            <span className={styles.badge}>
              {org.trial_active
                ? `${org.trial_plan} trial`
                : `${org.plan} plan`}
            </span>
          </div>
        </div>
      )}
      {org && !projects && <Skeleton className="h-72" />}
      {project && (
        <ProjectPanel
          key={`${project.id}:${org?.effective_plan ?? ""}`}
          project={project}
          manage={manage}
          onUpdated={(p) =>
            setProjects((x) => x?.map((y) => (y.id === p.id ? p : y)) ?? null)
          }
        />
      )}
      {projects?.length === 0 && (
        <EmptyState
          title="Create your first project"
          body="Keep your API key, spending limits, and usage together."
          action={
            manage && (
              <Button onClick={() => setShowCreate(true)}>
                <IconPlus size={15} /> Create project
              </Button>
            )
          }
        />
      )}
      {org && (
        <footer className={styles.footer}>
          <span>
            {limit < 0 ? `${live} projects` : `${live} of ${limit} projects`} ·{" "}
            <span className="capitalize">{org.role}</span>
            {org.trial_active && org.trial_ends_at
              ? ` · Trial ends ${new Date(org.trial_ends_at).toLocaleDateString()}`
              : ""}
          </span>
          <Link href="/pricing">
            Manage plan <IconArrowRight size={13} />
          </Link>
        </footer>
      )}
      {showCreate && (
        <Dialog title="Create project" onClose={() => setShowCreate(false)}>
          {atLimit ? (
            <div className={styles.form}>
              <p>
                Your plan includes {limit} project{limit === 1 ? "" : "s"}.
                Upgrade to add more.
              </p>
              {org?.trial_available && manage && (
                <>
                  <Button onClick={trial} disabled={startingTrial}>
                    {startingTrial && <Spinner size={13} />} Try Startup for 7
                    days
                  </Button>
                  <p className={styles.help}>
                    No card required. Your workspace returns to Free when the
                    trial ends.
                  </p>
                </>
              )}
              <Link className={styles.textLink} href="/pricing">
                View plans <IconArrowRight size={14} />
              </Link>
            </div>
          ) : (
            <form
              className={styles.form}
              onSubmit={(e) => {
                e.preventDefault();
                create();
              }}
            >
              <Field label="Project name">
                <Input
                  autoFocus
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="e.g. Production app"
                  maxLength={80}
                  required
                />
              </Field>
              <p className={styles.help}>
                Each project has its own keys and spending limits.
              </p>
              <Button type="submit" disabled={creating || !newName.trim()}>
                {creating && <Spinner size={13} />} Create project
              </Button>
            </form>
          )}
          {error && <ErrorNote>{error}</ErrorNote>}
        </Dialog>
      )}
    </div>
  );
}

function ProjectPanel({
  project,
  manage,
  onUpdated,
}: {
  project: Project;
  manage: boolean;
  onUpdated: (p: Project) => void;
}) {
  const { network } = useNetwork();
  const [env, setEnv] = useState<Environment>(
    network === "sandbox"
      ? "sandbox"
      : network === "solana"
        ? "mainnet"
        : "devnet",
  );
  const [tab, setTab] = useState("access");
  const [keys, setKeys] = useState<APIKey[] | null>(null);
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [events, setEvents] = useState<PlatformEvent[] | null>(null);
  const [usage, setUsage] = useState<ProjectUsage | null>(null);
  const [mcpUrl, setMcpUrl] = useState("");
  const [issued, setIssued] = useState<IssuedKey | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [confirm, setConfirm] = useState<{
    key: APIKey;
    action: "rotate" | "revoke";
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [client, setClient] = useState<McpClient>("cursor");
  const loadKeys = useCallback(async () => {
    const k = await api.listKeys(project.id);
    setKeys(k.keys);
    setMcpUrl(k.connect.mcp_url ?? "");
  }, [project.id]);
  const loadDetails = useCallback(async () => {
    const results = await Promise.allSettled([
      api.listProjectPasses(project.id),
      api.listProjectEvents(project.id, 20),
      api.projectUsage(project.id),
    ]);
    const [p, ev, u] = results;
    if (p.status === "fulfilled") setPasses(p.value.passes);
    if (ev.status === "fulfilled") setEvents(ev.value.events);
    if (u.status === "fulfilled") setUsage(u.value);
    const failed = results.find((r) => r.status === "rejected");
    if (failed?.status === "rejected") throw failed.reason;
  }, [project.id]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch project resources on mount
    loadKeys().catch((e) =>
      setError(e instanceof Error ? e.message : "Couldn't load API keys"),
    );
    loadDetails().catch((e) =>
      setError(
        e instanceof Error ? e.message : "Couldn't load project details",
      ),
    );
  }, [loadKeys, loadDetails]);
  const envKeys = keys?.filter((k) => k.environment === env) ?? [];
  const active = envKeys.filter((k) => keyState(k) === "Active");
  const setup = mcpUrl ? mcpSetup(client, mcpUrl) : null;
  async function confirmAction() {
    if (!confirm || busy) return;
    setBusy(true);
    setError(null);
    try {
      if (confirm.action === "rotate")
        setIssued((await api.rotateKey(confirm.key.id)).key);
      else {
        await api.revokeKey(confirm.key.id);
        if (issued?.id === confirm.key.id) setIssued(null);
      }
      setConfirm(null);
      await loadKeys();
      await loadDetails();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't update key");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <div className={styles.tabs} role="group" aria-label="Project sections">
        {[
          { id: "access", label: "API key & MCP" },
          { id: "usage", label: "Usage" },
          { id: "settings", label: "Project settings" },
        ].map((t) => (
          <button
            key={t.id}
            type="button"
            aria-pressed={tab === t.id}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div key={tab} className={styles.content}>
        {tab === "access" && (
          <>
            <Section>
              <div className={styles.sectionHeader}>
                <div>
                  <h2>API key</h2>
                  <p>Use the same key for MCP and the REST API.</p>
                </div>
                <label className={styles.envSelect}>
                  <span>Environment</span>
                  <select
                    className={select}
                    value={env}
                    onChange={(e) => setEnv(e.target.value as Environment)}
                  >
                    {ENVIRONMENTS.map((e) => (
                      <option key={e.id} value={e.id}>
                        {e.label}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <p className={styles.environmentNote}>
                <IconShield size={14} /> {ENV_COPY[env]}
              </p>
              {issued && issued.environment === env && (
                <IssuedSecret issued={issued} onDone={() => setIssued(null)} />
              )}
              {!keys ? (
                <Skeleton className="h-28" />
              ) : active.length === 0 ? (
                <div className={styles.emptyKey}>
                  <span className={styles.keyIcon}>
                    <IconLock size={23} />
                  </span>
                  <h3>Your connection starts here</h3>
                  <p>Create a key to connect your agent to Algebra.</p>
                  {manage ? (
                    <Button onClick={() => setShowCreate(true)}>
                      <IconPlus size={15} /> Create API key
                    </Button>
                  ) : (
                    <p>Ask a workspace admin to create an API key.</p>
                  )}
                </div>
              ) : (
                <div className={styles.keyList}>
                  {active.map((k) => (
                    <div key={k.id} className={styles.keyRow}>
                      <span className={styles.keyIcon}>
                        <IconLock size={18} />
                      </span>
                      <div className={styles.keyInfo}>
                        <div>
                          <strong>{k.name}</strong>
                          <span className={styles.activeBadge}>
                            <span /> Active
                          </span>
                        </div>
                        <code>{k.prefix}••••••••••••••••</code>
                        <small>Permissions: {k.scopes.join(", ")}</small>
                        <small>
                          Created {timeAgo(k.created_at)} ·{" "}
                          {k.last_used_at
                            ? `Last used ${timeAgo(k.last_used_at)}`
                            : "Not used yet"}
                        </small>
                      </div>
                      {manage && (
                        <div className={styles.keyActions}>
                          <Button
                            variant="secondary"
                            onClick={() =>
                              setConfirm({ key: k, action: "rotate" })
                            }
                          >
                            Rotate key
                          </Button>
                          <button
                            className={styles.subtleButton}
                            type="button"
                            onClick={() =>
                              setConfirm({ key: k, action: "revoke" })
                            }
                          >
                            Revoke
                          </button>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
              {!!active.length && !issued && (
                <p className={styles.keyFootnote}>
                  <IconLock size={13} /> Keys are shown once. Use your saved
                  key, or rotate it to get a new one.
                </p>
              )}
              {envKeys.some((k) => keyState(k) !== "Active") && (
                <details className={styles.details}>
                  <summary>
                    Key history <IconChevronDown size={14} />
                  </summary>
                  <div>
                    {envKeys
                      .filter((k) => keyState(k) !== "Active")
                      .map((k) => (
                        <p className={styles.historyRow} key={k.id}>
                          <span>
                            {k.name} <code>{k.prefix}…</code>
                          </span>
                          <span>{keyState(k)}</span>
                        </p>
                      ))}
                  </div>
                </details>
              )}
            </Section>
            <Section className={styles.connection}>
              <div className={styles.sectionHeader}>
                <div className={styles.connectionHeading}>
                  <span className={styles.plugIcon}>
                    <IconPlug size={20} />
                  </span>
                  <div>
                    <h2>Connect to Algebra MCP</h2>
                    <p>Your API key is all you need. No separate MCP token.</p>
                  </div>
                </div>
                <Link className={styles.textLink} href="/console/connect">
                  All apps <IconArrowRight size={14} />
                </Link>
              </div>
              <div className={styles.connectionBody}>
                <div className={styles.connectionStep}>
                  <span className={styles.step}>1</span>
                  <div>
                    <h3>Save your API key</h3>
                    <p>
                      Set <code>ALGEBRA_API_KEY</code> in your app’s
                      environment. Use a key from the environment selected
                      above.
                    </p>
                    <details className={styles.details}>
                      <summary>
                        How to set it <IconChevronDown size={14} />
                      </summary>
                      <div className={styles.envCommands}>
                        <Snippet
                          title="macOS / Linux"
                          code={'export ALGEBRA_API_KEY="<your saved API key>"'}
                        />
                        <Snippet
                          title="Windows PowerShell"
                          code={'$env:ALGEBRA_API_KEY = "<your saved API key>"'}
                        />
                      </div>
                    </details>
                  </div>
                </div>
                <div className={styles.connectionStep}>
                  <span className={styles.step}>2</span>
                  <div className={styles.setup}>
                    <h3>Add Algebra to your app</h3>
                    <div
                      className={styles.clientTabs}
                      role="group"
                      aria-label="MCP client"
                    >
                      {CLIENTS.map((c) => (
                        <button
                          key={c.id}
                          type="button"
                          aria-pressed={client === c.id}
                          onClick={() => setClient(c.id)}
                        >
                          {c.label}
                        </button>
                      ))}
                    </div>
                    <p>{CLIENTS.find((c) => c.id === client)?.location}</p>
                    {setup ? (
                      <div key={client} className={styles.snippet}>
                        <Snippet title={setup.title} code={setup.code} />
                      </div>
                    ) : (
                      <p className={styles.help}>
                        The server configuration will appear when your project
                        loads.
                      </p>
                    )}
                    {client === "claude-desktop" && (
                      <p className={styles.help}>
                        Replace &lt;your API key&gt; in AUTH_HEADER with your
                        saved key. Keep this configuration private.
                      </p>
                    )}
                  </div>
                </div>
              </div>
              {mcpUrl && (
                <div className={styles.endpoint}>
                  <span>MCP endpoint</span>
                  <code>{mcpUrl}</code>
                  <Copy text={mcpUrl} label="Copy URL" />
                </div>
              )}
            </Section>
            <div className={styles.accessNote}>
              <IconShield size={16} />
              <p>
                Spending stays within your project’s limits.
                <br />
                <span>
                  A matching Spend Pass is required for paid requests.
                </span>
              </p>
              <button
                type="button"
                className={styles.textLink}
                onClick={() => setTab("settings")}
              >
                Manage limits <IconArrowRight size={14} />
              </button>
            </div>
          </>
        )}
        {tab === "usage" && <UsagePanel usage={usage} />}
        {tab === "settings" && (
          <>
            <Section>
              <div className={styles.sectionHeader}>
                <div>
                  <h2>Project details</h2>
                  <p>{project.name}</p>
                </div>
              </div>
              <div className={styles.settingRow}>
                <div>
                  <h3>Project ID</h3>
                  <code className={styles.projectId}>{project.id}</code>
                </div>
                <Copy text={project.id} label="Copy ID" />
              </div>
              <div className={styles.settingRow}>
                <div>
                  <h3>Default routing</h3>
                  <p>
                    How Algebra chooses a provider when a request has no
                    preference.
                  </p>
                </div>
                <select
                  aria-label="Default routing"
                  className={select}
                  disabled={!manage}
                  value={project.default_strategy}
                  onChange={async (e) => {
                    try {
                      onUpdated(
                        await api.updateProject(project.id, {
                          default_strategy: e.target.value as Strategy,
                        }),
                      );
                    } catch (err) {
                      setError(
                        err instanceof Error
                          ? err.message
                          : "Couldn't save routing",
                      );
                    }
                  }}
                >
                  {[
                    { id: "", label: "Automatic (balanced)" },
                    { id: "balanced", label: "Balanced" },
                    { id: "cheapest", label: "Lowest price" },
                    { id: "fastest", label: "Fastest response" },
                  ].map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.label}
                    </option>
                  ))}
                </select>
              </div>
            </Section>
            <Section>
              <div className={styles.sectionHeader}>
                <div>
                  <h2>Spending limits</h2>
                  <p>
                    Keys use Spend Passes in the same project and environment.
                  </p>
                </div>
                <Link className={styles.textLink} href="/console/passes">
                  Spend Passes <IconArrowRight size={14} />
                </Link>
              </div>
              {!passes ? (
                <Skeleton className="h-20" />
              ) : passes.length === 0 ? (
                <p className={styles.help}>
                  No Spend Passes yet. Add one to enable paid requests.
                </p>
              ) : (
                <div className={styles.passList}>
                  {passes.map((p) => (
                    <div className={styles.settingRow} key={p.id}>
                      <div>
                        <h3>{p.label}</h3>
                        <p>
                          {formatUSDC(p.remaining_minor_units)} remaining of{" "}
                          {formatUSDC(p.budget_minor_units)}
                          {!p.active ? " · Inactive" : ""}
                        </p>
                      </div>
                      <EnvBadge env={p.environment} />
                    </div>
                  ))}
                </div>
              )}
              {manage && (
                <details className={styles.details}>
                  <summary>
                    Add a Spend Pass <IconPlus size={14} />
                  </summary>
                  <NewPassForm
                    projectId={project.id}
                    onCreated={() =>
                      loadDetails().catch((e) =>
                        setError(
                          e instanceof Error
                            ? e.message
                            : "Couldn't refresh passes",
                        ),
                      )
                    }
                  />
                </details>
              )}
            </Section>
            <Section>
              <div className={styles.sectionHeader}>
                <div>
                  <h2>Recent activity</h2>
                  <p>Key and project changes.</p>
                </div>
              </div>
              {!events ? (
                <Skeleton className="h-16" />
              ) : !events.length ? (
                <p className={styles.help}>No activity yet.</p>
              ) : (
                events.map((e) => (
                  <div key={e.id} className={styles.historyRow}>
                    <span>{e.event.replace(/[._]/g, " ")}</span>
                    <time dateTime={e.created_at}>{timeAgo(e.created_at)}</time>
                  </div>
                ))
              )}
            </Section>
          </>
        )}
      </div>
      {showCreate && (
        <Dialog title="Create API key" onClose={() => setShowCreate(false)}>
          <NewKeyForm
            projectId={project.id}
            env={env}
            onIssued={(k) => {
              setIssued(k);
              setShowCreate(false);
              loadKeys().catch((e) =>
                setError(
                  e instanceof Error ? e.message : "Couldn't refresh keys",
                ),
              );
              loadDetails().catch(() => {});
            }}
          />
        </Dialog>
      )}
      {confirm && (
        <Dialog
          title={
            confirm.action === "rotate" ? "Rotate API key?" : "Revoke API key?"
          }
          onClose={() => {
            if (!busy) setConfirm(null);
          }}
        >
          <div className={styles.form}>
            <p>
              {confirm.action === "rotate"
                ? `This replaces “${confirm.key.name}” with a new key. Update your apps to keep them connected.`
                : `Apps using “${confirm.key.name}” will lose access immediately. This can’t be undone.`}
            </p>
            <div className={styles.dialogActions}>
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() => setConfirm(null)}
              >
                Cancel
              </Button>
              <Button disabled={busy} onClick={confirmAction}>
                {busy && <Spinner size={13} />}
                {confirm.action === "rotate" ? "Rotate key" : "Revoke key"}
              </Button>
            </div>
            {error && <ErrorNote>{error}</ErrorNote>}
          </div>
        </Dialog>
      )}
    </>
  );
}

function IssuedSecret({
  issued,
  onDone,
}: {
  issued: IssuedKey;
  onDone: () => void;
}) {
  const [visible, setVisible] = useState(false);
  return (
    <div className={styles.secret} role="status">
      <div className={styles.secretHeading}>
        <IconCheck size={17} />
        <strong>Your API key is ready</strong>
        <EnvBadge env={issued.environment} />
      </div>
      <p>Copy and save it now. You won’t be able to view it again.</p>
      <div className={styles.secretValue}>
        <code>
          {visible ? issued.secret : `${issued.prefix}${"•".repeat(28)}`}
        </code>
        <button
          type="button"
          className={styles.iconButton}
          aria-label={visible ? "Hide API key" : "Show API key"}
          aria-pressed={visible}
          onClick={() => setVisible(!visible)}
        >
          {visible ? <IconEyeOff size={17} /> : <IconEye size={17} />}
        </button>
        <Copy text={issued.secret} label="Copy key" />
      </div>
      <div className={styles.secretFooter}>
        <span>
          Keep it in your app’s configuration, outside source control.
        </span>
        <button className={styles.textLink} type="button" onClick={onDone}>
          I’ve saved my key <IconCheck size={14} />
        </button>
      </div>
    </div>
  );
}
function NewKeyForm({
  projectId,
  env,
  onIssued,
}: {
  projectId: string;
  env: Environment;
  onIssued: (k: IssuedKey) => void;
}) {
  const [name, setName] = useState("MCP connection");
  const [scopes, setScopes] = useState<Scope[]>([
    "read",
    "simulate",
    "execute",
  ]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function submit() {
    if (busy || scopes.length === 0) return;
    setBusy(true);
    setError(null);
    try {
      onIssued(
        (
          await api.createKey(projectId, {
            name: name.trim() || "MCP connection",
            environment: env,
            scopes,
          })
        ).key,
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create API key");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      className={styles.form}
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <p>Connect MCP and the REST API with a single key.</p>
      <Field label="Key name">
        <Input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={80}
          placeholder="e.g. MCP connection"
        />
      </Field>
      <div className={styles.formEnvironment}>
        <EnvBadge env={env} />
        <p>{ENV_COPY[env]}</p>
      </div>
      <details className={styles.details}>
        <summary>
          Permissions <IconChevronDown size={14} />
        </summary>
        <fieldset className={styles.permissions}>
          <legend className="sr-only">API key permissions</legend>
          {[
            { id: "read", label: "Read", detail: "View requests and receipts" },
            {
              id: "simulate",
              label: "Simulate",
              detail: "Test without spending",
            },
            {
              id: "execute",
              label: "Execute",
              detail: "Make paid requests within pass limits",
            },
          ].map((s) => (
            <label key={s.id}>
              <input
                type="checkbox"
                checked={scopes.includes(s.id as Scope)}
                onChange={(e) =>
                  setScopes((x) =>
                    e.target.checked
                      ? [...x, s.id as Scope]
                      : x.filter((y) => y !== s.id),
                  )
                }
              />
              <span>
                {s.label}
                <small>{s.detail}</small>
              </span>
            </label>
          ))}
        </fieldset>
      </details>
      {error && <ErrorNote>{error}</ErrorNote>}
      <Button type="submit" disabled={busy || scopes.length === 0}>
        {busy ? <Spinner size={13} /> : <IconPlus size={15} />} Create API key
      </Button>
    </form>
  );
}
function UsagePanel({ usage }: { usage: ProjectUsage | null }) {
  if (!usage) return <Skeleton className="h-56" />;
  const included = usage.included_executions;
  const month = new Date(usage.since).toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
    timeZone: "UTC",
  });
  return (
    <Section>
      <div className={styles.sectionHeader}>
        <div>
          <h2>Usage</h2>
          <p>{month} · Sandbox runs are free.</p>
        </div>
      </div>
      <div className={styles.usageGrid}>
        <div>
          <span>Total requests</span>
          <strong>{usage.requests.toLocaleString()}</strong>
        </div>
        <div>
          <span>On-chain executions</span>
          <strong>
            {usage.managed_executions.toLocaleString()}
            <small>
              {included < 0
                ? " / unlimited"
                : ` / ${included.toLocaleString()}`}
            </small>
          </strong>
        </div>
      </div>
      {included > 0 && (
        <progress
          className={styles.progress}
          aria-label="Included executions used"
          max={included}
          value={Math.min(usage.managed_executions, included)}
        />
      )}
      {!usage.requests ? (
        <div className={styles.usageEmpty}>
          <h3>Ready for your first request</h3>
          <p>Connect your app or follow the quickstart to get started.</p>
          <Link className={styles.textLink} href="/console/quickstart">
            Open quickstart <IconArrowRight size={14} />
          </Link>
        </div>
      ) : (
        <div className={styles.tableWrap}>
          <table>
            <thead>
              <tr>
                <th>Environment</th>
                <th>Executions</th>
                <th>Paid to providers</th>
              </tr>
            </thead>
            <tbody>
              {usage.by_network.map((n) => (
                <tr key={n.network}>
                  <td>
                    {n.environment ? (
                      <EnvBadge env={n.environment} />
                    ) : (
                      n.network
                    )}
                  </td>
                  <td>{n.executions.toLocaleString()}</td>
                  <td>
                    {formatUSDC(n.spent_minor)}
                    {n.environment === "sandbox"
                      ? " simulated"
                      : n.environment === "devnet"
                        ? " test USDC"
                        : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}

function NewPassForm({
  projectId,
  onCreated,
}: {
  projectId: string;
  onCreated: () => void;
}) {
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
        label: `${ENVIRONMENTS.find((x) => x.id === env)?.label} pass`,
        agent_kind: "custom",
        currency: "USDC",
        budget_minor_units: micro(budget),
        budget_period: "month",
        max_per_purchase_minor_units: micro(perCall),
        allowed_categories: ["digital_services"],
        expires_in_days: 30,
        project_id: projectId,
        environment: env,
      });
      onCreated();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create the pass");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="mt-5 grid gap-3 border-t border-border pt-4 sm:grid-cols-3"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <Field label="Environment">
        <select
          className={select}
          value={env}
          onChange={(e) => setEnv(e.target.value as Environment)}
        >
          {ENVIRONMENTS.map((x) => (
            <option key={x.id} value={x.id}>
              {x.label}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Monthly budget (USDC)">
        <Input
          inputMode="decimal"
          value={budget}
          onChange={(e) => setBudget(e.target.value)}
        />
      </Field>
      <Field label="Per-request limit (USDC)">
        <Input
          inputMode="decimal"
          value={perCall}
          onChange={(e) => setPerCall(e.target.value)}
        />
      </Field>
      {error && (
        <div className="sm:col-span-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}
      <div className="sm:col-span-3">
        <Button
          type="submit"
          variant="secondary"
          disabled={busy || !(micro(budget) > 0) || !(micro(perCall) > 0)}
        >
          {busy && <Spinner size={13} />} Create Spend Pass
        </Button>
        <p className="mt-1.5 text-xs text-muted">
          For on-chain funding, open Spend Passes and connect your wallet.
        </p>
      </div>
    </form>
  );
}
