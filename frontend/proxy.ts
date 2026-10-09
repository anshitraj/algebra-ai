import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

// Optimistic route guard: no session cookie → straight to sign-in, before
// any console JavaScript loads. The real check is the API's, on every call;
// a stale or forged cookie gets a 401 there and the session provider sends
// the user back here.
const SESSION_COOKIE = "algebra_session";

export function proxy(request: NextRequest) {
  if (request.cookies.has(SESSION_COOKIE)) return NextResponse.next();
  const url = request.nextUrl.clone();
  const next = request.nextUrl.pathname + request.nextUrl.search;
  url.pathname = "/login";
  url.search = next && next !== "/console" ? `?next=${encodeURIComponent(next)}` : "";
  return NextResponse.redirect(url);
}

export const config = {
  matcher: ["/console/:path*", "/onboarding"],
};
