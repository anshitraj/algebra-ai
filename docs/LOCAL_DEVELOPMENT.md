# Local development

## Prerequisites

- Go 1.26+
- Node 20+ and pnpm (for the web app in `web/`)
- PostgreSQL: `make dev-up` (Docker: Postgres and Redis), the native script below (Windows, no Docker), or a hosted database via `DATABASE_URL`
- `openssl` (or anything that can generate 32 random bytes and base64 them) for `ALGEBRA_MASTER_KEY`

## One-time setup

```bash
cp .env.example .env
openssl rand -base64 32                     # paste into .env as ALGEBRA_MASTER_KEY
cp web/.env.local.example web/.env.local    # add an LLM key for the console's agent chat (GEMINI_API_KEY, ANTHROPIC_API_KEY or OPENAI_API_KEY)
pnpm --dir web install
```

Migrations (`migrations/*.sql`) run automatically when the API starts (`internal/platform/postgres.Migrate`, called from `wiring.Build`). There is no separate step.

## Without Docker (Windows)

`scripts\dev-native.ps1 up` starts a throwaway PostgreSQL cluster (127.0.0.1:5433) and an isolated Redis-compatible server (:6380) from the PostgreSQL and Memurai/Redis installs already on the
machine, with their data under `.data/` (gitignored). It never touches an existing Postgres or Redis, and never the `DATABASE_URL` in `.env`: the script sets its own in the process, and the
process environment wins over `.env`.

```powershell
scripts\dev-native.ps1 up        # Postgres :5433 (databases algebra and algebra_test) and Redis :6380
scripts\dev-native.ps1 api       # the API on 127.0.0.1:8080 against them, with the wallets below
scripts\dev-native.ps1 test      # the Postgres integration tests and the end-to-end tests, on algebra_test
scripts\dev-native.ps1 status    # and down
```

Wallets: if `.data/solana-devnet.json` exists it becomes the devnet rail's wallet; `.data/solana-mainnet.json` becomes the mainnet wallet only when `SOLANA_ALLOW_MAINNET=yes` is already set.

## Run it

```bash
go run ./cmd/api                    # REST API and MCP on :8080 (the MCP server is at /mcp)
pnpm --dir web dev                  # console on :3000, proxying /api/v1 and /mcp to the API
```

Open http://localhost:3000. Create an account (or use **Try the demo**: one click, no sign-up, on the sandbox rail with simulated USDC; `DEMO_ACCOUNTS=off` hides it), then **Spend passes** to issue
a USDC pass. The browser only ever talks to the web app's own origin; `web/next.config.ts` rewrites `/api/v1/*` and `/mcp` to `ALGEBRA_API_URL` (default `http://localhost:8080`), so the API's HttpOnly
session cookie is first-party. To run a second copy of the web app from the same checkout (Next locks one dev server per build dir), set `NEXT_DIST_DIR=.next-alt` and a different port.

With `ECONOMIC_SANDBOX=on` (the default outside production) the API hosts a simulated paid provider and a simulated rail, so the whole flow runs with no wallet and no money. Sandbox receipts are marked `test`.

### Against real providers on devnet

```bash
go run ./cmd/solana-wallet -new -out .data/solana-devnet.json     # fund at faucet.circle.com (Solana Devnet)
go run ./cmd/demo-provider                                         # alpha, beta, flaky, greedy, trap, meter on :8402; a devnet facilitator of its own
ECONOMIC_PROVIDERS="$(go run ./cmd/demo-provider -print-config)" go run ./cmd/api
```

`.claude/launch.json` has the same as named configurations (`demo-provider`, `api-demo`, `web-demo`). Mainnet, the first real payment and `x402-dryrun` (prices a real provider and has a node simulate
the payment, sending nothing) are in the runbook in [EXECUTION.md](EXECUTION.md).

### The MCP server on its own

```bash
go run ./cmd/mcp                 # stdio, for a local agent or IDE (no sandbox provider in that process)
go run ./cmd/mcp -http=:8081     # standalone streamable HTTP
```

An agent normally connects to the API's own `/mcp` instead: [MCP.md](MCP.md).

## Tests

```bash
make test               # unit tests: no external dependencies, always run
make test-integration   # needs DATABASE_URL and ALGEBRA_MASTER_KEY: real Postgres repos, and the end-to-end flows over HTTP and MCP
```

Tests that need Postgres check `DATABASE_URL` and `ALGEBRA_MASTER_KEY` at the top and skip themselves when they aren't set, so `make test` alone never needs a database. Against the native database:

```bash
DATABASE_URL="postgres://algebra@127.0.0.1:5433/algebra_test?sslmode=disable" REDIS_ADDR=127.0.0.1:6380 \
ALGEBRA_MASTER_KEY="$(cat .data/local-master.key)" go test ./... -count=1 -p 1
```

`-p 1` keeps the packages that reset tables from running at once. The end-to-end tests (`test/e2e`) are hermetic: they turn every provider catalog off, blank the Gemini key, and use the sandbox, a fake Solana
node and a fake Jupiter (`providers/jupiter/jupitertest`), so nothing leaves the machine. Web: `pnpm --dir web exec tsc --noEmit` and `pnpm --dir web lint`.

## Accounts and sessions

People sign in to the web app with Privy (an email code or a Solana wallet) or with email and password (argon2id) (`internal/api/v1/auth.go`). A sign-in creates:

- an **HttpOnly, SameSite=Lax session cookie** (`algebra_session`, SHA-256-hashed at rest in `user_sessions`), and
- a **per-session console agent**: the identity every agent-scoped endpoint acts as when called with the cookie. Its token is sealed (AES-256-GCM) on the session row; the web app's server-side loop fetches
  it via `POST /api/v1/auth/agent-token`, so tool calls authenticate as the agent, never as the human.

Signing out revokes both. Human-only endpoints (approving, passes and their controls, the kill switch, everything under `/api/v1/me`) accept **only** the session cookie. An agent bearer token is rejected
there, which is what keeps approval out of any agent's reach, including the console's own chat.

The Privy button appears once `PRIVY_APP_ID` is set (see `.env.example`). Password-reset emails go through Resend when `RESEND_API_KEY` is set; otherwise the reset link is printed
to the API's log, which is fine locally and never in production. `PASSWORD_LOGIN=off` removes email and password sign-in.

## Scripts without a browser

Mint an agent token from a signed-in session (`POST /api/v1/me/passes` with the cookie), or, for local scripts, set `ALGEBRA_DEV_AUTH=true` to re-enable the old shortcuts. That lets any caller act as any
user, so never on a reachable host:

```bash
curl -X POST localhost:8080/api/v1/users -H 'Content-Type: application/json' -d '{"email":"dev@example.com"}'
curl -X POST localhost:8080/api/v1/me/passes -b cookies.txt -H 'Content-Type: application/json' \
  -d '{"label":"scratch","agent_kind":"custom","currency":"USDC","budget_minor_units":1000000,"budget_period":"total","expires_in_days":1}'   # the token is in the response, once
```

## Agent evals

`pnpm --dir web eval:agent [scenario-id ...]` runs simulated conversations against the console's real agent (system prompt, tools, provider loop) with Algebra's API replaced by fixtures, and a judge model grades each
against a checklist (`web/evals/agent/scenarios.ts`). Needs `GEMINI_API_KEY` in `web/.env.local`. Add a scenario whenever a real conversation goes wrong.
