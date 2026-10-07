# Production

What it takes to run Algebra for real users, what the code already enforces, and what still needs an account or a decision only you can make.

## 1. What the code already enforces

| Area | State |
|---|---|
| Accounts | Email and password (argon2id), Google / GitHub OAuth (PKCE, verified-email linking only), password reset through Resend, HttpOnly / SameSite=Lax / Secure session cookies, per-device sign-out |
| Authorization | Human-only endpoints (approvals, passes and their controls, the kill switch, `/me`) accept only a session; agents get scoped, revocable tokens; every intent-scoped call checks the intent belongs to the caller |
| Spend firewall | Per-pass budget, per-call cap, approval line, provider allow-list, velocity limits, a rule for new providers, a kill switch, all re-checked right before a wallet signs ([SPEND_PASSES.md](SPEND_PASSES.md)) |
| Money safety | At most one live attempt and one commitment per outcome, enforced by Postgres; an ambiguous outcome freezes and is settled from chain evidence ([ECONOMIC_COORDINATION.md](ECONOMIC_COORDINATION.md)); each rail has hard per-payment ceilings of its own |
| Outbound HTTP | Providers, catalogs and Jupiter are reached through one client that only connects to public addresses (checked on the dialled IP), never follows a redirect with a payment, and bounds every body |
| Data | Kept answers sealed at rest and deleted after 24 hours by default; request inputs scrubbed and kept answers deleted on account erasure; data export includes passes and requests |
| Hardening | `APP_ENV=production` refuses to boot with unsafe settings (below); security headers and a CSP on the web app; per-IP limits on sign-in, sign-up and reset; request IDs and structured logs that never contain query strings, bodies, cookies or tokens; `/healthz` liveness and `/readyz` (Postgres and Redis); server timeouts; an hourly purge of dead sessions, reset tokens and expired answers |
| Abuse and cost | A daily cap on agent messages per person (`AGENT_TURNS_PER_DAY`), web searches for providers limited to ten an hour per agent (each is a model call), per-IP limits trusting only the `X-Forwarded-For` entries our own proxies wrote (`TRUSTED_PROXY_HOPS`) |
| Receipts | Ed25519 receipts with a public key set at `/.well-known/jwks.json`; the key is derived from the master key, so there is nothing new to store |
| Packaging | `Dockerfile` (API, distroless, non-root, migrations included) and `web/Dockerfile` (Next standalone, non-root) |
| CI | Go fmt, vet, build and tests; migrations on a fresh Postgres; web lint, typecheck and production build |
| Legal | `/terms`, `/privacy` and `/contact`, with the business details from build args (below); nothing is invented when they are unset |

## 2. What is not done, and what unblocks it

| Gap | Why | What unblocks it |
|---|---|---|
| A real payment has never been made | It needs a funded wallet, which is the operator's to provide | Fund a wallet and follow the runbook in [EXECUTION.md](EXECUTION.md) (devnet first) |
| The wallet is a key in a file | `SOLANA_*_KEYPAIR_FILE` is the simplest thing that works | A KMS or HSM-backed signer behind the same interface; until then a dedicated, small hot wallet, with `SOLANA_MAX_PAYMENT_USDC` and `JUPITER_MAX_SWAP_USDC` kept low |
| Master key in an environment variable | Static `ALGEBRA_MASTER_KEY` | Unwrap a KMS-protected key at start (see [GCP_DEPLOYMENT.md](GCP_DEPLOYMENT.md)) |
| Hosted funding model | Passes are limits on a wallet the instance controls; there is no deposit, balance or top-up flow | A product decision: bring-your-own wallet per person, or a funded pool with accounting |
| Base-only providers | No EVM rail and no cross-chain transfer (CCTP) | An EVM signer and a recipient allow-list; see "Not built" in [EXECUTION.md](EXECUTION.md) |
| OAuth for MCP | One-click connectors in the ChatGPT and claude.ai apps need it; agents use a Spend Pass bearer token | An authorization server, or the host's OAuth |
| The original shopping product | Its merchants need partner access or accounts | Not part of this product; see [legacy/](legacy/README.md). Set `ENABLED_MERCHANTS=none` |

## 3. Environment

API (`.env` or a secret manager):

