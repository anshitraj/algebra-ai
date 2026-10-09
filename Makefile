# One entry point per part of Algebra. Every target runs from the repository
# root; backend/ (Go) and frontend/ (Next.js) also work on their own.
#
#   make infra-up       Postgres and Redis in Docker
#   make api            the backend API (+ /mcp), http://localhost:8080
#   make web            the frontend, http://localhost:3000
#   make demo-provider  the devnet demo providers, http://127.0.0.1:8402
#   make mcp            the MCP server over stdio, for a local agent
#
# Windows without Docker or make: scripts/dev-native.ps1 does the same for
# Postgres, Redis and the API, and .claude/launch.json for the rest.

.PHONY: help infra-up infra-down api web demo-provider mcp \
        build build-backend build-frontend test test-integration vet fmt lint lint-frontend \
        docker-api docker-web

# Local state (wallet keys, merchant sessions) lives in the repository's .data,
# whichever folder a program runs from.
export ALGEBRA_DATA_DIR := $(CURDIR)/.data

help:
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed -E 's/:.*## /\t/'

infra-up: ## Postgres :5432 and Redis :6379 in Docker
	docker compose -f deploy/docker-compose.yml up -d
	@echo "Copy backend/.env.example to backend/.env and set ALGEBRA_MASTER_KEY."

infra-down: ## stop Postgres and Redis
	docker compose -f deploy/docker-compose.yml down

api: ## run the backend API
	cd backend && go run ./cmd/api

web: ## run the frontend
	cd frontend && pnpm dev

demo-provider: ## run the devnet demo providers
	cd backend && go run ./cmd/demo-provider

mcp: ## run the MCP server over stdio
	cd backend && go run ./cmd/mcp

build: build-backend build-frontend ## build everything

build-backend: ## compile every Go program
	cd backend && go build ./...

build-frontend: ## production build of the frontend
	cd frontend && pnpm build

vet: ## go vet
	cd backend && go vet ./...

fmt: ## gofmt the backend
	cd backend && gofmt -l -w .

lint: vet lint-frontend ## vet, eslint and typecheck

lint-frontend: ## eslint and tsc
	cd frontend && pnpm lint && pnpm exec tsc --noEmit

test: ## backend unit tests (anything needing Postgres skips itself)
	cd backend && go test ./...

# Needs a live DATABASE_URL and ALGEBRA_MASTER_KEY in the environment (infra-up
# first, or scripts/dev-native.ps1 test). See docs/LOCAL_DEVELOPMENT.md.
test-integration: ## Postgres-backed and end-to-end tests
	cd backend && go test ./internal/platform/postgres/... ./test/e2e/... -v

docker-api: ## build the API image
	docker build -t algebra-api backend

docker-web: ## build the frontend image (ALGEBRA_API_URL is baked in)
	docker build -t algebra-web --build-arg ALGEBRA_API_URL=http://localhost:8080 frontend
