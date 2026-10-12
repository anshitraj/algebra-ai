"use client";

// The invoice for one paid call: what was bought, from whom, for how much, how
// it was paid (with the transaction on Solscan and Solana Explorer), which
// rules allowed it, and Algebra's signature over all of it. The signature is
// the Intent Receipt (an Ed25519 JWS); this page checks it against Algebra's
// published key before it says "verified", and anyone can check it again at
// /verify without an account.

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import * as api from "@/lib/api-client";
import type { EconIntent, ReceiptVerification, SpendPass } from "@/lib/types";
import { formatMoney } from "@/lib/money";
import { explorerTx, networkWord, solscanTx } from "@/lib/explorer";
import { policyReport } from "@/lib/policy-check";
import { useSession } from "@/lib/session";
import { Logo } from "@/components/logo";
import { IconArrowLeft, IconCheck, IconExternal, IconReceipt, IconShield, IconX, Spinner } from "@/components/icons";
import { ErrorNote } from "./ui";

type Header = { alg?: string; kid?: string; typ?: string };

/** The JWS's protected header and signature, read without trusting them: the API's verification is what counts. */
function jwsParts(jws: string): { header: Header; signature: string } {
  const [h = "", , sig = ""] = jws.split(".");
  try {
    const json = atob(h.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(h.length / 4) * 4, "="));
    return { header: JSON.parse(json) as Header, signature: sig };
  } catch {
    return { header: {}, signature: sig };
  }
}

const date = (unix: number) => new Date(unix * 1000).toLocaleString(undefined, { dateStyle: "long", timeStyle: "short" });
const mid = (s: string, n = 6) => (s.length > n * 2 + 1 ? `${s.slice(0, n)}…${s.slice(-n)}` : s);