```
APP_ENV=production
DATABASE_URL=postgres://...?sslmode=require          # Neon: use the pooled connection string
REDIS_ADDR=redis.internal:6379                       # required in production (rate limits, locks)
ALGEBRA_MASTER_KEY=<32 random bytes, base64>         # openssl rand -base64 32; never reuse the dev key
PUBLIC_WEB_URL=https://app.yourdomain.com            # must be https
CORS_ALLOWED_ORIGINS=https://app.yourdomain.com
ENABLED_MERCHANTS=none                               # API-only; there is no shopping connector to run
RESEND_API_KEY=re_...                                # required in production
EMAIL_FROM=Algebra <no-reply@yourdomain.com>         # a sender on a Resend-verified domain
GOOGLE_CLIENT_ID=... / GOOGLE_CLIENT_SECRET=...      # optional
GITHUB_CLIENT_ID=... / GITHUB_CLIENT_SECRET=...      # optional
TRUSTED_PROXY_HOPS=2                                 # Google Cloud HTTPS LB; 1 for nginx / AWS ALB / most LBs (default)

# Solana. One wallet per cluster; a dedicated, small hot wallet.
SOLANA_MAINNET_KEYPAIR_FILE=/run/secrets/algebra-mainnet.json
SOLANA_MAINNET_RPC_URL=https://...                   # your own node: the public one is rate limited
SOLANA_ALLOW_MAINNET=yes
SOLANA_MAX_PAYMENT_USDC=0.50                         # a hard ceiling in the rail, whatever any pass says

# Optional
GEMINI_API_KEY=...                                   # web discovery of providers (and the console chat)
JUPITER_SWAP_ENABLED=on                              # swaps; JUPITER_API_KEY, JUPITER_MAX_SWAP_USDC, JUPITER_MAX_SLIPPAGE_BPS
RESULT_RETENTION=24h                                 # how long answers are kept for replay; off keeps none
CDP_BAZAAR_ENABLED=on                                # reads about 15 MB from Coinbase at most twice an hour; off if that is too much
ALGEBRA_OPERATOR_TOKEN=<openssl rand -hex 32>        # only if you onboard B2B tenants or integrators
```

Web (`web/.env.production` or runtime env):

```
ALGEBRA_API_URL=http://api.internal:8080   # also passed as a build arg: Next bakes the rewrites in
GEMINI_API_KEY=...                         # and/or ANTHROPIC_API_KEY / OPENAI_API_KEY, for the console's agent chat
```

Web **build args**: the legal pages, sitemap and share links are rendered at build time:

```
PUBLIC_WEB_URL=https://app.yourdomain.com
LEGAL_ENTITY_NAME=Your Company Private Limited
LEGAL_ADDRESS=Registered office, one line
LEGAL_JURISDICTION=Bengaluru
SUPPORT_EMAIL=support@yourdomain.com
GRIEVANCE_OFFICER_NAME=Full name
GRIEVANCE_OFFICER_EMAIL=grievance@yourdomain.com
```

`APP_ENV=production` refuses to start, listing every problem at once, if `PUBLIC_WEB_URL` isn't https, `ALGEBRA_DEV_AUTH` is on, the mock store is enabled (set `ENABLED_MERCHANTS=none`),
`RESEND_API_KEY` or `REDIS_ADDR` is missing, `DATABASE_URL` disables TLS, or a CORS origin isn't https. A Solana RPC node on the wrong cluster stops startup; one that is merely unreachable leaves
its rail out, loudly. Redis being unreachable at boot is fatal in production.

OAuth redirect URIs to register: `https://app.yourdomain.com/api/v1/auth/oauth/google/callback` and `.../github/callback`.

## 4. Build and run

```bash
docker build -t algebra-api .
docker build -t algebra-web --build-arg ALGEBRA_API_URL=http://api.internal:8080 \
  --build-arg PUBLIC_WEB_URL=https://app.yourdomain.com --build-arg LEGAL_ENTITY_NAME="..." \
  --build-arg SUPPORT_EMAIL=... --build-arg GRIEVANCE_OFFICER_NAME="..." --build-arg GRIEVANCE_OFFICER_EMAIL=... web
```

Topology: put the web container behind your HTTPS load balancer on the public domain; keep the API private (only the web container talks to it: the browser reaches `/api/v1` and `/mcp`
through the web app's rewrites). Probe the API's `/readyz` for readiness and `/healthz` for liveness. Migrations run when the API starts; run one API instance through a deploy before scaling out.
The API's write timeout is 90 seconds, which is what bounds a paid call plus its bookkeeping (a request is cut off at 80 seconds, and the outcome is still recorded).

## 5. Launch checklist

- [ ] A new `ALGEBRA_MASTER_KEY` generated for production (never the dev one) and kept in a secret manager
- [ ] A production database with its own credentials and point-in-time recovery; Redis provisioned and reachable
- [ ] Resend domain verified; send yourself a reset email
- [ ] Google / GitHub OAuth apps created with the production redirect URIs
- [ ] A dedicated mainnet wallet, funded with a small amount; its key file readable only by the API; `SOLANA_MAX_PAYMENT_USDC` low; your own RPC node
- [ ] A first real payment made and checked: `go run ./cmd/x402-dryrun` for the provider, a pass with a tiny budget, `cmd/verify-intent` on the receipt, the signature on the explorer
- [ ] The kill switch tried once, and a revoked pass shown to be refused
- [ ] An LLM key set on the web app with a spend limit in the provider console
- [ ] The API not publicly exposed; the web app on HTTPS with the security headers intact (`curl -I`)
- [ ] A log sink and an alert on the 5xx rate, `/readyz` failures and a rail that failed to start
- [ ] Legal build args set (entity, address, support email, Grievance Officer); Terms and Privacy reviewed by a lawyer
- [ ] `TRUSTED_PROXY_HOPS` matches your topology: sign in, open Account, and check "Where you're signed in" shows your real IP, not a proxy's
- [ ] Sign up, issue a pass, run one request end to end on production
- [ ] Download your data and delete a test account from Account → Your data
