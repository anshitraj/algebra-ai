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
  | { kind: "fields"; fields: Field[]; title: string }
  | { kind: "events"; title?: string; items: EventItem[] }
  | { kind: "places"; items: PlaceItem[] }
  | { kind: "flights"; items: FlightItem[] }
  | { kind: "messages"; items: MessageItem[] }
  | { kind: "balances"; items: BalanceItem[]; totalUsd?: number }
  | { kind: "series"; label?: string; points: SeriesPoint[] }
  | { kind: "translation"; text: string; from?: string; to?: string };

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

// --- Shapes beyond the basics: what a paid answer is, read from its fields rather than from who sold it, so any
// seller's calendar, places, flights, inbox, chart, balances or translation gets a card of its own.

export type EventItem = { title: string; start: string; end?: string; location?: string; url?: string };
export type PlaceItem = { name: string; address?: string; rating?: number; reviews?: number; lat?: number; lng?: number; url?: string; category?: string; timezone?: string };
export type FlightItem = { from: string; to: string; depart?: string; arrive?: string; carrier?: string; number?: string; price?: string; stops?: number };
export type MessageItem = { from: string; subject: string; date?: string; snippet?: string; unread?: boolean };
export type BalanceItem = { symbol: string; amount: string; usd?: number };
export type SeriesPoint = { t: string; v: number };

/** A date or time a person would read, as an ISO string; undefined for anything that isn't one. */
export function when(value: unknown): string | undefined {
  const v = record(value);
  const raw = value && typeof value === "object" && !Array.isArray(value) ? v.dateTime ?? v.date ?? v.datetime ?? v.time : value;
  if (typeof raw === "number" && Number.isFinite(raw) && raw > 1e9) return new Date(raw < 1e12 ? raw * 1000 : raw).toISOString();
  if (typeof raw !== "string" || !/\d/.test(raw) || raw.length > 40) return undefined;
  const ms = Date.parse(raw);
  return Number.isNaN(ms) ? undefined : new Date(ms).toISOString();
}

const first = (row: Row, keys: string[]) => keys.map((k) => row[k]).find((v) => v !== undefined && v !== null && v !== "");

/** The first list of records in a response: the response itself, or the first list under a key. */
function rowsOf(value: unknown): Row[] | undefined {
  const isRows = (v: unknown): v is Row[] => Array.isArray(v) && v.length > 0 && v.every((row) => row && typeof row === "object" && !Array.isArray(row));
  if (isRows(value)) return value;
  const r = record(value);
  for (const key of ["events", "items", "results", "data", "places", "flights", "offers", "messages", "emails", "balances", "tokens", "assets"]) {
    if (isRows(r[key])) return r[key] as Row[];
  }
  return Object.values(r).find(isRows);
}

function eventsOf(rows: Row[]): EventItem[] | undefined {
  const start = ["start", "start_time", "starts_at", "start_date", "startDate", "begin"];
  const items = rows.flatMap((row) => {
    const title = text(row.title, row.summary, row.name);
    const at = when(first(row, start)) ?? (row.end !== undefined || row.location !== undefined || row.venue !== undefined ? when(row.date) : undefined);
    if (!title || !at) return [];
    return [{ title, start: at, end: when(first(row, ["end", "end_time", "ends_at", "end_date", "endDate"])), location: text(row.location, record(row.venue).name, row.venue), url: safeResultUrl(row.url ?? row.htmlLink ?? row.link) }];
  });
  return items.length && items.length >= rows.length / 2 ? items.sort((a, b) => a.start.localeCompare(b.start)).slice(0, 12) : undefined;
}

function coords(row: Row): { lat?: number; lng?: number } {
  const loc = record(record(row.geometry).location ?? row.location ?? row.coordinates ?? row.geo);
  const lat = numeric(row.lat, row.latitude, loc.lat, loc.latitude);
  const lng = numeric(row.lng, row.lon, row.long, row.longitude, loc.lng, loc.lon, loc.longitude);
  return lat !== undefined && lng !== undefined && Math.abs(lat) <= 90 && Math.abs(lng) <= 180 ? { lat, lng } : {};
}

function placeOf(row: Row): PlaceItem | undefined {
  const { lat, lng } = coords(row);
  const displayName = record(row.displayName).text;
  const city = text(row.city);
  const name = text(row.name, displayName, row.title) ?? (city ? [city, text(row.region, row.region_name), text(row.country, row.country_name)].filter(Boolean).join(", ") : undefined);
  const address = text(row.formatted_address, row.formattedAddress, row.address, row.vicinity);
  // An IP lookup names a city without coordinates: still a place.
  const located = lat !== undefined || !!address || (city !== undefined && (row.ip !== undefined || row.query !== undefined));
  if (!name || !located) return undefined;
  return {
    name, address, lat, lng, timezone: text(row.timezone, row.time_zone, record(row.timezone).id),
    rating: numeric(row.rating), reviews: numeric(row.user_ratings_total, row.userRatingCount, row.review_count, row.reviews),
    category: text(row.category, Array.isArray(row.types) ? row.types[0] : undefined, row.primaryType),
    url: safeResultUrl(row.url ?? row.googleMapsUri ?? row.website) ?? `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(lat !== undefined ? `${lat},${lng}` : name)}`,
  };
}

