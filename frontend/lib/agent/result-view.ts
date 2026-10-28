// Provider payloads are data, never markup or instructions. Unknown shapes
// keep their own fields rather than being forced into a guessed API type.
import type { ReceiptInfo, StepDetail } from "./events";
type Row = Record<string, unknown>;
export type Field = { label: string; value: string };
export type ResultView =
  | { kind: "weather"; location?: string; temperature: number; unit: "°C" | "°F"; conditions?: string; feelsLike?: number; humidity?: number; wind?: number; windUnit: string }
  | { kind: "price"; symbol: string; price: number; history: number[] }
  | { kind: "risk"; score?: number; level?: string; checks: Field[] }
  | { kind: "search"; query?: string; items: { title: string; url?: string; snippet?: string }[] }
  | { kind: "image"; url: string; alt?: string }
  | { kind: "file"; url: string; title: string; name?: string }
  | { kind: "text"; text: string; title: string }
  | { kind: "table"; columns: string[]; rows: Row[]; count: number }
  | { kind: "fields"; fields: Field[]; title: string };

export function record(value: unknown): Row {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Row : {};
}

function numeric(...values: unknown[]): number | undefined {
  for (const value of values) {
    if ((typeof value === "number" || typeof value === "string" && value.trim() !== "") && Number.isFinite(Number(value))) return Number(value);
  }
}

function text(...values: unknown[]): string | undefined {
  return values.find((v): v is string => typeof v === "string" && v.trim().length > 0);
}

export function fieldLabel(key: string): string {
  return key.replace(/[_-]/g, " ").replace(/([a-z])([A-Z])/g, "$1 $2").replace(/^./, (s) => s.toUpperCase());
}

export function fieldValue(value: unknown): string {
  if (value === null) return "—";
  if (typeof value === "boolean") return value ? "Yes" : "No";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value ?? "—");
}

export function safeResultUrl(value: unknown): string | undefined {
  if (typeof value !== "string") return;
  try {
    const url = new URL(value);
    if (url.protocol === "https:" || url.protocol === "http:") return url.href;
  } catch { /* A non-URL remains plain text in the original response. */ }
}

export function decodeResult(value: unknown): unknown {
  let data = value;
  for (let i = 0; i < 4; i++) {
    if (typeof data === "string") {
      try { data = JSON.parse(data); } catch { return data; }
    } else {
      const r = record(data);
      const envelope = ["data", "result", "response", "output"].find((key) => r[key] !== null && typeof r[key] === "object");
      if (!envelope) break;
      // Do not throw away meaningful sibling results in multi-output APIs.
      const meaningful = Object.keys(r).filter((key) => !["status", "success", "code", "message", "meta", "metadata"].includes(key));
      if (meaningful.length !== 1) break;
      data = r[envelope];
    }
  }
  return data;
}

function fieldsOf(data: Row): Field[] {
  return Object.entries(data).filter(([, v]) => v === null || typeof v !== "object").slice(0, 10).map(([label, value]) => ({ label: fieldLabel(label), value: fieldValue(value) }));
}

