"use client";

// Web tools, in Account: the person's own API keys for TinyFish, Exa, Firecrawl
// and Tavily. A saved key makes that vendor's tools one more seller their agents
// can choose. The vendor bills the person's own account; Algebra keeps the key
// sealed, shows only its last four characters and never sends it anywhere but
// that vendor. The Spend Pass's budget and rules apply to these calls as to any.

import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { ToolKey } from "@/lib/types";
import { IconCheck, IconLock, Spinner } from "@/components/icons";
import { ErrorNote, Skeleton } from "@/components/console/ui";

export function WebTools() {
  const [tools, setTools] = useState<ToolKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api
      .listToolKeys()
      .then((r) => live && setTools(r.tools))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Couldn't load your web tools."));
    return () => {
      live = false;
    };
  }, []);

  function close() {
    setOpen(null);
    setKey("");
  }

  async function save(e: React.FormEvent, vendor: ToolKey) {
    e.preventDefault();
    setBusy(vendor.id);
    setError(null);
    try {
      setTools((await api.saveToolKey(vendor.id, key.trim())).tools);
      close();
      setDone(vendor.id);
      window.setTimeout(() => setDone((d) => (d === vendor.id ? null : d)), 2500);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't save that key.");
    } finally {
      setBusy(null);
    }
  }

  async function remove(vendor: ToolKey) {
    setBusy(vendor.id);
    setError(null);
    try {
      setTools((await api.deleteToolKey(vendor.id)).tools);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't remove that key.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <section id="web-tools" className="mt-8 scroll-mt-6">
      <h2 className="text-[0.95rem] font-semibold text-foreground">Web tools</h2>
      <p className="mt-1 text-sm text-muted">
        Add your own API key and your agents can search and read the web with that vendor. The vendor bills your account, and the agent&apos;s Spend Pass still sets the budget and the rules.
      </p>
      {error && (
        <div className="mt-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}
      <ul className="mt-3 divide-y divide-border overflow-hidden rounded-2xl border border-border bg-surface">
        {tools === null && !error && (
          <li className="p-5">
            <Skeleton className="h-16" />
          </li>
        )}
        {tools?.map((v) => (
          <li key={v.id} className="px-5 py-3.5">
            <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2">
              <div className="min-w-0 flex-1">
                <p className="text-sm text-foreground">{v.name}</p>
                <p className="text-xs text-muted">{v.tools.join(" · ")}</p>
              </div>
              {v.saved && open !== v.id && (
                <span className="inline-flex items-center gap-1.5 text-xs font-medium text-primary">
                  {done === v.id ? <IconCheck size={12} strokeWidth={3} /> : <IconLock size={12} />}
                  {done === v.id ? "Saved" : v.hint ? `Key saved · ends ${v.hint}` : "Key saved"}
                </span>
              )}
              {open !== v.id && (
                <button type="button" onClick={() => setOpen(v.id)} className="text-xs font-medium text-muted hover:text-foreground">
                  {v.saved ? "Replace" : "Add key"}
                </button>
              )}
              {v.saved && open !== v.id && (
                <button
                  type="button"
                  disabled={busy !== null}
                  onClick={() => remove(v)}
                  className="inline-flex items-center gap-1.5 text-xs font-medium text-muted hover:text-danger disabled:opacity-50"
                >
                  {busy === v.id && <Spinner size={12} />} Remove
                </button>
              )}
            </div>
            {open === v.id && (
              <form onSubmit={(e) => save(e, v)} className="mt-3 flex flex-col gap-2 sm:flex-row sm:items-end">
                <label className="block flex-1">
                  <span className="text-xs text-muted">
                    {v.name} API key ·{" "}
                    <a href={v.site} target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-foreground">
                      get one
                    </a>
                  </span>
                  <input
                    type="password"
                    name={`${v.id}-api-key`}
                    value={key}
                    onChange={(e) => setKey(e.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                    autoFocus
                    required
                    maxLength={256}
                    placeholder="Paste your key"
                    className="mt-1.5 h-10 w-full rounded-xl border border-border-strong bg-background px-3.5 font-mono text-sm text-foreground placeholder:font-sans placeholder:text-muted focus-visible:border-primary focus-visible:outline-none"
                  />
                </label>
                <div className="flex gap-2">
                  <button
                    type="submit"
                    disabled={busy !== null || key.trim().length < 8}
                    className="inline-flex h-10 items-center gap-2 rounded-xl bg-primary px-4 text-sm font-medium text-primary-tint disabled:opacity-40"
                  >
                    {busy === v.id && <Spinner size={13} />} Save
                  </button>
                  <button type="button" onClick={close} className="h-10 px-2 text-sm font-medium text-muted hover:text-foreground">
                    Cancel
                  </button>
                </div>
              </form>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
