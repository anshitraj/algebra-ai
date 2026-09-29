# Production launch

What it takes to run Algebra for real users, what the code already enforces, and what still needs a partner or an account only you can open.

## 1. What's production-ready in code

| Area | State |
|---|---|
| Accounts | Email + password (argon2id), Google/GitHub OAuth (PKCE, verified-email linking only), password reset via Resend, HttpOnly/SameSite=Lax/Secure session cookies, per-device sign-out |
| Authorization | Human-only endpoints (approve, payment methods, addresses, `/me`) accept only a session; agents get scoped, revocable tokens; every intent-scoped call checks the intent belongs to the caller's user |
| Policy | Per-user guardrails evaluated server-side on every purchase and again at payment time; hard caps can't be approved past |
| Agent | Gemini / Claude / OpenAI, streamed tool steps, approvals always a human tap |
| Web search | Live Google results through Gemini grounding — only links Google actually returned are kept, each resolved to the store's own URL and SSRF-checked; prices are labelled "as seen on the web" |
| Hardening | `APP_ENV=production` refuses to boot with unsafe settings (below); security headers + CSP; strict per-IP limit on sign-in/sign-up/reset (10/min); request IDs + structured access logs that never contain query strings, bodies, cookies or tokens; `/healthz` liveness, `/readyz` checks Postgres + Redis; server timeouts; hourly purge of dead sessions and reset tokens |
| Billing | Algebra's own plans via Razorpay Subscriptions (UPI/cards): checkout signature verified server-side, webhooks HMAC-verified with replay protection, cancel-at-period-end, free tier capped at 100 order executions/month (402 when exceeded), Growth counts overage |
| Packaging | `Dockerfile` (API, distroless, non-root, migrations included) and `web/Dockerfile` (Next standalone, non-root) |
| Abuse & cost | Daily cap on agent messages per person (`AGENT_TURNS_PER_DAY`, lower for demo accounts), demo accounts per IP per day, chat history trimmed before it reaches the model, 4 web searches per reply. Per-IP limits trust only the `X-Forwarded-For` entries our own proxies wrote (`TRUSTED_PROXY_HOPS`) |
| B2B endpoints | Registering a tenant or integrator needs `ALGEBRA_OPERATOR_TOKEN` (closed in production without it); revoking one needs its own token or the operator's |
| Data rights (DPDP Act) | Account → Your data: download everything as JSON, or delete the account (personal data erased, agents revoked, orders kept anonymously); a paid plan must be cancelled first |
| Legal pages | `/terms`, `/privacy`, `/refunds`, `/contact` — what Razorpay's website review and Google's OAuth consent screen check for. Business details come from build args (below); nothing is invented when they're unset |
| Error handling | Branded 404, route and console error boundaries, root `global-error`; `robots.txt` keeps `/console` and `/api` out of search, `sitemap.xml` lists public pages |
| CI | Go fmt/vet/build/race tests, migrations on a fresh Postgres, web lint + typecheck + production build |
| Plugins | Console → Plugins: each person switches sources on/off — live web prices (always on), bank offers, Amazon deals, Flipkart offers, and opt-in community deals (two subreddits of their choice, DesiDime). Enforced server-side; plugins only ever see the product search and can't check out. Community tips are labelled unverified and never enter a price. Optional `TAVILY_API_KEY` reads community posts from the last week |

## 2. Still demo — needs a partner or account