function viewOf(value: unknown, work?: string): ResultView {
  const r = record(value);
  const current = record(r.current ?? r.current_weather);
  const weather = { ...r, ...current };
  const celsius = numeric(weather.temperature_c, weather.temp_c, weather.temperature_2m);
  const fahrenheit = numeric(weather.temperature_f, weather.temp_f);
  if (celsius !== undefined || fahrenheit !== undefined) {
    const location = record(r.location);
    return { kind: "weather", location: text(location.name, r.location, r.city, r.name), temperature: celsius ?? fahrenheit!, unit: celsius !== undefined ? "°C" : "°F",
      conditions: text(weather.conditions, record(weather.condition).text, weather.description),
      feelsLike: celsius !== undefined ? numeric(weather.feels_like_c, weather.feelslike_c, weather.apparent_temperature) : numeric(weather.feels_like_f, weather.feelslike_f),
      humidity: numeric(weather.humidity_pct, weather.relative_humidity_2m, weather.humidity), wind: numeric(weather.wind_kmh, weather.wind_kph, weather.wind_speed_10m, weather.wind_mph),
      windUnit: numeric(weather.wind_kmh, weather.wind_kph, weather.wind_speed_10m) !== undefined ? "km/h" : "mph" };
  }
  const price = numeric(r.price_usd, r.usd_price, r.usdPrice, record(r.quote).USD && record(record(r.quote).USD).price, work === "token.price" && (r.currency === "USD" || r.quote_currency === "USD") ? r.price : undefined);
  if (price !== undefined && price >= 0) {
    const series = r.price_history ?? r.sparkline ?? record(r.sparkline_in_7d).price;
    const history = Array.isArray(series) ? series.map((v) => numeric(typeof v === "object" ? record(v).price : v)).filter((v): v is number => v !== undefined && v >= 0).slice(-60) : [];
    return { kind: "price", symbol: text(r.symbol, record(r.token).symbol, record(r.asset).symbol) ?? (r.mint === "So11111111111111111111111111111111111111112" ? "SOL" : "Token"), price, history };
  }
  if (r.risk_score !== undefined || r.risk_level !== undefined) return { kind: "risk", score: numeric(r.risk_score), level: text(r.risk_level), checks: fieldsOf(record(r.checks)) };
  const results = r.results ?? r.organic_results;
  if (Array.isArray(results) && results.some((item) => text(record(item).title))) return { kind: "search", query: text(r.query), items: results.slice(0, 8).map((item) => { const row = record(item); return { title: text(row.title) ?? "Search result", url: safeResultUrl(row.url ?? row.link), snippet: text(row.snippet, row.description) }; }) };
  const image = safeResultUrl(r.image_url ?? (work?.includes("image") ? r.url : undefined));
  if (image) return { kind: "image", url: image, alt: text(r.alt, r.caption) };
  const fileKey = ["audio_url", "video_url", "file_url", "document_url", "download_url"].find((key) => safeResultUrl(r[key]));
  if (fileKey) return { kind: "file", url: safeResultUrl(r[fileKey])!, title: fieldLabel(fileKey.replace(/_url$/, "")) + " result", name: text(r.filename, r.file_name, r.title) };
  const body = typeof value === "string" ? value : text(r.text, r.markdown, r.content, r.answer, r.summary);
  if (body) return { kind: "text", text: body, title: text(r.title, r.model) ?? "Response" };
  if (Array.isArray(value) && value.length && value.every((row) => row && typeof row === "object" && !Array.isArray(row))) {
    const rows = value.slice(0, 8) as Row[];
    return { kind: "table", columns: [...new Set(rows.flatMap((row) => Object.keys(row)))].slice(0, 6), rows, count: value.length };
  }
  // Lists commonly arrive under a named key (balances, transactions, items…).
  const list = Object.values(r).find((v) => Array.isArray(v) && v.length && v.every((row) => row && typeof row === "object" && !Array.isArray(row)));
  if (list) return viewOf(list);
  const fields = fieldsOf(r);
  return { kind: "fields", fields: fields.length ? fields : [{ label: "Result", value: value == null ? "No response body returned" : fieldValue(value) }], title: "API result" };
}

export function resultViews(response: unknown, work?: string): ResultView[] {
  const data = decodeResult(response);
  if (work?.includes("image") && Array.isArray(data)) {
    const images = data.map((item) => safeResultUrl(record(item).url ?? record(item).image_url)).filter((url): url is string => !!url);
    if (images.length) return images.slice(0, 8).map((url) => ({ kind: "image", url }));
  }
  const r = record(data);
  const children = ["weather", "token_price", "market", "risk", "search"].filter((key) => r[key] && typeof r[key] === "object").map((key) => viewOf(decodeResult(r[key]), work));
  return children.length ? children : [viewOf(data, work)];
}

/** Saved chats predate result payloads on receipts. Never bind by provider or list position. */
export function responseForReceipt(receipt: Pick<ReceiptInfo, "intentId" | "stepId" | "result">, steps: { id: string; detail?: StepDetail }[]): unknown {
  if (receipt.result !== undefined) return receipt.result;
  const exact = receipt.stepId ? steps.find((step) => step.id === receipt.stepId) : undefined;
  const matching = exact ?? [...steps].reverse().find((step) => step.detail?.response !== undefined && step.detail.rows?.some((row) => row.label === "Intent" && row.value === receipt.intentId));
  return matching?.detail?.response;
}
