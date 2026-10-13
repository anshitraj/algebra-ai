"use client";

// A paid call's receipt, in the chat, the moment the payment settles: what was
// paid and to whom, on which network, the transaction on both Solana
// explorers, and the receipt itself: a PDF to download and the signed page
// anyone can verify. A sandbox or devnet payment says it is test money.

import { useState } from "react";
import type { ReceiptInfo } from "@/lib/agent/events";
import { networkLabel } from "@/lib/network";
import { IconCheck, IconExternal, IconReceipt } from "@/components/icons";

function short(s: string, n = 8) {
  return s.length > n * 2 + 1 ? `${s.slice(0, n)}…${s.slice(-n)}` : s;
}

export function ReceiptCard({ receipt: r }: { receipt: ReceiptInfo }) {
  const [copied, setCopied] = useState(false);
  const net = r.network ? networkLabel(r.network) : "";
  const testMoney = r.test || r.network === "solana-devnet";
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
        <p className="text-[0.6875rem] leading-relaxed text-muted">
          Signed by Algebra (Ed25519). Anyone can check it at /verify. A receipt for this payment, not a tax invoice.
        </p>
      </div>
    </section>
  );
}
