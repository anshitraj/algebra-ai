"use client";

import { useEffect, useState } from "react";
import { motion } from "motion/react";
import * as api from "@/lib/api-client";
import type { Plugin, PluginPurpose, PluginTrust } from "@/lib/types";
import { ErrorNote, PageHeader, Skeleton } from "@/components/console/ui";
import { StoreLogo } from "@/components/store-logo";
import { IconCheck, IconShield, IconTag, Spinner } from "@/components/icons";

const GROUPS: { purpose: PluginPurpose; title: string; hint: string }[] = [
  { purpose: "prices", title: "Prices", hint: "Where your agent finds what things cost right now." },
  { purpose: "deals", title: "Deals & offers", hint: "Savings the store or your bank stands behind." },
  {
    purpose: "community",
    title: "Community deals",
    hint: "What people are posting. Often the best prices, but unverified — codes can expire within hours. Shown as tips, never counted in a price.",
  },
];

const TRUST: Record<PluginTrust, { label: string; cls: string }> = {
  store: { label: "From the store", cls: "bg-primary-tint text-primary" },
  curated: { label: "Run by Algebra", cls: "bg-background text-foreground/80 border border-border" },
  community: { label: "Community · unverified", cls: "bg-accent-tint text-accent" },
};

export default function PluginsPage() {
  const [plugins, setPlugins] = useState<Plugin[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .listPlugins()
      .then(setPlugins)
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load plugins"));
  }, []);

  function replace(p: Plugin) {
    setPlugins((list) => list?.map((x) => (x.id === p.id ? p : x)) ?? null);
  }

  return (
    <div className="mx-auto max-w-4xl">
      <PageHeader
        title="Plugins"
        description="Choose where your agent looks for prices and deals. Plugins only suggest — they can't buy, approve, or see who you are."
      />
      {error && (
        <div className="mt-6">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}

      {plugins === null && !error && (
        <div className="mt-10 grid gap-4 md:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-44 rounded-2xl" />
          ))}
        </div>
      )}

      {plugins &&
        GROUPS.map((g) => {
          const items = plugins.filter((p) => p.purpose === g.purpose);
          if (items.length === 0) return null;
          return (
            <section key={g.purpose} className="mt-10">
              <h2 className="text-[0.95rem] font-semibold text-foreground">{g.title}</h2>
              <p className="mt-1 max-w-2xl text-sm text-muted">{g.hint}</p>
              <div className="mt-4 grid gap-4 md:grid-cols-2">
                {items.map((p, i) => (
                  <PluginCard key={p.id} plugin={p} index={i} onChange={replace} onError={setError} />
                ))}
              </div>
            </section>
          );
        })}

      {plugins && (
        <section className="mt-12 rounded-2xl border border-dashed border-border-strong p-6">
          <div className="flex items-start gap-3">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-primary-tint text-primary">
              <IconShield size={18} />
            </span>
            <div>
              <p className="text-[0.95rem] font-medium text-foreground">Build your own plugin — coming soon</p>
              <p className="mt-1 text-sm leading-relaxed text-muted">
                Any MCP server that returns offers in Algebra&apos;s format will plug in here, with the same rules: it only sees the product being
                searched, its results are labelled with its name, and it can never check out.
              </p>
            </div>
          </div>
        </section>
      )}
    </div>
  );
}

