// The unlocked vault keys, in this tab's memory only (docs/WALLET.md): never
// stored, wiped when the vault locks, and locked again after 15 minutes
// without use or on reload.

import { useSyncExternalStore } from "react";
import { wipeKeys, type OwnerKeys } from "./keys.ts";

const IDLE_MS = 15 * 60 * 1000;

let keys: OwnerKeys | null = null;
let timer: ReturnType<typeof setTimeout> | undefined;
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

function arm() {
  clearTimeout(timer);
  timer = setTimeout(lockVault, IDLE_MS);
}

/** Holds freshly derived keys as the unlocked vault (wiping any before). */
export function unlockWith(next: OwnerKeys) {
  if (keys !== next) wipeKeys(keys);
  keys = next;
  arm();
  emit();
}

export function lockVault() {
  wipeKeys(keys);
  keys = null;
  clearTimeout(timer);
  emit();
}

/** The keys if the vault is unlocked; using them counts as activity. */
export function unlockedKeys(): OwnerKeys | null {
  if (keys) arm();
  return keys;
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** Whether the vault is unlocked in this tab, kept current. */
export function useVaultUnlocked(): boolean {
  return useSyncExternalStore(subscribe, () => keys !== null, () => false);
}
