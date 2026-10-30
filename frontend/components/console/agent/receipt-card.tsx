"use client";

// A paid call's receipt, in the chat, the moment the payment settles: what was
// paid and to whom, on which network, the transaction on both Solana
// explorers, and the receipt itself: a PDF to download and the signed page
// anyone can verify. A sandbox or devnet payment says it is test money. When a
// Spend Pass vault paid, a line says so, and the exact addresses and both
// transactions sit under "Signing and funding details".

import { useEffect, useState } from "react";
import Link from "next/link";
import * as api from "@/lib/api-client";
import type { ReceiptInfo } from "@/lib/agent/events";
import { agoText } from "@/lib/agent/steps";
import { explorerAddress, explorerTx, solscanTx } from "@/lib/explorer";
import { networkLabel } from "@/lib/network";
import type { IntentReceiptClaims } from "@/lib/types";
import { IconCheck, IconChevronDown, IconExternal, IconReceipt } from "@/components/icons";
import styles from "./delivery.module.css";

function short(s: string, n = 8) {
  return s.length > n * 2 + 1 ? `${s.slice(0, n)}…${s.slice(-n)}` : s;
}

/** The signed receipt's settlement, once it can be read: it names the vault that paid. Silent if it can't be. */
function useSettlement(intentId: string, enabled: boolean) {
  const [s, setS] = useState<IntentReceiptClaims["settlement"] | null>();
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    (async () => {
      try {
        const i = await api.getMyEconomicIntent(intentId);
        if (!i.receipt) { if (live) setS(null); return; }
        const v = await api.verifyReceipt(i.receipt);
        if (live) setS(v.valid ? v.claims?.settlement ?? null : null);
      } catch {
        if (live) setS(null);
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
  const [copyError, setCopyError] = useState(false);
  const [more, setMore] = useState(false);
  const net = r.network ? networkLabel(r.network) : "";
  const testMoney = r.test || r.network === "solana-devnet" || r.network === "sandbox";
  const onChain = r.network === "solana" || r.network === "solana-devnet";
  const settled = useSettlement(r.intentId, onChain && more);
  const funding = settled?.funding;
  return (
    <section className={styles.receipt} aria-label={`Receipt for ${r.provider}`}>
      <div className={styles.receiptHeader}>
        <span className={styles.receiptMark}>
          <IconReceipt size={18} />
        </span>
        <p>
          {r.reused ? "Reused an answer that cost " : "Paid "}
          <strong>{r.paid}</strong> {r.reused ? "from" : "to"} <Link href={`/console/providers/${encodeURIComponent(r.provider)}`}>{r.provider} <IconExternal size={10} /></Link>
          {r.reused ? <small>Paid {agoText(r.reused.ageSeconds)}, still fresh · nothing paid now</small> : net && <small>{r.network === "sandbox" ? "in demo mode" : `on ${net}`}</small>}
        </p>
        {testMoney && (
          <span className={styles.testBadge}>
            {r.network === "sandbox" ? "Play money" : "Test money"}
          </span>
        )}
      </div>
      <div className={styles.receiptBody}>
        <div className={styles.receiptLinks}>
        {r.transaction ? (
          <>
            <button
              type="button"
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(r.transaction ?? "");
                  setCopied(true);
                  setCopyError(false);
                  window.setTimeout(() => setCopied(false), 1500);
                } catch { setCopyError(true); }
              }}
              title={r.transaction}
              aria-label="Copy transaction signature"
            >
              <IconReceipt size={12} />Tx {copied ? <><IconCheck size={12} />Copied</> : <>{short(r.transaction)}<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" aria-hidden="true"><rect x="5" y="2" width="8" height="9" rx="1.5" /><path d="M10 12v2H2V5h2" /></svg></>}
            </button>
            {r.solscanUrl && (
              <a href={r.solscanUrl} target="_blank" rel="noopener noreferrer">
                Solscan <IconExternal size={11} />
              </a>
            )}
            {r.explorerUrl && (
              <a href={r.explorerUrl} target="_blank" rel="noopener noreferrer">
                Solana Explorer <IconExternal size={11} />
              </a>
            )}
          </>
        ) : null}
          <a
            href={r.pdfUrl}
            download={`algebra-receipt-${r.intentId}.pdf`}
          >
            <IconReceipt size={12} /> PDF receipt
          </a>
          <a href={r.pdfUrl} target="_blank" rel="noopener noreferrer">
            <IconExternal size={12} /> Open PDF
          </a>
          <a href={r.pageUrl}>
            <IconReceipt size={12} />Signed receipt
          </a>
        </div>
        {r.network === "sandbox" && <p className={styles.source}>Simulated payment · No chain transaction</p>}
        {copyError && <p className={styles.copyError} role="status">Couldn&apos;t copy. Transaction signature: <span>{r.transaction}</span></p>}
        {onChain && (
          <div className={styles.signing}>
            <button
              type="button"
              onClick={() => setMore((v) => !v)}
              aria-expanded={more}
              className="inline-flex items-center gap-1 text-xs font-medium text-muted hover:text-foreground"
            >
              <IconChevronDown size={13} className={`transition-transform ${more ? "rotate-180" : ""}`} />
              Signing and funding details
            </button>
            {more && <>
              {settled === undefined ? <p>Loading settlement details…</p> : !settled ? <p>Settlement details couldn&apos;t be loaded. The signed receipt is available above.</p> : null}
              <dl className="space-y-1.5 rounded-lg border border-border bg-primary-tint/40 px-3 py-2.5 text-[0.6875rem] leading-snug">
                {funding && <>
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
                </>}
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
            </>}
          </div>
        )}
      </div>
    </section>
  );
}
