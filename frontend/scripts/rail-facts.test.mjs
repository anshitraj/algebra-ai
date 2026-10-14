// Run: node --test scripts/rail-facts.test.mjs   (node 24 strips the types)
// What the chat tells the model about the paying wallet.
import test from "node:test";
import assert from "node:assert/strict";
import { railFacts } from "../lib/agent/rail-facts.ts";

const rail = (over = {}) => ({ network: "solana-devnet", configured: true, ...over });

test("the sandbox has no wallet to talk about", () => {
  assert.deepEqual(railFacts("sandbox", undefined, undefined), []);
  assert.deepEqual(railFacts("sandbox", rail({ funded: false }), { required: true }), []);
});

test("a vault-only network: a payer with no USDC is normal, not a problem", () => {
  const [line] = railFacts("solana-devnet", rail({ funded: false }), { required: true, signer_ready: true });
  assert.match(line, /Spend Pass vault/);
  assert.match(line, /zero balance there is normal/);
  assert.doesNotMatch(line, /holds no USDC on this network/);
});

test("a vault-only network whose payer has no SOL for fees says payments fail", () => {
  const [line] = railFacts("solana-devnet", rail({ funded: false }), { required: true, signer_ready: false });
  assert.match(line, /no SOL for network fees/);
  assert.match(line, /fail until it is topped up/);
});

test("without a vault requirement the payer's own balance still matters", () => {
  assert.match(railFacts("solana", rail({ network: "solana", funded: false }), undefined)[0], /holds no USDC on this network/);
  assert.match(railFacts("solana", rail({ network: "solana", funded: true, max_payment_minor: 500_000 }), { required: false })[0], /funded; it pays at most 0\.5 USDC per call/);
});

test("no wallet and an unreachable node come first", () => {
  assert.match(railFacts("solana-devnet", undefined, { required: true })[0], /no wallet for devnet/);
  assert.match(railFacts("solana", rail({ configured: false }), undefined)[0], /no wallet for mainnet/);
  assert.match(railFacts("solana-devnet", rail({ error: "x" }), { required: true })[0], /can't be reached/);
});