| Gap | Why it's not in code | What unblocks it |
|---|---|---|
| Real checkout at a store | Only Swiggy Instamart publishes an ordering API (built, cash on delivery). Amazon/Flipkart offer catalog search only; Blinkit offers nothing; Zepto hasn't published its tool schemas | Swiggy builder access (builders@swiggy.in) + link an account with `go run ./cmd/merchant-login -merchant swiggy_instamart -alias shipping:home` |
| Card / UPI payment | No card vault or PSP account exists (`SpreedlyProvider` is a stub that fails closed) | A tokenization vendor (Spreedly or your PSP's hosted fields) + a payment processor account |
| Amazon / Flipkart catalog | Needs their affiliate credentials | `AMAZON_CREATORS_*`, `FLIPKART_AFFILIATE_*` |
| Per-user merchant logins | Linked merchant sessions are one operator account per merchant, in encrypted files | Move sessions to Postgres per user before offering "connect your Swiggy account" to users |
| Master key in KMS | `ALGEBRA_MASTER_KEY` is a static env var | Unwrap a KMS-protected key at start (see `docs/GCP_DEPLOYMENT.md`) |

Until real checkout exists, run production with the web search + handoff links (users see real options and buy on the store's site), and keep the test store only in staging.

### Google Cloud APIs you do NOT need

Checked against what Algebra actually does — enabling these would cost money and change nothing:

| API | What it's for | Why not |
|---|---|---|
| **AI Commerce Search / Retail API** (`retail.googleapis.com`) | A retailer searching **its own** product catalog, uploaded to Google | Algebra has no catalog. It would not return a single Blinkit or Zepto price. |
| **Discovery Engine / Vertex AI Search** (`discoveryengine.googleapis.com`) | Search over **your own** documents, data stores, or a site you own | Same reason. It doesn't crawl other retailers for you. |
| **Custom Search JSON API** | Classic web search | Superseded here by Gemini grounding, and closed to new customers. The code still supports it if you already have keys. |

What Algebra uses instead is **Gemini with Google Search grounding** (`connectors/websearch/gemini.go`) on the same `GEMINI_API_KEY` as the agent — a live Google search, returning real listings with prices and links. Grounded requests are billed per request by Google, so results are cached in Redis for `WEB_SEARCH_CACHE_TTL` (10 minutes by default): repeat searches for the same product cost nothing and return in milliseconds.

### Zomato

There is no integration to build today. `mcp.zomato.com` does not resolve, and Zomato's developer API has been partner-only since 2021 — there is no public ordering or catalog API. The application to their MCP program is the blocker; nothing in this codebase is waiting on code. Food delivery is covered by Swiggy Instamart (built) and, for browsing, by web search.

## 3. Environment

API (`.env` / secret manager):

```
APP_ENV=production
DATABASE_URL=postgres://...?sslmode=require          # Neon: use the pooled connection string
REDIS_ADDR=redis.internal:6379                       # required in production
ALGEBRA_MASTER_KEY=<32 random bytes, base64>         # openssl rand -base64 32 — never reuse the dev key
PUBLIC_WEB_URL=https://app.yourdomain.com            # must be https
CORS_ALLOWED_ORIGINS=https://app.yourdomain.com
ENABLED_MERCHANTS=swiggy_instamart,amazon,flipkart,blinkit   # no "mock"
RESEND_API_KEY=re_...                                # required in production
EMAIL_FROM=Algebra <no-reply@yourdomain.com>         # a sender on a Resend-verified domain
GEMINI_API_KEY=...                                   # web search
RAZORPAY_KEY_ID=rzp_live_... / RAZORPAY_KEY_SECRET=... # live keys after Razorpay KYC
RAZORPAY_WEBHOOK_SECRET=...                          # webhook: https://app.yourdomain.com/api/v1/billing/webhooks/razorpay, subscription.* events
GROWTH_PRICE_INR=8499                                # or RAZORPAY_PLAN_GROWTH=plan_...
GOOGLE_CLIENT_ID=... / GOOGLE_CLIENT_SECRET=...       # optional
GITHUB_CLIENT_ID=... / GITHUB_CLIENT_SECRET=...       # optional
TRUSTED_PROXY_HOPS=2                                 # Google Cloud HTTPS LB; 1 for nginx / AWS ALB / most LBs (default)
ALGEBRA_OPERATOR_TOKEN=<openssl rand -hex 32>        # only if you onboard B2B tenants/integrators
AGENT_TURNS_PER_DAY=200                              # optional; DEMO_AGENT_TURNS_PER_DAY=40, DEMO_ACCOUNTS_PER_IP_PER_DAY=5
```

Web (`web/.env.production` / runtime env):

```
ALGEBRA_API_URL=http://api.internal:8080   # also passed as a build arg — Next bakes the rewrite
GEMINI_API_KEY=...                         # and/or ANTHROPIC_API_KEY / OPENAI_API_KEY
GEMINI_MODELS=gemini-3.1-pro-preview,gemini-3.8-flash
```

Web **build args** — the legal pages, sitemap and share links are rendered at build time:

```
PUBLIC_WEB_URL=https://app.yourdomain.com
LEGAL_ENTITY_NAME=Your Company Private Limited
LEGAL_ADDRESS=Registered office, one line
LEGAL_JURISDICTION=Bengaluru
SUPPORT_EMAIL=support@yourdomain.com
GRIEVANCE_OFFICER_NAME=Full name
GRIEVANCE_OFFICER_EMAIL=grievance@yourdomain.com
```

`APP_ENV=production` refuses to start — listing every problem at once — if `PUBLIC_WEB_URL` isn't https, `ALGEBRA_DEV_AUTH` is on, the mock store is enabled (unless `ALLOW_MOCK_MERCHANT=true` for staging), `RESEND_API_KEY` or `REDIS_ADDR` is missing, `DATABASE_URL` disables TLS, or a CORS origin isn't https. Redis being unreachable at boot is fatal in production.

OAuth redirect URIs to register: `https://app.yourdomain.com/api/v1/auth/oauth/google/callback` and `.../github/callback`.

## 4. Build and run

```bash
docker build -t algebra-api .
docker build -t algebra-web --build-arg ALGEBRA_API_URL=http://api.internal:8080 \
  --build-arg PUBLIC_WEB_URL=https://app.yourdomain.com --build-arg LEGAL_ENTITY_NAME="..." \
  --build-arg SUPPORT_EMAIL=... --build-arg GRIEVANCE_OFFICER_NAME="..." --build-arg GRIEVANCE_OFFICER_EMAIL=... web
```

Topology: put the web container behind your HTTPS load balancer on the public domain; keep the API private (only the web container talks to it — the browser reaches `/api/v1` through the web app's rewrite). Probe the API's `/readyz` for readiness and `/healthz` for liveness. Migrations run automatically when the API starts; run one API instance through a deploy before scaling out.

## 5. Launch checklist

- [ ] New `ALGEBRA_MASTER_KEY` generated for production (never the dev one) and stored in a secret manager
- [ ] Neon production branch with its own credentials; point-in-time recovery on
- [ ] Redis provisioned (Upstash / Memorystore) and reachable
- [ ] Resend domain verified; send yourself a reset email
- [ ] Google / GitHub OAuth apps created with the production redirect URIs
- [ ] `mock` removed from `ENABLED_MERCHANTS`
- [ ] LLM key set on the web app with a spend limit in the provider console
- [ ] API not publicly exposed; web on HTTPS with the security headers intact (check with `curl -I`)
- [ ] Log sink + an alert on 5xx rate and `/readyz` failures
- [ ] Razorpay live keys + webhook configured; one real upgrade and cancel tested
- [ ] Legal build args set (entity, address, support email, Grievance Officer); Terms / Privacy / Refunds reviewed by a lawyer
- [ ] `TRUSTED_PROXY_HOPS` matches your topology — sign in, open Account, and check "Where you're signed in" shows your real IP, not a proxy's
- [ ] Sign up, onboard, run one order end to end on production
- [ ] Download your data and delete a test account from Account → Your data
