"use client";

import { NETWORKS, useNetwork, type Network } from "@/lib/network";

const DOTS: Record<Network, string> = { sandbox: "bg-muted", "solana-devnet": "bg-accent", solana: "bg-primary" };

/**
 * Where the console works, for the whole page: Practice (play money, no wallet),
 * Test (devnet) or Live (mainnet). A demo account only practises. The wallet
 * is the wallet menu's.
 */
export function NetworkSwitch({ compact = false }: { compact?: boolean }) {
  const { network, demo, setNetwork, practiceAvailable } = useNetwork();
  if (demo) {
    return (
      <div className="flex items-center gap-3">
        <span
          title="Demo accounts work in practice mode: simulated providers and play USDC, nothing on a chain"
          className="inline-flex items-center gap-1.5 rounded-xl bg-accent-tint px-3 py-1.5 text-xs font-medium text-accent"
        >
          <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-accent" />
          Practice
        </span>
        {!compact && <span className="hidden text-xs text-muted md:inline">Play USDC: nothing real moves</span>}
      </div>
    );
  }
  const options = NETWORKS.filter((n) => n.id !== "sandbox" || practiceAvailable);
  if (compact) {
    // Three buttons don't fit beside the menu and the wallet on a phone: one control that says where you are.
    return (
      <label className="relative inline-flex items-center">
        <span className="sr-only">Where to work</span>
        <select
          value={network}
          onChange={(e) => setNetwork(e.target.value as Network)}
          title={options.find((n) => n.id === network)?.hint}
          className="h-9 rounded-xl border-0 bg-primary-tint/60 py-1 pr-2 pl-2.5 text-xs font-medium text-foreground"
        >
          {options.map((n) => (
            <option key={n.id} value={n.id}>
              {n.label}
            </option>
          ))}
        </select>
      </label>
    );
  }
  return (
    <div className="flex items-center gap-3">
      <div role="radiogroup" aria-label="Where to work" className="flex rounded-xl bg-primary-tint/50 p-1">
        {options.map((n) => {
          const on = network === n.id;
          return (
            <button
              key={n.id}
              type="button"
              role="radio"
              aria-checked={on}
              title={n.hint}
              onClick={() => setNetwork(n.id)}
              className={`inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-xs font-medium transition-colors sm:px-3 ${
                on ? "bg-surface text-foreground shadow-[0_1px_3px_rgb(0_0_0/0.08)]" : "text-muted hover:text-foreground"
              }`}
            >
              <span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${DOTS[n.id]}`} />
              {n.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}
