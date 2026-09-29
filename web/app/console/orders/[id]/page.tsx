"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import * as api from "@/lib/api-client";
import type { OrderDetail, ShippingProfile } from "@/lib/types";
import { merchantLabel } from "@/lib/agent/steps";
import { useNow } from "@/lib/use-now";
import { IconArrowLeft, IconCheck, IconExternal, IconMapPin, IconReceipt } from "@/components/icons";
import { ErrorNote, Panel, Skeleton, StatusBadge, formatMoney } from "@/components/console/ui";

export default function OrderPage() {
  const { id } = useParams<{ id: string }>();
  const [detail, setDetail] = useState<OrderDetail | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .getMyOrder(id)
      .then(setDetail)
      .catch((e) => setError(e instanceof Error ? e.message : "Couldn't load this order"));
  }, [id]);

  return (
    <div className="mx-auto max-w-3xl">
      <Link href="/console/orders" className="inline-flex items-center gap-1.5 text-sm text-muted hover:text-foreground print:hidden">
        <IconArrowLeft size={14} /> Orders
      </Link>
      <div className="mt-4">
        {error && <ErrorNote>{error}</ErrorNote>}
        {!detail && !error && (
          <div className="space-y-4">
            <Skeleton className="h-10 w-2/3" />
            <Skeleton className="h-32" />
            <Skeleton className="h-64" />
          </div>
        )}
        {detail && <OrderView detail={detail} />}
      </div>
    </div>
  );
}

function OrderView({ detail }: { detail: OrderDetail }) {
  const { order, simulated } = detail;
  const placed = new Date(order.placed_at);
  return (
    <>
      <div className="flex flex-wrap items-start justify-between gap-4 print:hidden">
        <div className="min-w-0">
          <p className="text-sm text-muted">
            {placed.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })} · {merchantLabel(order.merchant)}
          </p>
          <h1 className="font-display mt-1 text-[1.75rem] leading-tight font-semibold tracking-tight text-foreground">
            Order <span className="font-mono text-[1.5rem]">{order.merchant_order_id}</span>
          </h1>
        </div>
        <div className="flex items-center gap-2">
          {simulated && <span className="rounded-full bg-accent-tint px-2.5 py-0.5 text-xs font-semibold text-accent">Demo</span>}
          <StatusBadge status={order.status} />
        </div>
      </div>

      {simulated && (
        <p className="mt-5 rounded-xl bg-accent-tint px-4 py-3 text-sm leading-relaxed text-accent print:hidden">
          A simulated order: the product and price are a real listing, and your guardrails and approval ran for real, but
          no money moved and nothing will arrive.
        </p>
      )}

      <div className="mt-6 space-y-5 print:hidden">
        <Tracker detail={detail} />
        <Delivery shipTo={detail.ship_to} simulated={simulated} />
        <ShareLink orderNumber={order.merchant_order_id} />
        {detail.receipt && <SignedReceipt receipt={detail.receipt} />}
      </div>

      <Invoice detail={detail} />
    </>
  );
}

// --- progress ---

const STAGES = ["Order placed", "Packed", "Out for delivery", "Delivered"] as const;

/**
 * Which stage an order is at. A simulated order moves along its own
 * placed → delivery estimate window, so a demo shows the whole journey; a
 * real order shows only what its store reported.
 */
function stageOf(detail: OrderDetail, now: number): number {
  const { order } = detail;
  if (order.status === "DELIVERED") return 3;
  if (order.status === "SHIPPED") return 2;
  if (!detail.simulated || !order.delivery_eta) return 0;
  const start = new Date(order.placed_at).getTime();
  const end = new Date(order.delivery_eta).getTime();
  const progress = end > start ? (now - start) / (end - start) : 1;
  if (progress >= 1) return 3;
  if (progress >= 0.6) return 2;
  if (progress >= 0.15) return 1;
  return 0;
}

function Tracker({ detail }: { detail: OrderDetail }) {
  const now = useNow(30_000);
  const { order } = detail;
  if (order.status === "CANCELLED" || order.status === "FAILED") {
    return (
      <Panel>
        <p className="text-sm text-muted">This order was {order.status.toLowerCase()}.</p>
      </Panel>
    );
  }
  const stage = stageOf(detail, now);
  const eta = order.delivery_eta ? new Date(order.delivery_eta) : null;
  return (
    <Panel>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="font-display text-base font-semibold text-foreground">{STAGES[stage]}</h2>
        {eta && stage < 3 && (
          <p className="text-sm text-muted">
            Arriving {eta.toLocaleString(undefined, { weekday: "short", day: "numeric", month: "short", hour: "numeric", minute: "2-digit" })}
          </p>
        )}
      </div>
      <ol className="mt-5 grid grid-cols-4 gap-2" aria-label="Delivery progress">
        {STAGES.map((label, i) => {
          const done = i <= stage;
          return (
            <li key={label} className="min-w-0">
              <div className={`h-1.5 rounded-full ${done ? "bg-primary" : "bg-border"}`} />
              <div className="mt-2 flex items-center gap-1.5">
                {done && <IconCheck size={12} className="shrink-0 text-primary" />}
                <span className={`truncate text-xs ${done ? "text-foreground" : "text-muted"}`}>{label}</span>
              </div>
            </li>
          );
        })}
      </ol>
    </Panel>
  );
}

