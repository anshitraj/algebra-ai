# Production

What it takes to run Algebra for real users, what the code already enforces, and what still needs an account or a decision only you can make.

## 1. What the code already enforces

| Area | State |
|---|---|
| Accounts | A six-digit code emailed through Resend (ten minutes, five tries, one every 30 seconds and six an hour to an address; only a keyed hash is stored), email and password (argon2id) as a second way in, password reset through Resend, HttpOnly / SameSite=Lax / Secure session cookies, per-device sign-out |
| Authorization | Human-only endpoints (approvals, passes and their controls, the kill switch, `/me`) accept only a session; agents get scoped, revocable tokens; every intent-scoped call checks the intent belongs to the caller |
| Demo accounts | "Try the demo" is free and open to anyone, so a demo account pays in the sandbox only (simulated providers and USDC). It is enforced where an intent is created, and again before a payment is signed, so neither the console chat nor an agent holding a demo pass reaches a real wallet; the wallets behind the rails are not even listed to it. It needs `ECONOMIC_SANDBOX=on` |
| Spend firewall | Per-pass budget, per-call cap, approval line, provider allow-list, velocity limits, a rule for new providers, a kill switch, all re-checked right before a wallet signs ([SPEND_PASSES.md](SPEND_PASSES.md)) |
| Money safety | At most one live attempt and one commitment per outcome, enforced by Postgres; an ambiguous outcome freezes and is settled from chain evidence ([ECONOMIC_COORDINATION.md](ECONOMIC_COORDINATION.md)); each rail has hard per-payment ceilings of its own; the shared devnet wallet pays test money only inside a daily count for each account and for the server (the test-money guard, `TEST_MONEY_*`) |
| Outbound HTTP | Providers, catalogs and Jupiter are reached through one client that only connects to public addresses (checked on the dialled IP), never follows a redirect with a payment, and bounds every body |
| Data | Kept answers sealed at rest and deleted after 24 hours by default; request inputs scrubbed and kept answers deleted on account erasure; data export includes passes and requests |
| Hardening | Request bodies capped at 1 MiB (413 beyond it); `APP_ENV=production` refuses to boot with unsafe settings (below); security headers and a CSP on the web app; per-IP limits on sign-in, sign-up and reset; request IDs and structured logs that never contain query strings, bodies, cookies or tokens; `/healthz` liveness and `/readyz` (Postgres and Redis); server timeouts; an hourly purge of dead sessions, reset tokens and expired answers |
| Abuse and cost | A daily cap on agent messages per person (`AGENT_TURNS_PER_DAY`), web searches for providers limited to ten an hour per agent (each is a model call), per-IP limits trusting only the `X-Forwarded-For` entries our own proxies wrote (`TRUSTED_PROXY_HOPS`) |
| Receipts | Ed25519 receipts with a public key set at `/.well-known/jwks.json`; the key is derived from the master key, so there is nothing new to store |
| Packaging | `Dockerfile` (API, distroless, non-root, migrations included) and `frontend/Dockerfile` (Next standalone, non-root) |
| CI | Go fmt, vet, build and tests; migrations on a fresh Postgres; web lint, typecheck and production build; the Spend Pass program's unit tests; both Docker images |
| Legal | `/terms`, `/privacy` and `/contact`, with the business details from build args (below); nothing is invented when they are unset |

## 2. What is not done, and what unblocks it

