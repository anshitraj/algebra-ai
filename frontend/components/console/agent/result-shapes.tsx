"use client";

// Cards for the richer shapes a paid answer can have (lib/agent/result-view.ts
// reads them from the fields, whoever sold it): a schedule, places, flights,
// an inbox, balances, a time series, a translation. Each says only what the
// answer said; nothing is filled in.

import { useId } from "react";
import type { BalanceItem, EventItem, FlightItem, MessageItem, PlaceItem, SeriesPoint } from "@/lib/agent/result-view";
import { IconCalendar, IconChart, IconExternal, IconInbox, IconLanguages, IconPin, IconPlane, IconWallet } from "@/components/icons";
import { SolanaMark } from "@/components/chain-logos";
import styles from "./delivery.module.css";

const dayKey = (iso: string) => iso.slice(0, 10);
const time = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
const shortDate = (iso: string) => new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
const hasTime = (iso: string) => !iso.endsWith("T00:00:00.000Z");

export function EventsCard({ title, items }: { title?: string; items: EventItem[] }) {
  const days = new Map<string, EventItem[]>();
  for (const e of items) days.set(dayKey(e.start), [...(days.get(dayKey(e.start)) ?? []), e]);
  return (
    <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Schedule">
      <header>
        <span className={styles.resultIcon}><IconCalendar size={18} /></span>
        <h3>{title || "Schedule"}</h3>
        <span className={styles.count}>{items.length} {items.length === 1 ? "event" : "events"}</span>
      </header>
      <ol className={styles.agenda}>
        {[...days.entries()].map(([day, events]) => {
          const d = new Date(events[0].start);
          return (
            <li key={day}>
              <span className={styles.dateTile} aria-hidden="true">
                <small>{d.toLocaleDateString(undefined, { weekday: "short" })}</small>
                <strong>{d.getDate()}</strong>
              </span>
              <ul>
                {events.map((e, i) => (
                  <li key={i}>
                    <span className={styles.when}>{hasTime(e.start) ? `${time(e.start)}${e.end && hasTime(e.end) ? ` – ${time(e.end)}` : ""}` : "All day"}</span>
                    <div>
                      {e.url ? <a href={e.url} target="_blank" rel="noopener noreferrer">{e.title}<IconExternal size={11} /></a> : <strong>{e.title}</strong>}
                      {e.location && <p>{e.location}</p>}
                    </div>
                  </li>
                ))}
              </ul>
            </li>
          );
        })}
      </ol>
    </article>
  );
}

function Stars({ rating }: { rating: number }) {
  return (
    <span className={styles.rating} aria-label={`Rated ${rating} of 5`}>
      <svg width="11" height="11" viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="m12 3 2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9L12 3Z" /></svg>
      {rating.toFixed(1)}
    </span>
  );
}

export function PlacesCard({ items }: { items: PlaceItem[] }) {
  if (items.length === 1 && !items[0].rating) {
    const p = items[0];
    return (
      <article className={styles.resultCard} aria-label={`Place: ${p.name}`}>
        <header>
          <span className={styles.resultIcon}><IconPin size={18} /></span>
          <h3>Location</h3>
          {p.url && <a href={p.url} target="_blank" rel="noopener noreferrer">Open in Maps <IconExternal size={12} /></a>}
        </header>
        <strong className={styles.placeName}>{p.name}</strong>
        {p.address && <p className={styles.placeAddress}>{p.address}</p>}
        {(p.lat !== undefined || p.timezone) && (
          <p className={styles.coords}>{[p.lat !== undefined ? `${p.lat.toFixed(4)}, ${p.lng!.toFixed(4)}` : undefined, p.timezone].filter(Boolean).join(" · ")}</p>
        )}
      </article>
    );
  }
  return (
    <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Places">
      <header>
        <span className={styles.resultIcon}><IconPin size={18} /></span>
        <h3>Places</h3>
        <span className={styles.count}>{items.length} found</span>
      </header>
      <ol className={styles.placeList}>
        {items.map((p, i) => (
          <li key={i}>
            <span className={styles.placeIndex}>{i + 1}</span>
            <div>
              {p.url ? <a href={p.url} target="_blank" rel="noopener noreferrer">{p.name}<IconExternal size={11} /></a> : <strong>{p.name}</strong>}
              <p>
                {p.rating !== undefined && <Stars rating={p.rating} />}
                {p.reviews !== undefined && <span>{p.reviews.toLocaleString()} reviews</span>}
                {p.category && <span>{p.category.replace(/_/g, " ")}</span>}
              </p>
              {p.address && <p className={styles.placeAddress}>{p.address}</p>}
            </div>
          </li>
        ))}
      </ol>
    </article>
  );
}

