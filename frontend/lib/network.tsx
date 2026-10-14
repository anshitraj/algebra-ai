"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import * as api from "./api-client";
import type { RailStatus, WalletsView } from "./types";

/** A Solana cluster the console can work on. */
export type Cluster = "solana" | "solana-devnet";

/**
 * Where the console works: a cluster, or practice mode (Algebra's sandbox:
 * simulated providers and USDC, no chain, no wallet). A demo account is always
 * in practice mode, and the API enforces that; anyone else chooses.
 */
export type Network = Cluster | "sandbox";

/** The three places to work, in the order a new person meets them. */
export const NETWORKS: { id: Network; label: string; hint: string }[] = [
  { id: "sandbox", label: "Practice", hint: "Play money: no wallet needed and nothing real moves" },
  { id: "solana-devnet", label: "Test", hint: "Free test USDC on Solana devnet, paid from your own wallet" },
  { id: "solana", label: "Live", hint: "Real USDC on Solana mainnet" },
];

const WORDS: Record<string, string> = { solana: "Mainnet", "solana-devnet": "Devnet", sandbox: "Practice" };

/** What a sentence calls a network: "Mainnet", "Devnet" or "Practice". The switch itself uses NETWORKS' labels. */
export function networkLabel(n: string | undefined): string {
  return (n && WORDS[n]) || (n ?? "");
}

const KEY = "algebra:network";
/** The real cluster last worked on, kept while the person practises. */
const CLUSTER_KEY = "algebra:cluster";
/** Fired in this tab when the choice changes (other tabs get "storage"). */
const CHANGED = "algebra:network-changed";

function stored(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

/**
 * The saved choice, or practice mode when there is none (or storage is
 * blocked): the first place a new person works needs no wallet and no money.
 * Test and Live are picked on purpose.
 */
export function readSavedNetwork(): Network {
  const saved = stored(KEY);
  return saved === "solana" || saved === "solana-devnet" || saved === "sandbox" ? saved : "sandbox";
}

/** The real cluster the wallet and vault panels talk to: the last one chosen, devnet until one is. */
function readSavedCluster(): Cluster {
  const saved = stored(CLUSTER_KEY) ?? stored(KEY);
  return saved === "solana" ? "solana" : "solana-devnet";
}

/** Saves the choice for this browser and tells every switch on the page. */
export function saveNetwork(n: Network) {
  try {
    window.localStorage.setItem(KEY, n);
    if (n !== "sandbox") window.localStorage.setItem(CLUSTER_KEY, n);
  } catch {
    // the choice still applies for this page
  }
  window.dispatchEvent(new CustomEvent(CHANGED, { detail: n }));
}

/** Calls back whenever the choice changes, here or in another tab. */
export function onNetworkChange(cb: (n: Network) => void): () => void {
  const local = (e: Event) => cb((e as CustomEvent<Network>).detail);
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
  /** Where this console works and pays: practice mode ("sandbox") or a cluster. */
  network: Network;
  /** A demo account: it works in practice mode only, and has no cluster to choose. */
  demo: boolean;
  /** Practice mode, for a demo account or by choice: simulated providers and USDC, no wallet, no chain. */
  practice: boolean;
  /** The real cluster last worked on. The wallet and vault panels use it even while practising. */
  cluster: Cluster;
  /** False until the saved choice has been read: nothing should act on the default before that. */
  ready: boolean;
  /** Whether this server has practice mode at all. True until the server says otherwise. */
  practiceAvailable: boolean;
  setNetwork: (n: Network) => void;
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
  network: "sandbox",
  demo: false,
  practice: true,
  cluster: "solana-devnet",
  ready: false,
  practiceAvailable: true,
  setNetwork: () => {},
  rails: null,
  refreshRails: () => {},
  wallets: null,
  refreshWallets: () => {},
});

/**
 * Where the console works. On a cluster it filters the providers to the ones
 * payable there, and every call the console makes is restricted to it, so
 * nothing meant for devnet can be paid on mainnet. In practice mode nothing
 * touches a wallet or a chain. Remembered per browser. A demo account has no
 * choice: it practises.
 */
export function NetworkProvider({ children, demo = false }: { children: React.ReactNode; demo?: boolean }) {
  // Read at once when there is a browser: the console only mounts this after the session has loaded
  // there, so there is no server-rendered markup for it to differ from, and nobody sees the default flash by.
  const [saved, setSaved] = useState<{ choice: Network; cluster: Cluster; ready: boolean }>(() =>
    typeof window === "undefined"
      ? { choice: "sandbox", cluster: "solana-devnet", ready: false }
      : { choice: readSavedNetwork(), cluster: readSavedCluster(), ready: true },
  );
  const [rails, setRails] = useState<RailStatus[] | null>(null);
  const [practiceAvailable, setPracticeAvailable] = useState(true);
  const [railsKey, setRailsKey] = useState(0);
  const [wallets, setWallets] = useState<WalletsView | null>(null);
  const [walletsRead, setWalletsRead] = useState({ n: 0, force: false });

  useEffect(() => {
    // The same choice wherever it is made: another switch on the page, another tab.
    return onNetworkChange((n) => setSaved((s) => ({ ...s, choice: n, cluster: n === "sandbox" ? s.cluster : n, ready: true })));
  }, []);

  // Read once for the whole console: the switch, the chat and the panels all show the same wallets.
  // A demo account has none to show, and the API doesn't list Algebra's wallets to it.
  useEffect(() => {
    if (demo) return;
    api
      .listRails()
      .then((r) => {
        setRails(r.rails ?? []);
        setPracticeAvailable(r.sandbox !== false);
      })
      .catch(() => setRails([]));
  }, [railsKey, demo]);

  const refreshRails = useCallback(() => setRailsKey((k) => k + 1), []);

  // The person's own wallets on the real cluster, for the wallet menu and the
  // on-chain pass panel. A demo account has none, and a person with none gets none.
  const { cluster } = saved;
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

  const setNetwork = useCallback((n: Network) => {
    setSaved((s) => ({ ...s, choice: n, cluster: n === "sandbox" ? s.cluster : n }));
    saveNetwork(n);
  }, []);

  // A server without practice mode can't offer it: fall back to the saved real cluster.
  const choice: Network = !practiceAvailable && saved.choice === "sandbox" ? saved.cluster : saved.choice;
  const network: Network = demo ? "sandbox" : choice;
  return (
    <Ctx.Provider
      value={{
        network,
        demo,
        practice: network === "sandbox",
        cluster,
        ready: saved.ready,
        practiceAvailable,
        setNetwork,
        rails: demo ? [] : rails,
        refreshRails,
        wallets: demo || wallets?.network !== cluster ? null : wallets,
        refreshWallets,
      }}
    >
      {children}
    </Ctx.Provider>
  );
}

export function useNetwork() {
  return useContext(Ctx);
}
