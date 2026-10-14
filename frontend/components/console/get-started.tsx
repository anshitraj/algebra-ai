"use client";

// The first screen of Quickstart: three steps from an empty account to a paid
// call on devnet, with no address to copy anywhere. 1) devnet SOL and USDC are
// dropped on the person's own wallet, 2) a starter pass and its vault, 3) ask
// the agent. Each step shows where the person is: what is done is ticked, what
// is next has its one button.

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import { solscanTx } from "@/lib/explorer";
import { formatUSDC } from "@/lib/money";
import { getOnchainPass } from "@/lib/onchain-pass";
import { useSession } from "@/lib/session";
import type { FaucetDrop, FaucetStatus, SpendPass, WalletBalance } from "@/lib/types";
import { Copy } from "@/components/console/snippet";
import { Button, ErrorNote, Panel } from "@/components/console/ui";
import { IconCheck, Spinner } from "@/components/icons";

const short = (a: string) => (a.length > 12 ? `${a.slice(0, 4)}…${a.slice(-4)}` : a);
const solText = (lamports: number) => (lamports / 1e9).toLocaleString("en-US", { maximumFractionDigits: 3 });

// What a person needs to put USDC in a vault: a little SOL for fees and rent, and USDC to lock.
const ENOUGH_SOL = 20_000_000; // 0.02 SOL
const ENOUGH_USDC = 1_000_000; // 1 USDC

const TRY = "What's the weather in Lisbon and the SOL price? Under 0.01 USDC each";

