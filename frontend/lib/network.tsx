"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import * as api from "./api-client";
import type { RailStatus, WalletsView } from "./types";

/** A Solana cluster the console can work on. */
export type Cluster = "solana" | "solana-devnet";

/**
 * Where the console pays: a cluster, or Algebra's sandbox (simulated providers
 * and USDC, no chain). A demo account is always in the sandbox; the API
 * enforces that, and this only keeps the screen in step with it.
 */
export type Network = Cluster | "sandbox";

export const NETWORKS: { id: Cluster; label: string; hint: string }[] = [
  { id: "solana", label: "Mainnet", hint: "Real USDC" },
  { id: "solana-devnet", label: "Devnet", hint: "Test USDC from the faucet" },
];

export function networkLabel(n: string | undefined): string {
  return NETWORKS.find((x) => x.id === n)?.label ?? (n === "sandbox" ? "Sandbox" : (n ?? ""));
}

const KEY = "algebra:network";
/** Fired in this tab when the choice changes (other tabs get "storage"). */
const CHANGED = "algebra:network-changed";

/** The saved choice, or devnet when there is none (or storage is blocked): mainnet is read-only today, and is picked on purpose. */
export function readSavedNetwork(): Cluster {
  try {
    const saved = window.localStorage.getItem(KEY);
    if (saved === "solana" || saved === "solana-devnet") return saved;
  } catch {
    // storage blocked
  }
  return "solana-devnet";
}

/** Saves the choice for this browser and tells every switch on the page. */
export function saveNetwork(n: Cluster) {
  try {
    window.localStorage.setItem(KEY, n);
  } catch {
    // the choice still applies for this page
  }
  window.dispatchEvent(new CustomEvent(CHANGED, { detail: n }));
}

/** Calls back whenever the choice changes, here or in another tab. */
export function onNetworkChange(cb: (n: Cluster) => void): () => void {
  const local = (e: Event) => cb((e as CustomEvent<Cluster>).detail);
  const other = (e: StorageEvent) => {
    if (e.key === KEY) cb(readSavedNetwork());
  };
  window.addEventListener(CHANGED, local);
  window.addEventListener("storage", other);
  return () => {
    window.removeEventListener(CHANGED, local);
    window.removeEventListener("storage", other);
  };
}

type NetworkState = {
  network: Network;
  /** A demo account: it pays in the sandbox only, and has no cluster to choose. */
  demo: boolean;
  setNetwork: (n: Cluster) => void;
  /** Whether this server can pay on each cluster, from which wallet, with what balance. Null until read. */
  rails: RailStatus[] | null;
  /** Reads the wallets again, after a payment moved a balance. */
  refreshRails: () => void;
  /** What the person's own wallets hold on the chosen cluster. Null until read, or when it couldn't be. */
  wallets: WalletsView | null;
  /** Reads them again; force skips the server's short cache (after sending funds, say). */
  refreshWallets: (force?: boolean) => void;
};

const Ctx = createContext<NetworkState>({
  network: "solana-devnet",
  demo: false,
  setNetwork: () => {},
  rails: null,
  refreshRails: () => {},
  wallets: null,
  refreshWallets: () => {},
});

/**
 * Which cluster the console shows and pays on. It filters the providers to
 * the ones payable there, and every call the console makes is restricted to
 * it, so nothing meant for devnet can be paid on mainnet. Remembered per
 * browser. A demo account has no choice: it works in the sandbox.
 */
export function NetworkProvider({ children, demo = false }: { children: React.ReactNode; demo?: boolean }) {
  const [cluster, setCluster] = useState<Cluster>("solana-devnet");
  const [rails, setRails] = useState<RailStatus[] | null>(null);
  const [railsKey, setRailsKey] = useState(0);
  const [wallets, setWallets] = useState<WalletsView | null>(null);
  const [walletsRead, setWalletsRead] = useState({ n: 0, force: false });

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- restoring a saved choice after mount
    setCluster(readSavedNetwork());
    // The same choice wherever it is made: the site's top bar, another tab.
    return onNetworkChange(setCluster);
  }, []);

  // Read once for the whole console: the switch, the chat and the panels all show the same wallets.
  // A demo account has none to show, and the API doesn't list Algebra's wallets to it.
  useEffect(() => {
    if (demo) return;
    api
      .listRails()
      .then((r) => setRails(r.rails ?? []))
      .catch(() => setRails([]));
  }, [railsKey, demo]);

  const refreshRails = useCallback(() => setRailsKey((k) => k + 1), []);

  // The person's own wallets on the chosen cluster, for the wallet menu and the
  // on-chain pass panel. A demo account has none, and a person with none gets none.
  useEffect(() => {
    if (demo) return;
    let live = true;
    api
      .myWallets(cluster, walletsRead.force)
      .then((v) => live && setWallets(v))
      .catch(() => live && setWallets(null));
    return () => {
      live = false;
    };
  }, [cluster, demo, walletsRead]);

  const refreshWallets = useCallback((force = false) => setWalletsRead((r) => ({ n: r.n + 1, force })), []);

  const setNetwork = useCallback((n: Cluster) => {
    setCluster(n);
    saveNetwork(n);
  }, []);

  const network: Network = demo ? "sandbox" : cluster;
  return (
    <Ctx.Provider
      value={{ network, demo, setNetwork, rails: demo ? [] : rails, refreshRails, wallets: demo || wallets?.network !== cluster ? null : wallets, refreshWallets }}
    >
      {children}
    </Ctx.Provider>
  );
}

export function useNetwork() {
  return useContext(Ctx);
}
