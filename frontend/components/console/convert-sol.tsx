"use client";

// Convert SOL to USDC from the wallet menu, without leaving Algebra.
//
// Mainnet: a Jupiter swap (lite-api.jup.ag, the public Swap API). Jupiter
// quotes the best route and builds the transaction for the person's own
// wallet; the person's wallet (Privy's, for an email sign-in) shows it and
// signs it. Algebra never holds the SOL or the USDC, and never signs.
//
// Devnet: there is no market for Circle's test USDC, so no swap can exist.
// The honest shortcut is the faucet, with the address already copied.

import { useEffect, useRef, useState } from "react";
import type { AccountSigner } from "./account-wallet-signer";
import { solscanTx } from "@/lib/explorer";
import { IconExternal, IconRefresh, Spinner } from "@/components/icons";

const SOL_MINT = "So11111111111111111111111111111111111111112";
const USDC_MINT = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v";
const JUP = "https://lite-api.jup.ag/swap/v1";
const SLIPPAGE_BPS = 50;
// Left in the wallet for fees and the new USDC account's rent.
const KEEP_LAMPORTS = 10_000_000;

type Quote = {
  inAmount: string;
  outAmount: string;
  otherAmountThreshold: string;
  priceImpactPct: string;
  routePlan: { swapInfo: { label?: string } }[];
};

const usdc = (atoms: string | number) => (Number(atoms) / 1e6).toLocaleString("en-US", { maximumFractionDigits: 4 });

