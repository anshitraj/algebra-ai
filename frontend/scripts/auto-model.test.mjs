// Run: node --test scripts/auto-model.test.mjs   (node 24 strips the types)
// Which model "Auto" means: never a slow preview Pro model first.
import test from "node:test";
import assert from "node:assert/strict";
import { autoModel } from "../lib/agent/auto-model.ts";

const m = (...ids) => ids.map((id) => ({ id }));

test("Google's Auto skips a Pro model listed first and takes the Flash alias", () => {
  const list = m("gemini-3.1-pro-preview", "gemini-3.8-flash", "gemini-pro-latest", "gemini-flash-latest");
  assert.equal(autoModel("gemini", list)?.id, "gemini-flash-latest");
});

test("without the alias, the first Flash model", () => {
  assert.equal(autoModel("gemini", m("gemini-3.1-pro-preview", "gemini-3.8-flash"))?.id, "gemini-3.8-flash");
});

test("a Google list with no Flash model keeps its first", () => {
  assert.equal(autoModel("gemini", m("gemini-pro-latest", "gemini-ultra"))?.id, "gemini-pro-latest");
});

test("other providers keep their first model", () => {
  assert.equal(autoModel("anthropic", m("claude-sonnet-5-5", "claude-haiku-5-5"))?.id, "claude-sonnet-5-5");
  assert.equal(autoModel("openai", m("gpt-4o", "gpt-4o-mini"))?.id, "gpt-4o");
});

test("an empty list has no model", () => {
  assert.equal(autoModel("gemini", []), undefined);
});
