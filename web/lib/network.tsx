"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";

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

type NetworkState = { network: Network; setNetwork: (n: Network) => void };

const Ctx = createContext<NetworkState>({ network: "solana", setNetwork: () => {} });

/**
 * Which cluster the console shows and pays on. It filters the providers to
 * the ones payable there, and every call the console makes is restricted to
 * it, so nothing meant for devnet can be paid on mainnet. Remembered per
 * browser.
 */
export function NetworkProvider({ children }: { children: React.ReactNode }) {
  const [network, setState] = useState<Network>("solana");

  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(KEY);
      // eslint-disable-next-line react-hooks/set-state-in-effect -- restoring a saved choice after mount
      if (saved === "solana" || saved === "solana-devnet") setState(saved);
    } catch {
      // storage blocked: mainnet for this page
    }
  }, []);

  const setNetwork = useCallback((n: Network) => {
    setState(n);
    try {
      window.localStorage.setItem(KEY, n);
    } catch {
      // the choice still applies for this page
    }
  }, []);

  return <Ctx.Provider value={{ network, setNetwork }}>{children}</Ctx.Provider>;
}

export function useNetwork() {
  return useContext(Ctx);
}
