"use client";

// The bell's list: every transaction this person's account makes, each with the
// link to look it up on chain. Two sources, merged:
//  - what this browser did (a vault made, USDC added, test money received, SOL
//    swapped): the screen that sent the transaction calls notifyTx;
//  - what Algebra paid for them (the settlement of each paid call), read from
//    their executions, so a payment an agent made over MCP or REST shows up
//    here too, from any device.
// The list is kept in this browser, per account.

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import * as api from "./api-client";
import { useConsoleData } from "@/components/console/console-data";
import { isOnChain } from "./explorer";
import { formatUSDC } from "./money";
import type { EconIntent } from "./types";

export type TxKind = "vault" | "deposit" | "withdraw" | "freeze" | "unfreeze" | "revoke" | "close" | "faucet" | "swap" | "payment";

export type AppNotification = {
  id: string;
  kind: TxKind;
  title: string;
  detail?: string;
  /** The transaction's signature: the link to the chain is built from it. */
  signature?: string;
  network?: "solana" | "solana-devnet";
  /** Milliseconds since the epoch. */
  at: number;
};

/** What a screen reports; the list gives it an id and a time. */
export type NewNotification = Omit<AppNotification, "id" | "at"> & { id?: string };

const EVENT = "algebra:notify";
// What is kept in the browser, what the bell shows at most, and how many paid calls are remembered as looked at.
const MAX_KEPT = 40;
const MAX_SHOWN = 30;
const MAX_CHECKED = 200;
// Paid calls looked up at once: a page of history, not the whole of it.
const BATCH = 8;

/** Reports a transaction that just finished. The bell, mounted in the console, adds it to the list. */
export function notifyTx(n: NewNotification) {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent<NewNotification>(EVENT, { detail: n }));
}

type Saved = {
  items: AppNotification[];
  /** The time of the newest item when the list was last opened; null until the first look at the account's executions. */
  seenAt: number | null;
  /** Paid calls already looked at (whether or not they had a transaction to show), so each is read once. */
  checked: string[];
};

const EMPTY: Saved = { items: [], seenAt: null, checked: [] };
const keyFor = (userId: string) => `algebra:notifications:v1:${userId}`;

function isNotification(v: unknown): v is AppNotification {
  if (typeof v !== "object" || v === null) return false;
  const n = v as Record<string, unknown>;
  return typeof n.id === "string" && typeof n.kind === "string" && typeof n.title === "string" && typeof n.at === "number";
}

function read(userId: string): Saved {
  try {
    const raw = window.localStorage.getItem(keyFor(userId));
    if (!raw) return EMPTY;
    const v = JSON.parse(raw) as { items?: unknown; seenAt?: unknown; checked?: unknown };
    return {
      items: Array.isArray(v.items) ? v.items.filter(isNotification).slice(0, MAX_KEPT) : [],
      seenAt: typeof v.seenAt === "number" ? v.seenAt : null,
      checked: Array.isArray(v.checked) ? v.checked.filter((x): x is string => typeof x === "string").slice(0, MAX_CHECKED) : [],
    };
  } catch {
    return EMPTY;
  }
}

const WHAT: Record<string, string> = {
  "token.price": "a token price",
  "solana.token-risk": "a token risk check",
  "wallet.balances": "a wallet's balances",
  "wallet.risk": "a wallet screen",
  "web.search": "a web search",
  "web.scrape": "a web page",
  "news.search": "a news search",
  "llm.chat": "an AI answer",
  "image.generate": "an image",
  "weather.forecast": "a weather forecast",
  "text.translate": "a translation",
  "ip.geolocate": "an IP lookup",
  "domain.lookup": "a domain lookup",
  "email.verify": "an email check",
};

/**
 * A paid call that settled on a chain, as a notification, or null. A simulated
 * (practice) payment has no transaction to look at, so it has none.
 */
function paymentFrom(i: EconIntent): AppNotification | null {
  if (i.state !== "COMMITTED") return null;
  const r = [...(i.reservations ?? [])].reverse().find((x) => x.evidence?.transaction && !x.evidence.test && isOnChain(x.evidence.network));
  if (!r?.evidence.transaction) return null;
  return {
    id: `payment:${i.id}`,
    kind: "payment",
    title: "Payment sent",
    detail: `${formatUSDC(i.committed_minor)} for ${WHAT[i.capability] ?? i.capability.replace(/[._-]+/g, " ")}`,
    signature: r.evidence.transaction,
    network: r.evidence.network === "solana" ? "solana" : "solana-devnet",
    at: Date.parse(r.finished_at ?? i.updated_at) || Date.parse(i.updated_at) || Date.parse(i.created_at) || 0,
  };
}

const newest = (items: AppNotification[], floor = 0) => items.reduce((m, n) => Math.max(m, n.at), floor);

