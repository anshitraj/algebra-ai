"use client";

import { useId, useState } from "react";
import type { ReceiptInfo } from "@/lib/agent/events";
import { decodeResult, fieldLabel, fieldValue, record, resultViews, type ResultView } from "@/lib/agent/result-view";
import { IconCheck, IconExternal, IconReceipt, IconSearch, IconShield, IconWallet } from "@/components/icons";
import { SolanaMark } from "@/components/chain-logos";
import { BalancesCard, EventsCard, FlightsCard, MessagesCard, PlacesCard, SeriesCard, TranslationCard } from "./result-shapes";
import styles from "./delivery.module.css";

function WeatherArt({ conditions }: { conditions?: string }) {
  const gradientId = useId();
  const sunny = /sun|clear|fair/i.test(conditions ?? "");
  const rain = /rain|drizzle|shower/i.test(conditions ?? "");
  return <svg className={styles.weatherArt} viewBox="0 0 130 96" fill="none" aria-hidden="true">
    <defs><linearGradient id={gradientId} x1="40" y1="20" x2="80" y2="85" gradientUnits="userSpaceOnUse"><stop stopColor="#c4cbde" /><stop offset="1" stopColor="#eef0f7" /></linearGradient></defs>
    {sunny && <g stroke="#f3b943" strokeWidth="3" strokeLinecap="round"><circle cx="86" cy="32" r="19" fill="#ffd67c" /><path d="M86 4v-2M86 62v-2M57 32h-3M118 32h-3M66 12l-2-2M107 53l-2-2M66 52l-2 2M106 12l2-2" /></g>}
    <path d="M28 73a16 16 0 0 1-1-32 26 26 0 0 1 49-6 19 19 0 0 1 30 17 13 13 0 0 1-1 26H28Z" fill={`url(#${gradientId})`} />
    <path d="M57 84a13 13 0 0 1 0-26 20 20 0 0 1 37-5 15 15 0 0 1 24 13 10 10 0 0 1-1 20H57Z" fill="#e8ecf5" />
    {rain && <path d="m44 82-3 6m23-5-3 6m23-3-3 6" stroke="#94b5e4" strokeWidth="3" strokeLinecap="round" />}
  </svg>;
}

function WeatherMetricIcon({ kind }: { kind: "temperature" | "humidity" | "wind" }) {
  return <svg width="16" height="19" viewBox="0 0 20 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true">{kind === "temperature" ? <><path d="M8 14V5a2 2 0 0 1 4 0v9a4 4 0 1 1-4 0Z" /><path d="M10 9v10" /></> : kind === "humidity" ? <path d="M10 3S4 11 4 15a6 6 0 0 0 12 0c0-4-6-12-6-12Z" /> : <><path d="M2 8h11a3 3 0 1 0-3-3M2 12h14a3 3 0 1 1-3 3M2 17h5a2 2 0 1 1-2 2" /></>}</svg>;
}

function PriceHistory({ values }: { values: number[] }) {
  if (values.length < 2) return null;
  const min = Math.min(...values), max = Math.max(...values);
  const points = values.map((value, i) => `${i / (values.length - 1) * 100},${max === min ? 20 : 36 - (value - min) / (max - min) * 30}`).join(" ");
  return <svg className={styles.sparkline} viewBox="0 0 100 40" role="img" aria-label="Price history returned by the provider"><polyline points={points} fill="none" stroke="currentColor" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" /></svg>;
}

function Fields({ fields }: { fields: { label: string; value: string }[] }) {
  return <dl className={styles.fields}>{fields.map((field, i) => <div key={`${field.label}-${i}`}><dt>{field.label}</dt><dd>{field.value}</dd></div>)}</dl>;
}

