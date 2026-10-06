"use client";

import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { RailStatus } from "@/lib/types";
import { NETWORKS, useNetwork } from "@/lib/network";
import { formatUSDC } from "@/lib/money";

/** Mainnet or devnet, for the whole console, with the wallet that pays there. */
export function NetworkSwitch({ compact = false }: { compact?: boolean }) {
  const { network, setNetwork } = useNetwork();
  const [rails, setRails] = useState<RailStatus[] | null>(null);

  useEffect(() => {
    api
      .listRails()
      .then((r) => setRails(r.rails))
      .catch(() => setRails([]));
  }, []);

  const rail = rails?.find((r) => r.network === network);
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
              className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1 text-xs font-medium transition-colors ${
                on ? "bg-surface text-foreground shadow-[0_1px_3px_rgb(0_0_0/0.08)]" : "text-muted hover:text-foreground"
              }`}
            >
              <span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${n.id === "solana" ? "bg-primary" : "bg-accent"}`} />
              {n.label}
            </button>
          );
        })}
      </div>
      {!compact && rails !== null && (
        <span className="hidden text-xs text-muted md:inline">
          {!rail?.configured
            ? network === "solana"
              ? "No mainnet wallet on this server: calls can be priced, not paid"
              : "No devnet wallet on this server: calls can be priced, not paid"
            : rail.error
              ? "Wallet set, node unreachable"
              : `Wallet ${rail.address?.slice(0, 4)}…${rail.address?.slice(-4)} · ${rail.usdc_minor != null ? formatUSDC(rail.usdc_minor) : "no USDC yet"}`}
        </span>
      )}
    </div>
  );
}