export function Invoice({ intentId }: { intentId: string }) {
  const { user } = useSession();
  const [intent, setIntent] = useState<EconIntent | null>(null);
  const [check, setCheck] = useState<ReceiptVerification | null>(null);
  const [pass, setPass] = useState<SpendPass | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let live = true;
    (async () => {
      try {
        const i = await api.getMyEconomicIntent(intentId);
        if (!live) return;
        setIntent(i);
        if (!i.receipt) return;
        const [v, passes] = await Promise.all([api.verifyReceipt(i.receipt), api.listPasses().catch(() => null)]);
        if (!live) return;
        setCheck(v);
        setPass(passes?.passes?.find((p) => p.id === i.spend_pass_id) ?? null);
      } catch (e) {
        if (live) setError(e instanceof Error ? e.message : "Couldn't load this invoice");
      }
    })();
    return () => {
      live = false;
    };
  }, [intentId]);

  const receipt = intent?.receipt;
  const parts = useMemo(() => (receipt ? jwsParts(receipt) : null), [receipt]);

  if (error) return <ErrorNote>{error}</ErrorNote>;
  if (!intent || (intent.receipt && !check))
    return (
      <p className="flex items-center gap-2 text-sm text-muted">
        <Spinner size={14} /> Loading the invoice…
      </p>
    );
  if (!intent.receipt || !check)
    return (
      <div className="mx-auto max-w-2xl">
        <BackLink />
        <p className="mt-6 text-sm text-muted">
          There is no invoice for this request yet: Algebra signs one once a payment is committed. Its state is {intent.state.toLowerCase()}.
        </p>
      </div>
    );

  const c = check.claims;
  const s = c?.settlement;
  const network = s?.network;
  const onChain = !!s?.transaction && (network === "solana" || network === "solana-devnet");
  const human = c?.authority.method === "human";
  const report = pass && c ? policyReport(pass, s?.amount.minor_units ?? intent.committed_minor, human) : null;
  const number = `ALG-${(c?.jti ?? intent.id).replace(/[^A-Za-z0-9]/g, "").slice(-10).toUpperCase()}`;

  async function copyReceipt() {
    try {
      await navigator.clipboard.writeText(intent!.receipt!);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // clipboard blocked: the receipt is selectable below
    }
  }

  return (
    <div className="mx-auto max-w-3xl">
      <div className="flex flex-wrap items-center justify-between gap-3 print:hidden">
        <BackLink />
        <div className="flex gap-2">
          <a
            href={`/verify?r=${encodeURIComponent(intent.receipt)}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-border px-3 text-xs font-medium text-foreground hover:bg-primary-tint/50"
          >
            Verify publicly <IconExternal size={12} />
          </a>
          <button
            type="button"
            onClick={() => window.print()}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-primary px-3 text-xs font-medium text-primary-tint hover:opacity-90"
          >
            <IconReceipt size={14} /> Print or save as PDF
          </button>
        </div>
      </div>

      <article className="mt-4 overflow-hidden rounded-2xl border border-border bg-surface shadow-[0_24px_60px_-40px_rgb(0_0_0/0.35)] print:mt-0 print:rounded-none print:border-0 print:shadow-none">
        {/* Letterhead */}
        <header className="flex flex-wrap items-start justify-between gap-6 border-b border-border px-6 py-6 sm:px-9">
          <div className="flex items-center gap-3">
            <Logo size={34} className="text-foreground" />
            <div>
              <p className="font-display text-lg font-semibold tracking-tight text-foreground">Algebra</p>
              <p className="text-xs text-muted">Agent execution and payments</p>
            </div>
          </div>
          <div className="text-right">
            <p className="text-[0.7rem] font-medium tracking-[0.14em] text-muted uppercase">Invoice · receipt</p>
            <p className="mt-1 font-mono text-sm text-foreground">{number}</p>
            <p className="mt-0.5 text-xs text-muted">{c ? date(c.iat) : new Date(intent.updated_at).toLocaleString()}</p>
          </div>
        </header>

        {/* Status */}
        <div className="flex flex-wrap items-center gap-3 border-b border-border bg-primary-tint/40 px-6 py-3 sm:px-9">
          <span className="inline-flex items-center gap-1.5 rounded-full bg-primary px-2.5 py-1 text-xs font-semibold text-primary-tint">
            <IconCheck size={12} strokeWidth={3} /> Paid
          </span>
          {c?.test && (
            <span className="rounded-full bg-accent-tint px-2.5 py-1 text-xs font-medium text-accent">
              {network === "sandbox" ? "Sandbox: simulated USDC" : "Devnet: test USDC, no real value"}
            </span>
          )}
          <span className="text-xs text-muted">
            {human ? "You approved this exact call" : "Allowed by your Spend Pass without asking you"}
          </span>
        </div>

        {/* Parties */}
        <section className="grid gap-6 border-b border-border px-6 py-6 sm:grid-cols-3 sm:px-9">
          <Party title="From" lines={["Algebra", c?.iss ?? "", "Signs every committed payment"]} />
          <Party title="Billed to" lines={[user?.name || user?.email || "You", user?.name ? user.email : "", pass ? `Spend Pass: ${pass.label}` : ""]} />
          <Party title="Paid to" lines={[c?.provider.id ?? "", s?.pay_to ? `Wallet ${mid(s.pay_to, 5)}` : "", c?.reservation.executor.name ? `Agent: ${c.reservation.executor.name}` : ""]} />
        </section>

        {/* Line items */}
        <section className="px-6 py-6 sm:px-9">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-[0.7rem] tracking-wider text-muted uppercase">
                <th className="pb-2 font-medium">Description</th>
                <th className="pb-2 text-right font-medium">Qty</th>
                <th className="pb-2 text-right font-medium">Amount</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b border-border align-top">
                <td className="py-3 pr-4">
                  <p className="font-medium text-foreground">{c?.intent.capability ?? intent.capability}</p>
                  <p className="mt-0.5 text-xs text-muted">
                    {c?.provider.id}
                    {c?.routing ? ` · picked by Algebra's router (${c.routing.mode}${c.routing.fallback ? ", after a fallback" : ""})` : ""}
                  </p>
                  {c?.provider.quote && <p className="mt-0.5 text-xs text-muted">Quoted {formatMoney(c.provider.quote.minor_units, c.provider.quote.currency)}, ceiling {formatMoney(c.intent.budget_max.minor_units, c.intent.budget_max.currency)}</p>}
                </td>
                <td className="py-3 text-right font-mono text-foreground tabular-nums">{c?.intent.quantity ?? 1}</td>
                <td className="py-3 text-right font-mono text-foreground tabular-nums">
                  {s ? formatMoney(s.amount.minor_units, s.amount.currency) : formatMoney(intent.committed_minor, intent.currency)}
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td colSpan={2} className="pt-3 text-right text-xs text-muted">
                  Algebra fee
                </td>
                <td className="pt-3 text-right font-mono text-xs text-muted tabular-nums">0.00 USDC</td>
              </tr>
              <tr>
                <td colSpan={2} className="pt-1.5 text-right text-sm font-semibold text-foreground">
                  Total paid
                </td>
                <td className="pt-1.5 text-right font-mono text-base font-semibold text-foreground tabular-nums">
                  {s ? formatMoney(s.amount.minor_units, s.amount.currency) : formatMoney(intent.committed_minor, intent.currency)}
                </td>
              </tr>
            </tfoot>
          </table>
        </section>

        {/* Payment */}
        <section className="grid gap-x-8 gap-y-2 border-t border-border px-6 py-6 text-xs sm:grid-cols-2 sm:px-9">
          <h2 className="text-[0.7rem] font-medium tracking-wider text-muted uppercase sm:col-span-2">Payment</h2>
          <Fact label="Rail" value={s ? `${s.rail}${network ? ` · ${networkWord(network)}` : ""}` : "—"} />
          <Fact label="Asset" value={s?.asset && s.asset.length > 12 ? `USDC · mint ${mid(s.asset, 4)}` : (s?.asset ?? "USDC")} />
          {s?.payer && <Fact label="Settled by" value={`Algebra's settlement wallet ${mid(s.payer, 4)}`} />}
          {s?.pay_to && <Fact label="Paid to" value={mid(s.pay_to, 6)} mono />}
          {s?.transaction && <Fact label="Transaction" value={mid(s.transaction, 10)} mono />}
          <Fact label="Attempts" value={`${c?.coordination.attempts ?? intent.attempts}${c?.coordination.duplicate_commit_attempts_blocked ? ` · ${c.coordination.duplicate_commit_attempts_blocked} duplicate payment stopped` : " · paid once"}`} />
          {onChain && (
            <p className="flex flex-wrap gap-x-4 gap-y-1 pt-1 sm:col-span-2 print:hidden">
              <a className="inline-flex items-center gap-1 font-medium text-primary hover:underline" href={solscanTx(s!.transaction!, network)} target="_blank" rel="noopener noreferrer">
                View on Solscan <IconExternal size={11} />
              </a>
              <a className="inline-flex items-center gap-1 font-medium text-primary hover:underline" href={explorerTx(s!.transaction!, network)} target="_blank" rel="noopener noreferrer">
                View on Solana Explorer <IconExternal size={11} />
              </a>
            </p>
          )}
        </section>

        {/* Policy */}
        {report && (
          <section className={`border-t border-border px-6 py-6 sm:px-9 ${report.custom || human ? "" : "bg-accent-tint/30"}`}>
            <h2 className="text-[0.7rem] font-medium tracking-wider text-muted uppercase">Why it was allowed</h2>
            <p className="mt-2 flex items-start gap-2 text-sm font-medium text-foreground">
              <IconShield size={15} className="mt-0.5 shrink-0 text-primary" /> {report.headline}
            </p>
            <ul className="mt-2 grid gap-1.5 pl-6 text-xs sm:grid-cols-2">
              {report.checks.map((k) => (
                <li key={k.label}>
                  <span className="text-muted">{k.label}: </span>
                  <span className={k.state === "open" ? "text-muted italic" : "text-foreground"}>{k.result}</span>
                </li>
              ))}
            </ul>
            <p className="mt-2 pl-6 text-[0.7rem] text-muted">The pass as it is now; the signed policy version is {c?.authority.policy_version ?? "not recorded"}.</p>
          </section>
        )}

        {/* Signature */}
        <section className="border-t border-border bg-background/50 px-6 py-6 sm:px-9">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="flex items-start gap-3">
              <span className={`grid h-10 w-10 shrink-0 place-items-center rounded-full ${check.valid ? "bg-primary text-primary-tint" : "bg-danger text-danger-tint"}`}>
                {check.valid ? <IconCheck size={18} strokeWidth={2.8} /> : <IconX size={18} strokeWidth={2.8} />}
              </span>
              <div>
                <p className="font-display text-base font-semibold text-foreground">
                  {check.valid ? "Digitally signed by Algebra" : "Signature does not verify"}
                </p>
                <p className="mt-0.5 text-xs text-muted">
                  {check.valid
                    ? `Checked just now against Algebra's published key${check.recorded ? ", and matches the receipt Algebra logged for this payment" : ""}.`
                    : (check.reason ?? "The signature doesn't match Algebra's keys.")}
                </p>
              </div>
            </div>
            <p className="font-display text-2xl tracking-tight text-foreground/70 italic select-none" aria-hidden>
              Algebra
            </p>
          </div>
          <dl className="mt-4 grid gap-x-8 gap-y-1.5 text-xs sm:grid-cols-2">
            <Fact label="Algorithm" value={parts?.header.alg === "EdDSA" ? "Ed25519 (EdDSA)" : (parts?.header.alg ?? "—")} />
            <Fact label="Key ID" value={parts?.header.kid ?? "—"} mono />
            <Fact label="Receipt ID" value={c?.jti ?? "—"} mono />
            <Fact label="Signed" value={c ? date(c.iat) : "—"} />
            {c?.intent.hash && <Fact label="Request hash" value={mid(c.intent.hash, 10)} mono />}
            {c?.execution.result_hash && <Fact label="Result hash" value={mid(c.execution.result_hash, 10)} mono />}
          </dl>
          <div className="mt-3">
            <p className="text-[0.7rem] text-muted">Signature</p>
            <p className="mt-1 rounded-lg border border-border bg-surface px-3 py-2 font-mono text-[0.68rem] leading-relaxed break-all text-foreground">{parts?.signature}</p>
          </div>
          <div className="mt-3 flex flex-wrap items-center gap-3 print:hidden">
            <button type="button" onClick={copyReceipt} className="text-xs font-medium text-primary hover:underline">
              {copied ? "Copied the signed receipt" : "Copy the signed receipt"}
            </button>
            <span className="text-xs text-muted">
              Anyone can check it at <Link href="/verify" className="text-primary hover:underline">/verify</Link> or against <code className="font-mono">/.well-known/jwks.json</code>.
            </span>
          </div>
        </section>

        <footer className="border-t border-border px-6 py-4 text-[0.7rem] leading-relaxed text-muted sm:px-9">
          Intent {intent.id}. Algebra pays providers on your behalf, within your Spend Pass, and pays each request once at most. The provider&apos;s
          answer is its own data. This invoice is generated from the signed receipt; if the two ever differ, the signed receipt is authoritative.
        </footer>
      </article>
    </div>
  );
}

function BackLink() {
  return (
    <Link href="/console/executions" className="inline-flex items-center gap-1.5 text-xs font-medium text-muted hover:text-foreground">
      <IconArrowLeft size={14} /> Executions
    </Link>
  );
}

function Party({ title, lines }: { title: string; lines: string[] }) {
  const [first, ...rest] = lines.filter(Boolean);
  return (
    <div className="min-w-0">
      <p className="text-[0.7rem] font-medium tracking-wider text-muted uppercase">{title}</p>
      <p className="mt-1.5 truncate text-sm font-medium text-foreground">{first ?? "—"}</p>
      {rest.map((l) => (
        <p key={l} className="truncate text-xs text-muted">
          {l}
        </p>
      ))}
    </div>
  );
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex min-w-0 gap-2">
      <dt className="shrink-0 text-muted">{label}</dt>
      <dd className={`min-w-0 truncate text-foreground ${mono ? "font-mono" : ""}`}>{value}</dd>
    </div>
  );
}
