"use client";

// A Spend Pass enforced on Solana. The person's own wallet makes a vault for
// the pass and funds it; Algebra can only pull from it for one payment at a
// time, only into its payer, and only within the pass's limits, which the
// program checks on chain. Freezing, withdrawing and closing are the
// wallet's, with no one else's signature.

import { useCallback, useEffect, useState } from "react";
import { explorerAddress, explorerTx } from "@/lib/explorer";
import { networkLabel, useNetwork } from "@/lib/network";
import {
  OnchainError,
  connectWallet,
  getOnchainPass,
  linkOnchainPass,
  onchainNetworks,
  ownerTransaction,
  prepareOnchainPass,
  signAndSend,
  solanaWallets,
  type OnchainNetwork,
  type OnchainPassView,
  type OwnerAction,
  type StandardWallet,
} from "@/lib/onchain-pass";
import { IconExternal, IconLock, IconShield, IconWallet, Spinner } from "@/components/icons";
import { Button, ErrorNote, Input } from "@/components/console/ui";

const usdc = (minor: number | undefined) =>
  minor === undefined ? "—" : `${(minor / 1_000_000).toLocaleString(undefined, { maximumFractionDigits: 6 })} USDC`;
const micro = (v: string) => (v.trim() === "" ? NaN : Math.round(Number(v) * 1_000_000));
const short = (s: string) => (s.length > 12 ? `${s.slice(0, 4)}…${s.slice(-4)}` : s);
const sleep = (ms: number) => new Promise((ok) => setTimeout(ok, ms));

const PULL_WORDS: Record<string, string> = {
  SENDING: "Confirming",
  PULLED: "Funding a payment",
  VOID: "Didn't happen",
  SETTLED: "Spent",
  REFUNDING: "Returning",
  REFUNDED: "Returned",
};