| Gap | Why | What unblocks it |
|---|---|---|
| A real payment has never been made | It needs a funded wallet, which is the operator's to provide | Fund a wallet and follow the runbook in [EXECUTION.md](EXECUTION.md) (devnet first) |
| The Spend Pass program is on devnet only, and off | It has run on devnet, not on mainnet, has not been audited, and is off unless `ONCHAIN_PASSES=on`: people are no longer asked to make a vault | Real money needs a vault of some kind. A combined Solana and Base vault kept in Algebra's ledger is the plan; the Anchor program is the other way: deploy it (about 2.7 SOL in the deploying wallet, 1.331 SOL of it kept as rent), move its upgrade authority to a multisig, have it reviewed, then set `ONCHAIN_PASSES=on SPEND_PASS_REQUIRED=mainnet`; see [SPEND_PASS_PROGRAM.md](SPEND_PASS_PROGRAM.md#mainnet) |
| The wallet is a key in a file | `SOLANA_*_KEYPAIR_FILE` is the simplest thing that works. On devnet Algebra can also generate its own key and keep it sealed in the database (`SOLANA_DEVNET_SIGNER=generated`, devnet only) | A KMS or HSM-backed signer behind the same interface (unmerged work exists; see [MAINNET_READINESS.md](MAINNET_READINESS.md#2-the-signer)); until then a dedicated, small hot wallet, with `SOLANA_MAX_PAYMENT_USDC` and `JUPITER_MAX_SWAP_USDC` kept low. How the key is kept and recovered: [SIGNER.md](SIGNER.md) |
| Master key in an environment variable | The process still holds `ALGEBRA_MASTER_KEY` in its environment. On the Google Cloud VM it is stored wrapped by Cloud KMS and unwrapped at service start into memory-only storage, so nothing secret is at rest in a dashboard, image or disk ([deploy/gcp/README.md](../deploy/gcp/README.md)) | Unwrapping inside the API process itself, so the key is never in an environment file even in memory |
| Hosted funding model | Passes are limits on a wallet the instance controls; there is no deposit, balance or top-up flow. On devnet that wallet pays test money, bounded by the test-money guard | A deposit, balance and withdrawal ledger for Solana and Base together (a design is the next step); real money does not move until it exists |
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
RESEND_API_KEY=re_...                                # required in production: password resets, sign-in alerts, receipts
EMAIL_FROM=Algebra <no-reply@yourdomain.com>         # a sender on a Resend-verified domain
EMAIL_CODE_LOGIN=on                                  # the emailed sign-in code (default); off closes it
TRUSTED_PROXY_HOPS=2                                 # Google Cloud HTTPS LB; 1 for nginx / AWS ALB / most LBs (default)
EDGE_SHARED_SECRET=...                               # only when a web host (Vercel) proxies to the API: the same value as ALGEBRA_EDGE_SECRET there; openssl rand -hex 32

# Solana. One wallet per cluster; a dedicated, small hot wallet.
SOLANA_MAINNET_KEYPAIR_FILE=/run/secrets/algebra-mainnet.json
SOLANA_MAINNET_RPC_URL=https://...                   # your own node: the public one is rate limited
SOLANA_ALLOW_MAINNET=yes
SOLANA_MAX_PAYMENT_USDC=0.50                         # a hard ceiling in the rail, whatever any pass says

# Demo accounts ("Try the demo") pay in the sandbox: simulated providers and USDC. Off in production unless set.
ECONOMIC_SANDBOX=on
# Devnet payments are test money from the generated payer wallet, inside these daily counts (defaults shown).
# TEST_MONEY_PER_ACCOUNT_PER_DAY=25
# TEST_MONEY_PER_DAY=400
# Once a vault exists on mainnet: every mainnet payment must come from it (the Anchor program: ONCHAIN_PASSES=on).
# ONCHAIN_PASSES=on
# SPEND_PASS_REQUIRED=mainnet

# Optional
GEMINI_API_KEY=...                                   # web discovery of providers (and the console chat)
JUPITER_SWAP_ENABLED=on                              # swaps; JUPITER_API_KEY, JUPITER_MAX_SWAP_USDC, JUPITER_MAX_SLIPPAGE_BPS
RESULT_RETENTION=24h                                 # how long answers are kept for replay; off keeps none
CDP_BAZAAR_ENABLED=on                                # reads about 15 MB from Coinbase at most twice an hour; off if that is too much
LEADS_NOTIFY_EMAIL=sales@example.com                 # emails each Book a Demo enquiry (through Resend)
```

Required for a server that takes customers (the API warns at startup without it):

```bash
ALGEBRA_OPERATOR_TOKEN=<openssl rand -hex 32>        # read leads, verify/block/unblock providers, refresh the registry, B2B registration
```

Web (`frontend/.env.production` or runtime env):

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

`SUPPORT_EMAIL` is also the contact in `https://<your site>/.well-known/security.txt` (RFC 9116, `frontend/lib/security-txt.ts`): where someone who finds a
vulnerability writes. Leave it unset and that URL answers 404 instead of naming nobody; use a mailbox that is read.

`APP_ENV=production` refuses to start, listing every problem at once, if `PUBLIC_WEB_URL` isn't https, `ALGEBRA_DEV_AUTH` is on, the mock store is enabled (set `ENABLED_MERCHANTS=none`),
`RESEND_API_KEY` or `REDIS_ADDR` is missing, `DATABASE_URL` disables TLS, or a CORS origin isn't https. A Solana RPC node on the wrong cluster stops startup; one that is merely unreachable leaves
its rail out, loudly. Redis being unreachable at boot is fatal in production.

## 4. Build and run

```bash
docker build -t algebra-api backend
docker build -t algebra-web --build-arg ALGEBRA_API_URL=http://api.internal:8080 \
  --build-arg PUBLIC_WEB_URL=https://app.yourdomain.com --build-arg LEGAL_ENTITY_NAME="..." \
  --build-arg SUPPORT_EMAIL=... --build-arg GRIEVANCE_OFFICER_NAME="..." --build-arg GRIEVANCE_OFFICER_EMAIL=... frontend
```

Each image builds from its own folder, so the backend and the frontend are built, deployed and scaled separately; [deploy/README.md](../deploy/README.md) lists what each one needs.
Algebra's own production runs the backend on a hardened Google Cloud VM behind a Cloudflare tunnel, with secrets in Secret Manager and the master key wrapped by Cloud KMS: [deploy/gcp/README.md](../deploy/gcp/README.md).

Topology: put the web container behind your HTTPS load balancer on the public domain; keep the API private (only the web container talks to it: the browser reaches `/api/v1` and `/mcp`
through the web app's rewrites). Probe the API's `/readyz` for readiness and `/healthz` for liveness. Migrations run when the API starts; run one API instance through a deploy before scaling out.
The API's write timeout is 90 seconds, which is what bounds a paid call plus its bookkeeping (a request is cut off at 80 seconds, and the outcome is still recorded).

## 5. Launch checklist

- [ ] A new `ALGEBRA_MASTER_KEY` generated for production (never the dev one) and kept in a secret manager
- [ ] A production database with its own credentials and point-in-time recovery; Redis provisioned and reachable
- [ ] Resend domain verified; sign in once with an emailed code, and send yourself a reset email
- [ ] The devnet payer funded with test SOL and USDC (its address is in the API's log at start, and `go run ./cmd/solana-wallet -generated` prints it), and the test-money counts set to what that balance can carry
- [ ] A dedicated mainnet wallet, funded with a small amount; its key file readable only by the API; `SOLANA_MAX_PAYMENT_USDC` low; your own RPC node
- [ ] A first real payment made and checked: `go run ./cmd/x402-dryrun` for the provider, a pass with a tiny budget, `backend/cmd/verify-intent` on the receipt, the signature on the explorer
- [ ] The kill switch tried once, and a revoked pass shown to be refused
- [ ] If "Try the demo" is on: `ECONOMIC_SANDBOX=on`, and a demo account tried once (its chat answers from the sandbox and the Providers page shows nothing payable)
- [ ] An LLM key set on the web app with a spend limit in the provider console
- [ ] The API not publicly exposed; the web app on HTTPS with the security headers intact (`curl -I`)
- [ ] A log sink and an alert on the 5xx rate, `/readyz` failures and a rail that failed to start
- [ ] Legal build args set (entity, address, support email, Grievance Officer); Terms and Privacy reviewed by a lawyer
- [ ] `TRUSTED_PROXY_HOPS` matches your topology (and `EDGE_SHARED_SECRET` is set with the same value as `ALGEBRA_EDGE_SECRET` on the web app when a web host proxies to the API): sign in, open Account, and check "Where you're signed in" shows your real IP, not a proxy's
- [ ] Sign up, issue a pass, run one request end to end on production
- [ ] Download your data and delete a test account from Account → Your data
