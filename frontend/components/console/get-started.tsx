"use client";

// The first screen of Quickstart. Demo comes first (play money, no wallet:
// a real answer in seconds), then three steps from there to a paid call on
// Solana's test network, with no address to copy anywhere: 1) test SOL and USDC
// are dropped on the person's own wallet (one airdrop per account), 2) a starter
// pass and its vault, 3) ask the agent. Each step shows where the person is: what
// is done is ticked, what is next has its one button.

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import { formatUSDC } from "@/lib/money";
import { useNetwork } from "@/lib/network";
import { getOnchainPass } from "@/lib/onchain-pass";
import { useSession } from "@/lib/session";
import { solText, useAirdrop } from "@/lib/use-airdrop";
import type { SpendPass } from "@/lib/types";
import { Copy } from "@/components/console/snippet";
import { AirdropAction } from "@/components/console/airdrop-action";
import { Button, ErrorNote, Panel } from "@/components/console/ui";
import { IconCheck, Spinner } from "@/components/icons";

const short = (a: string) => (a.length > 12 ? `${a.slice(0, 4)}…${a.slice(-4)}` : a);

const TRY = "What's the weather in Lisbon and the SOL price? Under 0.01 USDC each";
// Demo mode serves token prices and token risk, so its example asks for one.
const TRY_PRACTICE = "What does SOL cost right now? Use the best provider";

export function GetStarted() {
  const { user } = useSession();
  const router = useRouter();
  const { setNetwork } = useNetwork();
  const demo = user?.mode === "demo";
  const airdrop = useAirdrop(!demo);
  const { address, held, funded } = airdrop;
  const [passes, setPasses] = useState<SpendPass[] | null>(null);
  const [vault, setVault] = useState<boolean | undefined>(undefined);
  const [busy, setBusy] = useState<"pass" | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadPasses = useCallback(() => {
    api
      .listPasses()
      .then((r) => setPasses(r.passes ?? []))
      .catch(() => setPasses([]));
  }, []);

  useEffect(() => {
    if (demo || !user) return;
    loadPasses();
  }, [demo, user, loadPasses]);

  // The pass that goes on chain: not the demo pass, which spends play money and has no vault to make.
  const pass = passes?.find((p) => p.active && p.currency === "USDC" && p.environment !== "sandbox") ?? null;
  useEffect(() => {
    if (!pass) return;
    getOnchainPass(pass.id)
      .then((v) => setVault(v.exists))
      .catch(() => setVault(false));
  }, [pass]);

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
        <h3 className="text-sm font-semibold text-foreground">You are in demo mode</h3>
        <p className="mt-1 text-sm leading-relaxed text-muted">
          The demo uses play money, so there is nothing to fund. To pay real sellers with test money from your own wallet, make an account with your email or an external wallet.{" "}
          <Link href="/signup" className="font-medium text-primary hover:underline">
            Create your account
          </Link>
        </p>
      </Panel>
    );
  }

  return (
    <div className="mt-8 space-y-4">
      {error && <ErrorNote>{error}</ErrorNote>}

      <Panel className="py-5">
        <h3 className="text-base font-semibold text-foreground">Try it first, in demo mode</h3>
        <p className="mt-1 text-sm leading-relaxed text-muted">
          Ask for a token price or a risk check and pay with play money. No wallet and no funds needed: you get a real answer with a signed receipt in seconds.
        </p>
        <div className="mt-3">
          <Button
            onClick={() => {
              setNetwork("sandbox");
              router.push(`/console/agent?prompt=${encodeURIComponent(TRY_PRACTICE)}`);
            }}
          >
            Open demo chat
          </Button>
        </div>
      </Panel>

      <h3 className="pt-4 text-sm font-semibold text-foreground">Then go real, with free test money</h3>

      <Card n={1} done={funded} title="Get your airdrop of test money" body="Test SOL for fees and test USDC to spend, sent straight to your wallet. One airdrop per account.">
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
          <p className="mt-3 text-sm text-muted">Your account has no wallet yet. Sign out and sign in with your email, or with an external wallet, to get one.</p>
        )}

        <AirdropAction airdrop={airdrop} />
      </Card>

      <Card
        n={2}
        done={vault === true}
        title="Make a spending limit and put test USDC in it"
        body="A pass is the limit your agent pays within; its vault holds the USDC it can spend. Nothing leaves your wallet without your approval."
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
              You approve it once. If you use Phantom, unlock it and turn on Testnet Mode first (Settings, Developer Settings).
            </p>
          </div>
        )}
      </Card>

      <Card n={3} title="Ask again, for real" body="Same kind of question, now paid from your vault on Solana's test network. It compares the sellers, you pick one, and you get a signed receipt with a Solscan link.">
        <div className="mt-3">
          <Link
            href={`/console/agent?prompt=${encodeURIComponent(TRY)}`}
            onClick={() => setNetwork("solana-devnet")}
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
          <h3 className="text-base font-semibold text-foreground">{title}</h3>
          <p className="mt-1 text-sm leading-relaxed text-muted">{body}</p>
          {children}
        </div>
      </div>
    </Panel>
  );
}