export function FlightsCard({ items }: { items: FlightItem[] }) {
  return (
    <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Flights">
      <header>
        <span className={styles.resultIcon}><IconPlane size={18} /></span>
        <h3>Flights</h3>
        <span className={styles.count}>{items.length} {items.length === 1 ? "option" : "options"}</span>
      </header>
      <ol className={styles.flightList}>
        {items.map((f, i) => (
          <li key={i}>
            <div className={styles.route}>
              <div><strong>{f.from}</strong>{f.depart && <small>{time(f.depart)}</small>}</div>
              <span className={styles.routeLine} aria-hidden="true"><i /><IconPlane size={13} /><i /></span>
              <div><strong>{f.to}</strong>{f.arrive && <small>{time(f.arrive)}</small>}</div>
            </div>
            <p className={styles.flightMeta}>
              {[f.carrier, f.number, f.depart && shortDate(f.depart), f.stops !== undefined ? (f.stops === 0 ? "Nonstop" : `${f.stops} stop${f.stops > 1 ? "s" : ""}`) : undefined].filter(Boolean).join(" · ")}
            </p>
            {f.price && <strong className={styles.flightPrice}>{f.price}</strong>}
          </li>
        ))}
      </ol>
    </article>
  );
}

export function MessagesCard({ items }: { items: MessageItem[] }) {
  return (
    <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Messages">
      <header>
        <span className={styles.resultIcon}><IconInbox size={18} /></span>
        <h3>Inbox</h3>
        <span className={styles.count}>{items.length} {items.length === 1 ? "message" : "messages"}</span>
      </header>
      <ol className={styles.inbox}>
        {items.map((m, i) => (
          <li key={i} data-unread={m.unread || undefined}>
            <span className={styles.avatar} aria-hidden="true">{m.from.trim().charAt(0).toUpperCase()}</span>
            <div>
              <p className={styles.inboxTop}><strong>{m.from}</strong>{m.date && <small>{shortDate(m.date)}</small>}</p>
              <p className={styles.subject}>{m.subject}</p>
              {m.snippet && <p className={styles.snippet}>{m.snippet}</p>}
            </div>
          </li>
        ))}
      </ol>
    </article>
  );
}

export function BalancesCard({ items, totalUsd }: { items: BalanceItem[]; totalUsd?: number }) {
  const usd = (n: number) => n.toLocaleString("en-US", { style: "currency", currency: "USD", maximumFractionDigits: n < 1 ? 4 : 2 });
  return (
    <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Balances">
      <header>
        <span className={styles.resultIcon}><IconWallet size={18} /></span>
        <h3>Balances</h3>
        {totalUsd !== undefined && <span className={styles.count}>{usd(totalUsd)} total</span>}
      </header>
      <ul className={styles.balanceList}>
        {items.map((b, i) => (
          <li key={i}>
            <span className={styles.balanceMark} aria-hidden="true">{/^w?sol$/i.test(b.symbol) ? <SolanaMark size={16} /> : b.symbol.slice(0, 2)}</span>
            <strong>{b.symbol}</strong>
            <span className={styles.balanceAmount}>{b.amount}</span>
            {b.usd !== undefined && <span className={styles.balanceUsd}>{usd(b.usd)}</span>}
          </li>
        ))}
      </ul>
    </article>
  );
}

export function SeriesCard({ label, points }: { label?: string; points: SeriesPoint[] }) {
  const id = useId();
  const values = points.map((p) => p.v);
  const min = Math.min(...values), max = Math.max(...values);
  const span = max - min || 1;
  const xy = points.map((p, i) => [(i / (points.length - 1)) * 300, 92 - ((p.v - min) / span) * 80] as const);
  const line = xy.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
  const last = values[values.length - 1], firstV = values[0];
  const change = firstV ? ((last - firstV) / Math.abs(firstV)) * 100 : 0;
  const fmt = (n: number) => n.toLocaleString("en-US", { maximumFractionDigits: Math.abs(n) < 1 ? 6 : 2 });
  return (
    <article className={`${styles.resultCard} ${styles.wide}`} aria-label={label ? `${label} over time` : "Values over time"}>
      <header>
        <span className={styles.resultIcon}><IconChart size={18} /></span>
        <h3>{label || "Over time"}</h3>
        <span className={styles.count}>{shortDate(points[0].t)} – {shortDate(points[points.length - 1].t)}</span>
      </header>
      <div className={styles.seriesHead}>
        <strong>{fmt(last)}</strong>
        <span data-trend={change >= 0 ? "up" : "down"}>{change >= 0 ? "+" : ""}{change.toFixed(2)}%</span>
      </div>
      <svg className={styles.series} viewBox="0 0 300 100" preserveAspectRatio="none" role="img" aria-label={`From ${fmt(firstV)} to ${fmt(last)}; low ${fmt(min)}, high ${fmt(max)}`}>
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="currentColor" stopOpacity="0.16" />
            <stop offset="1" stopColor="currentColor" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path d={`${line} L300 100 L0 100 Z`} fill={`url(#${id})`} />
        <path d={line} fill="none" stroke="currentColor" strokeWidth="1.6" vectorEffect="non-scaling-stroke" strokeLinejoin="round" pathLength={1} className={styles.seriesLine} />
      </svg>
      <p className={styles.seriesRange}><span>Low {fmt(min)}</span><span>High {fmt(max)}</span></p>
    </article>
  );
}

export function TranslationCard({ text, from, to }: { text: string; from?: string; to?: string }) {
  return (
    <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Translation">
      <header>
        <span className={styles.resultIcon}><IconLanguages size={18} /></span>
        <h3>Translation</h3>
        {(from || to) && <span className={styles.count}>{[from, to].filter(Boolean).map((l) => l!.toUpperCase()).join(" → ")}</span>}
      </header>
      <p className={styles.translation}>{text}</p>
    </article>
  );
}
