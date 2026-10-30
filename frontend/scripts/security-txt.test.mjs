import test from "node:test";
import assert from "node:assert/strict";
import { securityTxt } from "../lib/security-txt.ts";

const now = new Date("2026-10-30T00:00:00Z");

test("a configured contact makes a file with the lines RFC 9116 requires", () => {
  const body = securityTxt({ contact: "security@example.com", site: "https://www.example.com/", now });
  assert.match(body, /^Contact: mailto:security@example\.com$/m);
  assert.match(body, /^Expires: 2027-04-28T00:00:00\.000Z$/m);
  assert.match(body, /^Canonical: https:\/\/www\.example\.com\/\.well-known\/security\.txt$/m);
  assert.ok(body.endsWith("\n"));
});

test("Expires is less than a year ahead, as the RFC asks", () => {
  const body = securityTxt({ contact: "a@b.co", site: "https://x.example", now });
  const expires = new Date(/^Expires: (.+)$/m.exec(body)[1]);
  assert.ok(expires > now && expires.getTime() - now.getTime() < 365 * 24 * 3600 * 1000);
});

test("no contact, or one that isn't an address, is no file at all", () => {
  for (const contact of ["", "   ", "not an address", "a@b", "a b@c.co", "<x@y.co>"]) {
    assert.equal(securityTxt({ contact, site: "https://x.example", now }), null, JSON.stringify(contact));
  }
});

test("a local site gets no canonical line, since it isn't where the file lives", () => {
  const body = securityTxt({ contact: "a@b.co", site: "http://localhost:3000", now });
  assert.doesNotMatch(body, /Canonical/);
});
