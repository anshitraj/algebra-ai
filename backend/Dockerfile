# Algebra backend: the REST API and the MCP server it serves at /mcp. Build it from this folder:
#   docker build -t algebra-api .          (from the repository root: docker build -t algebra-api backend)
#   docker run --env-file .env.production -p 8080:8080 algebra-api
# Migrations ship in the image and run on startup (internal/platform/wiring).

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY connectors ./connectors
COPY providers ./providers
COPY policy ./policy
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# Distroless: no shell, no package manager, runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/api /app/api
COPY migrations /app/migrations
ENV APP_ENV=production \
    HTTP_ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
# Liveness: GET /healthz. Readiness: GET /readyz (checks Postgres + Redis).
ENTRYPOINT ["/app/api"]
