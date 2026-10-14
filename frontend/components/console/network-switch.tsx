"use client";

import { useEffect, useId, useRef, useState } from "react";
import { NETWORKS, useNetwork, type Network } from "@/lib/network";
import { IconCheck, IconChevronDown } from "@/components/icons";

const DOTS: Record<Network, string> = { sandbox: "bg-muted", "solana-devnet": "bg-accent", solana: "bg-primary" };

// What each place spends, in two words, so the choice is clear before it is made.
const MONEY: Record<Network, { tag: string; cls: string }> = {
  sandbox: { tag: "play money", cls: "bg-primary-tint text-muted" },
  "solana-devnet": { tag: "test money", cls: "bg-accent-tint text-accent" },
  solana: { tag: "real money", cls: "bg-danger-tint text-danger" },
};

/**
 * Where the console works, for the whole page: Demo (play money, no wallet),
 * Devnet (free test money) or Mainnet (real USDC). One button says where you
 * are; clicking it opens the places to switch to. A demo account only has the
 * demo. The wallet is the wallet menu's.
 */
export function NetworkSwitch({ compact = false }: { compact?: boolean }) {
  const { network, demo, setNetwork, practiceAvailable } = useNetwork();
  const [open, setOpen] = useState(false);
  const holder = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const items = useRef<(HTMLButtonElement | null)[]>([]);
  const menuId = useId();
  const options = NETWORKS.filter((n) => n.id !== "sandbox" || practiceAvailable);
  const current = NETWORKS.find((n) => n.id === network) ?? NETWORKS[0];

  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      if (!holder.current?.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        trigger.current?.focus();
      }
    };
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", key);
    // The place you are in has the keyboard, so Enter on it closes the menu and arrows move from it.
    items.current[Math.max(0, options.findIndex((n) => n.id === network))]?.focus();
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", key);
    };
    // Only when it opens: not each time the choice or the list changes under an open menu.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  if (demo) {
    return (
      <div className="flex items-center gap-3">
        <span
          title="Demo accounts work in demo mode: simulated providers and play USDC, nothing on a chain"
          className="inline-flex items-center gap-1.5 rounded-xl bg-accent-tint px-3 py-1.5 text-xs font-medium text-accent"
        >
          <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-accent" />
          Demo
        </span>
        {!compact && <span className="hidden text-xs text-muted md:inline">Play USDC: nothing real moves</span>}
      </div>
    );
  }

  function pick(id: Network) {
    setNetwork(id);
    setOpen(false);
    trigger.current?.focus();
  }

  function move(e: React.KeyboardEvent, from: number) {
    const last = options.length - 1;
    const to = e.key === "ArrowDown" ? (from + 1) % options.length : e.key === "ArrowUp" ? (from - 1 + options.length) % options.length : e.key === "Home" ? 0 : e.key === "End" ? last : -1;
    if (to < 0) return;
    e.preventDefault();
    items.current[to]?.focus();
  }

  return (
    <div ref={holder} className="relative">
      <button
        ref={trigger}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen((o) => !o)}
        title={`Working in ${current.label}. Click to switch.`}
        className={`inline-flex h-9 items-center gap-2 rounded-xl border border-border bg-surface text-xs font-medium text-foreground transition-colors hover:bg-primary-tint/60 ${compact ? "px-2.5" : "px-3"}`}
      >
        <span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${DOTS[current.id]}`} />
        {current.label}
        <IconChevronDown size={13} className={`text-muted transition-transform ${open ? "rotate-180" : ""}`} />
      </button>

      {open && (
        <div
          id={menuId}
          role="menu"
          aria-label="Switch where to work"
          // On a phone it spans the screen under the header (the bell and the wallet sit beside the button, so
          // anchoring to the button could run off the left edge); from sm up it hangs from the button.
          className="fixed inset-x-3 top-[3.75rem] z-50 rounded-2xl border border-border bg-surface p-1.5 text-left shadow-[0_18px_44px_-18px_rgb(0_0_0/0.35)] sm:absolute sm:inset-x-auto sm:top-full sm:right-0 sm:mt-2 sm:w-[19rem]"
        >
          <p className="px-3 pt-2 pb-1.5 text-[0.6875rem] font-medium tracking-wider text-muted uppercase">Switch to</p>
          {options.map((n, i) => {
            const on = n.id === network;
            return (
              <button
                key={n.id}
                ref={(el) => {
                  items.current[i] = el;
                }}
                type="button"
                role="menuitemradio"
                aria-checked={on}
                onClick={() => pick(n.id)}
                onKeyDown={(e) => move(e, i)}
                className={`flex w-full items-start gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-primary-tint/60 focus-visible:bg-primary-tint/60 focus-visible:outline-none ${on ? "bg-primary-tint/40" : ""}`}
              >
                <span aria-hidden="true" className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${DOTS[n.id]}`} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2 text-sm font-medium text-foreground">
                    {n.label}
                    <span className={`rounded-full px-1.5 py-px text-[0.625rem] font-medium ${MONEY[n.id].cls}`}>{MONEY[n.id].tag}</span>
                  </span>
                  <span className="mt-0.5 block text-xs leading-snug text-muted">{n.hint}</span>
                </span>
                {on && <IconCheck size={15} className="mt-0.5 shrink-0 text-primary" />}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
