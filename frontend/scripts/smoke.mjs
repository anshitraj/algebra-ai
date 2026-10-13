#!/usr/bin/env node
// Smoke test of the production build, run the way the container runs it
// (frontend/Dockerfile): the standalone server, NODE_ENV=production.
//
//   pnpm build && pnpm smoke
//
// A dev server never sends the Content-Security-Policy and never serves the
// built bundles, so a page that works under `next dev` can still be broken
// for every real visitor. This starts the built server next to a stub of the
// Go API and checks what only a production build shows: that every page
// renders, the security headers are sent, the CSP lets the browser reach
// each origin the client code calls, the console is closed without a session,
// the rewrites reach the API and the built assets load.
//
// The rewrite to the API is baked in at build time, so the stub listens where
// ALGEBRA_API_URL pointed during the build (http://localhost:8080 by default).
// Build and smoke with the same value; with that port taken, use another:
//   ALGEBRA_API_URL=http://127.0.0.1:8791 pnpm build && ALGEBRA_API_URL=http://127.0.0.1:8791 pnpm smoke

import { spawn } from "node:child_process";
import { cpSync, existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const dist = process.env.NEXT_DIST_DIR ?? ".next";
const apiURL = new URL(process.env.ALGEBRA_API_URL ?? "http://localhost:8080");
const port = Number(process.env.SMOKE_PORT ?? 3991);
const base = `http://127.0.0.1:${port}`;

const PUBLIC_PAGES = ["/", "/pricing", "/book-demo", "/verify", "/demo", "/login", "/signup", "/forgot-password", "/terms", "/privacy", "/contact"];
const CONSOLE_PAGES = [
  "/console",
  "/console/agent",
  "/console/providers",
  "/console/passes",
  "/console/firewall",
  "/console/routing",
  "/console/intelligence",
  "/console/executions",
  "/console/projects",
  "/console/quickstart",
  "/console/connect",
  "/console/settings",
  "/onboarding",
];

// Origins the browser must reach, each with the code that calls it. The scan
// of the client source below adds any Solana RPC, Jupiter or Privy origin
// written there, so a new one can't ship without the CSP allowing it.
const MUST_CONNECT = [
  ["https://api.devnet.solana.com", "components/console/account-wallet-signer.tsx (devnet RPC)"],
  ["wss://api.devnet.solana.com", "components/console/account-wallet-signer.tsx (devnet subscriptions)"],
  ["https://api.mainnet-beta.solana.com", "components/console/account-wallet-signer.tsx (mainnet RPC)"],
  ["wss://api.mainnet-beta.solana.com", "components/console/account-wallet-signer.tsx (mainnet subscriptions)"],
  ["https://lite-api.jup.ag", "components/console/convert-sol.tsx (Jupiter Swap API)"],
  ["https://auth.privy.io", "sign-in with Privy"],
];
const CALLED_HOST = /^(api\.[a-z0-9-]+\.solana\.com|[a-z0-9-]+\.jup\.ag|[a-z0-9-]+\.privy\.io)$/;

const failures = [];
let passed = 0;
function check(name, ok, detail = "") {
  if (ok) passed++;
  else failures.push(detail ? `${name}: ${detail}` : name);
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}${ok || !detail ? "" : `\n        ${detail}`}`);
}

/** Whether one CSP directive lets the page reach url. */
function cspAllows(csp, directive, url) {
  const sources = (csp.split(";").map((d) => d.trim().split(/\s+/)).find((d) => d[0] === directive) ?? []).slice(1);
  const u = new URL(url);
  return sources.some((s) => {
    if (s === "*") return true;
    if (s.endsWith(":") && !s.includes("/")) return u.protocol === s;
    const m = /^([a-z]+):\/\/(\*\.)?([^/:]+)(?::(\d+|\*))?/.exec(s);
    if (!m || `${m[1]}:` !== u.protocol) return false;
    if (m[4] && m[4] !== "*" && m[4] !== u.port) return false;
    return m[2] ? u.hostname.endsWith(`.${m[3]}`) : u.hostname === m[3];
  });
}

/** Origins of the Solana RPC, Jupiter and Privy URLs written in client source. */
function calledOrigins() {
  const found = new Map();
  const walk = (dir) => {
    for (const name of readdirSync(dir)) {
      const p = path.join(dir, name);
      if (statSync(p).isDirectory()) walk(p);
      else if (/\.tsx?$/.test(name)) {
        for (const m of readFileSync(p, "utf8").matchAll(/\b(https|wss):\/\/([a-z0-9.-]+)/g)) {
          if (CALLED_HOST.test(m[2])) found.set(`${m[1]}://${m[2]}`, path.relative(root, p).replaceAll("\\", "/"));
        }
      }
    }
  };
  walk(path.join(root, "components"));
  return found;
}

