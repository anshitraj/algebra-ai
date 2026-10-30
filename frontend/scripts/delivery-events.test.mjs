import test from "node:test";
import assert from "node:assert/strict";
import { createJiti } from "jiti";
const jiti = createJiti(import.meta.url);
const { receiptOf } = await jiti.import("../lib/agent/run-tool.ts");

test("delivered receipt carries the actual response and originating step", () => {
  const result = { current: { temperature_c: 18.6 } };
  const receipt = receiptOf({ intent_id: "paid-1", outcome: "delivered", paid_minor: 1000, network: "solana-devnet", response: result }, "demo:nimbus", { work: "weather.forecast", stepId: "step-1", elapsedMs: 5800 });
  assert.deepEqual(receipt.result, result);
  assert.equal(receipt.work, "weather.forecast");
  assert.equal(receipt.stepId, "step-1");
  assert.equal(receipt.elapsedMs, 5800);
  assert.equal(receipt.paidMinor, 1000);
});

test("pending, failed and unpaid responses do not produce paid-delivery receipts", () => {
  for (const outcome of ["failed", "being_confirmed", "refused", "approval_required"]) assert.equal(receiptOf({ intent_id: "test", outcome, paid_minor: 1000 }, "provider"), undefined);
  assert.equal(receiptOf({ intent_id: "test", outcome: "delivered", paid_minor: 0 }, "provider"), undefined);
});

test("legacy calls to receiptOf stay compatible without step metadata", () => {
  const receipt = receiptOf({ intent_id: "old", outcome: "delivered", paid_minor: 500 }, "provider");
  assert.equal(receipt.paid, "0.0005 USDC");
  assert.equal(receipt.stepId, undefined);
});
