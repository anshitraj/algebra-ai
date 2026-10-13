"use client";

// A paid call's receipt, in the chat, the moment the payment settles: what was
// paid and to whom, on which network, the transaction on both Solana
// explorers, and the receipt itself: a PDF to download and the signed page
// anyone can verify. A sandbox or devnet payment says it is test money. When a
// Spend Pass vault paid, a line says so, and the exact addresses and both
// transactions sit under "Signing and funding details".

import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { ReceiptInfo } from "@/lib/agent/events";
import { explorerAddress, explorerTx, solscanTx } from "@/lib/explorer";
import { networkLabel } from "@/lib/network";
import type { IntentReceiptClaims } from "@/lib/types";
import { IconCheck, IconChevronDown, IconExternal, IconReceipt } from "@/components/icons";

function short(s: string, n = 8) {
  return s.length > n * 2 + 1 ? `${s.slice(0, n)}…${s.slice(-n)}` : s;
}

/** The signed receipt's settlement, once it can be read: it names the vault that paid. Silent if it can't be. */
function useSettlement(intentId: string, enabled: boolean) {
  const [s, setS] = useState<IntentReceiptClaims["settlement"]>();
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    (async () => {
      try {
        const i = await api.getMyEconomicIntent(intentId);
        if (!i.receipt) return;
        const v = await api.verifyReceipt(i.receipt);
        if (live && v.valid) setS(v.claims?.settlement);
      } catch {
        /* the card stands without the details */
      }
    })();
    return () => {
      live = false;
    };
  }, [intentId, enabled]);
  return s;
}

function Detail({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5">
      <dt className="w-32 shrink-0 text-muted">{label}</dt>
      <dd className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 text-foreground">{children}</dd>
    </div>
  );
}

function Ext({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-medium text-primary hover:underline">
      {children} <IconExternal size={10} />
    </a>
  );
}

export function ReceiptCard({ receipt: r }: { receipt: ReceiptInfo }) {
  const [copied, setCopied] = useState(false);
  const [more, setMore] = useState(false);
  const net = r.network ? networkLabel(r.network) : "";
  const testMoney = r.test || r.network === "solana-devnet";
  const onChain = r.network === "solana" || r.network === "solana-devnet";
  const settled = useSettlement(r.intentId, onChain);
  const funding = settled?.funding;
  return (
    <section className="rounded-2xl border border-primary/30 bg-surface" aria-label="Receipt">
      <div className="flex flex-wrap items-center gap-2.5 border-b border-border px-4 py-3">
        <span className="inline-flex h-6 w-6 items-center justify-center rounded-full bg-primary text-primary-tint">
          <IconCheck size={13} strokeWidth={3} />
        </span>
        <p className="text-sm text-foreground">
          Paid <span className="font-mono font-semibold tabular-nums">{r.paid}</span> to <span className="font-mono">{r.provider}</span>
          {net ? ` on ${net}` : ""}
        </p>
        {testMoney && <span className="ml-auto rounded-full border border-dashed border-border-strong px-2 py-0.5 text-[0.6875rem] text-muted">test money</span>}
      </div>
      <div className="space-y-2.5 px-4 py-3 text-sm">
        {r.transaction ? (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
            <span className="text-xs text-muted">Transaction</span>
            <button
              type="button"
              onClick={() => {
                void navigator.clipboard?.writeText(r.transaction ?? "").then(() => {
                  setCopied(true);
                  window.setTimeout(() => setCopied(false), 1500);
                });
              }}
              title="Copy the signature"
              className="font-mono text-xs text-foreground hover:underline"
            >
              {copied ? "Copied" : short(r.transaction)}
            </button>
            {r.solscanUrl && (
              <a href={r.solscanUrl} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline">
                Solscan <IconExternal size={11} />
              </a>
            )}
            {r.explorerUrl && (
              <a href={r.explorerUrl} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline">
                Solana Explorer <IconExternal size={11} />
              </a>
            )}
            {!r.explorerUrl && <span className="text-xs text-muted">simulated: no chain transaction</span>}
          </div>
        ) : null}
        <div className="flex flex-wrap items-center gap-2">
          <a
            href={r.pdfUrl}
            download={`algebra-receipt-${r.intentId}.pdf`}
            className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-primary px-3 text-xs font-semibold text-primary-tint hover:opacity-90"
          >
            <IconReceipt size={14} /> Download PDF receipt
          </a>
          <a href={r.pdfUrl} target="_blank" rel="noopener noreferrer" className="inline-flex h-8 items-center gap-1 rounded-lg px-2.5 text-xs font-medium text-foreground hover:bg-primary-tint">
            Open PDF <IconExternal size={11} />
          </a>
          <a href={r.pageUrl} className="inline-flex h-8 items-center gap-1 rounded-lg px-2.5 text-xs font-medium text-foreground hover:bg-primary-tint">
            Signed receipt
          </a>
        </div>
        {funding && (
          <div className="space-y-1.5">
            <p className="text-xs text-foreground">
              Paid from your Spend Pass vault, signed by Algebra. Anything unspent went back to the vault.
            </p>
            <button
              type="button"
              onClick={() => setMore((v) => !v)}
              aria-expanded={more}
              className="inline-flex items-center gap-1 text-xs font-medium text-muted hover:text-foreground"
            >
              <IconChevronDown size={13} className={`transition-transform ${more ? "rotate-180" : ""}`} />
              Signing and funding details
            </button>
            {more && (
              <dl className="space-y-1.5 rounded-lg border border-border bg-primary-tint/40 px-3 py-2.5 text-[0.6875rem] leading-snug">
                <Detail label="Vault">
                  <span className="font-mono break-all">{funding.vault}</span>
                  <Ext href={explorerAddress(funding.vault, r.network)}>Explorer</Ext>
                </Detail>
                <Detail label="Vault owner">
                  <span className="font-mono break-all">{funding.owner}</span>
                  <span className="text-muted">your wallet</span>
                </Detail>
                <Detail label="Vault transaction">
                  <span className="font-mono break-all">{short(funding.pull_signature, 10)}</span>
                  <Ext href={solscanTx(funding.pull_signature, r.network)}>Solscan</Ext>
                  <Ext href={explorerTx(funding.pull_signature, r.network)}>Explorer</Ext>
                </Detail>
                {settled?.payer && (
                  <Detail label="Payment signed by">
                    <span className="font-mono break-all">{settled.payer}</span>
                    <span className="text-muted">Algebra&apos;s payer key</span>
                  </Detail>
                )}
                {settled?.pay_to && (
                  <Detail label="Paid to">
                    <span className="font-mono break-all">{settled.pay_to}</span>
                  </Detail>
                )}
              </dl>
            )}
          </div>
        )}
        <p className="text-[0.6875rem] leading-relaxed text-muted">
          Signed by Algebra (Ed25519). Anyone can check it at /verify. A receipt for this payment, not a tax invoice.
        </p>
      </div>
    </section>
  );
}