/** The Go API, as far as these checks need it: it says it was reached, and knows one signed-in session. */
function startStubAPI() {
  const server = createServer((req, res) => {
    const send = (status, body) => {
      res.writeHead(status, { "Content-Type": "application/json", "X-Smoke-Stub": "1" });
      res.end(JSON.stringify(body));
    };
    const url = new URL(req.url ?? "/", "http://stub");
    if (url.pathname === "/.well-known/jwks.json") return send(200, { keys: [] });
    if (url.pathname === "/api/v1/providers") return send(200, { total: 0, count: 0, categories: [], providers: [], trust: {} });
    if (url.pathname === "/api/v1/auth/agent-token") return send(401, { error: "not signed in" });
    return send(404, { error: "not in the smoke stub", path: url.pathname });
  });
  return new Promise((resolve, reject) => {
    server.once("error", (e) =>
      reject(new Error(e.code === "EADDRINUSE" ? `${apiURL.host} is taken. Build and smoke with ALGEBRA_API_URL set to a free port (see the top of this file).` : String(e))),
    );
    server.listen(Number(apiURL.port || 80), apiURL.hostname === "localhost" ? "127.0.0.1" : apiURL.hostname, () => resolve(server));
  });
}

/** The built server, as the image runs it: standalone, with the static files beside it. */
function startWeb() {
  const standalone = path.join(root, dist, "standalone");
  const entry = path.join(standalone, "server.js");
  if (!existsSync(entry)) throw new Error(`No production build at ${path.relative(root, entry)}. Run pnpm build first (with the same NEXT_DIST_DIR, if you set one).`);
  cpSync(path.join(root, dist, "static"), path.join(standalone, dist, "static"), { recursive: true });
  if (existsSync(path.join(root, "public"))) cpSync(path.join(root, "public"), path.join(standalone, "public"), { recursive: true });
  const child = spawn(process.execPath, [entry], {
    cwd: standalone,
    env: { ...process.env, NODE_ENV: "production", PORT: String(port), HOSTNAME: "127.0.0.1", NEXT_TELEMETRY_DISABLED: "1" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let log = "";
  child.stdout.on("data", (d) => (log += d));
  child.stderr.on("data", (d) => (log += d));
  return { child, log: () => log };
}

async function waitUntilUp(web) {
  for (let i = 0; i < 120; i++) {
    if (web.child.exitCode !== null) throw new Error(`The server exited (${web.child.exitCode}):\n${web.log()}`);
    try {
      await fetch(`${base}/robots.txt`, { signal: AbortSignal.timeout(2000) });
      return;
    } catch {
      await new Promise((r) => setTimeout(r, 500));
    }
  }
  throw new Error(`The server didn't answer on ${base} within a minute:\n${web.log()}`);
}

const get = (p, init = {}) => fetch(base + p, { redirect: "manual", signal: AbortSignal.timeout(30_000), ...init });

async function run() {
  // 1. Every public page renders from the build.
  let home = "";
  for (const p of PUBLIC_PAGES) {
    const res = await get(p);
    const html = await res.text();
    if (p === "/") home = html;
    const broken = /Application error|Internal Server Error|__next_error__/.test(html);
    check(`GET ${p} renders`, res.status === 200 && /text\/html/.test(res.headers.get("content-type") ?? "") && html.includes("</html>") && !broken, `status ${res.status}${broken ? ", an error page" : ""}`);
  }
  const missing = await get("/no-such-page-smoke");
  check("an unknown path is a 404", missing.status === 404, `status ${missing.status}`);

  // 2. The security headers only production sends.
  const res = await get("/");
  const h = (k) => res.headers.get(k) ?? "";
  const csp = h("content-security-policy");
  check("Content-Security-Policy is sent", csp.includes("default-src 'self'"), "missing: the app is not running as a production build");
  check("Strict-Transport-Security is sent", /max-age=\d{7,}/.test(h("strict-transport-security")));
  check("X-Frame-Options: DENY and frame-ancestors 'none'", h("x-frame-options") === "DENY" && csp.includes("frame-ancestors 'none'"));
  check("X-Content-Type-Options: nosniff", h("x-content-type-options") === "nosniff");
  check("no X-Powered-By", !res.headers.has("x-powered-by"));
  check("the CSP has no script wildcard", !/script-src[^;]*\s(\*|https:)(\s|;|$)/.test(csp));

  // 3. The CSP lets the browser reach what the client code calls.
  const called = new Map(MUST_CONNECT);
  for (const [origin, file] of calledOrigins()) if (!called.has(origin)) called.set(origin, file);
  for (const [origin, why] of called) check(`connect-src allows ${origin}`, cspAllows(csp, "connect-src", origin), `blocked in production; called from ${why}. Add it in next.config.ts`);
  // Privy's own list (docs.privy.io, "Content Security Policy"): the sign-in frame, WalletConnect's
  // relay and verifier, Coinbase's wallet link, Privy's RPC and Cloudflare's bot check. Privy's
  // dashboard asks for these before an app goes to production.
  for (const [directive, origin] of [
    ["frame-src", "https://auth.privy.io"],
    ["frame-src", "https://verify.walletconnect.com"],
    ["frame-src", "https://verify.walletconnect.org"],
    ["frame-src", "https://challenges.cloudflare.com"],
    ["child-src", "https://auth.privy.io"],
    ["script-src", "https://challenges.cloudflare.com"],
    ["connect-src", "wss://relay.walletconnect.com"],
    ["connect-src", "wss://relay.walletconnect.org"],
    ["connect-src", "wss://www.walletlink.org"],
    ["connect-src", "https://solana-mainnet.rpc.privy.systems"],
    ["connect-src", "https://explorer-api.walletconnect.com"],
  ]) {
    check(`${directive} allows ${origin} (Privy)`, cspAllows(csp, directive, origin));
  }
  for (const d of ["object-src 'none'", "base-uri 'self'", "form-action 'self'", "worker-src 'self'", "manifest-src 'self'"]) {
    check(`the CSP sets ${d} (Privy)`, csp.split(";").some((x) => x.trim() === d));
  }

  // 4. The console is closed without a session, and renders with one.
  for (const p of ["/console", "/console/agent?prompt=hi", "/onboarding"]) {
    const r = await get(p);
    const to = r.headers.get("location") ?? "";
    check(`${p} without a session goes to sign-in`, r.status >= 300 && r.status < 400 && new URL(to, base).pathname === "/login", `status ${r.status}, location ${to || "(none)"}`);
  }
  const deep = await get("/console/agent?prompt=hi");
  check("sign-in keeps where the person was going", (new URL(deep.headers.get("location") ?? "/", base).searchParams.get("next") ?? "").startsWith("/console/agent"));
  for (const p of CONSOLE_PAGES) {
    const r = await get(p, { headers: { Cookie: "algebra_session=smoke" } });
    const html = await r.text();
    check(`GET ${p} renders with a session cookie`, r.status === 200 && html.includes("</html>") && !/Application error|__next_error__/.test(html), `status ${r.status}`);
  }

  // 5. The rewrites reach the API, and the app's own route handlers answer.
  for (const p of ["/api/v1/providers?limit=1", "/.well-known/jwks.json"]) {
    const r = await get(p);
    check(`${p} is proxied to the API`, r.status === 200 && r.headers.get("x-smoke-stub") === "1", `status ${r.status}; was the build made with ALGEBRA_API_URL=${apiURL.origin}?`);
  }
  const models = await get("/api/agent/providers");
  const list = await models.json().catch(() => null);
  check("GET /api/agent/providers lists the model providers", models.status === 200 && Array.isArray(list?.providers), `status ${models.status}`);
  const chat = await get("/api/agent/chat", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ message: "hi" }) });
  // 503: no model key on this server; 401: a key, and no session. Either way nothing runs for a stranger.
  check("POST /api/agent/chat refuses a request with no session", chat.status === 401 || chat.status === 503, `status ${chat.status}`);
  const junk = await get("/api/agent/chat", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{" });
  check("POST /api/agent/chat rejects a malformed body", junk.status === 400, `status ${junk.status}`);

  // 6. The built assets the home page asks for are served.
  const assets = [...new Set([...home.matchAll(/(?:src|href)="(\/_next\/static\/[^"]+\.(?:js|css))"/g)].map((m) => m[1]))];
  check("the home page references built assets", assets.length > 0);
  for (const a of [assets.find((x) => x.endsWith(".js")), assets.find((x) => x.endsWith(".css"))].filter(Boolean)) {
    const r = await get(a);
    await r.arrayBuffer();
    check(`GET ${a.length > 60 ? `${a.slice(0, 57)}...` : a}`, r.status === 200 && /immutable/.test(r.headers.get("cache-control") ?? ""), `status ${r.status}`);
  }
  for (const p of ["/robots.txt", "/sitemap.xml"]) check(`GET ${p}`, (await get(p)).status === 200);
  // Every email shows this mark, fetched by the mail client from the public site.
  const mark = await get("/email/algebra-mark.png");
  check("GET /email/algebra-mark.png is a PNG", mark.status === 200 && /image\/png/.test(mark.headers.get("content-type") ?? ""), `status ${mark.status}`);
}

let stub;
let web;
let code = 1;
try {
  stub = await startStubAPI();
  web = startWeb();
  await waitUntilUp(web);
  await run();
  console.log(`\n${passed} passed, ${failures.length} failed`);
  if (failures.length) console.log(failures.map((f) => `  - ${f}`).join("\n"));
  code = failures.length ? 1 : 0;
} catch (err) {
  console.error(`\nsmoke: ${err instanceof Error ? err.message : err}`);
} finally {
  web?.child.kill();
  stub?.close();
}
process.exit(code);
