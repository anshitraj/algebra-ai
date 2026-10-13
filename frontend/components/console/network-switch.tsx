"use client";

import { NETWORKS, useNetwork } from "@/lib/network";

/** Mainnet or devnet, for the whole console. A demo account has the sandbox instead. The wallet is the wallet menu's. */
export function NetworkSwitch({ compact = false }: { compact?: boolean }) {
  const { network, demo, setNetwork } = useNetwork();
  if (demo) {
    return (
      <div className="flex items-center gap-3">
        <span
          title="Demo accounts pay in Algebra's sandbox: simulated providers and USDC, nothing on a chain"
          className="inline-flex items-center gap-1.5 rounded-xl bg-accent-tint px-3 py-1.5 text-xs font-medium text-accent"
        >
          <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-accent" />
          Sandbox
        </span>
        {!compact && <span className="hidden text-xs text-muted md:inline">Simulated USDC: nothing moves on a chain</span>}
      </div>
    );
  }
  return (
    <div className="flex items-center gap-3">
      <div role="radiogroup" aria-label="Solana network" className="flex rounded-xl bg-primary-tint/50 p-1">
        {NETWORKS.map((n) => {
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
              <span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${n.id === "solana" ? "bg-primary" : "bg-accent"}`} />
              {n.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}