function flightsOf(rows: Row[]): FlightItem[] | undefined {
  const items = rows.flatMap((row) => {
    const dep = record(row.departure), arr = record(row.arrival);
    const from = text(row.origin, row.departure_airport, dep.iata, dep.airport, typeof row.from === "string" ? row.from : undefined);
    const to = text(row.destination, row.arrival_airport, arr.iata, arr.airport, typeof row.to === "string" ? row.to : undefined);
    const carrier = text(row.airline, row.carrier, record(row.airline).name);
    const number = text(row.flight_number, row.flight, record(row.flight).iata);
    if (!from || !to || (!carrier && !number)) return [];
    const price = numeric(row.price, record(row.price).total, row.total_amount, row.amount);
    const currency = text(row.currency, record(row.price).currency, row.total_currency) ?? "";
    return [{ from, to, carrier, number, depart: when(row.departure_time ?? row.depart_at ?? dep.scheduled ?? dep.time), arrive: when(row.arrival_time ?? row.arrive_at ?? arr.scheduled ?? arr.time), price: price !== undefined ? `${price.toLocaleString("en-US", { maximumFractionDigits: 2 })} ${currency}`.trim() : undefined, stops: numeric(row.stops, row.number_of_stops) }];
  });
  return items.length ? items.slice(0, 10) : undefined;
}

function messagesOf(rows: Row[]): MessageItem[] | undefined {
  const items = rows.flatMap((row) => {
    const fromRow = record(row.from);
    const from = text(typeof row.from === "string" ? row.from : undefined, fromRow.name, fromRow.email, row.sender, row.from_address);
    const subject = text(row.subject, row.title);
    if (!from || !subject || (row.subject === undefined && row.sender === undefined && row.from === undefined)) return [];
    return [{ from, subject, date: when(row.date ?? row.received_at ?? row.created_at ?? row.timestamp), snippet: text(row.snippet, row.preview, row.text), unread: row.unread === true || row.read === false }];
  });
  return items.length ? items.slice(0, 10) : undefined;
}

function balancesOf(rows: Row[]): BalanceItem[] | undefined {
  const items = rows.flatMap((row) => {
    const symbol = text(row.symbol, row.token, record(row.token).symbol, row.asset, row.currency);
    const amount = first(row, ["ui_amount", "uiAmount", "amount", "balance", "quantity"]);
    if (!symbol || amount === undefined || typeof amount === "object") return [];
    return [{ symbol, amount: fieldValue(amount), usd: numeric(row.usd_value, row.value_usd, row.usd, row.valueUsd) }];
  });
  return items.length && items.length >= rows.length / 2 ? items.slice(0, 12) : undefined;
}

function seriesOf(value: unknown): SeriesPoint[] | undefined {
  const r = record(value);
  // CoinGecko-style [[timestamp, value], ...].
  const pairs = Array.isArray(value) ? value : r.prices ?? r.series ?? r.points;
  if (Array.isArray(pairs) && pairs.length >= 3 && pairs.every((p) => Array.isArray(p) && p.length >= 2)) {
    const points = pairs.flatMap((p) => {
      const t = when(p[0]), v = numeric(p[1]);
      return t && v !== undefined ? [{ t, v }] : [];
    });
    if (points.length >= 3) return points.slice(-120);
  }
  const rows = rowsOf(value);
  if (!rows || rows.length < 3 || rows.some((row) => Object.keys(row).length > 6)) return undefined;
  const points = rows.flatMap((row) => {
    const t = when(first(row, ["date", "time", "timestamp", "t", "day", "period"]));
    const v = numeric(row.value, row.close, row.price, row.v, row.count, row.total);
    return t && v !== undefined ? [{ t, v }] : [];
  });
  return points.length >= 3 && points.length >= rows.length * 0.8 ? points.sort((a, b) => a.t.localeCompare(b.t)).slice(-120) : undefined;
}

function translationOf(r: Row): { text: string; from?: string; to?: string } | undefined {
  // Google answers {data: {translations: [...]}}; decodeResult may already have unwrapped the "data" envelope.
  const list = record(r.data).translations ?? r.translations;
  const google = record(Array.isArray(list) ? list[0] : undefined);
  const out = text(r.translated_text, r.translatedText, r.translation, google.translatedText);
  if (!out) return undefined;
  return { text: out, from: text(r.source_lang, r.source_language, r.from, google.detectedSourceLanguage), to: text(r.target_lang, r.target_language, r.to) };
}

/** The richer shapes, tried before the generic ones. */
function shapedView(value: unknown, work?: string): ResultView | undefined {
  const r = record(value);
  const translation = translationOf(r);
  if (translation) return { kind: "translation", ...translation };
  const series = seriesOf(value);
  if (series) return { kind: "series", label: text(r.label, r.metric, r.symbol, work), points: series };
  const rows = rowsOf(value);
  if (rows) {
    const events = eventsOf(rows);
    if (events) return { kind: "events", title: text(r.calendar, r.title, r.summary), items: events };
    const messages = messagesOf(rows);
    if (messages) return { kind: "messages", items: messages };
    const flights = flightsOf(rows);
    if (flights) return { kind: "flights", items: flights };
    const places = rows.map(placeOf).filter((p): p is PlaceItem => !!p);
    if (places.length && places.length >= rows.length / 2) return { kind: "places", items: places.slice(0, 10) };
    const balances = balancesOf(rows);
    if (balances) return { kind: "balances", items: balances, totalUsd: numeric(r.total_usd, r.totalUsd, r.total_value_usd) };
  }
  // One place: an IP lookup, a geocoded address.
  const place = !Array.isArray(value) ? placeOf(r) : undefined;
  if (place) return { kind: "places", items: [place] };
  return undefined;
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
  const shaped = shapedView(value, work);
  if (shaped) return shaped;
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