export function ConvertSol({
  address,
  network,
  solLamports,
  signer,
  wantSigner,
  onBusy,
  onDone,
  copyAddress,
}: {
  address: string;
  network: string;
  /** What the wallet holds, when known. */
  solLamports?: number;
  /** The account wallet's signer, once Privy has loaded (see wantSigner). */
  signer: AccountSigner | null;
  /** Asks the menu to load Privy's signer. */
  wantSigner: () => void;
  /** True while a swap is being signed: the menu stays open through Privy's window. */
  onBusy: (busy: boolean) => void;
  onDone: () => void;
  copyAddress: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [amount, setAmount] = useState("");
  const [quote, setQuote] = useState<Quote | null>(null);
  const [quoting, setQuoting] = useState(false);
  const [state, setState] = useState<"idle" | "signing" | "done">("idle");
  const [error, setError] = useState("");
  const [tx, setTx] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const lamports = Math.floor(Number(amount) * 1e9);
  const max = solLamports != null ? Math.max(0, solLamports - KEEP_LAMPORTS) : undefined;
  const tooMuch = max != null && lamports > max;

  // A fresh quote half a second after the amount stops changing.
  useEffect(() => {
    if (network !== "solana" || !open) return;
    if (timer.current) clearTimeout(timer.current);
    if (!(lamports > 0) || tooMuch) return;
    timer.current = setTimeout(async () => {
      setQuoting(true);
      setError("");
      try {
        const r = await fetch(`${JUP}/quote?inputMint=${SOL_MINT}&outputMint=${USDC_MINT}&amount=${lamports}&slippageBps=${SLIPPAGE_BPS}&restrictIntermediateTokens=true`);
        if (!r.ok) throw new Error(`Jupiter answered ${r.status}`);
        setQuote((await r.json()) as Quote);
      } catch (e) {
        setQuote(null);
        setError(e instanceof Error ? `Couldn't get a price: ${e.message}` : "Couldn't get a price");
      } finally {
        setQuoting(false);
      }
    }, 500);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [lamports, tooMuch, network, open]);

  // Only a quote for exactly the amount typed now counts.
  const live = quote && lamports > 0 && !tooMuch && quote.inAmount === String(lamports) ? quote : null;

  async function convert() {
    const quote = live;
    if (!quote) return;
    if (!signer) {
      wantSigner();
      setError("Loading your wallet… press Convert again in a moment.");
      return;
    }
    setState("signing");
    setError("");
    onBusy(true);
    try {
      const r = await fetch(`${JUP}/swap`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ quoteResponse: quote, userPublicKey: address, wrapAndUnwrapSol: true, dynamicComputeUnitLimit: true, prioritizationFeeLamports: "auto" }),
      });
      const body = (await r.json()) as { swapTransaction?: string; error?: string };
      if (!r.ok || !body.swapTransaction) throw new Error(body.error ?? `Jupiter couldn't build the swap (${r.status})`);
      const sig = await signer.signAndSend(body.swapTransaction, "solana");
      setTx(sig);
      setState("done");
      setAmount("");
      setQuote(null);
      onDone();
    } catch (e) {
      const said = e instanceof Error ? e.message : String(e);
      setError(/reject|denied|cancel|closed/i.test(said) ? "Cancelled. Nothing was swapped." : said);
      setState("idle");
    } finally {
      onBusy(false);
    }
  }

  if (network !== "solana") {
    return (
      <div className="mt-3 rounded-xl border border-border px-3 py-2.5">
        <p className="text-xs font-semibold text-foreground">Need USDC?</p>
        <p className="mt-1 text-[0.6875rem] leading-relaxed text-muted">
          Devnet USDC is Circle&apos;s test token: nothing trades it, so it can&apos;t be swapped from SOL. Circle gives it away instead. Swaps from SOL work on
          mainnet.
        </p>
        <button
          type="button"
          onClick={() => {
            copyAddress();
            window.open("https://faucet.circle.com", "_blank", "noopener,noreferrer");
          }}
          className="mt-2 inline-flex h-8 w-full items-center justify-center gap-1.5 rounded-lg border border-border-strong text-xs font-medium text-foreground hover:bg-primary-tint/60"
        >
          Copy address and get test USDC <IconExternal size={11} />
        </button>
      </div>
    );
  }

  return (
    <div className="mt-3 rounded-xl border border-border px-3 py-2.5">
      {!open ? (
        <button type="button" onClick={() => (setOpen(true), wantSigner())} className="flex w-full items-center justify-between text-left">
          <span>
            <span className="block text-xs font-semibold text-foreground">Convert SOL to USDC</span>
            <span className="block text-[0.6875rem] text-muted">Best price across Solana, via Jupiter. You sign it.</span>
          </span>
          <IconRefresh size={14} className="text-muted" />
        </button>
      ) : (
        <div>
          <p className="text-xs font-semibold text-foreground">Convert SOL to USDC</p>
          <div className="mt-2 flex items-center gap-1.5">
            <input
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              inputMode="decimal"
              placeholder="0.1"
              aria-label="SOL to convert"
              className="min-w-0 flex-1 rounded-lg border border-border-strong bg-surface px-2.5 py-1.5 font-mono text-xs text-foreground"
            />
            <span className="text-xs text-muted">SOL</span>
            {max != null && max > 0 && (
              <button type="button" onClick={() => setAmount(String(max / 1e9))} className="rounded-md px-1.5 py-1 text-[0.6875rem] font-medium text-primary hover:bg-primary-tint/60">
                Max
              </button>
            )}
          </div>
          {max === 0 ? (
            <p className="mt-1.5 text-[0.6875rem] text-muted">No SOL here yet to convert. Send SOL to the address above first.</p>
          ) : (
            tooMuch && <p className="mt-1.5 text-[0.6875rem] text-danger">Keep at least 0.01 SOL for fees.</p>
          )}
          <p className="mt-1.5 min-h-[1rem] text-[0.6875rem] text-muted">
            {quoting ? (
              <span className="inline-flex items-center gap-1">
                <Spinner size={10} /> Getting the best price…
              </span>
            ) : live ? (
              <>
                ≈ <span className="font-mono text-foreground">{usdc(live.outAmount)} USDC</span> · at least {usdc(live.otherAmountThreshold)} (0.5% slippage)
                {live.routePlan[0]?.swapInfo.label ? ` · via ${live.routePlan.map((r) => r.swapInfo.label).filter(Boolean).slice(0, 2).join(", ")}` : ""}
              </>
            ) : null}
          </p>
          <button
            type="button"
            onClick={convert}
            disabled={!live || state === "signing"}
            className="mt-1.5 inline-flex h-8 w-full items-center justify-center gap-1.5 rounded-lg bg-primary text-xs font-medium text-primary-tint hover:opacity-90 disabled:opacity-40"
          >
            {state === "signing" && <Spinner size={12} />} {state === "signing" ? "Approve it in your wallet…" : "Convert"}
          </button>
          <p className="mt-1.5 text-[0.6875rem] leading-relaxed text-muted">Real SOL and real USDC on Solana mainnet. The swap goes from your wallet to your wallet; Algebra never holds either.</p>
        </div>
      )}
      {error && <p className="mt-1.5 text-[0.6875rem] text-danger">{error}</p>}
      {state === "done" && tx && (
        <a href={solscanTx(tx, "solana")} target="_blank" rel="noopener noreferrer" className="mt-1.5 inline-flex items-center gap-1 text-[0.6875rem] font-medium text-primary underline">
          Converted. See it on Solscan <IconExternal size={10} />
        </a>
      )}
    </div>
  );
}
