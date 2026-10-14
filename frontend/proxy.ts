import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";
import { visitorIp } from "./lib/visitor-ip";

// Optimistic route guard: no session cookie → straight to sign-in, before
// any console JavaScript loads. The real check is the API's, on every call;
// a stale or forged cookie gets a 401 there and the session provider sends
// the user back here.
const SESSION_COOKIE = "algebra_session";

// The API's per-IP limits (sign-in attempts, demo accounts, anonymous reads)
// need the visitor's address, and behind this app and the API host's own proxy
// the API only ever sees this host's: every visitor would share one budget.
// So requests on their way to the API carry the address (the platform's own
// header, which a visitor can't choose) and a shared secret that proves it
// came from here. The API reads the address only with the secret
// (EDGE_SHARED_SECRET there, ALGEBRA_EDGE_SECRET here); without it, or when
// this app has none, it falls back to X-Forwarded-For. A visitor's own copies
// of these headers are always dropped.
const API_PATH = /^\/(api\/v1\/|mcp$|\.well-known\/jwks\.json$)/;

function toApi(request: NextRequest) {
  const headers = new Headers(request.headers);
  headers.delete("x-algebra-edge-secret");
  headers.delete("x-algebra-client-ip");
  const secret = process.env.ALGEBRA_EDGE_SECRET?.trim();
  const ip = secret ? visitorIp(request.headers) : null;
  if (secret && ip) {
    headers.set("x-algebra-client-ip", ip);
    headers.set("x-algebra-edge-secret", secret);
  }
  return NextResponse.next({ request: { headers } });
}

export function proxy(request: NextRequest) {
  if (API_PATH.test(request.nextUrl.pathname)) return toApi(request);
  if (request.cookies.has(SESSION_COOKIE)) return NextResponse.next();
  const url = request.nextUrl.clone();
  const next = request.nextUrl.pathname + request.nextUrl.search;
  url.pathname = "/login";
  url.search = next && next !== "/console" ? `?next=${encodeURIComponent(next)}` : "";
  return NextResponse.redirect(url);
}

export const config = {
  matcher: ["/console/:path*", "/onboarding", "/api/v1/:path*", "/mcp", "/.well-known/jwks.json"],
};