function PluginCard({
  plugin: p,
  index,
  onChange,
  onError,
}: {
  plugin: Plugin;
  index: number;
  onChange: (p: Plugin) => void;
  onError: (e: string | null) => void;
}) {
  const [busy, setBusy] = useState(false);

  async function toggle() {
    if (p.core || busy) return;
    setBusy(true);
    onError(null);
    try {
      onChange(await api.setPlugin(p.id, !p.enabled));
    } catch (e) {
      onError(e instanceof Error ? e.message : "Couldn't change that plugin");
    } finally {
      setBusy(false);
    }
  }

  const trust = TRUST[p.trust];
  const on = p.enabled && p.ready;
  return (
    <motion.article
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, delay: index * 0.05, ease: [0.16, 1, 0.3, 1] }}
      className={`flex flex-col rounded-2xl border bg-surface p-5 transition-colors ${on ? "border-primary/40" : "border-border"}`}
    >
      <div className="flex items-start gap-3.5">
        {p.icon ? (
          <StoreLogo store={p.icon} size={40} />
        ) : (
          <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[10px] bg-primary-tint text-primary">
            <IconTag size={20} />
          </span>
        )}
        <div className="min-w-0 flex-1">
          <h3 className="text-[0.95rem] font-semibold text-foreground">{p.name}</h3>
          <span className={`mt-1 inline-block rounded-full px-2 py-0.5 text-[0.7rem] font-medium ${trust.cls}`}>{trust.label}</span>
        </div>
        {p.core ? (
          <span className="inline-flex shrink-0 items-center gap-1 text-xs font-medium text-muted">
            <IconCheck size={12} strokeWidth={2.6} /> Always on
          </span>
        ) : (
          <button
            type="button"
            role="switch"
            aria-checked={on}
            aria-label={`${on ? "Turn off" : "Turn on"} ${p.name}`}
            disabled={busy || !p.ready}
            onClick={toggle}
            className="relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-50"
            style={{ background: on ? "var(--color-primary)" : "var(--color-border-strong)" }}
          >
            <motion.span
              className="absolute top-0.5 left-0.5 h-5 w-5 rounded-full bg-surface shadow"
              animate={{ x: on ? 20 : 0 }}
              transition={{ type: "spring", stiffness: 500, damping: 32 }}
            />
          </button>
        )}
      </div>

      <p className="mt-3.5 text-sm leading-relaxed text-foreground/85">{p.summary}</p>
      <p className="mt-2 text-xs leading-relaxed text-muted">
        <span className="font-medium text-foreground/70">Sees:</span> {p.sees}
      </p>

      {!p.ready && <p className="mt-3 rounded-lg bg-background px-3 py-2 text-xs text-muted">Needs setup · {p.detail}</p>}

      {p.id === "reddit_deals" && p.ready && <SubredditPicker plugin={p} onChange={onChange} onError={onError} />}
    </motion.article>
  );
}

const DEFAULT_SUBS = ["dealsforindia", "IndianShoppers"];

function SubredditPicker({ plugin: p, onChange, onError }: { plugin: Plugin; onChange: (p: Plugin) => void; onError: (e: string | null) => void }) {
  const current = p.config.subreddits?.length ? p.config.subreddits : DEFAULT_SUBS;
  const [subs, setSubs] = useState<string[]>([current[0] ?? "", current[1] ?? ""]);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const dirty = subs.map((s) => s.trim()).join(",") !== [current[0] ?? "", current[1] ?? ""].join(",");

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    onError(null);
    try {
      const list = subs.map((s) => s.trim().replace(/^\/?r\//, "")).filter(Boolean);
      // Saving subreddits turns the plugin on: picking them is choosing to use it.
      onChange(await api.setPlugin(p.id, true, { subreddits: list }));
      setSaved(true);
      window.setTimeout(() => setSaved(false), 1800);
    } catch (err) {
      onError(err instanceof Error ? err.message : "Couldn't save those subreddits");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={save} className="mt-4 border-t border-border pt-4">
      <p className="text-xs font-medium text-foreground">Subreddits to read (up to 2)</p>
      <div className="mt-2 grid grid-cols-2 gap-2">
        {[0, 1].map((i) => (
          <label key={i} className="flex h-9 items-center rounded-lg border border-border-strong bg-background px-2.5 focus-within:border-primary">
            <span className="text-sm text-muted">r/</span>
            <input
              value={subs[i]}
              onChange={(e) => setSubs((s) => s.map((v, j) => (j === i ? e.target.value : v)))}
              placeholder={i === 0 ? "dealsforindia" : "optional"}
              maxLength={24}
              autoComplete="off"
              spellCheck={false}
              aria-label={`Subreddit ${i + 1}`}
              className="min-w-0 flex-1 bg-transparent text-sm text-foreground placeholder:text-muted/60 focus:outline-none"
            />
          </label>
        ))}
      </div>
      <div className="mt-2.5 flex items-center gap-3">
        <button
          type="submit"
          disabled={busy || !dirty}
          className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-primary px-3 text-xs font-medium text-primary-tint disabled:opacity-40"
        >
          {busy ? <Spinner size={12} /> : saved ? <IconCheck size={12} strokeWidth={2.6} /> : null}
          {saved ? "Saved" : "Save subreddits"}
        </button>
        <span className="text-xs text-muted">e.g. r/IndianFitness for protein and supplement deals.</span>
      </div>
    </form>
  );
}
