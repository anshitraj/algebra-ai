"use client";

// The airdrop, as one piece of interface: the button that sends a person test
// SOL and test USDC on devnet, or what to see instead (their one airdrop is
// spent, their wallet already holds enough, this server has no faucet). Used
// by the wallet menu, the Quickstart guide and Get started, over useAirdrop.

import Link from "next/link";
import { solscanTx } from "@/lib/explorer";
import { formatUSDC } from "@/lib/money";
import { useSession } from "@/lib/session";
import { solText, useAirdrop, type Airdrop } from "@/lib/use-airdrop";
import { Button } from "@/components/console/ui";
import { IconCheck, IconExternal, Spinner } from "@/components/icons";

/** The public faucets, for a server with none of its own, or for more than the one airdrop. */
function PublicFaucets({ lead }: { lead: string }) {
  return (
    <>
      {lead}{" "}
      <a className="font-medium text-primary hover:underline" href="https://faucet.solana.com" target="_blank" rel="noopener noreferrer">
        faucet.solana.com
      </a>{" "}
      (SOL) and{" "}
      <a className="font-medium text-primary hover:underline" href="https://faucet.circle.com" target="_blank" rel="noopener noreferrer">
        faucet.circle.com
      </a>{" "}
      (USDC).
    </>
  );
}

/**
 * What to do about test money, right now. compact is the wallet menu's small
 * version. airdrop is the hook's state (one per screen, so a screen that also
 * shows the wallet's balance reads it once).
 */
export function AirdropAction({ airdrop, compact = false }: { airdrop: Airdrop; compact?: boolean }) {
  const { faucet, address, funded, received, busy, error, drop } = airdrop;
  const text = compact ? "text-[0.6875rem] leading-relaxed" : "text-sm leading-relaxed";

  if (faucet === undefined) return <div className={`animate-pulse rounded-md bg-border/60 ${compact ? "h-9 w-full" : "mt-3 h-10 w-48"}`} />;

  if (received) {
    const what = [received.sol_lamports ? `${solText(received.sol_lamports)} SOL` : "", received.usdc_minor ? formatUSDC(received.usdc_minor) : ""].filter(Boolean).join(" and ");
    return (
      <div className={`${compact ? "" : "mt-3"} ${text} text-foreground`}>
        <p className="flex items-start gap-1.5">
          <IconCheck size={compact ? 12 : 15} className="mt-0.5 shrink-0 text-primary" />
          <span>
            You got your airdrop{what ? `: ${what}` : ""}. It is one per account.{" "}
            {received.signature && (
              <a href={solscanTx(received.signature, "solana-devnet")} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-medium text-primary hover:underline">
                View transaction <IconExternal size={compact ? 10 : 12} />
              </a>
            )}
          </span>
        </p>
        <p className={`mt-1.5 text-muted ${compact ? "" : "text-xs"}`}>
          <PublicFaucets lead="Need more? The public faucets give more:" />
        </p>
      </div>
    );
  }

  if (!faucet?.enabled) {
    return (
      <p className={`${compact ? "" : "mt-3"} ${text} text-muted`}>
        <PublicFaucets lead="This server has no airdrop of its own. Send your address devnet SOL and USDC at" />
      </p>
    );
  }

  if (funded) {
    return (
      <p className={`${compact ? "" : "mt-3"} ${text} text-foreground`}>
        <IconCheck size={compact ? 12 : 15} className="mr-1.5 inline text-primary" />
        Your wallet already holds enough test money, so you keep your airdrop.
      </p>
    );
  }

  return (
    <div className={compact ? "" : "mt-3"}>
      <Button onClick={drop} disabled={busy || !address} className={compact ? "min-h-9 w-full px-3 py-1.5 text-xs" : ""}>
        {busy && <Spinner size={13} />} Get airdrop: {solText(faucet.sol_lamports ?? 0)} SOL + {formatUSDC(faucet.usdc_minor ?? 0)}
      </Button>
      <p className={`mt-1.5 text-muted ${compact ? "text-[0.6875rem]" : "text-xs"}`}>
        {busy ? "Sending on devnet: a few seconds." : "One airdrop per account, sent straight to your wallet. No address to copy."}
      </p>
      {error && (
        <p role="alert" className={`mt-1.5 text-danger ${compact ? "text-[0.6875rem]" : "text-xs"}`}>
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * The airdrop where the Quickstart page can't be missed: one bar above the guide,
 * so nobody has to finish the guide, or scroll, to find where test money comes
 * from. Nothing for a demo account (it has play money and no wallet).
 */
export function DevnetAirdropBar() {
  const { user } = useSession();
  const demo = !user || user.mode === "demo";
  const airdrop = useAirdrop(!demo);
  if (demo) return null;
  return (
    <section aria-label="Devnet airdrop" className="mb-6 flex flex-wrap items-start justify-between gap-x-8 gap-y-3 rounded-xl border border-border bg-surface px-5 py-4">
      <div className="min-w-0 flex-1 basis-64">
        <p className="text-sm font-semibold text-foreground">Devnet: free test money</p>
        <p className="mt-0.5 text-xs leading-relaxed text-muted">
          Test SOL and USDC sent to your wallet, so you can make a spending limit and pay for real on devnet. No address to copy.
        </p>
      </div>
      <div className="min-w-0 basis-72 [&>div]:mt-0">
        <AirdropAction airdrop={airdrop} compact />
      </div>
    </section>
  );
}

/**
 * The devnet step of the Quickstart guide, for someone who has finished the
 * sandbox run: the airdrop, then the spending limit and its vault. It reads
 * and sends through the same hook as the wallet menu.
 */
export function DevnetAirdropCard() {
  const airdrop = useAirdrop(true);
  const ready = !!airdrop.received || airdrop.funded;
  return (
    <div className="mt-6 rounded-xl border border-border bg-surface p-4 text-left">
      <p className="text-sm font-semibold text-foreground">Next: devnet, with free test money</p>
      <p className="mt-1 text-sm leading-relaxed text-muted">
        Get test SOL and USDC sent to your wallet, then make a spending limit with a vault on Solana, so your agent can pay for real on devnet.
      </p>
      <AirdropAction airdrop={airdrop} />
      <div className="mt-3">
        <Link
          href="/console/passes"
          className={`inline-flex items-center gap-1.5 text-sm font-medium hover:underline ${ready ? "text-primary" : "text-muted"}`}
        >
          Make a spending limit and its vault
        </Link>
      </div>
    </div>
  );
}
