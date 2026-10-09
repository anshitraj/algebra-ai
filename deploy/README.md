# Running and deploying each part

Algebra is three things that run on their own: **Postgres and Redis**, the **backend** (the API and the MCP server it serves) and the **frontend** (the console). A few more programs are run from source: the demo providers, the MCP server over stdio and the wallet tools. Each has its own folder, its own environment example and, where it ships as a container, its own Dockerfile.

| Part | Folder | Port | Ships as | Needs |
|---|---|---|---|---|
| Postgres 16 | [`docker-compose.yml`](docker-compose.yml) or a managed database | 5432 | image | |
| Redis 7 | [`docker-compose.yml`](docker-compose.yml) or a managed one | 6379 | image | |
| Backend API + `/mcp` | [`../backend`](../backend/README.md) | 8080 | `backend/Dockerfile` | Postgres, Redis, `ALGEBRA_MASTER_KEY` |
| Frontend | [`../frontend`](../frontend/README.md) | 3000 | `frontend/Dockerfile` | the backend's address |
| Demo providers (devnet) | `../backend/cmd/demo-provider` | 8402 | from source | a devnet wallet |
| MCP over stdio | `../backend/cmd/mcp` | stdio | a binary: `go build ./cmd/mcp` | the backend's database |

The browser only ever reaches the frontend. The frontend proxies `/api/v1/*` and `/mcp` to the backend, so the backend can stay on a private network.

## On one machine, from source

```bash
make infra-up       # Postgres :5432 and Redis :6379 in Docker (Windows without Docker: scripts\dev-native.ps1 up)
make api            # the backend on :8080
make web            # the frontend on :3000
make demo-provider  # optional: the devnet demo providers on :8402
```

Each target stands alone: stop one and the others keep running.

## On one machine, in containers

```bash
docker compose -f deploy/docker-compose.yml up -d                  # Postgres and Redis only
docker compose -f deploy/docker-compose.yml --profile api up -d    # + the backend
docker compose -f deploy/docker-compose.yml --profile web up -d    # + the frontend
docker compose -f deploy/docker-compose.yml --profile app up -d    # all of it
```

The `api` and `web` services here are for trying the stack together: plain http, `APP_ENV=development`, the sandbox providers on. They need `ALGEBRA_MASTER_KEY` in the environment (`openssl rand -base64 32`).

## For real: each part on its own host

1. **Postgres and Redis**: managed ones. The API refuses to start in production against a database without TLS or without Redis.
2. **Backend**: build the image from `backend/` and run it with the variables in [`../docs/PRODUCTION.md`](../docs/PRODUCTION.md) §3 (`backend/.env.example` lists them all). It applies migrations on start, so run one instance through a deploy before scaling out. Probe `/readyz` (Postgres and Redis) for readiness and `/healthz` for liveness. Requests can take up to 80 seconds (a paid call and its bookkeeping), so the host's request timeout must allow it.
3. **Frontend**: build the image from `frontend/` with `ALGEBRA_API_URL` pointing at the backend, plus `PUBLIC_WEB_URL` and the `LEGAL_*` details the legal pages print (all are baked in at build time). At runtime it needs `ALGEBRA_API_URL` again and one LLM key for the agent chat.
4. Put the frontend on your public https domain and keep the backend private.

```bash
docker build -t algebra-api backend
docker build -t algebra-web --build-arg ALGEBRA_API_URL=http://api.internal:8080 \
  --build-arg PUBLIC_WEB_URL=https://app.yourdomain.com frontend
```

Hosts that deploy from a folder (Render, Railway, Fly.io, Cloud Run, Vercel for the frontend) take `backend` and `frontend` as the root or build context of two separate services. The launch checklist, every variable, and what is still a demo are in [`../docs/PRODUCTION.md`](../docs/PRODUCTION.md); the Google Cloud mapping is in [`../docs/GCP_DEPLOYMENT.md`](../docs/GCP_DEPLOYMENT.md).

## Not containerised yet

- **The demo providers** write a wallet key and advertise their own address, so a hosted copy needs a public base URL option and a persistent volume. For now they run from source next to the backend.
- **The MCP server over stdio** is a binary for a person's own machine; the backend already serves the same tools at `/mcp` for everything else.
- **The compose `api` and `web` services** and **the `docker` CI job** build the images but have not been run by their author: no Docker engine was available to test them.
