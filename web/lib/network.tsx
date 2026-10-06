"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import * as api from "./api-client";
import type { RailStatus } from "./types";

/** The Solana cluster the console is working on. */
export type Network = "solana" | "solana-devnet";

export const NETWORKS: { id: Network; label: string; hint: string }[] = [
  { id: "solana", label: "Mainnet", hint: "Real USDC" },
  { id: "solana-devnet", label: "Devnet", hint: "Test USDC from the faucet" },
];

export function networkLabel(n: string | undefined): string {
  return NETWORKS.find((x) => x.id === n)?.label ?? (n === "sandbox" ? "Sandbox" : (n ?? ""));
}

const KEY = "algebra:network";

type NetworkState = {
  network: Network;
  setNetwork: (n: Network) => void;
  /** Whether this server can pay on each cluster, from which wallet, with what balance. Null until read. */
  rails: RailStatus[] | null;
  /** Reads the wallets again, after a payment moved a balance. */
  refreshRails: () => void;
};

const Ctx = createContext<NetworkState>({ network: "solana", setNetwork: () => {}, rails: null, refreshRails: () => {} });

/**
 * Which cluster the console shows and pays on. It filters the providers to
 * the ones payable there, and every call the console makes is restricted to
 * it, so nothing meant for devnet can be paid on mainnet. Remembered per
 * browser.
 */
export function NetworkProvider({ children }: { children: React.ReactNode }) {
  const [network, setState] = useState<Network>("solana");
  const [rails, setRails] = useState<RailStatus[] | null>(null);
  const [railsKey, setRailsKey] = useState(0);

  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(KEY);
      // eslint-disable-next-line react-hooks/set-state-in-effect -- restoring a saved choice after mount
      if (saved === "solana" || saved === "solana-devnet") setState(saved);
    } catch {
      // storage blocked: mainnet for this page
    }
  }, []);

  // Read once for the whole console: the switch, the chat and the panels all show the same wallets.
  useEffect(() => {
    api
      .listRails()
      .then((r) => setRails(r.rails ?? []))
      .catch(() => setRails([]));
  }, [railsKey]);

  const refreshRails = useCallback(() => setRailsKey((k) => k + 1), []);

  const setNetwork = useCallback((n: Network) => {
    setState(n);
    try {
      window.localStorage.setItem(KEY, n);
    } catch {
      // the choice still applies for this page
    }
  }, []);

  return <Ctx.Provider value={{ network, setNetwork, rails, refreshRails }}>{children}</Ctx.Provider>;
}

export function useNetwork() {
  return useContext(Ctx);
}
