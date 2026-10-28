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
  const [table] = resultViews({ balances: [{ token: "SOL", amount: "0" }, { token: "USDC", amount: "1" }] });
  assert.equal(table.kind, "table");
  assert.equal(table.count, 2);
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
