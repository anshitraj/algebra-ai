# Algebra web app — production image (Next.js standalone output).
#   docker build -t algebra-web --build-arg ALGEBRA_API_URL=https://api.internal:8080 frontend
#   docker run --env-file frontend/.env.production -p 3000:3000 algebra-web
#
# ALGEBRA_API_URL is needed at BUILD time too: Next bakes the /api/v1
# rewrite destination into the build. So are PUBLIC_WEB_URL (sitemap,
# robots, share links) and the LEGAL_* / SUPPORT_EMAIL / GRIEVANCE_* details
# the terms, privacy and contact pages print — those pages are
# rendered at build time. LLM keys (GEMINI_API_KEY, ...) are runtime-only —
# never pass them as build args.

FROM node:22-alpine AS deps
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

FROM node:22-alpine AS build
WORKDIR /app
RUN corepack enable
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ARG ALGEBRA_API_URL=http://api:8080
ARG PUBLIC_WEB_URL=""
ARG LEGAL_ENTITY_NAME=""
ARG LEGAL_ADDRESS=""
ARG LEGAL_JURISDICTION=""
ARG SUPPORT_EMAIL=""
ARG GRIEVANCE_OFFICER_NAME=""
ARG GRIEVANCE_OFFICER_EMAIL=""
ENV ALGEBRA_API_URL=$ALGEBRA_API_URL \
    PUBLIC_WEB_URL=$PUBLIC_WEB_URL \
    LEGAL_ENTITY_NAME=$LEGAL_ENTITY_NAME \
    LEGAL_ADDRESS=$LEGAL_ADDRESS \
    LEGAL_JURISDICTION=$LEGAL_JURISDICTION \
    SUPPORT_EMAIL=$SUPPORT_EMAIL \
    GRIEVANCE_OFFICER_NAME=$GRIEVANCE_OFFICER_NAME \
    GRIEVANCE_OFFICER_EMAIL=$GRIEVANCE_OFFICER_EMAIL \
    NEXT_TELEMETRY_DISABLED=1
RUN mkdir -p public && pnpm build

FROM node:22-alpine AS run
WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0
RUN addgroup -S app && adduser -S app -G app
COPY --from=build --chown=app:app /app/.next/standalone ./
COPY --from=build --chown=app:app /app/.next/static ./.next/static
COPY --from=build --chown=app:app /app/public ./public
USER app
EXPOSE 3000
CMD ["node", "server.js"]
