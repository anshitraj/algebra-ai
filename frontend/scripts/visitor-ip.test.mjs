// Run: node --test scripts/visitor-ip.test.mjs   (node 24 strips the types)
import test from "node:test";
import assert from "node:assert/strict";
import { visitorIp } from "../lib/visitor-ip.ts";

const h = (o) => new Headers(o);

test("the platform's own header wins, then x-real-ip, then x-forwarded-for", () => {
  assert.equal(visitorIp(h({ "x-vercel-forwarded-for": "198.51.100.23", "x-real-ip": "1.1.1.1", "x-forwarded-for": "2.2.2.2" })), "198.51.100.23");
  assert.equal(visitorIp(h({ "x-real-ip": "203.0.113.8", "x-forwarded-for": "2.2.2.2" })), "203.0.113.8");
  assert.equal(visitorIp(h({ "x-forwarded-for": "2.2.2.2" })), "2.2.2.2");
});

test("a list gives its first entry; whitespace is trimmed", () => {
  assert.equal(visitorIp(h({ "x-forwarded-for": " 198.51.100.23 , 10.0.0.1" })), "198.51.100.23");
});

test("IPv6 is accepted", () => {
  assert.equal(visitorIp(h({ "x-vercel-forwarded-for": "2001:db8::1" })), "2001:db8::1");
});

test("anything that is not an address is not used", () => {
  assert.equal(visitorIp(h({})), null);
  assert.equal(visitorIp(h({ "x-vercel-forwarded-for": "not-an-ip" })), null);
  assert.equal(visitorIp(h({ "x-vercel-forwarded-for": "" , "x-real-ip": "999.1.1.1" })), null);
  // A bad first header does not stop a good later one.
  assert.equal(visitorIp(h({ "x-vercel-forwarded-for": "garbage", "x-real-ip": "203.0.113.8" })), "203.0.113.8");
});
