"use client";

// Mainnet or devnet, from the site's top bar. It is the console's own choice
// (lib/network), saved in this browser, so picking Devnet here opens the
// console on devnet, and switching in the console moves this too.

import { useEffect, useState } from "react";
import { NETWORKS, onNetworkChange, readSavedNetwork, saveNetwork, type Network } from "@/lib/network";

export function NetworkToggle({ className = "" }: { className?: string }) {
  const [network, setNetwork] = useState<Network>("solana");

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- restoring the saved choice after mount
    setNetwork(readSavedNetwork());
    return onNetworkChange(setNetwork);
  }, []);

  function pick(n: Network) {
    setNetwork(n);
    saveNetwork(n);
  }

  return (
    <div role="radiogroup" aria-label="Solana network" className={`network-toggle ${className}`}>
      {NETWORKS.map((n, i) => (
        <button
          key={n.id}
          type="button"
          role="radio"
          aria-checked={network === n.id}
          tabIndex={network === n.id ? 0 : -1}
          data-network={n.id}
          onClick={() => pick(n.id)}
          onKeyDown={(e) => {
            if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
            e.preventDefault();
            const next = NETWORKS[(i + (e.key === "ArrowRight" ? 1 : NETWORKS.length - 1)) % NETWORKS.length];
            pick(next.id);
            (e.currentTarget.parentElement?.querySelector(`[data-network="${next.id}"]`) as HTMLElement | null)?.focus();
          }}
        >
          <i aria-hidden="true" />
          {n.label}
          <span className="sr-only">: {n.hint}</span>
        </button>
      ))}
    </div>
  );
}
