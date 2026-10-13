"use client";

import { useId, useState } from "react";
import type { EconIntent } from "@/lib/types";
import { formatUSDC } from "@/lib/money";
import { Skeleton } from "./ui";
import styles from "./overview-charts.module.css";

type Day = { date: Date; spend: number; requests: number; paid: number };
const number = (value: number) => value.toLocaleString("en-US", { maximumFractionDigits: 6 });
const dateLabel = (date: Date) => date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
const dayKey = (date: Date) => `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`;

function dailyActivity(intents: EconIntent[], days: number): Day[] {
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const series: Day[] = Array.from({ length: days }, (_, i) => {
    const date = new Date(today);
    date.setDate(date.getDate() - days + 1 + i);
    return { date, spend: 0, requests: 0, paid: 0 };
  });
  const lookup = new Map(series.map((day) => [dayKey(day.date), day]));
  for (const intent of intents) {
    const day = lookup.get(dayKey(new Date(intent.created_at)));
    if (!day) continue;
    day.requests += 1;
    if (intent.committed_minor > 0) {
      day.paid += 1;
      if (intent.currency === "USDC") day.spend += intent.committed_minor;
    }
  }
  return series;
}

function exampleActivity(days: number): Day[] {
  return dailyActivity([], days).map((day, i) => {
    const requests = [12, 19, 9, 26, 18, 31, 24, 14, 22, 35][i % 10];
    return { ...day, requests, paid: requests - (i % 4 + 1), spend: requests * [24000, 32000, 18000, 28000][i % 4] };
  });
}

export function OverviewCharts({ intents, error }: { intents: EconIntent[] | null; error: string | null }) {
  const [days, setDays] = useState<7 | 30>(7);
  const [example, setExample] = useState(false);
  const id = useId();
  const series = example ? exampleActivity(days) : dailyActivity(intents ?? [], days);
  const loading = intents === null && !error;
  const start = series[0].date;
  const end = new Date(series.at(-1)!.date);
  end.setDate(end.getDate() + 1);
  const inRange = (intents ?? []).filter((intent) => {
    const date = new Date(intent.created_at);
    return date >= start && date < end;
  });
  const total = series.reduce((sum, day) => sum + day.spend, 0);
  const requests = series.reduce((sum, day) => sum + day.requests, 0);
  const capabilities = new Map<string, number>();
  for (const intent of inRange) {
    if (intent.currency === "USDC" && intent.committed_minor > 0) {
      capabilities.set(intent.capability, (capabilities.get(intent.capability) ?? 0) + intent.committed_minor);
    }
  }
  const breakdown: [string, number][] = example
    ? [["Web search", total * 0.46], ["Model inference", total * 0.32], ["Data enrichment", total * 0.16], ["Other APIs", total * 0.06]]
    : [...capabilities].sort((a, b) => b[1] - a[1]).slice(0, 4);

  return (
    <section className={styles.section} aria-labelledby={`${id}-heading`}>
      <div className={styles.heading}>
        <div>
          <h2 id={`${id}-heading`}>Activity insights</h2>
          <p>{example ? "Illustrative data · no payments are made" : "A closer look at your agent operations"}</p>
        </div>
        <div className={styles.controls}>
          <button type="button" className={styles.example} aria-pressed={example} onClick={() => setExample(!example)}>
            {example ? "Use my activity" : "Preview example"}
          </button>
          <div className={styles.period} role="group" aria-label="Chart period">
            {([7, 30] as const).map((period) => <button type="button" key={period} aria-pressed={days === period} onClick={() => setDays(period)}>{period} days</button>)}
          </div>
        </div>
      </div>
      {!example && intents && intents.length >= 200 && <p className={styles.note}>Charts show the latest 200 requests. Summary spending and paid-call totals cover the full 30-day period.</p>}
      {!example && error && <p className={styles.note} role="status">Activity insights could not load. You can preview example data.</p>}
      <div className={styles.grid}>
        <article className={`${styles.card} console-inverse`}>
          <div className={styles.cardHeading}><h3>Daily spending</h3><span>{example ? "Example" : "USDC"}</span></div>
          <div className={styles.metric}>{loading && !example ? <Skeleton className="h-8 w-32" /> : error && !example ? "—" : formatUSDC(total)}</div>
          <p className={styles.description}>Committed spend for requests created in this period.</p>
          {loading && !example ? <Skeleton className="mt-6 h-48" /> : <TimeChart key={days} series={series} kind="bar" unavailable={!!error && !example} />}
        </article>
        <article className={`${styles.card} console-inverse`}>
          <div className={styles.cardHeading}><h3>Request volume</h3><span>{example ? "Example" : "Requests"}</span></div>
          <div className={styles.metric}>{loading && !example ? <Skeleton className="h-8 w-24" /> : error && !example ? "—" : number(requests)} <small>requests</small></div>
          <p className={styles.description}>Requests created and those that resulted in a payment.</p>
          {loading && !example ? <Skeleton className="mt-6 h-48" /> : <TimeChart key={days} series={series} kind="line" unavailable={!!error && !example} />}
        </article>
        <article className={`${styles.card} ${styles.breakdown}`}>
          <div>
            <div className={styles.cardHeading}><h3>Spend by capability</h3><span>{example ? "Example" : `${days} days`}</span></div>
            <p className={styles.description}>Where your agents put their budget to work.</p>
          </div>
          {loading && !example ? <Skeleton className="h-16" /> : breakdown.length > 0 ? <dl className={styles.capabilities}>
            {breakdown.map(([capability, spend]) => <div key={capability}>
              <div className={styles.capabilityLabel}><dt title={capability}>{capability}</dt><dd>{formatUSDC(Math.round(spend))} <span>{Math.round(spend / total * 100)}%</span></dd></div>
              <div className={styles.track} aria-hidden="true"><span style={{ width: `${spend / total * 100}%` }} /></div>
            </div>)}
          </dl> : <p className={styles.empty}>{error ? "Spending data is unavailable." : "Your capability breakdown appears after your first paid request."}</p>}
        </article>
      </div>
      <p className={styles.note}>{example ? "Example charts are illustrative and separate from your account totals." : `Based on loaded requests · ${dateLabel(start)} – ${dateLabel(series.at(-1)!.date)} · dates in your local time`}</p>
    </section>
  );
}

