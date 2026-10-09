# GCP Deployment (target — not deployed by this build)

> Launch checklist, required env and what is still demo: [PRODUCTION.md](PRODUCTION.md).

No GCP project, service account, or credentials are configured in this environment. Nothing in this repository deploys anything. This document is the target mapping so a real deployment can follow it directly; treat every command below as illustrative, not something that has been run.

## Target service mapping

| Component | GCP service | Notes |
|---|---|---|
| `backend/cmd/api` (REST) | Cloud Run | Stateless container; scale-to-zero friendly. |
| `backend/cmd/mcp` (`-http`) | Cloud Run | Same image, different entrypoint/flag. Streamable HTTP transport is stateless per mandate §5, which is exactly what Cloud Run needs. |
| Postgres | Cloud SQL for PostgreSQL | Authoritative commerce state — see `backend/migrations/`. |
| Redis | Memorystore | Cache / locks / idempotency fast-path only, never authoritative. |
| Async events | Pub/Sub | Not built in this session (Phase 1 has no async event bus yet — see `backend/internal/app`'s synchronous service calls). Wire up when a workload actually benefits from decoupling, per mandate §36: "Do not introduce Kafka merely because this is a commerce project." |
| Secrets (`ALGEBRA_MASTER_KEY`, `RESEND_API_KEY`, OAuth client secrets, LLM keys, vault credentials once real) | Secret Manager | |
| Envelope-encryption master key | Cloud KMS | Production should unwrap a KMS-protected DEK at process start instead of reading `ALGEBRA_MASTER_KEY` as a static env var (that's the local-dev-only path — see `backend/internal/platform/config`). |
| Container images | Artifact Registry | |
| Logs / traces / metrics | Cloud Logging / Cloud Trace / Cloud Monitoring | `backend/internal/platform/logging` already emits structured JSON with redaction; a Cloud Logging sink is a matter of where stdout goes, not a code change. |
| Browser workers (future, Phase 6) | GCE or GKE | Cloud Run's execution model doesn't fit a persistent, isolated browser session — use a compute option built for that instead of forcing it onto Cloud Run (mandate §37). |

## Illustrative deploy shape

```bash
# Build & push (illustrative — no registry configured here)
gcloud builds submit --tag REGION-docker.pkg.dev/PROJECT/algebra/api:latest backend    # backend/Dockerfile
gcloud builds submit --tag REGION-docker.pkg.dev/PROJECT/algebra/web:latest frontend   # frontend/Dockerfile (see docs/PRODUCTION.md for the ALGEBRA_API_URL build arg)

# Deploy (illustrative)
gcloud run deploy algebra-api \
  --image REGION-docker.pkg.dev/PROJECT/algebra/api:latest \
  --set-secrets DATABASE_URL=algebra-db-url:latest,ALGEBRA_MASTER_KEY=algebra-master-key:latest \
  --vpc-connector algebra-connector  # for Cloud SQL/Memorystore private IP access
```

## Prerequisites before any of this is real

- A GCP project with billing enabled.
- Cloud SQL instance + database + credentials.
- Memorystore instance (if the Redis-backed paths are exercised).
- A KMS keyring/key for the envelope-encryption DEK.
- Secret Manager entries for every credential in `.env.example`'s "not yet used" section, once those integrations are real.
- A domain + TLS setup for the production API/MCP endpoints.

None of these exist in this session; this file exists so setting them up later is a checklist, not a design exercise.
