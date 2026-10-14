"use client";

// The devnet airdrop, as the wallet menu, the Quickstart guide and Get started
// all need it: whether this server can give one, whether the account has had
// its one (an account gets one airdrop, ever, and so does a wallet), what the
// wallet holds, and the drop itself. One place, so the three say the same thing.

import { useCallback, useEffect, useState } from "react";
import * as api from "./api-client";
import { formatUSDC } from "./money";
import { useNetwork } from "./network";
import { notifyTx } from "./notifications";
import { useSession } from "./session";
import type { FaucetReceipt, FaucetStatus, WalletBalance } from "./types";

// What a wallet needs to put USDC in a vault: a little SOL for fees and rent, and USDC to lock.
export const ENOUGH_SOL = 20_000_000; // 0.02 SOL
export const ENOUGH_USDC = 1_000_000; // 1 USDC

export const solText = (lamports: number) => (lamports / 1e9).toLocaleString("en-US", { maximumFractionDigits: 3 });

/** Fired on this page when an airdrop was given or found already given, for every place that shows it. */
const CHANGED = "algebra:airdrop-changed";

export type Airdrop = {
  /** Undefined while it is read; null when the server couldn't be asked. */
  faucet: FaucetStatus | null | undefined;
  /** The wallet a drop goes to. */
  address: string | undefined;
  /** What that wallet holds on devnet, once read. */
  held: WalletBalance | null;
  /** The wallet already holds what a pass and its vault need. */
  funded: boolean;
  /** What the account was given, once it has had its one airdrop. */
  received: FaucetReceipt | null;
  busy: boolean;
  error: string | null;
  drop: () => Promise<void>;
};

/**
 * enabled false does nothing (a demo account, a menu that is closed): no
 * requests are made. wallet picks the wallet a drop goes to, when the person
 * has chosen one (the wallet menu); otherwise the server's default for the account.
 */
export function useAirdrop(enabled = true, wallet?: string): Airdrop {
  const { user } = useSession();
  const { refreshWallets } = useNetwork();
  const [faucet, setFaucet] = useState<FaucetStatus | null | undefined>(undefined);
  const [wallets, setWallets] = useState<WalletBalance[] | null>(null);
  const [got, setGot] = useState<FaucetReceipt | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadWallets = useCallback((force = false) => {
    api
      .myWallets("solana-devnet", force)
      .then((v) => setWallets(v.wallets))
      .catch(() => {});
  }, []);

  const loadFaucet = useCallback(() => {
    return api
      .getFaucet()
      .then((f) => {
        setFaucet(f);
        if (f.received) setGot(f.received);
      })
      .catch(() => setFaucet(null));
  }, []);

  useEffect(() => {
    if (!enabled || !user) return;
    void loadFaucet();
    loadWallets();
    // Another place on the page gave (or found) the airdrop: read it again, so two buttons never disagree.
    const again = () => {
      void loadFaucet();
      loadWallets(true);
    };
    window.addEventListener(CHANGED, again);
    return () => window.removeEventListener(CHANGED, again);
  }, [enabled, user, loadFaucet, loadWallets]);

  const address = wallet ?? faucet?.wallet ?? wallets?.[0]?.address ?? user?.wallets?.find((w) => w.chain === "solana")?.address;
  const held = wallets?.find((w) => w.address === address) ?? null;
  const funded = !!held?.ok && held.sol_lamports >= ENOUGH_SOL && held.usdc_minor >= ENOUGH_USDC;

  const drop = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await api.requestFaucet(address);
      setGot({ ...r.drop, at: new Date().toISOString() });
      notifyTx({
        kind: "faucet",
        title: "Test money received",
        detail:
          [r.drop.sol_lamports ? `${solText(r.drop.sol_lamports)} SOL` : "", r.drop.usdc_minor ? formatUSDC(r.drop.usdc_minor) : ""].filter(Boolean).join(" and ") +
          " sent to your wallet",
        signature: r.drop.signature,
        network: "solana-devnet",
      });
      window.dispatchEvent(new Event(CHANGED));
      // The chain needs a moment before the wallet shows it.
      window.setTimeout(() => {
        loadWallets(true);
        refreshWallets(true);
        window.dispatchEvent(new Event(CHANGED));
      }, 2500);
    } catch (e) {
      // "You already had it" is an answer, not a failure: show what the account was given.
      if (e instanceof api.ApiError && e.status === 409) {
        const f = await api.getFaucet().catch(() => null);
        if (f?.received) {
          setFaucet(f);
          setGot(f.received);
          window.dispatchEvent(new Event(CHANGED));
          return;
        }
      }
      setError(e instanceof Error ? e.message : "The faucet didn't answer. Try again in a minute.");
    } finally {
      setBusy(false);
    }
  }, [address, loadWallets, refreshWallets]);

  return { faucet, address, held, funded, received: got, busy, error, drop };
}