function TimeChart({ series, kind, unavailable }: { series: Day[]; kind: "bar" | "line"; unavailable: boolean }) {
  const [selected, setSelected] = useState<number | null>(null);
  const id = useId();
  const width = 520;
  const left = 48;
  const right = 12;
  const top = 16;
  const bottom = 170;
  const highest = Math.max(...series.map((day) => kind === "bar" ? day.spend : day.requests));
  const max = kind === "bar" ? highest || 1_000_000 : Math.max(2, Math.ceil(highest / 2) * 2);
  const step = (width - left - right) / series.length;
  const x = (i: number) => left + step * (i + 0.5);
  const y = (value: number) => bottom - value / max * (bottom - top);
  const points = (key: "requests" | "paid") => series.map((day, i) => `${x(i)},${y(day[key])}`).join(" ");
  const active = selected !== null ? series[Math.min(selected, series.length - 1)] : null;
  const hasData = series.some((day) => kind === "bar" ? day.spend > 0 : day.requests > 0);
  return (
    <div className={styles.chart}>
      <div className={styles.chartDetail} aria-live="polite">
        {unavailable ? <span>Activity data is unavailable</span> : active ? <><span>{dateLabel(active.date)}</span><strong>{kind === "bar" ? formatUSDC(active.spend) : `${active.requests} requests · ${active.paid} paid`}</strong></> : <span>{hasData ? "Hover or focus a day to explore" : "No activity in this period"}</span>}
      </div>
      <svg viewBox={`0 0 ${width} 202`} className={styles.svg} role="group" aria-labelledby={`${id}-title`}>
        <title id={`${id}-title`}>{kind === "bar" ? "Daily spending in USDC" : "Daily requests and paid requests"}</title>
        {[0, 0.5, 1].map((fraction) => <g key={fraction} aria-hidden="true">
          <line x1={left} x2={width - right} y1={y(max * fraction)} y2={y(max * fraction)} className={styles.gridLine} />
          <text x={left - 8} y={y(max * fraction) + 3} textAnchor="end" className={styles.axis}>{number(kind === "bar" ? max * fraction / 1_000_000 : Math.round(max * fraction))}</text>
        </g>)}
        {kind === "line" && <g aria-hidden="true">
          <polygon points={`${x(0)},${bottom} ${points("requests")} ${x(series.length - 1)},${bottom}`} fill={`url(#${id}-area)`} />
          <defs><linearGradient id={`${id}-area`} x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="var(--color-foreground)" stopOpacity=".12" /><stop offset="100%" stopColor="var(--color-foreground)" stopOpacity="0" /></linearGradient></defs>
          <polyline points={points("paid")} className={styles.paidLine} />
          <polyline points={points("requests")} className={styles.requestLine} />
        </g>}
        {series.map((day, i) => <g key={day.date.toISOString()}>
          {kind === "bar" ? <rect x={x(i) - step * 0.3} y={y(day.spend)} width={step * 0.6} height={bottom - y(day.spend)} rx="2" className={styles.bar} opacity={selected === null || selected === i ? 1 : 0.4} /> : <>
            <circle cx={x(i)} cy={y(day.requests)} r={selected === i ? 4 : series.length <= 7 ? 3 : 0} className={styles.dot} />
            {selected === i && <line x1={x(i)} x2={x(i)} y1={top} y2={bottom} className={styles.gridLine} />}
          </>}
          {(series.length <= 7 || i === 0 || i === series.length - 1 || i % 7 === 0) && <text x={x(i)} y="195" textAnchor="middle" className={styles.axis} aria-hidden="true">{series.length <= 7 ? day.date.toLocaleDateString(undefined, { weekday: "short" }) : dateLabel(day.date)}</text>}
          <rect x={left + step * i} y={top - 5} width={step} height={bottom - top + 10} className={styles.hitArea} tabIndex={unavailable ? -1 : 0} role="img"
            aria-label={unavailable ? "Activity data unavailable" : `${dateLabel(day.date)}: ${kind === "bar" ? formatUSDC(day.spend) : `${day.requests} requests, ${day.paid} paid`}`}
            onMouseEnter={() => setSelected(i)} onMouseLeave={() => setSelected(null)} onFocus={() => setSelected(i)} onBlur={() => setSelected(null)}>
            <title>{`${dateLabel(day.date)} · ${kind === "bar" ? formatUSDC(day.spend) : `${day.requests} requests · ${day.paid} paid`}`}</title>
          </rect>
        </g>)}
      </svg>
      <div className={styles.legend} aria-hidden="true"><span><i />{kind === "bar" ? "Committed spend" : "All requests"}</span>{kind === "line" && <span><i className={styles.dashed} />Paid requests</span>}</div>
    </div>
  );
}