export function OnchainPassPanel({ passId, active }: { passId: string; active: boolean }) {
  const { network } = useNetwork();
  const [nets, setNets] = useState<OnchainNetwork[] | null>(null);
  const [view, setView] = useState<OnchainPassView | null | undefined>(undefined);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [amount, setAmount] = useState("1");
  const [wallets, setWallets] = useState<StandardWallet[]>([]);

  const load = useCallback(async () => {
    try {
      setView(await getOnchainPass(passId));
    } catch (e) {
      if (e instanceof OnchainError && e.status === 404) setView(null);
      else if (e instanceof OnchainError && e.status === 501) setView(null);
      else setError(e instanceof Error ? e.message : String(e));
    }
  }, [passId]);

  useEffect(() => {
    onchainNetworks()
      .then((r) => setNets(r.networks))
      .catch(() => setNets([]));
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reading the pass once on mount
    void load();
    setWallets(solanaWallets());
    // A wallet that loads after the page announces itself.
    const onRegister = (e: Event) => {
      const cb = (e as CustomEvent).detail as ((api: { register: (...w: StandardWallet[]) => () => void }) => void) | undefined;
      cb?.({ register: () => () => {} });
      setWallets(solanaWallets());
    };
    window.addEventListener("wallet-standard:register-wallet", onRegister);
    return () => window.removeEventListener("wallet-standard:register-wallet", onRegister);
  }, [load]);

  const net = nets?.find((n) => n.network === (view?.binding.network ?? network));

  async function withWallet<T>(want: string | undefined, fn: (w: StandardWallet, address: string, acct: Awaited<ReturnType<typeof connectWallet>>) => Promise<T>) {
    const ws = solanaWallets();
    if (ws.length === 0) throw new Error("No Solana wallet found in this browser. Install Phantom, Solflare or Backpack, then reload.");
    // With several wallets, the one holding the owner's account; else the first.
    for (const w of ws) {
      const acct = await connectWallet(w);
      if (!want || acct.address === want) return fn(w, acct.address, acct);
      if (ws.length === 1) throw new Error(`This pass belongs to wallet ${short(want)}; ${w.name} is on ${short(acct.address)}. Switch accounts in ${w.name}.`);
    }
    throw new Error(`None of your wallets is on ${short(want ?? "")}, the wallet that owns this pass.`);
  }

  async function create() {
    if (!net) return;
    const deposit = micro(amount);
    if (!(deposit >= 0)) {
      setError("Enter how much USDC to put in, for example 1.");
      return;
    }
    setBusy("create");
    setError("");
    try {
      await withWallet(undefined, async (w, owner, acct) => {
        const prep = await prepareOnchainPass(passId, { network: net.network, owner, deposit_minor: deposit });
        await signAndSend(w, acct, prep.transaction, net.network);
        // The pass exists once the transaction lands; link it as soon as it does.
        for (let i = 0; i < 30; i++) {
          try {
            await linkOnchainPass(passId, net.network, prep.pass_address);
            return;
          } catch (e) {
            if (!(e instanceof OnchainError && e.status === 404)) throw e;
            await sleep(2000);
          }
        }
        throw new Error("The transaction hasn't landed yet. Reload in a minute; the pass will link then.");
      });
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  }

  async function act(action: OwnerAction) {
    if (!view) return;
    let minor = 0;
    if (action === "deposit" || action === "withdraw") {
      minor = micro(amount);
      if (!(minor > 0)) {
        setError("Enter an amount of USDC first.");
        return;
      }
    }
    if (action === "revoke" && !window.confirm("Revoke this pass on Solana? It can never pull again. The money stays yours to withdraw.")) return;
    if (action === "close" && !window.confirm("Close this pass on Solana and return everything in it to your wallet?")) return;
    setBusy(action);
    setError("");
    try {
      await withWallet(view.binding.owner_wallet, async (w, _addr, acct) => {
        const { transaction } = await ownerTransaction(passId, action, minor);
        await signAndSend(w, acct, transaction, view.binding.network);
      });
      // Show the new state once the chain has it.
      const before = JSON.stringify([view.state, view.vault_minor, view.exists]);
      for (let i = 0; i < 15; i++) {
        await sleep(2000);
        const next = await getOnchainPass(passId).catch(() => null);
        if (next && JSON.stringify([next.state, next.vault_minor, next.exists]) !== before) {
          setView(next);
          return;
        }
      }
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  }

  if (view === undefined || nets === null) return null;
  if (view === null && (!active || nets.length === 0)) return null;

  return (
    <div className="mt-4 rounded-lg border border-border bg-surface p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <IconShield size={16} />
          <h4 className="text-sm font-semibold text-foreground">Enforced on Solana</h4>
          {view?.exists && view.state && (
            <span
              className={`rounded-full px-2 py-0.5 text-[0.7rem] font-medium ${
                view.state.revoked || view.state.frozen ? "bg-danger-tint text-danger" : "bg-primary-tint text-primary"
              }`}
            >
              {view.state.revoked ? "Revoked on chain" : view.state.frozen ? "Frozen on chain" : "Live on chain"}
            </span>
          )}
          {view && <span className="text-xs text-muted">{networkLabel(view.binding.network)}</span>}
        </div>
        {view && (
          <a href={view.explorer} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-xs text-primary">
            {short(view.binding.address)} <IconExternal size={11} />
          </a>
        )}
      </div>

      {view === null ? (
        <>
          <p className="mt-2 text-sm text-muted">
            Put this pass&apos;s limits in a Solana program, funded from your own wallet. Algebra can then only pull what one payment needs,
            within these limits, checked on chain. Even Algebra can&apos;t take more, and you can freeze it from your wallet.
          </p>
          {net ? (
            <div className="mt-3 flex flex-wrap items-end gap-2">
              <label className="block">
                <span className="text-xs text-muted">Fund it with (USDC, {networkLabel(net.network)})</span>
                <div className="mt-1 w-32">
                  <Input value={amount} onChange={(e) => setAmount(e.target.value)} inputMode="decimal" aria-label="USDC to put in the pass" />
                </div>
              </label>
              <Button onClick={create} disabled={busy !== "" || wallets.length === 0}>
                {busy === "create" ? <Spinner size={13} /> : <IconWallet size={14} />} Create with my wallet
              </Button>
            </div>
          ) : (
            <p className="mt-2 text-xs text-muted">Switch the console to {nets.map((n) => networkLabel(n.network)).join(" or ")} to put this pass on chain.</p>
          )}
          {wallets.length === 0 && net && (
            <p className="mt-2 text-xs text-muted">No Solana wallet found in this browser. Install Phantom, Solflare or Backpack, then reload.</p>
          )}
          {net && (
            <p className="mt-2 text-xs text-muted">
              Program{" "}
              <a className="text-primary" href={explorerAddress(net.program_id, net.network)} target="_blank" rel="noopener noreferrer">
                {short(net.program_id)}
              </a>{" "}
              · pays only into Algebra&apos;s payer {short(net.agent)}
            </p>
          )}
        </>
      ) : !view.exists ? (
        <p className="mt-2 text-sm text-muted">This pass was closed on chain. Its money went back to {short(view.binding.owner_wallet)}.</p>
      ) : (
        view.state && (
          <>
            <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm sm:grid-cols-4">
              <div>
                <dt className="text-xs text-muted">In the vault</dt>
                <dd className="font-mono tabular-nums text-foreground">{usdc(view.vault_minor)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted">Spent on chain</dt>
                <dd className="font-mono tabular-nums text-foreground">
                  {usdc(view.state.spent)} <span className="text-muted">/ {usdc(view.state.total_budget)}</span>
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted">Most per call</dt>
                <dd className="font-mono tabular-nums text-foreground">{usdc(view.state.per_call_cap)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted">Owner wallet</dt>
                <dd className="font-mono text-foreground">
                  <a href={explorerAddress(view.binding.owner_wallet, view.binding.network)} target="_blank" rel="noopener noreferrer">
                    {short(view.binding.owner_wallet)}
                  </a>
                </dd>
              </div>
            </dl>
            <div className="mt-3 flex flex-wrap items-end gap-2">
              <Button variant={view.state.frozen ? "primary" : "danger"} onClick={() => act(view.state!.frozen ? "unfreeze" : "freeze")} disabled={busy !== "" || view.state.revoked}>
                {busy === "freeze" || busy === "unfreeze" ? <Spinner size={13} /> : <IconLock size={14} />}
                {view.state.frozen ? "Unfreeze" : "Freeze on chain"}
              </Button>
              <div className="w-24">
                <Input value={amount} onChange={(e) => setAmount(e.target.value)} inputMode="decimal" aria-label="USDC amount" />
              </div>
              <Button variant="secondary" onClick={() => act("deposit")} disabled={busy !== "" || view.state.revoked}>
                {busy === "deposit" && <Spinner size={13} />} Add
              </Button>
              <Button variant="secondary" onClick={() => act("withdraw")} disabled={busy !== ""}>
                {busy === "withdraw" && <Spinner size={13} />} Withdraw
              </Button>
              {!view.state.revoked && (
                <Button variant="secondary" onClick={() => act("revoke")} disabled={busy !== ""}>
                  {busy === "revoke" && <Spinner size={13} />} Revoke
                </Button>
              )}
              <Button variant="secondary" onClick={() => act("close")} disabled={busy !== ""}>
                {busy === "close" && <Spinner size={13} />} Close &amp; return all
              </Button>
            </div>
            {view.pulls.length > 0 && (
              <ul className="mt-3 divide-y divide-border text-xs">
                {view.pulls.slice(0, 5).map((p) => (
                  <li key={p.reservation_id} className="flex flex-wrap items-center justify-between gap-2 py-1.5">
                    <span className="text-muted">
                      {PULL_WORDS[p.state] ?? p.state} · <span className="font-mono text-foreground">{usdc(p.amount_minor)}</span>
                      {p.refund_minor ? <> · {usdc(p.refund_minor)} returned</> : null}
                    </span>
                    <span className="flex gap-3">
                      <a className="text-primary" href={explorerTx(p.pull_signature, p.network)} target="_blank" rel="noopener noreferrer">
                        pull
                      </a>
                      {p.refund_signature && (
                        <a className="text-primary" href={explorerTx(p.refund_signature, p.network)} target="_blank" rel="noopener noreferrer">
                          refund
                        </a>
                      )}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </>
        )
      )}
      {error && (
        <div className="mt-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}
    </div>
  );
}