export function GetStarted() {
  const { user } = useSession();
  const demo = user?.mode === "demo";
  const [faucet, setFaucet] = useState<FaucetStatus | null | undefined>(undefined);
  const [wallets, setWallets] = useState<WalletBalance[] | null>(null);
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [vault, setVault] = useState<boolean | undefined>(undefined);
  const [busy, setBusy] = useState<"drop" | "pass" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState<FaucetDrop | null>(null);

  const loadWallets = useCallback((force = false) => {
    api
      .myWallets("solana-devnet", force)
      .then((v) => setWallets(v.wallets))
      .catch(() => {});
  }, []);
  const loadPasses = useCallback(() => {
    api
      .listPasses()
      .then((r) => setPasses(r.passes ?? []))
      .catch(() => setPasses([]));
  }, []);

  useEffect(() => {
    if (demo || !user) return;
    api
      .getFaucet()
      .then(setFaucet)
      .catch(() => setFaucet(null));
    loadWallets();
    loadPasses();
  }, [demo, user, loadWallets, loadPasses]);

  const pass = passes?.find((p) => p.active && p.currency === "USDC") ?? null;
  useEffect(() => {
    if (!pass) return;
    getOnchainPass(pass.id)
      .then((v) => setVault(v.exists))
      .catch(() => setVault(false));
  }, [pass]);

  const address = faucet?.wallet ?? wallets?.[0]?.address ?? user?.wallets?.find((w) => w.chain === "solana")?.address;
  const held = wallets?.find((w) => w.address === address) ?? null;
  const funded = !!held?.ok && held.sol_lamports >= ENOUGH_SOL && held.usdc_minor >= ENOUGH_USDC;

  async function drop() {
    setBusy("drop");
    setError(null);
    try {
      const r = await api.requestFaucet(address);
      setSent(r.drop);
      // The chain needs a moment before the wallet shows it.
      window.setTimeout(() => loadWallets(true), 2500);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The faucet didn't answer. Try again in a minute.");
    } finally {
      setBusy(null);
    }
  }

  async function makePass() {
    setBusy("pass");
    setError(null);
    try {
      await api.createPass({
        label: "Starter pass",
        agent_kind: "custom",
        currency: "USDC",
        budget_minor_units: 1_000_000,
        budget_period: "total",
        max_per_purchase_minor_units: 100_000,
        approve_above_minor_units: 50_000,
        allowed_categories: ["digital_services"],
        expires_in_days: 30,
      });
      loadPasses();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't make the pass. Try again.");
    } finally {
      setBusy(null);
    }
  }

  if (demo) {
    return (
      <Panel className="mt-8">
        <h2 className="text-sm font-semibold text-foreground">You are in the demo</h2>
        <p className="mt-1 text-sm leading-relaxed text-muted">
          The demo pays with simulated USDC in the sandbox: nothing to fund. To pay on devnet with a wallet, sign in with an email or a Solana wallet.{" "}
          <Link href="/login?mode=production" className="font-medium text-primary hover:underline">
            Sign in
          </Link>
        </p>
      </Panel>
    );
  }

  return (
    <div className="mt-8 space-y-4">
      {error && <ErrorNote>{error}</ErrorNote>}

      <Card n={1} done={funded} title="Get test funds" body="Free devnet SOL for fees and USDC to spend, sent straight to your wallet. Once a day.">
        {address ? (
          <p className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
            <span className="text-muted">Your wallet</span>
            <span className="font-mono text-foreground" title={address}>
              {short(address)}
            </span>
            <Copy text={address} label="Copy" />
            {held?.ok ? (
              <span className="text-muted">
                holds <span className="font-mono text-foreground">{solText(held.sol_lamports)} SOL</span> and{" "}
                <span className="font-mono text-foreground">{formatUSDC(held.usdc_minor)}</span>
              </span>
            ) : null}
          </p>
        ) : (
          <p className="mt-3 text-sm text-muted">Your account has no Solana wallet yet. Sign out and sign in with an email or a wallet to get one.</p>
        )}

        {faucet?.enabled ? (
          <div className="mt-3">
            <Button onClick={drop} disabled={busy !== null || !address || funded}>
              {busy === "drop" && <Spinner size={13} />}{" "}
              {funded ? "You have enough" : `Get ${solText(faucet.sol_lamports ?? 0)} SOL + ${formatUSDC(faucet.usdc_minor ?? 0)}`}
            </Button>
            {busy === "drop" && <p className="mt-2 text-xs text-muted">Sending on devnet: a few seconds.</p>}
            {sent && (
              <p className="mt-2 text-sm text-foreground">
                Sent{sent.sol_lamports ? ` ${solText(sent.sol_lamports)} SOL` : ""}
                {sent.sol_lamports && sent.usdc_minor ? " and" : ""}
                {sent.usdc_minor ? ` ${formatUSDC(sent.usdc_minor)}` : ""}.{" "}
                <a href={solscanTx(sent.signature, "solana-devnet")} target="_blank" rel="noopener noreferrer" className="font-medium text-primary hover:underline">
                  See it on Solscan
                </a>
              </p>
            )}
          </div>
        ) : faucet === undefined ? (
          <div className="mt-3 h-10 w-48 animate-pulse rounded-md bg-border/60" />
        ) : (
          <p className="mt-3 text-sm leading-relaxed text-muted">
            This server has no faucet of its own. Send your address devnet SOL at{" "}
            <a className="font-medium text-primary hover:underline" href="https://faucet.solana.com" target="_blank" rel="noopener noreferrer">
              faucet.solana.com
            </a>{" "}
            and devnet USDC at{" "}
            <a className="font-medium text-primary hover:underline" href="https://faucet.circle.com" target="_blank" rel="noopener noreferrer">
              faucet.circle.com
            </a>
            .
          </p>
        )}
      </Card>

      <Card
        n={2}
        done={vault === true}
        title="Make a pass and put USDC in its vault"
        body="A pass is the limit your agent pays within; its vault holds the USDC it can spend. Nothing leaves your wallet without your signature."
      >
        {passes === null ? (
          <div className="mt-3 h-10 w-48 animate-pulse rounded-md bg-border/60" />
        ) : !pass ? (
          <div className="mt-3">
            <Button onClick={makePass} disabled={busy !== null}>
              {busy === "pass" && <Spinner size={13} />} Create a starter pass
            </Button>
            <p className="mt-2 text-xs text-muted">1 USDC in total, at most 0.10 a call, asks you above 0.05. You can change it any time.</p>
          </div>
        ) : vault === true ? (
          <p className="mt-3 text-sm text-foreground">
            <strong>{pass.label}</strong> has its vault on Solana.{" "}
            <Link href={`/console/passes#pass-${encodeURIComponent(pass.id)}`} className="font-medium text-primary hover:underline">
              Open it
            </Link>
          </p>
        ) : (
          <div className="mt-3">
            <Link
              href={`/console/passes#pass-${encodeURIComponent(pass.id)}`}
              className="console-button inline-flex min-h-10 items-center justify-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-tint transition-opacity hover:opacity-90"
            >
              Create the vault for “{pass.label}”
            </Link>
            <p className="mt-2 text-xs text-muted">
              Your wallet asks you to approve it once. Phantom: unlock it and set it to Devnet first (Settings, Developer Settings, Testnet Mode).
            </p>
          </div>
        )}
      </Card>

      <Card n={3} title="Ask the agent" body="Ask for work, not a provider. It compares the providers, you pick one or let it pay within your pass, and you get a signed receipt with a Solscan link.">
        <div className="mt-3">
          <Link
            href={`/console/agent?prompt=${encodeURIComponent(TRY)}`}
            className="console-button inline-flex min-h-10 items-center justify-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-tint transition-opacity hover:opacity-90"
          >
            Open the chat with an example
          </Link>
        </div>
      </Card>
    </div>
  );
}

function Card({ n, title, body, done, children }: { n: number; title: string; body: string; done?: boolean; children?: React.ReactNode }) {
  return (
    <Panel className="py-5">
      <div className="flex gap-4">
        <span className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-sm font-semibold ${done ? "bg-primary text-primary-tint" : "bg-border text-muted"}`}>
          {done ? <IconCheck size={15} /> : n}
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="text-base font-semibold text-foreground">{title}</h2>
          <p className="mt-1 text-sm leading-relaxed text-muted">{body}</p>
          {children}
        </div>
      </div>
    </Panel>
  );
}
