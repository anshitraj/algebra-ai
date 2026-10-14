"use client";

// A Spend Pass enforced on Solana. The person's own wallet makes a vault for
// the pass and funds it; Algebra can only pull from it for one payment at a
// time, only into its payer, and only within the pass's limits, which the
// program checks on chain. Freezing, withdrawing and closing are the
// wallet's, with no one else's signature.

import dynamic from "next/dynamic";
import { useCallback, useEffect, useMemo, useState } from "react";
import { explorerAddress, explorerTx } from "@/lib/explorer";
import { formatUSDC } from "@/lib/money";
import { networkLabel, useNetwork } from "@/lib/network";
import { useSession } from "@/lib/session";
import { getAuthProviders } from "@/lib/api-client";
import type { AccountSigner } from "./account-wallet-signer";
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

// Privy's SDK is large: loaded only for an account whose wallet Privy holds.
const AccountWalletSigner = dynamic(() => import("./account-wallet-signer"), { ssr: false });

const PULL_WORDS: Record<string, string> = {
  SENDING: "Confirming",
  PULLED: "Funding a payment",
  VOID: "Didn't happen",
  SETTLED: "Spent",
  REFUNDING: "Returning",
  REFUNDED: "Returned",
};

/** A person closed or declined a wallet's popup (each wallet words it differently: "User rejected the request", "Approval Denied"). */
const CANCELLED = /reject|denied|declin|cancel|closed|dismiss/i;

/** Why the account wallet didn't send a transaction. */
function accountFailure(e: unknown) {
  const said = (e instanceof Error ? e.message : String(e ?? "")).trim();
  if (CANCELLED.test(said) || /exited/i.test(said)) return said.startsWith("You ") ? said : "You cancelled in Privy. Nothing was sent.";
  return said || "Privy didn't send the transaction. Try again.";
}

/** Why a wallet didn't share its account: the connect popup, not a transaction. */
function connectFailure(e: unknown, wallet: string) {
  const said = (e instanceof Error ? e.message : String(e ?? "")).trim();
  if (CANCELLED.test(said)) return `You cancelled the connection in ${wallet}. Nothing was sent.`;
  return `${wallet} didn't connect${said ? ` (it said: ${said})` : ""}. Unlock ${wallet}, approve the connection, and try again.`;
}

/** Why a wallet didn't send a transaction Algebra had already checked on that network. */
function walletFailure(e: unknown, wallet: string, network: string) {
  const said = (e instanceof Error ? e.message : String(e ?? "")).trim();
  if (CANCELLED.test(said)) return `You cancelled the request in ${wallet}. Nothing was sent.`;
  const where = networkLabel(network);
  const hint = network === "solana-devnet" ? ` In Phantom: Settings, Developer Settings, Testnet Mode, Solana Devnet.` : "";
  return `${wallet} didn't send the transaction${said ? ` (it said: ${said})` : ""}. Algebra had checked that it succeeds on ${where} from this wallet, so the usual cause is the wallet being on another network: switch ${wallet} to ${where} and try again.${hint}`;
}

