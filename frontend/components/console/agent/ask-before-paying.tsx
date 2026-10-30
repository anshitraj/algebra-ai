"use client";

import Link from "next/link";
import { useEffect, useId, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import * as api from "@/lib/api-client";
import type { SpendPass } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { approvalRule } from "@/lib/pass-rules";
import { IconCheck, IconChevronDown, IconLock, IconUnlock, Spinner } from "@/components/icons";

type Choice = "always" | "never";

const CHOICES: { id: Choice; label: string; hint: string }[] = [
  { id: "always", label: "Asks before every payment", hint: "You see the offers and press Pay. Nothing is paid before that." },
  { id: "never", label: "Pays within this pass", hint: "The recommended offer is paid on its own when the pass allows it. Its limits still apply." },
];

/**
 * What the chat does before it pays, and the place to change it. The setting is the pass's own approval rule, the one
 * the server enforces for every agent on the pass, so this is a second door to the one rule, not a second setting.
 * "Every payment" and "from an amount" show the offers and nothing is paid until the person presses Pay (the press is
 * recorded on the server as their approval); "never" pays the recommended offer as soon as the pass allows it. An
 * amount is set on the Spend firewall page, and choosing either option here replaces it.
 */
export function AskBeforePaying({
  pass,
  disabled,
  onChanged,
}: {
  pass: SpendPass;
  disabled?: boolean;
  /** The rule the server saved: the amount to ask from in minor units (0 asks every time), or undefined for never. */
  onChanged: (askAbove: number | undefined) => void;
}) {
  const rule = approvalRule(pass);
  const asks = rule.kind !== "never";
  const label = rule.kind === "always" ? "Asks before every payment" : rule.kind === "above" ? "Asks before paying" : "Pays within this pass";
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState<Choice | null>(null);
  const [error, setError] = useState<string | null>(null);
  const holder = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const items = useRef<(HTMLButtonElement | null)[]>([]);
  const menuId = useId();

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
    // The rule in force has the keyboard, so Enter on it just closes the menu.
    items.current[Math.max(0, CHOICES.findIndex((c) => c.id === rule.kind))]?.focus();
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", key);
    };
    // Only when it opens: not each time the rule changes under an open menu.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  async function choose(next: Choice) {
    if (saving) return;
    if (rule.kind === next) {
      setOpen(false);
      trigger.current?.focus();
      return;
    }
    setSaving(next);
    setError(null);
    try {
      // The rules are saved together, so the cap and the providers go back as they are.
      const saved = await api.setPassRules(pass.id, {
        approve_above_minor_units: next === "always" ? 0 : null,
        max_per_purchase_minor_units: pass.max_per_purchase_minor_units ?? null,
        allowed_merchants: pass.allowed_merchants ?? [],
      });
      onChanged(saved.approve_above_minor_units ?? undefined);
      setOpen(false);
      trigger.current?.focus();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't change the rule");
    } finally {
      setSaving(null);
    }
  }

  function move(e: React.KeyboardEvent, from: number) {
    const to = e.key === "ArrowDown" ? (from + 1) % CHOICES.length : e.key === "ArrowUp" ? (from - 1 + CHOICES.length) % CHOICES.length : -1;
    if (to < 0) return;
    e.preventDefault();
    items.current[to]?.focus();
  }

  return (
    // On a phone the menu spans the composer (the positioned ancestor); from sm up it hangs from the chip.
    <div ref={holder} className="min-w-0 max-w-full sm:relative">
      <button
        ref={trigger}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        disabled={disabled}
        onClick={() => {
          setError(null);
          setOpen((o) => !o);
        }}
        title={
          rule.kind === "never"
            ? "This pass never asks: the recommended offer is paid on its own when the pass allows it. Click to change."
            : rule.kind === "always"
              ? "This pass asks for your yes on every payment: pick an offer and press Pay. Click to change."
              : `This pass asks from ${formatUSDC(rule.minor)}. In the chat you pick an offer and press Pay. Click to change.`
        }
        className="inline-flex min-h-11 max-w-full items-center gap-1.5 rounded-lg px-2 text-xs font-medium text-foreground transition-colors hover:bg-primary-tint disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-transparent"
      >
        {asks ? <IconLock size={13} className="shrink-0 text-primary" /> : <IconUnlock size={13} className="shrink-0 text-accent" />}
        <span className="truncate">{label}</span>
        <IconChevronDown size={12} className={`shrink-0 text-muted transition-transform ${open ? "rotate-180" : ""}`} />
      </button>
      <AnimatePresence>
        {open && (
          <motion.div
            id={menuId}
            initial={{ opacity: 0, y: 6, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 4, scale: 0.98 }}
            transition={{ duration: 0.15 }}
            role="menu"
            aria-label="Before a payment"
            className="absolute inset-x-2 bottom-full z-30 mb-2 rounded-xl border border-border bg-surface p-1.5 shadow-[0_18px_44px_-18px_rgba(11,16,32,0.4)] sm:inset-x-auto sm:left-0 sm:w-80"
          >
            <p className="px-2.5 pt-1.5 pb-1 text-xs leading-snug break-words text-muted">
              This is the rule of “{pass.label}”, for every agent that pays under it.
            </p>
            {CHOICES.map((c, i) => (
              <button
                key={c.id}
                ref={(el) => {
                  items.current[i] = el;
                }}
                type="button"
                role="menuitemradio"
                aria-checked={rule.kind === c.id}
                disabled={saving !== null}
                onClick={() => choose(c.id)}
                onKeyDown={(e) => move(e, i)}
                className="flex w-full items-start gap-2 rounded-lg px-2.5 py-2 text-left transition-colors hover:bg-primary-tint focus-visible:bg-primary-tint focus-visible:outline-none disabled:cursor-wait disabled:opacity-60 disabled:hover:bg-transparent"
              >
                <span className="min-w-0 flex-1">
                  <span className="block text-sm text-foreground">{c.label}</span>
                  <span className="mt-0.5 block text-xs leading-snug text-muted">{c.hint}</span>
                </span>
                {saving === c.id ? (
                  <Spinner size={14} className="mt-0.5 shrink-0 text-muted" />
                ) : (
                  rule.kind === c.id && <IconCheck size={15} className="mt-0.5 shrink-0 text-primary" />
                )}
              </button>
            ))}
            {rule.kind === "above" && (
              <div role="menuitemradio" aria-checked="true" aria-disabled="true" className="flex items-start gap-2 rounded-lg px-2.5 py-2">
                <span className="min-w-0 flex-1">
                  <span className="block text-sm text-foreground">Asks before paying from {formatUSDC(rule.minor)}</span>
                  <span className="mt-0.5 block text-xs leading-snug text-muted">The amount is set on the Spend Passes page.</span>
                </span>
                <IconCheck size={15} className="mt-0.5 shrink-0 text-primary" />
              </div>
            )}
            {error && (
              <p role="alert" className="px-2.5 py-1.5 text-xs text-danger">
                {error}
              </p>
            )}
            <Link
              href={`/console/firewall#pass-${encodeURIComponent(pass.id)}`}
              className="mt-1 block rounded-lg border-t border-border px-2.5 pt-2.5 pb-2 text-xs text-muted transition-colors hover:text-foreground"
            >
              Amounts, providers and speed live on the Spend Passes page
            </Link>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
