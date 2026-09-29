"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import * as api from "@/lib/api-client";
import type { ReceiptVerification } from "@/lib/types";
import { merchantLabel } from "@/lib/agent/steps";
import { IconCheck, IconX, Spinner } from "@/components/icons";

const money = (minor: number, currency = "INR") =>
  `${currency === "INR" ? "₹" : `${currency} `}${(minor / 100).toLocaleString("en-IN", { maximumFractionDigits: 2 })}`;

const when = (unix: number) => new Date(unix * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });

export function VerifyReceipt() {
  const params = useSearchParams();
  const linked = params.get("r");
  const [text, setText] = useState(() => linked ?? "");
  const [busy, setBusy] = useState(!!linked);
  const [result, setResult] = useState<ReceiptVerification | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function check(receipt: string) {
    if (!receipt.trim()) return;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      setResult(await api.verifyReceipt(receipt.trim()));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't check that receipt");
    } finally {
      setBusy(false);
    }
  }

  // A link like /verify?r=<receipt> checks straight away.
  useEffect(() => {
    if (!linked) return;
    api
      .verifyReceipt(linked.trim())
      .then(setResult)
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't check that receipt"))
      .finally(() => setBusy(false));
  }, [linked]);

  return (
    <div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          check(text);
        }}
      >
        <label htmlFor="receipt" className="text-sm font-medium text-foreground">
          Receipt
        </label>
        <textarea
          id="receipt"
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={5}
          spellCheck={false}
          placeholder="eyJhbGciOiJFZERTQSIs…"
          className="mt-2 block w-full resize-y rounded-xl border border-border-strong bg-surface px-3.5 py-3 font-mono text-xs leading-relaxed break-all text-foreground placeholder:text-muted/70 focus-visible:border-primary focus-visible:outline-none"
        />
        <button
          type="submit"
          disabled={busy || !text.trim()}
          className="mt-3 inline-flex h-11 items-center gap-2 rounded-xl bg-primary px-5 text-sm font-medium text-primary-tint transition-opacity hover:opacity-95 disabled:opacity-40"
        >
          {busy && <Spinner size={15} />} Verify
        </button>
      </form>

      {error && <p className="mt-6 rounded-xl bg-danger-tint px-4 py-3 text-sm text-danger">{error}</p>}
      {result && <Result v={result} />}
    </div>
  );
}

function Result({ v }: { v: ReceiptVerification }) {
  if (!v.valid || !v.claims) {
    return (
      <div className="mt-6 flex gap-3 rounded-2xl border border-danger/40 bg-danger-tint px-5 py-4">
        <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-full bg-danger text-danger-tint">
          <IconX size={14} strokeWidth={2.6} />
        </span>
        <div>
          <p className="font-medium text-danger">Not a genuine receipt</p>
          <p className="mt-0.5 text-sm text-danger/90">{v.reason ?? "The signature doesn't match."}</p>
        </div>
      </div>
    );
  }
  const c = v.claims;
  const human = c.authorization.method === "human";
  return (
    <div className="mt-6 overflow-hidden rounded-2xl border border-primary/40 bg-surface">
      <div className="flex gap-3 border-b border-border bg-primary-tint/60 px-5 py-4">
        <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-full bg-primary text-primary-tint">
          <IconCheck size={14} strokeWidth={2.8} />
        </span>
        <div className="min-w-0">
          <p className="font-medium text-foreground">
            Genuine — signed by Algebra{v.recorded ? " and on record" : ""}
          </p>
          <p className="mt-0.5 text-sm text-muted">
            {human ? "The person approved this exact purchase themselves" : "Within the limits the person set, so their rules approved it"} on{" "}
            {when(c.authorization.approved_at || c.iat)}.
          </p>
        </div>
        {c.test && <span className="ml-auto h-fit shrink-0 rounded-full bg-accent-tint px-2 py-0.5 text-xs font-semibold text-accent">Test order</span>}
      </div>
      <dl className="grid gap-x-6 gap-y-3 px-5 py-4 text-sm sm:grid-cols-2">
        <Row label="Amount" value={money(c.amount.minor_units, c.amount.currency)} mono />
        <Row label="Store" value={merchantLabel(c.merchant)} />
        <Row label="Order" value={c.merchant_order_id} mono />
        <Row label="Agent" value={c.agent.name || c.agent.id} />
        {v.pass && (
          <Row label="Spend pass" value={`${v.pass.label} — ${v.pass.revoked ? "since revoked" : v.pass.active ? "active" : "expired"}`} />
        )}
        <Row label="Person" value={c.sub} mono />
      </dl>
      {c.items?.length > 0 && (
        <ul className="divide-y divide-border border-t border-border text-sm">
          {c.items.map((it, i) => (
            <li key={i} className="flex items-baseline justify-between gap-4 px-5 py-2.5">
              <span className="min-w-0 text-foreground">
                {it.quantity > 1 ? `${it.quantity}× ` : ""}
                {it.name}
              </span>
              <span className="shrink-0 font-mono text-muted tabular-nums">{money(it.unit_minor_units * it.quantity, c.amount.currency)}</span>
            </li>
          ))}
        </ul>
      )}
      <p className="border-t border-border px-5 py-3 text-xs text-muted">
        Receipt {c.jti} · issued by {c.iss}
        {c.authorization.policy_version ? ` · policy ${c.authorization.policy_version}` : ""}
      </p>
    </div>
  );
}

function Row({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className={`truncate text-foreground ${mono ? "font-mono text-[0.8rem]" : ""}`}>{value}</dd>
    </div>
  );
}
