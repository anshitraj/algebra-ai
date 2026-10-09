# Algebra frontend

The web app: the landing page, sign-in, and the console where a person issues Spend Passes, watches what their agents bought, sees how the router chose, and uses the kill switch. It also hosts the console's agent chat, which runs an LLM (Anthropic, OpenAI or Google) server-side and calls Algebra's API as the signed-in person's console agent.

It is a [Next.js](https://nextjs.org) app (App Router, Tailwind). It is separate from the [backend](../backend/README.md): the browser only ever talks to this app's own origin, and `next.config.ts` rewrites `/api/v1/*` and `/mcp` to the API, so the API's session cookie is first-party. The two are built, run and deployed independently. The project overview is in the [root README](../README.md).

## Run it

```bash
cp .env.local.example .env.local    # add one LLM key (GEMINI_API_KEY, ANTHROPIC_API_KEY or OPENAI_API_KEY) for the agent chat
pnpm install
pnpm dev                            # http://localhost:3000, with the backend on :8080
```

From the repository root: `make web`. The API address is `ALGEBRA_API_URL` (default `http://localhost:8080`). A second copy of the dev server from the same checkout needs its own build folder and port: `NEXT_DIST_DIR=.next-alt pnpm exec next dev --port 3210`.

| Script | |
|---|---|
| `pnpm dev` | development server |
| `pnpm build` / `pnpm start` | production build and server |
| `pnpm lint` | ESLint |
| `pnpm exec tsc --noEmit` | typecheck |

## Configuration

Server-only variables, set in `.env.local` (see [`.env.local.example`](.env.local.example)) or the host's environment; nothing here reaches the browser bundle.

| Variable | What |
|---|---|
| `ALGEBRA_API_URL` | Where the backend is. Needed at **build** time too: Next bakes the rewrite in. |
| `GEMINI_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` | At least one, for the agent chat. A provider with no key shows as unavailable in the model picker. |
| `*_MODELS` | Optional model lists for the picker (defaults in `lib/agent/models.ts`). |

The legal pages, sitemap and share links are rendered at build time from `PUBLIC_WEB_URL`, `LEGAL_ENTITY_NAME`, `LEGAL_ADDRESS`, `LEGAL_JURISDICTION`, `SUPPORT_EMAIL`, `GRIEVANCE_OFFICER_NAME` and `GRIEVANCE_OFFICER_EMAIL`; nothing is invented when they are unset ([docs/PRODUCTION.md](../docs/PRODUCTION.md)).

## Layout

```
app/                routes: the landing page, (auth)/, onboarding/, console/ (agent, routing, firewall, passes,
                    executions, providers, connect, settings), (legal)/, verify/, api/agent/ (the chat's server side)
components/         UI, including components/auth/ (Privy sign-in) and components/console/
lib/                the API client and types, the agent (lib/agent/: models, tools, providers), formatting
DESIGN.md           the design system: tokens, colors, type
```

## Docker

```bash
docker build -t algebra-web --build-arg ALGEBRA_API_URL=https://api.internal:8080 .
docker run --env-file .env.production -p 3000:3000 algebra-web
```

From the repository root: `docker build -t algebra-web --build-arg ALGEBRA_API_URL=... frontend`. The image is Next's standalone output on Node 22, running as a non-root user. LLM keys are runtime-only: never pass them as build arguments.
