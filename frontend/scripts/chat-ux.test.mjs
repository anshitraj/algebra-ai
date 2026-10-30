// Run: node --test scripts/chat-ux.test.mjs   (node 24 strips the types)
// The helpers behind the comparison list.
import test from "node:test";
import assert from "node:assert/strict";
import { autoPicks, sharedRefusal, MAX_AUTO_PAY } from "../lib/agent/option-view.ts";
import { vaultFix } from "../lib/decision.ts";

const rec = (over = {}) => ({
  provider: "demo:alpha",
  candidate_id: "c-" + (over.provider ?? "demo:alpha"),
  total_minor: 1_200,
  price_source: "live",
  reliability: 0.9,
  score: 1,
  decision: "ALLOW",
  seller: { known: false, payment_tested: false, payee_pinned: false },
  ...over,
});
const group = (id, recs, strategies = ["cheapest", "balanced", "fastest"]) => ({
  id,
  work: "token.price",
  title: "Token price",
  input: {},
  maxPriceUsdc: 0.01,
  network: "solana-devnet",
  decision: "ALLOW",
  recommendations: recs.map((r, i) => ({ ...r, strategy: strategies[i] })),
  compared: recs,
  refused: [],
});

test("refusals every offer shares are said once", () => {
  const vault = "ONCHAIN_PASS_REQUIRED: this pass has no vault";
  assert.deepEqual(sharedRefusal([rec({ decision: "BLOCK", reasons: [vault] }), rec({ provider: "demo:b", decision: "BLOCK", reasons: [vault] })]), [vault]);
  assert.equal(sharedRefusal([rec({ decision: "BLOCK", reasons: [vault] }), rec({ provider: "demo:b", decision: "ALLOW" })]), null);
  assert.equal(sharedRefusal([]), null);
});

test("automatic payment picks the recommended offer only when the pass allows it outright", () => {
  const ok = group("g1", [rec({ provider: "demo:a", total_minor: 1_000 }), rec({ provider: "demo:b", total_minor: 1_200 }), rec({ provider: "demo:c", total_minor: 2_000 })]);
  const picks = autoPicks([ok], 500_000);
  assert.equal(picks.length, 1);
  assert.equal(picks[0].rec.provider, "demo:b"); // the balanced one
  assert.ok(picks[0].ways.includes("balanced"));

  const held = group("g2", [rec({ provider: "demo:a" }), rec({ provider: "demo:b", decision: "HOLD" }), rec({ provider: "demo:c" })]);
  assert.deepEqual(autoPicks([held], 500_000), []); // waits for a yes: the person presses Pay
  const refused = group("g3", [rec({ provider: "demo:a" }), rec({ provider: "demo:b", decision: "BLOCK" }), rec({ provider: "demo:c" })]);
  assert.deepEqual(autoPicks([refused], 500_000), []);
  const listed = group("g4", [rec({ provider: "demo:a" }), rec({ provider: "demo:b", price_source: "unlisted" }), rec({ provider: "demo:c" })]);
  assert.deepEqual(autoPicks([listed], 500_000), []);
});

test("automatic payment stays within what the pass has left and within three at a time", () => {
  const mk = (id, minor) => group(id, [rec({ provider: "demo:x" + id, total_minor: minor }), rec({ provider: "demo:b" + id, total_minor: minor }), rec({ provider: "demo:z" + id, total_minor: minor })]);
  const groups = [mk("a", 400), mk("b", 400), mk("c", 400)];
  // 1,000 left: two of 400 fit, the third doesn't.
  assert.equal(autoPicks(groups, 1_000).length, 2);
  const many = ["a", "b", "c", "d", "e"].map((id) => mk(id, 1));
  assert.equal(autoPicks(many, 1_000_000).length, MAX_AUTO_PAY);
  assert.equal(autoPicks(groups, undefined).length, 3);
});

test("the vault link lands on the pass's own card", () => {
  const fix = vaultFix(["ONCHAIN_PASS_REQUIRED"], "pass_a1256abe-e77e");
  assert.equal(fix?.href, "/console/firewall#pass-pass_a1256abe-e77e");
  assert.equal(vaultFix(["ONCHAIN_PASS_REQUIRED"])?.href, "/console/firewall");
  assert.equal(vaultFix(["PASS_BUDGET_EXHAUSTED"]), null);
});
