import test from "node:test";
import assert from "node:assert/strict";
import { decodeResult, responseForReceipt, resultViews, safeResultUrl } from "../lib/agent/result-view.ts";

test("live weather maps nested current values and retains zeros", () => {
  const [view] = resultViews({ location: { name: "Lisbon, Portugal" }, current: { temperature_c: 18.6, feels_like_c: 16.8, humidity_pct: 0, wind_kmh: 0, conditions: "Overcast" } });
  assert.equal(view.kind, "weather");
  assert.equal(view.location, "Lisbon, Portugal");
  assert.equal(view.temperature, 18.6);
  assert.equal(view.feelsLike, 16.8);
  assert.equal(view.humidity, 0);
  assert.equal(view.wind, 0);
  assert.equal(view.windUnit, "km/h");
});

test("flat demo weather and Fahrenheit APIs use the returned units", () => {
  assert.equal(resultViews({ temperature_c: 0, wind_kph: 3 })[0].temperature, 0);
  const [view] = resultViews({ temp_f: 55, feels_like_f: 51, wind_mph: 8 });
  assert.equal(view.unit, "°F");
  assert.equal(view.windUnit, "mph");
  assert.equal(view.feelsLike, 51);
  assert.equal(view.humidity, undefined);
});

test("price cards do not invent the token, a USD denomination or chart history", () => {
  const [view] = resultViews({ mint: "unknown-token", price_usd: "0.009" });
  assert.equal(view.kind, "price");
  assert.equal(view.symbol, "Token");
  assert.equal(view.price, .009);
  assert.deepEqual(view.history, []);
  assert.equal(resultViews({ price: 12, currency: "EUR" }, "token.price")[0].kind, "fields");
  assert.equal(resultViews({ price_usd: 0 })[0].price, 0);
});

test("a chart includes only explicit numeric provider history", () => {
  assert.deepEqual(resultViews({ symbol: "SOL", price_usd: 12, price_history: [11, null, { price: 13 }, "bad", 12] })[0].history, [11, 13, 12]);
});

test("JSON strings and single response envelopes are unwrapped", () => {
  assert.equal(resultViews(JSON.stringify({ success: true, data: { response: { price_usd: "1.0" } } }))[0].kind, "price");
  assert.equal(decodeResult("not JSON: provider text"), "not JSON: provider text");
});

test("combined weather and token responses render separate result cards", () => {
  assert.deepEqual(resultViews({ weather: { temperature_c: 18 }, token_price: { symbol: "SOL", price_usd: 152 } }).map(v => v.kind), ["weather", "price"]);
});

test("risk reports, search, text, image and record arrays use their own views", () => {
  assert.equal(resultViews({ risk_score: 0, risk_level: "low", checks: { freeze_authority_revoked: true } })[0].score, 0);
  assert.equal(resultViews({ results: [{ title: "Result", url: "https://example.com", snippet: "Description" }] })[0].kind, "search");
  assert.equal(resultViews({ text: "<script>alert(1)</script>" })[0].text, "<script>alert(1)</script>");
  assert.equal(resultViews({ image_url: "https://example.com/image.png" })[0].kind, "image");
  assert.equal(resultViews({ audio_url: "https://example.com/audio.mp3" })[0].kind, "file");
  assert.equal(resultViews({ document_url: "https://example.com/report.pdf" })[0].title, "Document result");
  assert.equal(resultViews({ data: [{ url: "https://example.com/generated.png" }] }, "image.generate")[0].kind, "image");
  // A list of token amounts is a balances card (it used to be a plain table).
  const [balances] = resultViews({ balances: [{ token: "SOL", amount: "0" }, { token: "USDC", amount: "1" }] });
  assert.equal(balances.kind, "balances");
  assert.equal(balances.items.length, 2);
});

test("provider links reject executable protocols", () => {
  for (const value of ["javascript:alert(1)", "data:text/html,<script>", "file:///tmp/secret", "not a URL"]) assert.equal(safeResultUrl(value), undefined);
  assert.equal(resultViews({ results: [{ title: "Unsafe link", url: "javascript:alert(1)" }] })[0].items[0].url, undefined);
  assert.equal(resultViews({ image_url: "javascript:alert(1)" })[0].kind, "fields");
});

test("unfamiliar, empty and truncated responses keep a readable fallback", () => {
  for (const value of [{ custom_metric: 3, enabled: false }, {}, null, { truncated: true, first_characters: '{"temperature_c":20' }]) assert.equal(resultViews(value)[0].kind, "fields");
  assert.equal(resultViews({ enabled: false })[0].fields[0].value, "No");
  assert.equal(resultViews({ custom_metric: 3 })[0].fields[0].label, "Custom metric");
});

