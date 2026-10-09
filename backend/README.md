# Algebra backend

The server side of Algebra: the REST API, the MCP server it serves at `/mcp`, the router and the spend firewall, the Solana rails, the demo providers and the wallet tools. It is one Go module (`github.com/project-algebra/algebra`) that builds several programs, and it needs one Postgres (and Redis for rate limits and locks).

It is separate from the [frontend](../frontend/README.md): the console talks to this API over HTTP and nothing else, so the two are built, run and deployed independently. The project overview is in the [root README](../README.md); the design is in [docs/](../docs/).

## Programs (`cmd/`)

| Program | What it is | Run it |
|---|---|---|
| `api` | The REST API, the MCP server at `/mcp` and the sandbox provider. Applies `migrations/` on start. This is the Docker image. | `go run ./cmd/api` |
| `mcp` | The same MCP tools over stdio (or `-http=:8081`), for a local agent. | `go run ./cmd/mcp` |
| `demo-provider` | Paid x402 APIs on Solana devnet that behave like real ones (honest, slow, down, overcharging, a honeypot, metered), for demos. Its own facilitator. | `go run ./cmd/demo-provider` |
| `solana-wallet` | Creates a wallet file, or shows the configured wallet's address and balances. | `go run ./cmd/solana-wallet` |
| `x402-dryrun` | Prices a real x402 resource and has a Solana node simulate the payment; sends nothing. | `go run ./cmd/x402-dryrun -url URL` |
| `verify-intent` | Verifies an Intent Receipt without trusting the API. | `go run ./cmd/verify-intent` |
| `paychan-smoke` | An x402 `upto` payment through the real payment-channels program on devnet. | `go run ./cmd/paychan-smoke` |
| `monid-import` | Rebuilds the snapshot of Monid's tools in `providers/monid/`. | `go run ./cmd/monid-import` |
| `merchant-login` | Links a Zepto or Swiggy account (the original shopping product). | `go run ./cmd/merchant-login` |

## Layout

```
cmd/                 the programs above
internal/domain      entities and rules, zero I/O (econ, routing, spendpass, chain, receipt, account)
internal/app         application services: the one place business rules live
internal/platform    postgres, redis, solana, safehttp, config, logging, wiring
internal/api/v1      REST transport (thin)         internal/mcpserver   MCP transport (thin)
providers/           the rails and the catalogs (x402client, solanax402, paychan, jupiter, paysh, bazaar, ...)
policy/              the deterministic rule engine, a public package
connectors/          the original shopping product's merchants, kept
migrations/          versioned SQL; Postgres is authoritative
openapi/             the REST API's OpenAPI documents
test/e2e             end-to-end tests over real HTTP and MCP
examples/            a worked integration example
```

## Run it

Everything here runs from this folder (or from the repository root with `make api`, `make demo-provider`, `make mcp`, `make test`).

```bash
cp .env.example .env            # then set ALGEBRA_MASTER_KEY: openssl rand -base64 32
go run ./cmd/api                # REST + MCP on :8080
```

The API needs Postgres and Redis; `make infra-up` starts both in Docker, and `scripts/dev-native.ps1 up` does it on Windows without Docker. Migrations run when the API starts.

Configuration is environment variables, documented in [`.env.example`](.env.example). A `.env` in the working directory is read at start and never overrides a variable that is already set. `APP_ENV=production` refuses to start on unsafe settings and lists every problem at once ([docs/PRODUCTION.md](../docs/PRODUCTION.md)).

Local state that isn't in the database (wallet key files, merchant sessions) goes in the directory named by `ALGEBRA_DATA_DIR`, `.data/` in the working directory when unset. In a deployment, point it at a persistent volume.

## Test

```bash
go test ./...                    # unit tests; anything needing Postgres skips itself
go vet ./... && gofmt -l .       # what CI checks
```

With a database, the Postgres repositories and the end-to-end flows run too (`-p 1` keeps the packages that reset tables from overlapping):

```bash
DATABASE_URL="postgres://algebra@127.0.0.1:5433/algebra_test?sslmode=disable" REDIS_ADDR=127.0.0.1:6380 \
ALGEBRA_MASTER_KEY="$(cat ../.data/local-master.key)" go test ./... -count=1 -p 1
```

The end-to-end tests are hermetic: they turn every provider catalog off and use the sandbox, a fake Solana node and a fake Jupiter, so nothing leaves the machine.

## Docker

```bash
docker build -t algebra-api .
docker run --env-file .env.production -p 8080:8080 algebra-api
```

The image is distroless, runs as a non-root user and carries `migrations/`. Probe `/readyz` for readiness (Postgres and Redis) and `/healthz` for liveness. The demo provider and the wallet tools are not in the image: run them from source or build them with `go build ./cmd/<name>`.
