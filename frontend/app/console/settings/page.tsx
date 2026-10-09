"use client";

import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import { useSession } from "@/lib/session";
import type { SessionInfo } from "@/lib/types";
import { GitHubMark, GoogleMark, IconCheck, IconExternal, IconMail, IconWallet, Spinner } from "@/components/icons";
import { Avatar } from "@/components/console/shell";
import { ErrorNote, PageHeader, Skeleton, timeAgo } from "@/components/console/ui";

function describeAgent(ua: string) {
  if (!ua) return "Unknown device";
  const browser = /Edg\//.test(ua) ? "Edge" : /Chrome\//.test(ua) ? "Chrome" : /Firefox\//.test(ua) ? "Firefox" : /Safari\//.test(ua) ? "Safari" : "Browser";
  const os = /Windows/.test(ua) ? "Windows" : /Mac OS X/.test(ua) ? "macOS" : /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${browser} on ${os}` : browser;
}

export default function SettingsPage() {
  const { user, setUser, signOut } = useSession();
  const [name, setName] = useState(user?.name ?? "");
  const [savingName, setSavingName] = useState(false);
  const [nameSaved, setNameSaved] = useState(false);
  const [sessions, setSessions] = useState<SessionInfo[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [revoking, setRevoking] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [confirmText, setConfirmText] = useState("");
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  useEffect(() => {
    api
      .listSessions()
      .then(setSessions)
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load sessions"));
  }, []);

  if (!user) return null;

  async function saveName(e: React.FormEvent) {
    e.preventDefault();
    setSavingName(true);
    setError(null);
    try {
      setUser(await api.updateMe(name));
      setNameSaved(true);
      window.setTimeout(() => setNameSaved(false), 2000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't save");
    } finally {
      setSavingName(false);
    }
  }

  async function revoke(id: string) {
    setRevoking(id);
    try {
      await api.revokeSession(id);
      setSessions((s) => s?.filter((x) => x.id !== id) ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't sign that device out");
    } finally {
      setRevoking(null);
    }
  }

  async function deleteAccount(e: React.FormEvent) {
    e.preventDefault();
    setDeleteBusy(true);
    setDeleteError(null);
    try {
      await api.deleteAccount(confirmText.trim());
      // The session is already gone server-side; this clears it here too.
      await signOut();
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : "Couldn't delete the account");
      setDeleteBusy(false);
    }
  }

  const methods = [
    { id: "password", label: "Email and password", icon: <IconMail size={18} />, on: user.has_password },
    // Google and GitHub sign-in are no longer offered; an account that linked one earlier still sees it.
    ...(user.linked_providers.includes("google") ? [{ id: "google", label: "Google", icon: <GoogleMark size={18} />, on: true }] : []),
    ...(user.linked_providers.includes("github") ? [{ id: "github", label: "GitHub", icon: <GitHubMark size={18} />, on: true }] : []),
    // Privy is listed once used: whether this server offers it is on the sign-in page.
    ...(user.linked_providers.includes("privy")
      ? [{ id: "privy", label: "Privy (wallet or email code)", icon: <IconWallet size={18} />, on: true }]
      : []),
  ];
  const wallets = user.wallets ?? [];

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title="Account" description="Who you are to Algebra, how you sign in, and where you're signed in." />
      {error && (
        <div className="mt-6">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}

      <section className="mt-10 rounded-2xl border border-border bg-surface p-6">
        <div className="flex items-center gap-4">
          <Avatar user={user} size={52} />
          <div className="min-w-0">
            <p className="truncate text-[0.95rem] font-medium text-foreground">{user.name || "No name yet"}</p>
            <p className="flex items-center gap-1.5 truncate text-sm text-muted">
              {user.email || "Signed in with a wallet"}
              {user.email_verified && (
                <span className="inline-flex items-center gap-1 rounded-full bg-primary-tint px-2 py-0.5 text-[0.7rem] font-medium text-primary">
                  <IconCheck size={10} strokeWidth={3} /> Verified
                </span>
              )}
            </p>
          </div>
        </div>
        <form onSubmit={saveName} className="mt-6 flex flex-col gap-3 sm:flex-row sm:items-end">
          <label className="block flex-1">
            <span className="text-sm font-medium text-foreground">Display name</span>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
              className="mt-1.5 h-11 w-full rounded-xl border border-border-strong bg-background px-3.5 text-[0.95rem] text-foreground focus-visible:border-primary focus-visible:outline-none"
            />
          </label>
          <button
            type="submit"
            disabled={savingName || name.trim() === (user.name ?? "")}
            className="inline-flex h-11 items-center justify-center gap-2 rounded-xl bg-primary px-4 text-sm font-medium text-primary-tint disabled:opacity-40"
          >
            {savingName ? <Spinner size={14} /> : nameSaved ? <IconCheck size={15} /> : null}
            {nameSaved ? "Saved" : "Save"}
          </button>
        </form>
      </section>

      <section className="mt-8">
        <h2 className="text-[0.95rem] font-semibold text-foreground">Sign-in methods</h2>
        <ul className="mt-3 divide-y divide-border overflow-hidden rounded-2xl border border-border bg-surface">
          {methods.map((m) => (
            <li key={m.id} className="flex items-center gap-3.5 px-5 py-3.5">
              <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-background text-foreground">{m.icon}</span>
              <span className="flex-1 text-sm text-foreground">{m.label}</span>
              {m.on ? (
                <span className="text-xs font-medium text-primary">Connected</span>
              ) : m.id === "password" ? (
                <a href="/forgot-password" className="text-xs font-medium text-muted hover:text-foreground">
                  Set a password
                </a>
              ) : (
                <span className="text-xs text-muted">Sign in with {m.label} once to link it</span>
              )}
            </li>
          ))}
        </ul>
      </section>

      {wallets.length > 0 && (
        <section className="mt-8">
          <h2 className="text-[0.95rem] font-semibold text-foreground">Wallets</h2>
          <p className="mt-1 text-sm text-muted">
            Solana wallets you signed in with. Algebra knows their addresses only; it never holds their keys.
          </p>
          <ul className="mt-3 divide-y divide-border overflow-hidden rounded-2xl border border-border bg-surface">
            {wallets.map((w) => (
              <li key={w.address} className="flex items-center gap-3.5 px-5 py-3.5">
                <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-background text-foreground">
                  <IconWallet size={18} />
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-sm text-foreground" title={w.address}>
                    {w.address}
                  </p>
                  <p className="text-xs text-muted">
                    {w.kind === "embedded" ? "Privy wallet, made for you at sign-in; Privy secures its key for you" : "Your own wallet"}
                  </p>
                </div>
                <a
                  href={`https://explorer.solana.com/address/${w.address}`}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex shrink-0 items-center gap-1 text-xs font-medium text-muted hover:text-foreground"
                >
                  Explorer <IconExternal size={12} />
                </a>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="mt-8">
        <h2 className="text-[0.95rem] font-semibold text-foreground">Where you&apos;re signed in</h2>
        <p className="mt-1 text-sm text-muted">Signing a device out also revokes the agent it was using.</p>
        <ul className="mt-3 divide-y divide-border overflow-hidden rounded-2xl border border-border bg-surface">
          {sessions === null && (
            <li className="p-5">
              <Skeleton className="h-10" />
            </li>
          )}
          {sessions?.map((s) => (
            <li key={s.id} className="flex items-center gap-4 px-5 py-3.5">
              <div className="min-w-0 flex-1">
                <p className="text-sm text-foreground">
                  {describeAgent(s.user_agent)}
                  {s.current && <span className="ml-2 rounded-full bg-primary-tint px-2 py-0.5 text-[0.7rem] font-medium text-primary">This device</span>}
                </p>
                <p className="text-xs text-muted">
                  {s.ip || "Unknown IP"} · active {timeAgo(s.last_seen_at)} · signed in {timeAgo(s.created_at)}
                </p>
              </div>
              {s.current ? (
                <button type="button" onClick={signOut} className="text-sm font-medium text-danger hover:underline">
                  Sign out
                </button>
              ) : (
                <button
                  type="button"
                  disabled={revoking === s.id}
                  onClick={() => revoke(s.id)}
                  className="inline-flex items-center gap-1.5 text-sm font-medium text-muted hover:text-danger disabled:opacity-50"
                >
                  {revoking === s.id && <Spinner size={13} />} Sign out
                </button>
              )}
            </li>
          ))}
        </ul>
      </section>

      <section className="mt-8">
        <h2 className="text-[0.95rem] font-semibold text-foreground">Your data</h2>
        <div className="mt-3 divide-y divide-border overflow-hidden rounded-2xl border border-border bg-surface">
          <div className="flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-center">
            <div className="flex-1">
              <p className="text-sm text-foreground">Download your data</p>
              <p className="mt-0.5 text-xs text-muted">Your account, Spend Passes, the requests your agents made and devices, as one JSON file.</p>
            </div>
            <a
              href={api.DATA_EXPORT_URL}
              download
              className="inline-flex h-9 shrink-0 items-center justify-center rounded-xl border border-border-strong px-3.5 text-sm font-medium text-foreground hover:bg-background"
            >
              Download
            </a>
          </div>
          <div className="px-5 py-4">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
              <div className="flex-1">
                <p className="text-sm text-foreground">Delete account</p>
                <p className="mt-0.5 text-xs text-muted">
                  Erases your name, email and sign-ins, revokes every agent and Spend Pass, and deletes the answers kept for your requests and what they asked. The requests and their receipts stay on record, tied to nobody. This can&apos;t be undone.
                </p>
              </div>
              {!deleting && (
                <button
                  type="button"
                  onClick={() => setDeleting(true)}
                  className="inline-flex h-9 shrink-0 items-center justify-center rounded-xl border border-danger/40 px-3.5 text-sm font-medium text-danger hover:bg-danger-tint"
                >
                  Delete account
                </button>
              )}
            </div>
            {deleting && (
              <form onSubmit={deleteAccount} className="mt-4 rounded-xl bg-danger-tint/60 p-4">
                <label className="block">
                  <span className="text-sm text-foreground">
                    Type <span className="font-mono font-semibold">DELETE</span> to confirm
                  </span>
                  <input
                    value={confirmText}
                    onChange={(e) => setConfirmText(e.target.value)}
                    autoComplete="off"
                    autoFocus
                    className="mt-1.5 h-10 w-full rounded-xl border border-border-strong bg-background px-3 font-mono text-sm text-foreground focus-visible:border-danger focus-visible:outline-none sm:max-w-xs"
                  />
                </label>
                {deleteError && <p className="mt-2 text-sm text-danger">{deleteError}</p>}
                <div className="mt-3 flex gap-2.5">
                  <button
                    type="submit"
                    disabled={confirmText.trim() !== "DELETE" || deleteBusy}
                    className="inline-flex h-9 items-center gap-2 rounded-xl bg-danger px-3.5 text-sm font-medium text-danger-tint disabled:opacity-40"
                  >
                    {deleteBusy && <Spinner size={13} />} Delete my account
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setDeleting(false);
                      setConfirmText("");
                      setDeleteError(null);
                    }}
                    className="inline-flex h-9 items-center rounded-xl px-3.5 text-sm font-medium text-muted hover:text-foreground"
                  >
                    Keep it
                  </button>
                </div>
              </form>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}
