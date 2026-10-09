"use client";

import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { IconCheck, IconChevronDown } from "@/components/icons";

export type ProviderEntry = {
  id: string;
  label: string;
  available: boolean;
  models: { id: string; label: string }[];
};

export type ModelChoice = { provider: string; model: string } | null; // null = Auto

export function modelLabel(providers: ProviderEntry[] | null, choice: ModelChoice) {
  if (!choice) return "Auto";
  return providers?.find((p) => p.id === choice.provider)?.models.find((m) => m.id === choice.model)?.label ?? choice.model;
}

export function ModelPicker({
  providers,
  value,
  onChange,
  lockedProvider,
}: {
  providers: ProviderEntry[] | null;
  value: ModelChoice;
  onChange: (c: ModelChoice) => void;
  /** Mid-conversation, only models from this provider keep history compatible. */
  lockedProvider: string | null;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false);
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const pick = (c: ModelChoice) => {
    onChange(c);
    setOpen(false);
  };

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className="inline-flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-sm text-muted transition-colors hover:bg-primary-tint hover:text-foreground"
      >
        {modelLabel(providers, value)}
        <IconChevronDown size={15} className={`transition-transform ${open ? "rotate-180" : ""}`} />
      </button>
      <AnimatePresence>
        {open && (
          <motion.div
            initial={{ opacity: 0, y: 6, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 4, scale: 0.98 }}
            transition={{ duration: 0.15 }}
            role="listbox"
            aria-label="Model"
            className="absolute bottom-full left-0 z-30 mb-2 max-h-[min(420px,60vh)] w-72 overflow-y-auto rounded-xl border border-border bg-surface p-1.5 shadow-[0_18px_44px_-18px_rgba(11,16,32,0.4)]"
          >
            <Option selected={value === null} onClick={() => pick(null)} label="Auto" hint="Best available model" />
            {providers?.map((p) => {
              const lockedOut = lockedProvider !== null && lockedProvider !== p.id;
              return (
                <div key={p.id} className="mt-1.5 border-t border-border pt-1.5">
                  <p className="px-2.5 pt-1 pb-1.5 text-[0.7rem] font-medium tracking-wide text-muted uppercase">{p.label}</p>
                  {!p.available ? (
                    <p className="px-2.5 pb-2 text-xs text-muted/80">Add its API key on the server to enable</p>
                  ) : (
                    p.models.map((m) => (
                      <Option
                        key={m.id}
                        selected={value?.provider === p.id && value.model === m.id}
                        disabled={lockedOut}
                        onClick={() => pick({ provider: p.id, model: m.id })}
                        label={m.label}
                        hint={lockedOut ? "Start a new chat to switch provider" : undefined}
                      />
                    ))
                  )}
                </div>
              );
            })}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

function Option({
  label,
  hint,
  selected,
  disabled,
  onClick,
}: {
  label: string;
  hint?: string;
  selected: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="option"
      aria-selected={selected}
      disabled={disabled}
      onClick={onClick}
      className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left transition-colors hover:bg-primary-tint disabled:cursor-not-allowed disabled:opacity-45 disabled:hover:bg-transparent"
    >
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm text-foreground">{label}</span>
        {hint && <span className="block truncate text-xs text-muted">{hint}</span>}
      </span>
      {selected && <IconCheck size={15} className="shrink-0 text-primary" />}
    </button>
  );
}
