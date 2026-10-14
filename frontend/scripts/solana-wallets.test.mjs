// Run: node --test scripts/solana-wallets.test.mjs   (node 24 strips the types)
import test from "node:test";
import assert from "node:assert/strict";
import { canSignIn, sortWallets, walletFailure, shouldTryOffchain, watchWallets, signWithWallet, connectAccount } from "../lib/solana-wallets.ts";

const account = { address: "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin", publicKey: new Uint8Array(32) };

function wallet(name, over = {}) {
  return {
    name,
    icon: "data:image/png;base64,AAAA",
    chains: ["solana:mainnet"],
    accounts: [],
    features: {
      "standard:connect": { connect: async () => ({ accounts: [account] }) },
      "solana:signMessage": { signMessage: async () => [{ signedMessage: new Uint8Array(), signature: new Uint8Array([1, 2, 3, 255]) }] },
    },
    ...over,
  };
}

test("walletFailure: a rejected request says nothing was sent", () => {
  for (const err of [{ code: 4001, message: "User rejected the request." }, new Error("The user rejected the request"), "Popup closed"]) {
    const f = walletFailure(err, "Phantom");
    assert.equal(f.kind, "cancelled");
    assert.match(f.message, /You cancelled in Phantom/);
    assert.match(f.message, /nothing was sent/);
  }
});

test("walletFailure: a request already waiting is named, not called a cancel", () => {
  const f = walletFailure({ code: -32002, message: "Request of type 'connect' already pending for origin http://localhost:3300" }, "Phantom");
  assert.equal(f.kind, "pending");
  assert.match(f.message, /already has a request waiting/);
});

test("walletFailure: locked, unsupported and unknown each get their own advice", () => {
  assert.equal(walletFailure(new Error("Wallet is locked"), "Solflare").kind, "locked");
  const hw = walletFailure(new Error("Ledger device: UNKNOWN_ERROR off-chain messages not supported"), "Phantom");
  assert.equal(hw.kind, "unsupported");
  assert.match(hw.message, /sign in with email/);
  const other = walletFailure(new Error("Unexpected error"), "Backpack");
  assert.equal(other.kind, "other");
  assert.match(other.message, /Backpack: Unexpected error/);
});

test("walletFailure: reads the wallet's error out of a wrapper", () => {
  const wrapped = { name: "WalletSignMessageError", message: "Unexpected error", error: { code: 4001, message: "User rejected the request." } };
  assert.equal(walletFailure(wrapped, "Phantom").kind, "cancelled");
});

test("walletFailure: survives nothing useful at all", () => {
  for (const err of [undefined, null, {}, 42]) {
    const f = walletFailure(err, "");
    assert.equal(f.kind, "other");
    assert.match(f.message, /Your wallet didn't finish signing you in/);
  }
});

test("shouldTryOffchain only for the unsupported kind", () => {
  assert.equal(shouldTryOffchain(new Error("hardware wallet does not support this")), true);
  assert.equal(shouldTryOffchain({ code: 4001, message: "User rejected the request." }), false);
});

test("canSignIn: Solana + connect + signMessage, never Privy's own wallet", () => {
  assert.equal(canSignIn(wallet("Phantom")), true);
  assert.equal(canSignIn(wallet("MetaMask", { chains: ["eip155:1"] })), false);
  assert.equal(canSignIn(wallet("NoSign", { features: { "standard:connect": { connect: async () => ({ accounts: [] }) } } })), false);
  assert.equal(canSignIn(wallet("Privy", { features: { ...wallet("x").features, "privy:": {} } })), false);
});

test("sortWallets puts Phantom, Solflare, Backpack first", () => {
  const names = sortWallets([wallet("Zed"), wallet("Backpack"), wallet("Alpha"), wallet("Phantom"), wallet("Solflare")]).map((w) => w.name);
  assert.deepEqual(names, ["Phantom", "Solflare", "Backpack", "Alpha", "Zed"]);
});

test("signWithWallet returns base64; connectAccount returns the first account", async () => {
  const w = wallet("Phantom");
  assert.equal(await signWithWallet(w, account, new Uint8Array([1])), Buffer.from([1, 2, 3, 255]).toString("base64"));
  assert.equal((await connectAccount(w)).address, account.address);
  await assert.rejects(connectAccount(wallet("Empty", { features: { "standard:connect": { connect: async () => ({ accounts: [] }) }, "solana:signMessage": {} } })), /without sharing an account/);
});

test("watchWallets finds wallets that registered before and after it started", () => {
  const events = new EventTarget();
  globalThis.window = events;
  const seen = [];
  const early = wallet("Phantom");
  const late = wallet("Solflare");
  // A wallet that announces itself whenever the app says it is ready.
  const listeners = [];
  events.addEventListener("wallet-standard:app-ready", (e) => listeners.push(e.detail));
  const stop = watchWallets((ws) => seen.push(ws.map((w) => w.name)));
  assert.equal(listeners.length, 1, "app-ready was announced");
  // The wallet answers app-ready by registering.
  listeners[0].register(early);
  // One that loads afterwards fires register-wallet with a callback.
  events.dispatchEvent(new CustomEvent("wallet-standard:register-wallet", { detail: (api) => api.register(late) }));
  assert.deepEqual(seen.at(-1), ["Phantom", "Solflare"]);
  stop();
  events.dispatchEvent(new CustomEvent("wallet-standard:register-wallet", { detail: (api) => api.register(wallet("Ignored")) }));
  assert.equal(seen.at(-1).includes("Ignored"), false);
  delete globalThis.window;
});