export function OnchainPassPanel({ passId, active }: { passId: string; active: boolean }) {
  const { network, wallets: balances, refreshWallets } = useNetwork();
  const { user } = useSession();
  // The account wallet: the one Privy made at sign-in. It signs by default.
  const account = useMemo(() => user?.wallets?.find((w) => w.chain === "solana" && w.kind === "embedded"), [user?.wallets]);
  const [privyAppId, setPrivyAppId] = useState("");
  const [accountSigner, setAccountSigner] = useState<AccountSigner | null>(null);
  const [useBrowserWallet, setUseBrowserWallet] = useState(false);
  const [nets, setNets] = useState<OnchainNetwork[] | null>(null);
  const [view, setView] = useState<OnchainPassView | null | undefined>(undefined);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [amount, setAmount] = useState("1");
  const [wallets, setWallets] = useState<StandardWallet[]>([]);
  // A revoked or expired pass can't spend, so its vault isn't looked up with the page: one
  // request per such pass was most of what a long pass list cost. "Check vault" asks.
  const [peek, setPeek] = useState(false);
  const lookup = active || peek;

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
    // Only a card that will show the vault controls needs the Privy app ID.
    if (!account || !lookup) return;
    getAuthProviders()
      .then((p) => setPrivyAppId(p.privy && p.privy_app_id ? p.privy_app_id : ""))
      .catch(() => setPrivyAppId(""));
  }, [account, lookup]);

  useEffect(() => {
    onchainNetworks()
      .then((r) => setNets(r.networks))
      .catch(() => setNets([]));
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reading the pass once on mount
    if (lookup) void load();
    setWallets(solanaWallets());
    // A wallet that loads after the page announces itself.
    const onRegister = (e: Event) => {
      const cb = (e as CustomEvent).detail as ((api: { register: (...w: StandardWallet[]) => () => void }) => void) | undefined;
      cb?.({ register: () => () => {} });
      setWallets(solanaWallets());
    };
    window.addEventListener("wallet-standard:register-wallet", onRegister);
    return () => window.removeEventListener("wallet-standard:register-wallet", onRegister);
  }, [load, lookup]);

  const net = nets?.find((n) => n.network === (view?.binding.network ?? network));
  // The account wallet signs unless the person picked a browser wallet (or there is no account wallet).
  const signWithAccount = !!account && !useBrowserWallet;

  async function withWallet<T>(want: string | undefined, fn: (w: StandardWallet, address: string, acct: Awaited<ReturnType<typeof connectWallet>>) => Promise<T>) {
    const ws = solanaWallets();
    if (ws.length === 0) throw new Error("No Solana wallet found in this browser. Install Phantom, Solflare or Backpack, then reload.");
    // With several wallets, the one holding the owner's account; else the first.
    for (const w of ws) {
      let acct: Awaited<ReturnType<typeof connectWallet>>;
      try {
        acct = await connectWallet(w);
      } catch (e) {
        throw new Error(connectFailure(e, w.name));
      }
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
    // The pass exists once the transaction lands; link it as soon as it does.
    const link = async (passAddress: string) => {
      for (let i = 0; i < 30; i++) {
        try {
          await linkOnchainPass(passId, net.network, passAddress);
          return;
        } catch (e) {
          if (!(e instanceof OnchainError && e.status === 404)) throw e;
          await sleep(2000);
        }
      }
      throw new Error("The transaction hasn't landed yet. Reload in a minute; the pass will link then.");
    };
    try {
      if (signWithAccount) {
        const prep = await prepareOnchainPass(passId, { network: net.network, owner: accountSigner!.address, deposit_minor: deposit });
        try {
          await accountSigner!.signAndSend(prep.transaction, net.network);
        } catch (e) {
          throw new Error(accountFailure(e));
        }
        await link(prep.pass_address);
      } else {
        await withWallet(undefined, async (w, owner, acct) => {
          const prep = await prepareOnchainPass(passId, { network: net.network, owner, deposit_minor: deposit });
          try {
            await signAndSend(w, acct, prep.transaction, net.network);
          } catch (e) {
            throw new Error(walletFailure(e, w.name, net.network));
          }
          await link(prep.pass_address);
        });
      }
      refreshWallets(true);
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
      if (account && view.binding.owner_wallet === account.address) {
        if (!accountSigner) throw new Error("Your account wallet is still loading. Try again in a moment.");
        const { transaction } = await ownerTransaction(passId, action, minor);
        try {
          await accountSigner.signAndSend(transaction, view.binding.network);
        } catch (e) {
          throw new Error(accountFailure(e));
        }
      } else {
        await withWallet(view.binding.owner_wallet, async (w, _addr, acct) => {
          const { transaction } = await ownerTransaction(passId, action, minor);
          try {
            await signAndSend(w, acct, transaction, view.binding.network);
          } catch (e) {
            throw new Error(walletFailure(e, w.name, view.binding.network));
          }
        });
      }
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

  // A pass that can't spend isn't looked up with the page: the person asks.
  if (view === undefined && !lookup) {
    return (
      <button type="button" onClick={() => setPeek(true)} className="mt-3 text-xs font-medium text-muted underline decoration-border-strong underline-offset-4 hover:text-foreground">
        Check on-chain vault
      </button>
    );
  }
  if (view === undefined || nets === null) return null;
  if (view === null && (!active || nets.length === 0)) return null;

  const held = account ? balances?.wallets.find((w) => w.address === account.address) : undefined;
  const createDisabled = busy !== "" || (signWithAccount ? !accountSigner : wallets.length === 0);

  return (
    <div className="mt-4 rounded-lg border border-border bg-surface p-4">
      {account && privyAppId && <AccountWalletSigner appId={privyAppId} email={user?.email} address={account.address} onSigner={setAccountSigner} />}
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

      {net && net.signer_ready === false && (
        <p className="mt-2 rounded-md bg-danger-tint px-3 py-2 text-xs leading-snug text-danger" role="status">
          Algebra&apos;s {networkLabel(net.network)} payer is out of SOL for network fees, so a vault can&apos;t fund payments right now. It is topped up from the faucet
          automatically; try again in a little while.
        </p>
      )}

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
              <Button onClick={create} disabled={createDisabled}>
                {busy === "create" || (signWithAccount && !accountSigner) ? <Spinner size={13} /> : <IconWallet size={14} />}{" "}
                {signWithAccount ? "Create with my account wallet" : "Create with my browser wallet"}
              </Button>
            </div>
          ) : (
            <p className="mt-2 text-xs text-muted">Switch the console to {nets.map((n) => networkLabel(n.network)).join(" or ")} to put this pass on chain.</p>
          )}
          {net && account && (
            <div className="mt-3 rounded-lg bg-primary-tint/40 px-3 py-2.5 text-xs leading-relaxed text-muted">
              {signWithAccount ? (
                <>
                  <p>
                    Paid from your account wallet <span className="font-mono text-foreground">{short(account.address)}</span>
                    {held?.ok ? (
                      <>
                        {" "}
                        · holds <span className="font-mono text-foreground">{formatUSDC(held.usdc_minor)}</span> and{" "}
                        <span className="font-mono text-foreground">{(held.sol_lamports / 1e9).toLocaleString("en-US", { maximumFractionDigits: 4 })} SOL</span>{" "}
                        on {networkLabel(net.network)}
                      </>
                    ) : null}
                    . Privy asks you to confirm it&apos;s you (your email code), then shows you the transaction to approve.
                  </p>
                  <p className="mt-1">
                    It needs the USDC you put in and a little SOL for fees. To fund it, hover your wallet at the top right for its address and QR code.
                  </p>
                  {wallets.length > 0 && (
                    <button type="button" className="mt-1 text-primary underline" onClick={() => setUseBrowserWallet(true)}>
                      Use a browser wallet ({wallets.map((w) => w.name).join(", ")}) instead
                    </button>
                  )}
                </>
              ) : (
                <>
                  <p>Signed by the Solana wallet in this browser, which must be set to {networkLabel(net.network)} and hold the USDC and a little SOL there.</p>
                  <button type="button" className="mt-1 text-primary underline" onClick={() => setUseBrowserWallet(false)}>
                    Use my account wallet {short(account.address)} instead
                  </button>
                </>
              )}
            </div>
          )}
          {!signWithAccount && wallets.length === 0 && net && (
            <p className="mt-2 text-xs text-muted">No Solana wallet found in this browser. Install Phantom, Solflare or Backpack, then reload.</p>
          )}
          {net && (
            <>
            <p className="mt-2 text-xs text-muted">
              Program{" "}
              <a className="text-primary" href={explorerAddress(net.program_id, net.network)} target="_blank" rel="noopener noreferrer">
                {short(net.program_id)}
              </a>{" "}
              · can only pay out to Algebra&apos;s settlement account, which pays providers (not a wallet you fund)
            </p>
            {!account && (
              <p className="mt-1.5 text-xs leading-relaxed text-muted">
                Signed by the Solana wallet in this browser (Phantom, Solflare or Backpack), which must be set to {networkLabel(net.network)} and hold the USDC
                and a little SOL there.
              </p>
            )}
            </>
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