// --- delivery + map ---

function addressLines(a: ShippingProfile): string[] {
  return [a.line1, a.line2, [a.city, a.state].filter(Boolean).join(", ") + (a.postal_code ? ` ${a.postal_code}` : "")].filter(
    (l): l is string => !!l && l.trim() !== ""
  );
}

function mapQuery(a: ShippingProfile): string {
  return [a.line2 || a.line1, a.city, a.postal_code, a.country === "IN" ? "India" : a.country].filter(Boolean).join(", ");
}

function Delivery({ shipTo, simulated }: { shipTo?: ShippingProfile; simulated: boolean }) {
  // The map is a Google Maps embed; it loads only when opened, so the
  // address never leaves this page unless the user asks for the map.
  const [showMap, setShowMap] = useState(false);
  if (!shipTo) {
    return (
      <Panel>
        <p className="text-sm text-muted">No delivery address is on file for this order.</p>
      </Panel>
    );
  }
  const q = encodeURIComponent(mapQuery(shipTo));
  return (
    <Panel className="p-0">
      <div className="flex flex-wrap items-start justify-between gap-4 p-6">
        <div className="flex min-w-0 gap-3">
          <span className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-primary-tint text-primary">
            <IconMapPin size={16} />
          </span>
          <div className="min-w-0 text-sm leading-relaxed">
            <p className="font-medium text-foreground">{shipTo.recipient_name}</p>
            {addressLines(shipTo).map((l) => (
              <p key={l} className="text-muted">
                {l}
              </p>
            ))}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            onClick={() => setShowMap((v) => !v)}
            aria-expanded={showMap}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-border-strong px-3 text-sm font-medium text-foreground hover:bg-primary-tint/60"
          >
            <IconMapPin size={14} /> {showMap ? "Hide map" : "View on map"}
          </button>
          <a
            href={`https://www.google.com/maps/search/?api=1&query=${q}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex h-9 items-center gap-1.5 rounded-lg px-3 text-sm font-medium text-muted hover:bg-primary-tint/60 hover:text-foreground"
          >
            Open in Google Maps <IconExternal size={13} />
          </a>
        </div>
      </div>
      {showMap && (
        <div className="border-t border-border">
          <iframe
            title="Delivery location"
            src={`https://maps.google.com/maps?q=${q}&z=15&output=embed`}
            className="block h-72 w-full rounded-b-2xl"
            loading="lazy"
            referrerPolicy="no-referrer"
          />
          {simulated && (
            <p className="px-6 py-3 text-xs text-muted">The pin marks where this demo order &ldquo;delivers&rdquo; — a made-up address in a real neighbourhood.</p>
          )}
        </div>
      )}
    </Panel>
  );
}

