// Run: node --test scripts/privy-identity.test.mjs   (node 24 strips the types)
// How the sign-in gets Privy's identity token with as few requests as possible, and what it does
// when Privy answers 429 (lib/privy-identity.ts).
import test from "node:test";
import assert from "node:assert/strict";
import { NoIdentityToken, gatherIdentityToken, viewIdentityToken } from "../lib/privy-identity.ts";

const b64u = (o) => Buffer.from(typeof o === "string" ? o : JSON.stringify(o)).toString("base64url");
/** A token shaped like Privy's: signed by nobody, which is fine: the code under test only reads it. */
function jwt(did, accounts) {
  return `${b64u({ alg: "ES256", typ: "JWT" })}.${b64u({ sub: did, iss: "privy.io", linked_accounts: JSON.stringify(accounts) })}.${b64u("sig")}`;
}
const EMAIL = { type: "email", address: "a@b.co" };
const SOL = { type: "wallet", chain_type: "solana", address: "11111111111111111111111111111111", wallet_client_type: "privy" };
const ETH = { type: "wallet", chain_type: "ethereum", address: "0xabc" };
const DID = "did:privy:abc";

const rate = () => Object.assign(new Error("Too many requests"), { status: 429 });

/** A stand-in for the Privy SDK: a token store and counters for the requests the sign-in makes. */
function sdk({ store = null, made = false, create, refresh, did = DID, lateBy = 0 }) {
  const s = { token: store, creates: 0, refreshes: 0, sleeps: [], waits: [], steps: [] };
  let reads = 0;
  const opts = {
    did,
    walletJustMade: made,
    createWallet: async () => {
      s.creates++;
      create?.(s);
    },
    refreshUser: async () => {
      s.refreshes++;
      refresh?.(s);
    },
    // lateBy: the store reaches the panel only after that many reads, as a render would
    stored: () => (++reads <= lateBy ? null : s.token),
    isRefused: (err) => err?.status === 429,
    sleep: async (ms) => {
      s.sleeps.push(ms);
    },
    onStep: (x) => s.steps.push(x),
    onWait: (x) => s.waits.push(x),
  };
  return { s, opts };
}

test("viewIdentityToken tells whose token it is and whether a Solana wallet is in it", () => {
  assert.deepEqual(viewIdentityToken(jwt(DID, [EMAIL])), { did: DID, solanaWallet: false });
  assert.deepEqual(viewIdentityToken(jwt(DID, [EMAIL, SOL])), { did: DID, solanaWallet: true });
  assert.equal(viewIdentityToken(jwt(DID, [EMAIL, ETH])).solanaWallet, false);
  // a name with accents and CJK characters in the claims doesn't break the reading
  assert.equal(viewIdentityToken(jwt(DID, [{ type: "google_oauth", name: "Zoë 李" }, SOL])).solanaWallet, true);
  // some tokens carry the linked accounts as a real array instead of a JSON string
  assert.equal(viewIdentityToken(`${b64u({ alg: "ES256" })}.${b64u({ sub: DID, linked_accounts: [SOL] })}.x`).solanaWallet, true);
});

test("viewIdentityToken returns null for anything that isn't a Privy token", () => {
  for (const bad of [null, undefined, "", "abc", "a.b.c", "a..c", `${b64u("{}")}.${b64u({ nosub: 1 })}.x`, `${b64u("{}")}.${b64u({ sub: 5 })}.x`]) {
    assert.equal(viewIdentityToken(bad), null, JSON.stringify(bad));
  }
});

test("a person who already had a wallet: the token from sign-in is used and Privy is not asked for anything", async () => {
  const token = jwt(DID, [EMAIL, SOL]);
  const { s, opts } = sdk({ store: token });
  assert.deepEqual(await gatherIdentityToken(opts), { token, walletPending: false });
  assert.equal(s.creates + s.refreshes, 0);
  assert.deepEqual(s.steps, ["reading your Privy identity"]);
});

test("a wallet sign-in needs no wallet in the token to be used when none was made", async () => {
  const token = jwt(DID, [SOL]);
  const { s, opts } = sdk({ store: token });
  assert.equal((await gatherIdentityToken(opts)).token, token);
  assert.equal(s.refreshes, 0);
});

test("a new wallet already in the stored token (the SDK refreshed after making it): no request to users/me", async () => {
  const withWallet = jwt(DID, [EMAIL, SOL]);
  const { s, opts } = sdk({ store: jwt(DID, [EMAIL]), made: true, create: (x) => (x.token = withWallet) });
  const got = await gatherIdentityToken(opts);
  assert.deepEqual(got, { token: withWallet, walletPending: false });
  assert.equal(s.creates, 1);
  assert.equal(s.refreshes, 0);
  assert.deepEqual(s.steps, ["making your wallet", "reading your Privy identity"]);
});