function ResultCard({ view, receipt }: { view: ResultView; receipt: ReceiptInfo }) {
  const [imageFailed, setImageFailed] = useState(false);
  if (view.kind === "weather") return <article className={`${styles.resultCard} ${styles.weather}`} aria-label="Weather result">
    <div className={styles.weatherMain}><WeatherArt conditions={view.conditions} /><div><p className={styles.eyebrow}>{view.location || "Current weather"}</p><strong className={styles.temperature}>{view.temperature.toLocaleString("en-US", { maximumFractionDigits: 1 })}{view.unit}</strong>{view.conditions && <p className={styles.condition}>{view.conditions}</p>}</div></div>
    <div className={styles.weatherMetrics}>
      {view.feelsLike !== undefined && <div><span><WeatherMetricIcon kind="temperature" /></span><p>Feels like<strong>{view.feelsLike}{view.unit}</strong></p></div>}
      {view.humidity !== undefined && <div><span><WeatherMetricIcon kind="humidity" /></span><p>Humidity<strong>{view.humidity}%</strong></p></div>}
      {view.wind !== undefined && <div><span><WeatherMetricIcon kind="wind" /></span><p>Wind<strong>{view.wind} {view.windUnit}</strong></p></div>}
    </div>
  </article>;
  if (view.kind === "price") return <article className={`${styles.resultCard} ${styles.priceCard}`} aria-label={`${view.symbol} price result`}>
    <div className={styles.priceMain}><span className={styles.assetMark} aria-hidden="true">{view.symbol === "SOL" ? <SolanaMark size={33} /> : view.symbol.slice(0, 2)}</span><div><p className={styles.eyebrow}>{view.symbol} price <span>(USD)</span></p><strong className={styles.priceValue}>{new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: view.price >= 1 ? 2 : 0, maximumFractionDigits: 8 }).format(view.price)}</strong></div><PriceHistory values={view.history} /></div>
    <div className={styles.callCost}><span><IconWallet size={19} /></span><div><p>Call cost</p><strong>{receipt.paid}</strong></div></div>
  </article>;
  if (view.kind === "risk") return <article className={`${styles.resultCard} ${styles.riskCard}`} aria-label="Token risk result"><header><span className={styles.resultIcon}><IconShield size={21} /></span><div><p className={styles.eyebrow}>Provider risk assessment</p><h3>{view.level ? `${fieldLabel(view.level)} risk` : "Risk report"}</h3></div>{view.score !== undefined && <strong className={styles.riskScore}>{view.score}<small>Score</small></strong>}</header><Fields fields={view.checks} /></article>;
  if (view.kind === "search") return <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Search results"><header><IconSearch size={18} /><h3>{view.query || "Search results"}</h3><span className={styles.count}>{view.items.length} shown</span></header><ol className={styles.searchResults}>{view.items.map((item, i) => <li key={i}><span>{i + 1}</span><div>{item.url ? <a href={item.url} target="_blank" rel="noopener noreferrer">{item.title}<IconExternal size={12} /></a> : <strong>{item.title}</strong>}{item.url && <small>{new URL(item.url).hostname}</small>}{item.snippet && <p>{item.snippet}</p>}</div></li>)}</ol></article>;
  if (view.kind === "image") return <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Image result"><header><IconReceipt size={17} /><h3>Image result</h3><a href={view.url} target="_blank" rel="noopener noreferrer">Open image <IconExternal size={12} /></a></header>{!imageFailed ?
    // eslint-disable-next-line @next/next/no-img-element -- arbitrary API output URLs are displayed without server-side fetching
    <img className={styles.outputImage} src={view.url} alt={view.alt || "Image returned by the API"} loading="lazy" referrerPolicy="no-referrer" onError={() => setImageFailed(true)} /> : <p className={styles.imageFallback}>The image preview couldn&apos;t load. Open the image to view the returned file.</p>}</article>;
  if (view.kind === "file") return <article className={`${styles.resultCard} ${styles.wide}`} aria-label={view.title}><header><span className={styles.resultIcon}><IconReceipt size={19} /></span><h3>{view.title}</h3><a href={view.url} target="_blank" rel="noopener noreferrer">Open file <IconExternal size={12} /></a></header><p className={styles.textResult}>{view.name || new URL(view.url).pathname.split("/").pop() || "Returned file"}</p><p className={styles.source}>{new URL(view.url).hostname}</p></article>;
  if (view.kind === "text") return <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Text result"><header><IconReceipt size={17} /><h3>{view.title}</h3></header><p className={styles.textResult}>{view.text}</p></article>;
  if (view.kind === "table") return <article className={`${styles.resultCard} ${styles.wide}`} aria-label="Data result"><header><IconReceipt size={17} /><h3>Data result</h3><span className={styles.count}>{view.count} records</span></header><div className={styles.tableScroll}><table><thead><tr>{view.columns.map((column) => <th key={column}>{fieldLabel(column)}</th>)}</tr></thead><tbody>{view.rows.map((row, i) => <tr key={i}>{view.columns.map((column) => <td key={column}>{fieldValue(row[column])}</td>)}</tr>)}</tbody></table></div>{view.count > view.rows.length && <p className={styles.source}>Showing {view.rows.length} of {view.count} records. Full output is in Response details.</p>}</article>;
  if (view.kind === "events") return <EventsCard title={view.title} items={view.items} />;
  if (view.kind === "places") return <PlacesCard items={view.items} />;
  if (view.kind === "flights") return <FlightsCard items={view.items} />;
  if (view.kind === "messages") return <MessagesCard items={view.items} />;
  if (view.kind === "balances") return <BalancesCard items={view.items} totalUsd={view.totalUsd} />;
  if (view.kind === "series") return <SeriesCard label={view.label} points={view.points} />;
  if (view.kind === "translation") return <TranslationCard text={view.text} from={view.from} to={view.to} />;
  return <article className={`${styles.resultCard} ${styles.wide}`} aria-label="API result"><header><span className={styles.resultIcon}><IconCheck size={19} /></span><h3>{view.title}</h3></header><Fields fields={view.fields} /></article>;
}

export function ResultCards({ results }: { results: { receipt: ReceiptInfo; response: unknown }[] }) {
  return <div className={styles.results}>{results.flatMap(({ receipt, response }) => resultViews(response, receipt.work).map((view, i) => <ResultCard key={`${receipt.intentId}-${i}`} view={view} receipt={receipt} />))}</div>;
}

export function ResultSource({ response }: { response: unknown }) {
  const data = record(decodeResult(response));
  const source = [data.source, data.attribution].filter((v): v is string => typeof v === "string");
  if (!source.length && data.demo !== true) return null;
  return <div>
    {source.length > 0 && <p className={styles.source}>{source.join(" · ")}</p>}
    {data.demo === true && <p className={styles.source}>Demo response · Sample data</p>}
  </div>;
}

export function ResponseDetails({ receipt, response }: { receipt: ReceiptInfo; response: unknown }) {
  return <div className={styles.responseDetails}>
    <details><summary>Response details <span>{receipt.provider}</span></summary><pre>{typeof response === "string" ? response : JSON.stringify(response, null, 2) ?? "No response body returned"}</pre></details>
  </div>;
}
