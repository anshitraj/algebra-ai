"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import * as api from "@/lib/api-client";
import type { IntentReceiptClaims, ReceiptMoney, ReceiptVerification } from "@/lib/types";
import { formatMoney } from "@/lib/money";
import { explorerTx, networkWord } from "@/lib/explorer";
import { IconCheck, IconExternal, IconX, Spinner } from "@/components/icons";

const when = (unix: number) => new Date(unix * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
const money = (m: ReceiptMoney) => formatMoney(m.minor_units, m.currency);

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

const FULFILMENT: Record<string, string> = {
  fulfilled: "The provider delivered a result.",
  FULFILLED: "The provider delivered a result.",
  not_fulfilled: "Paid, but the result wasn't usable.",
  NOT_FULFILLED: "Paid, but the result wasn't usable.",
  result_unknown: "Paid; the result isn't known.",
  UNKNOWN: "Paid; the result isn't known.",
};

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
  const c: IntentReceiptClaims = v.claims;
  const human = c.authority.method === "human";
  const s = c.settlement;
  const q = c.execution.quality;
  return (
    <div className="mt-6 overflow-hidden rounded-2xl border border-primary/40 bg-surface">
      <div className="flex gap-3 border-b border-border bg-primary-tint/60 px-5 py-4">
        <span className="mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-full bg-primary text-primary-tint">
          <IconCheck size={14} strokeWidth={2.8} />
        </span>
        <div className="min-w-0">
          <p className="font-medium text-foreground">Genuine — signed by Algebra{v.recorded ? " and on record" : ""}</p>
          <p className="mt-0.5 text-sm text-muted">
            {human ? "The person approved this exact call themselves" : "Within the limits of the person's Spend Pass, so their rules approved it"} on{" "}
            {when(c.iat)}.
          </p>
        </div>
        {c.test && <span className="ml-auto h-fit shrink-0 rounded-full bg-accent-tint px-2 py-0.5 text-xs font-semibold text-accent">Test money</span>}
      </div>

      <dl className="grid gap-x-6 gap-y-3 px-5 py-4 text-sm sm:grid-cols-2">
        <Row label="Asked for" value={c.intent.capability} mono />
        <Row label="Provider" value={c.provider.id} mono />
        <Row label="Paid" value={s ? money(s.amount) : "Nothing was paid"} mono />
        <Row label="Ceiling the person set" value={money(c.intent.budget_max)} mono />
        {s?.network && <Row label="Network" value={networkWord(s.network) || s.network} />}
        <Row label="Agent" value={c.reservation.executor.name || c.reservation.executor.id} />
        {c.authority.spend_pass_id && <Row label="Spend pass" value={c.authority.spend_pass_id} mono />}
        <Row label="Person" value={c.sub} mono />
      </dl>

      {s?.transaction && (
        <a
          href={explorerTx(s.transaction, s.network)}
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center justify-between gap-3 border-t border-border px-5 py-3 text-sm text-foreground hover:bg-primary-tint/40"
        >
          <span className="min-w-0">
            <span className="block text-xs text-muted">Transaction on Solana Explorer</span>
            <span className="block truncate font-mono text-[0.8rem]">{s.transaction}</span>
          </span>
          <IconExternal size={14} className="shrink-0 text-muted" />
        </a>
      )}

      <ul className="divide-y divide-border border-t border-border text-sm">
        <Line label="Result" value={FULFILMENT[c.final_state.fulfillment] ?? FULFILMENT[c.execution.status] ?? c.execution.status} />
        {q && (
          <Line
            label="Judged by Algebra"
            value={`${q.evaluator}${q.schema_valid === undefined ? "" : q.schema_valid ? ", matches the expected shape" : ", doesn't match the expected shape"}${typeof q.score === "number" ? `, ${Math.round(q.score)}/100` : ""}`}
          />
        )}
        {c.routing && (
          <Line
            label="How it was chosen"
            value={`${c.routing.mode.toLowerCase()} routing, ${c.routing.fallback ? `fallback #${c.routing.plan_rank}` : "first choice"}`}
          />
        )}
        <Line
          label="Safety"
          value={`${c.coordination.attempts} attempt${c.coordination.attempts === 1 ? "" : "s"}, ${c.coordination.duplicate_commit_attempts_blocked} duplicate payment${c.coordination.duplicate_commit_attempts_blocked === 1 ? "" : "s"} stopped${c.coordination.reconciliation_required ? ", reconciled afterwards" : ""}`}
        />
      </ul>

      <p className="border-t border-border px-5 py-3 text-xs break-all text-muted">
        Receipt {c.jti} · issued by {c.iss}
        {c.authority.policy_version ? ` · policy ${c.authority.policy_version}` : ""} · intent {c.intent.id}
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

function Line({ label, value }: { label: string; value: string }) {
  return (
    <li className="flex items-baseline justify-between gap-4 px-5 py-2.5">
      <span className="shrink-0 text-muted">{label}</span>
      <span className="min-w-0 text-right text-foreground">{value}</span>
    </li>
  );
}
