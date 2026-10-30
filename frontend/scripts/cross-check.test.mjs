import test from "node:test";
import assert from "node:assert/strict";
import { checkForModel, checkSets, MAX_CHECK } from "../lib/agent/cross-check.ts";

const card = (group, provider) => ({ group, work: "token.price", provider, intentId: `eint_${group}_${provider}` });

test("a comparison two providers answered is a check, and one provider alone is not", () => {
  assert.deepEqual(checkSets([card("g1", "alpha"), card("g1", "beta")]).map((s) => s.map((p) => p.provider)), [["alpha", "beta"]]);
  assert.deepEqual(checkSets([card("g1", "alpha")]), []);
  assert.deepEqual(checkSets([]), []);
});

test("each comparison is its own check, and cards of different comparisons are never mixed", () => {
  const sets = checkSets([card("g1", "alpha"), card("g2", "alpha"), card("g1", "beta"), card("g3", "gamma"), card("g2", "beta")]);
  assert.deepEqual(sets.map((s) => s.map((p) => `${p.group}:${p.provider}`)), [["g1:alpha", "g1:beta"], ["g2:alpha", "g2:beta"]]);
});

test("one provider twice is not a check, and counts once in one", () => {
  assert.deepEqual(checkSets([card("g1", "alpha"), card("g1", "alpha")]), []);
  const sets = checkSets([card("g1", "alpha"), card("g1", "alpha"), card("g1", "beta")]);
  assert.deepEqual(sets[0].map((p) => p.provider), ["alpha", "beta"]);
});

test("no more answers are compared than the API takes", () => {
  const many = ["a", "b", "c", "d", "e", "f", "g"].map((p) => card("g1", p));
  assert.equal(checkSets(many)[0].length, MAX_CHECK);
});

test("the model is never told that answers it could not compare agree", () => {
  const info = (verdict, summary) => ({ group: "g1", work: "token.price", report: { verdict, summary, providers: ["alpha", "beta"] } });
  const unclear = checkForModel(info("unclear", "Nothing in these answers could be compared."));
  assert.equal(unclear.cross_check.verdict, "unclear");
  assert.match(unclear.cross_check.note, /do not call them consistent/);
  const agree = checkForModel(info("agree", "All 2 providers agree."));
  assert.match(agree.cross_check.note, /one sentence/);
  assert.match(agree.cross_check.note, /untrusted/);
  assert.deepEqual(agree.cross_check.providers, ["alpha", "beta"]);
});
