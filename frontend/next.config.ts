import type { NextConfig } from "next";

// The browser only ever talks to this app's own origin. /api/v1/* is proxied
// to the Go API, so the HttpOnly session cookie it sets is first-party and
// no CORS preflight or token-in-JavaScript is involved. ALGEBRA_API_URL is
// server-only; it's read at build/start time.
const API_URL = process.env.ALGEBRA_API_URL ?? process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
const isProd = process.env.NODE_ENV === "production";

// Everything is self-hosted (next/font, no third-party scripts). Images may
// come from OAuth avatar hosts, hence https: for img-src. 'unsafe-inline'
// for scripts is what Next's inline bootstrap needs without a nonce proxy;
// dev additionally needs 'unsafe-eval' for fast refresh, so CSP is prod-only.
const csp = [
  "default-src 'self'",
  // Razorpay Checkout (billing page only) loads its script and opens its
  // payment frames from these origins.
  // Privy (sign-in): its auth frame, Cloudflare's bot check and the
  // WalletConnect relay, as listed in Privy's CSP guide.
  "script-src 'self' 'unsafe-inline' https://checkout.razorpay.com https://challenges.cloudflare.com",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob: https:",
  "font-src 'self' data:",
  "connect-src 'self' https://*.razorpay.com https://auth.privy.io wss://relay.walletconnect.com wss://relay.walletconnect.org wss://www.walletlink.org https://*.rpc.privy.systems https://explorer-api.walletconnect.com",
  // The order page's delivery map is a Google Maps embed, loaded only when
  // the user opens it.
  "frame-src https://api.razorpay.com https://checkout.razorpay.com https://maps.google.com https://www.google.com https://auth.privy.io https://verify.walletconnect.com https://verify.walletconnect.org https://challenges.cloudflare.com",
  "child-src https://auth.privy.io https://verify.walletconnect.com https://verify.walletconnect.org",
  "worker-src 'self'",
  "frame-ancestors 'none'",
  "base-uri 'self'",
  "form-action 'self'",
  "object-src 'none'",
  "upgrade-insecure-requests",
].join("; ");

const securityHeaders = [
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=(), payment=(), usb=()" },
  ...(isProd
    ? [
        { key: "Strict-Transport-Security", value: "max-age=63072000; includeSubDomains" },
        { key: "Content-Security-Policy", value: csp },
      ]
    : []),
];

const nextConfig: NextConfig = {
  // Self-contained server bundle for the container image (web/Dockerfile).
  output: "standalone",
  poweredByHeader: false,
  // Lets a second dev server run from the same checkout (Next locks a
  // distDir per `next dev`), e.g. NEXT_DIST_DIR=.next-alt.
  distDir: process.env.NEXT_DIST_DIR ?? ".next",
  async rewrites() {
    return [
      { source: "/api/v1/:path*", destination: `${API_URL}/api/v1/:path*` },
      // The MCP server the API itself serves, for agents that speak MCP.
      { source: "/mcp", destination: `${API_URL}/mcp` },
      // The key set that verifies spend receipts, on the public domain.
      { source: "/.well-known/jwks.json", destination: `${API_URL}/.well-known/jwks.json` },
    ];
  },
  async headers() {
    return [{ source: "/:path*", headers: securityHeaders }];
  },
};

export default nextConfig;
