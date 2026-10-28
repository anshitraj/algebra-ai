"use client";

import { useState } from "react";
import type { ReceiptInfo } from "@/lib/agent/events";
import { formatUSDC } from "@/lib/money";
import { networkLabel } from "@/lib/network";
import { responseForReceipt } from "@/lib/agent/result-view";
import { IconCheck, IconChevronDown } from "@/components/icons";
import { ReceiptCard } from "./receipt-card";
import { ResponseDetails, ResultCards, ResultSource } from "./result-cards";
import { Timeline, type Step } from "./timeline";
import styles from "./delivery.module.css";

export function DeliveryPanel({ receipts, steps, running }: { receipts: ReceiptInfo[]; steps: Step[]; running: boolean }) {
  const [open, setOpen] = useState(false);
  const total = receipts.reduce((sum, r) => sum + r.paidMinor, 0);
  const networks = [...new Set(receipts.map((r) => r.network).filter(Boolean))].map(networkLabel);
  const needsAttention = steps.some((step) => step.status === "error" || step.status === "blocked" || step.status === "waiting");
  const timed = steps.filter((s) => s.endedAt !== undefined);
  const elapsed = timed.length ? Math.max(...timed.map((s) => s.endedAt!)) - Math.min(...timed.map((s) => s.startedAt)) : receipts.reduce((sum, r) => sum + (r.elapsedMs ?? 0), 0);
  // Older saved chats have the response only in their trace. Bind by intent
  // ID, never by provider name or array position (one provider can run twice).
  const results = receipts.flatMap((receipt) => {
    const response = responseForReceipt(receipt, steps);
    return response !== undefined ? [{ receipt, response }] : [];
  });
  return <section className={styles.delivery} aria-label="Delivered API results">
    <button type="button" className={styles.deliveredBar} aria-expanded={open} title="Delivery activity and response details" onClick={() => setOpen((v) => !v)}>
      <span className={styles.deliveredCheck}><IconCheck size={15} strokeWidth={2.5} /></span>
      <span><strong>{receipts.length === 1 ? "Delivered" : `${receipts.length} calls delivered`}</strong><span className={styles.deliveryMeta}> · {formatUSDC(total)}{networks.length ? ` on ${networks.join(", ")}` : ""}{elapsed > 0 ? ` · ${(elapsed / 1000).toFixed(1)}s` : ""}{running ? " · still working" : needsAttention ? " · other steps need attention" : ""}</span></span>
      <IconChevronDown size={14} className={open ? styles.expanded : ""} />
    </button>
    {open && <div className={styles.activity}><Timeline steps={steps} running={running} defaultOpen onAsk={undefined} />{results.map(({ receipt, response }) => <ResponseDetails key={receipt.intentId} receipt={receipt} response={response} />)}</div>}
    {receipts.map((receipt) => <ReceiptCard key={receipt.intentId} receipt={receipt} />)}
    <ResultCards results={results} />
    {results.map(({ receipt, response }) => <ResultSource key={receipt.intentId} response={response} />)}
    {!results.length && <p className={styles.source}>The call was delivered. Its response is not available in this saved chat; the receipt remains available above.</p>}
  </section>;
}