type NotificationsState = {
  /** Newest first. */
  items: AppNotification[];
  /** How many are newer than the last time the person opened the list. */
  unread: number;
  /** When the list was last opened (the time of the newest item then); null until the first read. */
  seenAt: number | null;
  markAllRead: () => void;
  clear: () => void;
};

const Ctx = createContext<NotificationsState>({ items: [], unread: 0, seenAt: null, markAllRead: () => {}, clear: () => {} });

/** Inside ConsoleDataProvider: the paid calls come from its executions list. */
export function NotificationsProvider({ userId, children }: { userId: string; children: React.ReactNode }) {
  const { intents } = useConsoleData();
  const [saved, setSaved] = useState<Saved>(EMPTY);
  const [loaded, setLoaded] = useState(false);
  const inFlight = useRef<Set<string>>(new Set());

  // Restore this account's list after mount (storage isn't there during SSR), and follow other tabs.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- restoring from localStorage after mount
    setSaved(read(userId));
    setLoaded(true);
    const onStorage = (e: StorageEvent) => {
      if (e.key === keyFor(userId)) setSaved(read(userId));
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [userId]);

  // A transaction finished somewhere on the page.
  useEffect(() => {
    const onNotify = (e: Event) => {
      const n = (e as CustomEvent<NewNotification>).detail;
      if (!n || typeof n.title !== "string") return;
      setSaved((prev) => {
        const id = n.id ?? `${n.kind}:${n.signature ?? Date.now()}`;
        // Newer than anything already read, even when this computer's clock is behind the server's.
        const at = Math.max(Date.now(), (prev.seenAt ?? 0) + 1);
        const item: AppNotification = { ...n, id, at };
        return { ...prev, items: [item, ...prev.items.filter((x) => x.id !== id)].slice(0, MAX_KEPT) };
      });
    };
    window.addEventListener(EVENT, onNotify);
    return () => window.removeEventListener(EVENT, onNotify);
  }, []);

  useEffect(() => {
    if (!loaded) return;
    try {
      window.localStorage.setItem(keyFor(userId), JSON.stringify(saved));
    } catch {
      // storage blocked or full: the list lives for this page
    }
  }, [saved, loaded, userId]);

  // The paid calls: the executions list says which committed; each one's own record has its settlement.
  // Each is read once. The first look at an account on this browser sets the baseline: what is already
  // there is history, not news, so only what comes after counts as new.
  const { checked, seenAt: baseline } = saved;
  useEffect(() => {
    if (!loaded || intents === null) return;
    const todo = intents
      .filter((i) => i.state === "COMMITTED" && i.committed_minor > 0 && !checked.includes(i.id) && !inFlight.current.has(i.id))
      .slice(0, BATCH);
    if (todo.length === 0) {
      // Nothing to look up: if this is the first look, this is the baseline.
      if (baseline === null) queueMicrotask(() => setSaved((s) => (s.seenAt === null ? { ...s, seenAt: newest(s.items) } : s)));
      return;
    }
    for (const i of todo) inFlight.current.add(i.id);
    void Promise.all(
      todo.map(async (i) => {
        try {
          return { id: i.id, done: true, item: paymentFrom(await api.getMyEconomicIntent(i.id)) };
        } catch (e) {
          // A call that can't be read (gone, or not ours) is skipped for good; a network failure is tried again.
          return { id: i.id, done: e instanceof api.ApiError, item: null };
        } finally {
          inFlight.current.delete(i.id);
        }
      }),
    ).then((rows) => {
      setSaved((s) => {
        const added = rows.flatMap((r) => (r.item ? [r.item] : []));
        const items = [...added, ...s.items.filter((x) => !added.some((a) => a.id === x.id))].slice(0, MAX_KEPT);
        const done = rows.filter((r) => r.done).map((r) => r.id);
        return { items, checked: [...done, ...s.checked.filter((id) => !done.includes(id))].slice(0, MAX_CHECKED), seenAt: s.seenAt ?? newest(items) };
      });
    });
  }, [intents, loaded, checked, baseline]);

  const items = useMemo(() => [...saved.items].sort((a, b) => b.at - a.at).slice(0, MAX_SHOWN), [saved.items]);

  const markAllRead = useCallback(() => {
    setSaved((s) => {
      const to = newest(s.items, s.seenAt ?? 0);
      return to === s.seenAt ? s : { ...s, seenAt: to };
    });
  }, []);

  const clear = useCallback(() => {
    // The paid calls stay in `checked`, so clearing the list doesn't bring them back.
    setSaved((s) => ({ ...s, items: [], seenAt: newest(s.items, s.seenAt ?? 0) }));
  }, []);

  const seenAt = saved.seenAt;
  const unread = seenAt === null ? 0 : items.filter((n) => n.at > seenAt).length;

  return <Ctx.Provider value={{ items, unread, seenAt, markAllRead, clear }}>{children}</Ctx.Provider>;
}

export function useNotifications() {
  return useContext(Ctx);
}
