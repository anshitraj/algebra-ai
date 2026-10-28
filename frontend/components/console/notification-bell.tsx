"use client";

// The bell in the top bar: every transaction the account made, newest first,
// each with a link to look it up on chain. See lib/notifications.tsx for what
// feeds it. Opening the list marks what is in it as seen.

import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { solscanTx } from "@/lib/explorer";
import { useNotifications, type AppNotification, type TxKind } from "@/lib/notifications";
import { IconBell, IconExternal, IconReceipt } from "@/components/icons";
import { timeAgo } from "./ui";

const ICONS: Record<TxKind, (p: { size?: number }) => React.ReactNode> = {
  payment: (p) => <IconReceipt {...p} />,
};

const where = (n: AppNotification) => (n.network === "solana" ? "Mainnet" : n.network === "solana-devnet" ? "Testnet" : "");

export function NotificationBell() {
  const { items, unread, seenAt, markAllRead, clear } = useNotifications();
  const [open, setOpen] = useState(false);
  // What was new when the list was opened: marked as new in it, though the bell has already counted it as seen.
  const [fresh, setFresh] = useState<Set<string>>(() => new Set());
  const [announce, setAnnounce] = useState("");
  const box = useRef<HTMLDivElement>(null);
  const lastUnread = useRef(0);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  // Say a new transaction out loud for screen readers: the badge alone is silent.
  useEffect(() => {
    if (unread > lastUnread.current && items[0]) {
      setAnnounce(`${items[0].title}${items[0].detail ? `: ${items[0].detail}` : ""}`);
    }
    lastUnread.current = unread;
  }, [unread, items]);

  function toggle() {
    if (open) {
      setOpen(false);
      return;
    }
    setFresh(new Set(items.filter((n) => seenAt !== null && n.at > seenAt).map((n) => n.id)));
    setOpen(true);
    markAllRead();
  }

  return (
    <div ref={box} className="relative">
      {/* First, so the toolbar's rule that hides a last span (the switch's caption) never reaches it. */}
      <span className="sr-only" role="status" aria-live="polite">
        {announce}
      </span>
      <button
        type="button"
        onClick={toggle}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={unread > 0 ? `Notifications, ${unread} new` : "Notifications"}
        title="Notifications"
        className="relative inline-flex h-9 w-9 items-center justify-center rounded-xl text-muted transition-colors hover:bg-primary-tint/60 hover:text-foreground"
      >
        <IconBell size={18} />
        {unread > 0 && (
          <span className="absolute -top-0.5 -right-0.5 min-w-4 rounded-full bg-accent px-1 text-center font-mono text-[0.625rem] leading-4 font-medium text-accent-tint tabular-nums">
            {unread > 9 ? "9+" : unread}
          </span>
        )}
      </button>

      <AnimatePresence>
        {open && (
          <motion.div
            role="dialog"
            aria-label="Notifications"
            initial={{ opacity: 0, y: 6, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 4, scale: 0.98 }}
            transition={{ duration: 0.16 }}
            className="fixed inset-x-3 top-16 z-50 overflow-hidden rounded-xl border border-border bg-surface shadow-[0_16px_40px_-16px_rgba(11,16,32,0.35)] sm:absolute sm:inset-x-auto sm:top-full sm:right-0 sm:mt-2 sm:w-[23rem]"
          >
            <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
              <h2 className="text-sm font-semibold text-foreground">Notifications</h2>
              {items.length > 0 && (
                <button type="button" onClick={clear} className="text-xs font-medium text-muted hover:text-foreground">
                  Clear
                </button>
              )}
            </div>
            {items.length === 0 ? (
              <p className="px-4 py-8 text-center text-sm leading-relaxed text-muted">
                Nothing yet. Each transaction you make shows up here with a link to it: making a vault, adding USDC, a payment.
              </p>
            ) : (
              <ul className="max-h-[min(26rem,70dvh)] divide-y divide-border overflow-y-auto">
                {items.map((n) => (
                  <li key={n.id} className="flex gap-3 px-4 py-3">
                    <span className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-primary-tint text-primary">{ICONS[n.kind]({ size: 16 })}</span>
                    <div className="min-w-0 flex-1">
                      <p className="flex items-center gap-2 text-sm font-medium text-foreground">
                        <span className="min-w-0 truncate">{n.title}</span>
                        {fresh.has(n.id) && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent" aria-label="New" />}
                      </p>
                      {n.detail && <p className="mt-0.5 text-xs leading-relaxed text-muted">{n.detail}</p>}
                      <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[0.6875rem] text-muted">
                        <span>
                          {timeAgo(new Date(n.at).toISOString())}
                          {where(n) ? ` · ${where(n)}` : ""}
                        </span>
                        {n.signature && (
                          <a
                            href={solscanTx(n.signature, n.network)}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="inline-flex items-center gap-1 font-medium text-primary hover:underline"
                          >
                            View transaction <IconExternal size={11} />
                          </a>
                        )}
                      </p>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