test("a new wallet missing from the stored token: exactly one refresh", async () => {
  const withWallet = jwt(DID, [EMAIL, SOL]);
  const { s, opts } = sdk({ store: jwt(DID, [EMAIL]), made: true, refresh: (x) => (x.token = withWallet) });
  assert.equal((await gatherIdentityToken(opts)).token, withWallet);
  assert.equal(s.refreshes, 1);
  assert.deepEqual(s.waits, []);
});

test("Privy refuses the refresh twice, then accepts: pauses of 2s and 4s, then it works", async () => {
  const withWallet = jwt(DID, [EMAIL, SOL]);
  let n = 0;
  const { s, opts } = sdk({
    store: jwt(DID, [EMAIL]),
    made: true,
    refresh: (x) => {
      if (++n <= 2) throw rate();
      x.token = withWallet;
    },
  });
  assert.equal((await gatherIdentityToken(opts)).token, withWallet);
  assert.equal(s.refreshes, 3);
  assert.deepEqual(s.waits, [2, 4]);
  assert.deepEqual(s.sleeps.filter((ms) => ms >= 1000), [2000, 4000]);
});

test("Privy keeps refusing the refresh: signs in with the stored token, wallet pending", async () => {
  const stale = jwt(DID, [EMAIL]);
  const { s, opts } = sdk({
    store: stale,
    made: true,
    refresh: () => {
      throw rate();
    },
  });
  assert.deepEqual(await gatherIdentityToken(opts), { token: stale, walletPending: true });
  assert.equal(s.refreshes, 4); // the first try and three retries
  assert.deepEqual(s.waits, [2, 4, 8]);
});

test("Privy keeps refusing and nothing is stored: the refusal is thrown", async () => {
  const { opts } = sdk({
    made: true,
    refresh: () => {
      throw rate();
    },
  });
  await assert.rejects(gatherIdentityToken(opts), (e) => e.status === 429);
});

test("the dashboard isn't set to return identity tokens: NoIdentityToken, not a stale token", async () => {
  const none = sdk({ refresh: (x) => (x.token = null) });
  await assert.rejects(gatherIdentityToken(none.opts), (e) => e instanceof NoIdentityToken && /Return user data/.test(e.message));
  // a refresh answered without a token clears the stored one too, as the SDK does
  const cleared = sdk({ store: jwt(DID, [EMAIL]), made: true, refresh: (x) => (x.token = null) });
  await assert.rejects(gatherIdentityToken(cleared.opts), (e) => e instanceof NoIdentityToken);
});

test("a token left from another Privy user is never used", async () => {
  const other = jwt("did:privy:someone-else", [EMAIL, SOL]);
  const mine = jwt(DID, [EMAIL, SOL]);
  const refreshes = sdk({ store: other, refresh: (x) => (x.token = mine) });
  assert.equal((await gatherIdentityToken(refreshes.opts)).token, mine);
  assert.equal(refreshes.s.refreshes, 1);
  const refused = sdk({
    store: other,
    refresh: () => {
      throw rate();
    },
  });
  await assert.rejects(gatherIdentityToken(refused.opts), (e) => e.status === 429);
});

test("making the wallet refused once: tried again after 2s, then the rest runs", async () => {
  const withWallet = jwt(DID, [EMAIL, SOL]);
  let n = 0;
  const { s, opts } = sdk({
    store: jwt(DID, [EMAIL]),
    made: true,
    create: (x) => {
      if (++n === 1) throw rate();
      x.token = withWallet;
    },
  });
  assert.equal((await gatherIdentityToken(opts)).token, withWallet);
  assert.equal(s.creates, 2);
  assert.deepEqual(s.waits, [2]);
  assert.equal(s.refreshes, 0);
});

test("making the wallet failing for another reason is not retried", async () => {
  const { s, opts } = sdk({
    store: jwt(DID, [EMAIL]),
    made: true,
    create: () => {
      throw new Error("Failed to connect to wallet proxy");
    },
  });
  await assert.rejects(gatherIdentityToken(opts), /wallet proxy/);
  assert.equal(s.creates, 1);
  assert.deepEqual(s.waits, []);
});

test("a token that reaches the panel a render late is waited for, not fetched again", async () => {
  const token = jwt(DID, [EMAIL, SOL]);
  const { s, opts } = sdk({ store: token, lateBy: 3 });
  assert.equal((await gatherIdentityToken(opts)).token, token);
  assert.equal(s.refreshes, 0);
  assert.equal(s.sleeps.length, 3);
});

test("a refresh that works but still lists no wallet: signs in with it, wallet pending", async () => {
  const stale = jwt(DID, [EMAIL]);
  const { s, opts } = sdk({ store: stale, made: true, refresh: () => {} });
  assert.deepEqual(await gatherIdentityToken(opts), { token: stale, walletPending: true });
  assert.equal(s.refreshes, 1);
});
