// Run: node --test scripts/console-lib.test.mjs   (node 24 strips the types)
// The two pure helpers behind the invoice and the explorer links.
import test from "node:test";
import assert from "node:assert/strict";
import { policyReport } from "../lib/policy-check.ts";
import { explorerTx, solscanTx, isOnChain } from "../lib/explorer.ts";
import { approvalRule } from "../lib/pass-rules.ts";

const pass = (over = {}) => ({
  id: "pass_1",
  label: "Claude — paid APIs",
  budget_minor_units: 500_000,
  remaining_minor_units: 500_000,
  max_per_purchase_minor_units: 50_000,
  approve_above_minor_units: 20_000,
  allowed_merchants: [],
  ...over,
});
const budget = (r) => r.checks.find((c) => c.label === "Budget").result;

test("budget line before the payment: this call comes off once", () => {
  assert.equal(budget(policyReport(pass(), 2_000, true)), "0.498 USDC left of 0.5 USDC after this call");
});

test("two payments in one turn: the second one also counts the first", () => {
  // the pass was read before either payment: 0.5 left
  assert.equal(budget(policyReport(pass(), 2_000, true, false, "before", 0)), "0.498 USDC left of 0.5 USDC after this call");
  assert.equal(budget(policyReport(pass(), 1_000, true, false, "before", 2_000)), "0.497 USDC left of 0.5 USDC after this call");
});

test("an invoice reads the pass as it is now, which already counts the call", () => {
  // 0.497 left now; the invoice must not take the 0.002 off again (it used to say 0.495)
  const now = pass({ remaining_minor_units: 497_000 });
  assert.equal(budget(policyReport(now, 2_000, true, false, "now")), "0.497 USDC left of 0.5 USDC now");
});

test("a dry run says what the call would use", () => {
  assert.equal(budget(policyReport(pass(), 3_000, false, true)), "0.5 USDC left of 0.5 USDC; this call would use 0.003 USDC");
});

test("budget never goes below zero", () => {
  assert.equal(budget(policyReport(pass({ remaining_minor_units: 1_000 }), 5_000, true)), "0 USDC left of 0.5 USDC after this call");
});

test("explorer links: devnet carries ?cluster=devnet, mainnet does not", () => {
  const sig = "4PrRktVG6sQ6iygmybx6VjYbwwSyR6jSMQYqomv3MSLWoSGMnAPDdDmCro2RhXHjSdFkNVzA8BNaP9qQDY5DgMva";
  assert.equal(solscanTx(sig, "solana-devnet"), `https://solscan.io/tx/${sig}?cluster=devnet`);
  assert.equal(explorerTx(sig, "solana-devnet"), `https://explorer.solana.com/tx/${sig}?cluster=devnet`);
  assert.equal(solscanTx(sig, "solana"), `https://solscan.io/tx/${sig}`);
  assert.equal(explorerTx(sig, "solana"), `https://explorer.solana.com/tx/${sig}`);
});

test("the sandbox's simulated transactions are not on chain", () => {
  assert.equal(isOnChain("solana"), true);
  assert.equal(isOnChain("solana-devnet"), true);
  assert.equal(isOnChain("sandbox"), false);
  assert.equal(isOnChain(undefined), false);
});

test("zero on a pass means ask before every call, not never", () => {
  assert.deepEqual(approvalRule({}), { kind: "never" });
  assert.deepEqual(approvalRule({ approve_above_minor_units: null }), { kind: "never" });
  assert.deepEqual(approvalRule({ approve_above_minor_units: 0 }), { kind: "always" });
  assert.deepEqual(approvalRule({ approve_above_minor_units: 50_000 }), { kind: "above", minor: 50_000 });
});

// Provider names: catalogs that only know a host get a readable name, never a deployment hash.
import { nameFromHost, providerTitle, shortHost } from "../lib/provider-presentation.ts";

test("a host becomes the brand it names", () => {
  assert.equal(nameFromHost("api.bentcrypto.com"), "Bentcrypto");
  assert.equal(nameFromHost("x402.browserbase.com"), "Browserbase");
  assert.equal(nameFromHost("example.co.uk"), "Example");
});

test("a hosting platform's subdomain is the app's name, without its deployment hash", () => {
  assert.equal(nameFromHost("token-lemon-eta.vercel.app"), "Token Lemon Eta");
  assert.equal(nameFromHost("softbot-screenshot-mainnet.softbot.workers.dev"), "Softbot Screenshot");
  assert.equal(nameFromHost("blockrun-sol-vbsbhh7lea-uc.a.run.app"), "Blockrun");
  assert.equal(nameFromHost("myapp-git-main-team.vercel.app"), "Myapp");
  assert.equal(nameFromHost("1mpixels-one.vercel.app"), "1mpixels One");
});

test("UUID projects and tunnels have no name to show", () => {
  assert.equal(nameFromHost("01a046b2-7104-74c2-8a02-04715d7235e1-1aw0hwejp-xai-org.vercel.app"), "");
  assert.equal(nameFromHost("adenological-kenya-emphatically.ngrok-free.dev"), "");
});

test("a provider listed by host is titled by its description when the host has no name", () => {
  const host = "01a046b2-7104-74c2-8a02-04715d7235e1-1aw0hwejp-xai-org.vercel.app";
  assert.equal(providerTitle({ name: host, host, description: "Claude inference. GET this URL → HTTP 402" }), "Claude inference");
  assert.equal(providerTitle({ name: host, host, description: "" }), "Independent API");
  assert.equal(providerTitle({ name: "DKIM key lookup", host: "dkim.example.com", description: "" }), "DKIM key lookup");
  assert.equal(shortHost(host), "…-1aw0hwejp-xai-org.vercel.app");
});