test("multiple receipts stay bound to their own response even with the same provider", () => {
  const steps = [
    {id:"first", detail:{rows:[{label:"Intent",value:"weather"}],response:'{"temperature_c":18.6}'}},
    {id:"second", detail:{rows:[{label:"Intent",value:"price"}],response:'{"price_usd":0.009}'}}
  ];
  assert.equal(responseForReceipt({intentId:"price"}, steps), '{"price_usd":0.009}');
  assert.equal(responseForReceipt({intentId:"missing"}, steps), undefined);
});

test("direct payloads and explicit step IDs take precedence over a legacy trace", () => {
  const steps = [{id:"older", detail:{rows:[{label:"Intent",value:"same"}],response:'old'}},{id:"latest", detail:{response:'new'}}];
  assert.equal(responseForReceipt({intentId:"same",stepId:"latest"}, steps), 'new');
  assert.equal(responseForReceipt({intentId:"same",result:null}, steps), null);
});

test("a calendar answer becomes a schedule, sorted, with all-day and timed events", () => {
  const [view] = resultViews({ calendar: "Work", events: [
    { summary: "Standup", start: { dateTime: "2026-11-02T09:30:00Z" }, end: { dateTime: "2026-11-02T09:45:00Z" }, location: "Zoom" },
    { title: "Offsite", start: "2026-11-01", end: "2026-11-02" },
  ] });
  assert.equal(view.kind, "events");
  assert.equal(view.title, "Work");
  assert.deepEqual(view.items.map((e) => e.title), ["Offsite", "Standup"]);
  assert.equal(view.items[1].location, "Zoom");
});

test("news results with a date stay search results, not events", () => {
  const [view] = resultViews({ query: "solana", results: [{ title: "Solana news", url: "https://news.example/a", date: "2026-10-20" }] });
  assert.equal(view.kind, "search");
});

test("places keep rating, reviews and a maps link from coordinates", () => {
  const [view] = resultViews({ results: [
    { name: "Blue Tokai", formatted_address: "Lodhi Colony, Delhi", rating: 4.6, user_ratings_total: 812, geometry: { location: { lat: 28.59, lng: 77.22 } }, types: ["cafe"] },
  ] });
  assert.equal(view.kind, "places");
  assert.equal(view.items[0].rating, 4.6);
  assert.equal(view.items[0].reviews, 812);
  assert.match(view.items[0].url, /google\.com\/maps.*28\.59%2C77\.22/);
});

test("an IP lookup is one place", () => {
  const [view] = resultViews({ ip: "8.8.8.8", city: "Mountain View", region: "California", country: "US", latitude: 37.4, longitude: -122.08 });
  assert.equal(view.kind, "places");
  assert.equal(view.items[0].name, "Mountain View, California, US");
});

test("flights, an inbox and balances each get their own card", () => {
  assert.equal(resultViews({ flights: [{ origin: "DEL", destination: "BOM", airline: "IndiGo", flight_number: "6E 201", price: 4200, currency: "INR", departure_time: "2026-11-03T04:40:00Z" }] })[0].kind, "flights");
  const [inbox] = resultViews({ messages: [{ from: { name: "Circle", email: "hi@circle.com" }, subject: "Your USDC arrived", date: "2026-10-28T10:00:00Z", unread: true }] });
  assert.equal(inbox.kind, "messages");
  assert.equal(inbox.items[0].from, "Circle");
  const [bal] = resultViews({ balances: [{ symbol: "SOL", ui_amount: 1.5, usd_value: 150 }, { symbol: "USDC", ui_amount: 12 }] });
  assert.equal(bal.kind, "balances");
  assert.equal(bal.items[0].usd, 150);
});

test("a time series draws a chart; [[ts, value]] pairs too", () => {
  const [rows] = resultViews([{ date: "2026-10-01", close: 1 }, { date: "2026-10-02", close: 2 }, { date: "2026-10-03", close: 3 }]);
  assert.equal(rows.kind, "series");
  assert.equal(rows.points.length, 3);
  const [pairs] = resultViews({ prices: [[1761955200000, 150], [1762041600000, 152], [1762128000000, 149]] });
  assert.equal(pairs.kind, "series");
});

test("a translation reads Google's shape and plain shapes", () => {
  const [g] = resultViews({ data: { translations: [{ translatedText: "Bonjour", detectedSourceLanguage: "en" }] } });
  assert.equal(g.kind, "translation");
  assert.equal(g.text, "Bonjour");
  assert.equal(resultViews({ translated_text: "Hola", target_lang: "es" })[0].kind, "translation");
});

test("ordinary records are still a table", () => {
  const [view] = resultViews([{ id: 1, name: "a", score: 3 }, { id: 2, name: "b", score: 4 }]);
  assert.equal(view.kind, "table");
});

test("an IP lookup without coordinates is still a place, with its timezone", () => {
  const [view] = resultViews({ ip: "8.8.8.8", city: "Austin", country: "US", timezone: "America/Chicago" });
  assert.equal(view.kind, "places");
  assert.equal(view.items[0].name, "Austin, US");
  assert.equal(view.items[0].timezone, "America/Chicago");
  assert.match(view.items[0].url, /query=Austin%2C%20US/);
});