// The signed spend receipt: proof, checkable by anyone — the store, a
// payment company, a dispute team — that this purchase was authorized.
function SignedReceipt({ receipt }: { receipt: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="rounded-2xl border border-border px-5 py-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-primary-tint text-primary">
            <IconCheck size={14} strokeWidth={2.6} />
          </span>
          <div className="min-w-0">
            <p className="text-sm font-medium text-foreground">Signed spend receipt</p>
            <p className="text-xs text-muted">Proof that you authorized exactly this purchase — anyone can verify it, no account needed.</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() =>
              navigator.clipboard?.writeText(receipt).then(
                () => {
                  setCopied(true);
                  window.setTimeout(() => setCopied(false), 1500);
                },
                () => {}
              )
            }
            className="rounded-lg border border-border-strong px-3 py-1.5 text-xs font-medium text-foreground hover:bg-primary-tint"
          >
            {copied ? "Copied" : "Copy receipt"}
          </button>
          <a
            href={`/verify?r=${encodeURIComponent(receipt)}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 rounded-lg bg-primary px-3 py-1.5 text-xs font-medium text-primary-tint"
          >
            Verify <IconExternal size={12} />
          </a>
        </div>
      </div>
    </div>
  );
}

function ShareLink({ orderNumber }: { orderNumber: string }) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(window.location.href);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      // Clipboard blocked — the address bar still has the link.
    }
  }
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-border px-5 py-3.5">
      <p className="text-sm text-muted">
        Order <span className="font-mono text-foreground">{orderNumber}</span> has its own page — share the link or come back to it any time.
      </p>
      <button type="button" onClick={copy} className="text-sm font-medium text-primary hover:underline">
        {copied ? "Copied" : "Copy link"}
      </button>
    </div>
  );
}

// --- invoice ---

function Invoice({ detail }: { detail: OrderDetail }) {
  const { order, simulated, ship_to: shipTo } = detail;
  const subtotal = order.items.reduce((sum, it) => sum + it.unit_price.minor_units * it.quantity, 0);
  const delivery = Math.max(order.total.minor_units - subtotal, 0);
  const cur = order.total.currency;
  const placed = new Date(order.placed_at);
  // Demo checkout names each line "Product — Store".
  const lines = order.items.map((it) => {
    const [name, store] = it.name.split(" — ");
    return { ...it, name, store };
  });
  const stores = [...new Set(lines.map((l) => l.store).filter(Boolean))];

  return (
    <section aria-labelledby="invoice-title" className="relative mt-8 overflow-hidden rounded-2xl border border-border bg-surface p-7 print:mt-0 print:border-0 print:p-0">
      {simulated && (
        <span
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 -rotate-12 text-[4.5rem] font-bold tracking-widest whitespace-nowrap text-accent/10 select-none"
        >
          DEMO
        </span>
      )}
      <div className="relative flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 id="invoice-title" className="font-display flex items-center gap-2 text-lg font-semibold text-foreground">
            <IconReceipt size={18} className="text-primary" /> {simulated ? "Demo invoice" : "Order summary"}
          </h2>
          <p className="mt-1 text-xs text-muted">
            {simulated
              ? "Not a tax invoice — generated for a simulated order. No money was charged."
              : "Your store issues the tax invoice; this is Algebra's record of the order."}
          </p>
        </div>
        <button
          type="button"
          onClick={() => window.print()}
          className="inline-flex h-9 items-center rounded-lg border border-border-strong px-3 text-sm font-medium text-foreground hover:bg-primary-tint/60 print:hidden"
        >
          Print or save PDF
        </button>
      </div>

      <dl className="relative mt-6 grid gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
        <div>
          <dt className="text-xs text-muted">Invoice no.</dt>
          <dd className="font-mono text-foreground">INV-{order.merchant_order_id}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted">Date</dt>
          <dd className="text-foreground">{placed.toLocaleDateString(undefined, { dateStyle: "medium" })}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted">Sold by</dt>
          <dd className="text-foreground">
            {stores.length ? `${stores.join(", ")} listing${simulated ? " (via demo checkout)" : ""}` : merchantLabel(order.merchant)}
          </dd>
        </div>
        {shipTo && (
          <div className="sm:col-span-2">
            <dt className="text-xs text-muted">Ship to</dt>
            <dd className="text-foreground">{[shipTo.recipient_name, ...addressLines(shipTo)].join(", ")}</dd>
          </div>
        )}
        <div>
          <dt className="text-xs text-muted">Paid with</dt>
          <dd className="text-foreground">{simulated ? "Demo card — nothing charged" : (detail.payment_alias ?? "—").replace(/^payment:/, "")}</dd>
        </div>
      </dl>

      <table className="relative mt-6 w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs text-muted">
            <th className="py-2 font-medium">Item</th>
            <th className="py-2 pl-4 text-right font-medium">Qty</th>
            <th className="py-2 pl-4 text-right font-medium">Price</th>
            <th className="py-2 pl-4 text-right font-medium">Amount</th>
          </tr>
        </thead>
        <tbody>
          {lines.map((l) => (
            <tr key={l.merchant_product_id} className="border-b border-border align-top">
              <td className="py-2.5 pr-3 text-foreground">{l.name}</td>
              <td className="py-2.5 pl-4 text-right tabular-nums text-foreground">{l.quantity}</td>
              <td className="py-2.5 pl-4 text-right font-mono whitespace-nowrap tabular-nums text-foreground">{formatMoney(l.unit_price)}</td>
              <td className="py-2.5 pl-4 text-right font-mono whitespace-nowrap tabular-nums text-foreground">
                {formatMoney({ minor_units: l.unit_price.minor_units * l.quantity, currency: l.unit_price.currency })}
              </td>
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr>
            <td colSpan={3} className="pt-3 text-right text-muted">Subtotal</td>
            <td className="pt-3 pl-4 text-right font-mono whitespace-nowrap tabular-nums text-foreground">{formatMoney({ minor_units: subtotal, currency: cur })}</td>
          </tr>
          <tr>
            <td colSpan={3} className="pt-1 text-right text-muted">Delivery</td>
            <td className="pt-1 pl-4 text-right font-mono whitespace-nowrap tabular-nums text-foreground">
              {delivery ? formatMoney({ minor_units: delivery, currency: cur }) : "Free"}
            </td>
          </tr>
          <tr>
            <td colSpan={3} className="pt-2 text-right font-semibold text-foreground">Total</td>
            <td className="pt-2 pl-4 text-right font-mono font-semibold whitespace-nowrap tabular-nums text-foreground">{formatMoney(order.total)}</td>
          </tr>
        </tfoot>
      </table>

      <p className="relative mt-6 text-xs text-muted">
        Order <span className="font-mono">{order.merchant_order_id}</span> · placed through Algebra, within your guardrails.
      </p>
    </section>
  );
}
