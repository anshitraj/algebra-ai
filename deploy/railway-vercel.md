# Test deployment: backend on Railway, frontend on Vercel (devnet)

The quickest way to put Algebra on the internet for testing. Railway runs the API with Postgres and Redis; Vercel runs the web app and proxies `/api/v1` and `/mcp` to the API. Devnet only; mainnet stays off. For a long-lived deployment use the VM guide ([vm/README.md](vm/README.md)) or Cloud Run ([../docs/GCP_DEPLOYMENT.md](../docs/GCP_DEPLOYMENT.md)).

**Not run by its author.** No Railway or Vercel account was available. Two things are unverified and decided by your plans, not by this code:
- A paid call can take up to 80 seconds and the agent chat streams for up to 120 (`maxDuration` in `frontend/app/api/agent/chat/route.ts`). Vercel's plan limits on function and proxy time may cut those short; try a paid call first, and use the Pro plan if it does.
- Railway and Vercel both sit in front of the API, and both rewrite `X-Forwarded-For`, so the API sees Vercel's address for every visitor and gives the whole site one per-IP budget (ten sign-ins a minute, five demo accounts a day). Set the same secret as `EDGE_SHARED_SECRET` on Railway and `ALGEBRA_EDGE_SECRET` on Vercel (`openssl rand -hex 32`): the web app's proxy then sends the visitor's address and the API trusts it only with that secret. `TRUSTED_PROXY_HOPS=2` stays as the fallback.

## Order matters

Vercel needs the API's URL when it builds, and the API needs the web app's URL for CORS and cookies. So: Railway first with a placeholder, then Vercel, then fix the placeholder.

## 1. Railway (backend)

1. railway.com, New Project, **Deploy from GitHub repo**, pick `anshitraj/algebra-ai` (branch `main`, or `fix/account-wallet` until PRs #4 and #5 are merged).
2. Open the service, Settings:
   - **Root Directory**: `backend`
   - Variables: `RAILWAY_DOCKERFILE_PATH` = `Dockerfile.railway`
3. In the same project, **New**, Database, **PostgreSQL**; then **New**, Database, **Redis**.
4. Service, **Volumes**: add one, mount path `/data` (the demo providers' wallet lives there; without it they get a new wallet on every deploy).
5. Service, **Variables** (Raw Editor). Replace the `<...>` values:

```
APP_ENV=production
PORT=8080
DATABASE_URL=${{Postgres.DATABASE_URL}}
REDIS_ADDR=${{Redis.REDIS_URL}}
ALGEBRA_MASTER_KEY=<output of: openssl rand -base64 32>
PUBLIC_WEB_URL=https://placeholder.example.com
CORS_ALLOWED_ORIGINS=https://placeholder.example.com
ENABLED_MERCHANTS=none
TRUSTED_PROXY_HOPS=2
ALGEBRA_DATA_DIR=/data
RESEND_API_KEY=<your Resend key>
EMAIL_FROM=Algebra <no-reply@example.com>
PRIVY_APP_ID=<your Privy app id>
SOLANA_DEVNET_KEYPAIR=<the JSON array from your devnet payer key file>
ECONOMIC_SANDBOX=on
```

   `ALGEBRA_MASTER_KEY`: make a new one, never your laptop's, and keep a copy; losing it makes stored data unreadable. `SOLANA_DEVNET_KEYPAIR` is a secret: paste it only into Railway's variable editor, never into GitHub or chat. `RESEND_API_KEY` must be set for production mode to start; without a verified sending domain, email sign-up and reset can't send, but Privy sign-in works.
   If the API can't connect to Postgres, append `?sslmode=require` to `DATABASE_URL`.

   **Devnet faucet (optional).** Quickstart can drop 0.2 SOL and 2 USDC on a signed-in person's own wallet, so nobody copies an address to a public faucet. Make a devnet-only wallet (`go run ./cmd/solana-wallet -new -out faucet.json`), fund it (SOL at faucet.solana.com, USDC at faucet.circle.com) and set `DEVNET_FAUCET_KEYPAIR` to its key in Railway's variable editor, never in GitHub or chat. One drop per account and wallet a day, `DEVNET_FAUCET_PER_DAY` (200) for everyone; `DEVNET_FAUCET_SOL` and `DEVNET_FAUCET_USDC` set the amounts. The server turns it off on any cluster that isn't devnet. Unset: Quickstart points to the public faucets instead.
6. Service, Settings, Networking: **Generate Domain**. Copy the `https://....up.railway.app` address.
7. Wait for the deploy. Open `https://<that address>/readyz`: it should answer ok. Logs show the migrations running and the demo providers starting; if the demo providers can't fund themselves from the devnet faucet, send their wallet a little devnet SOL and USDC (the address is in the logs).

## 2. Vercel (frontend)

1. vercel.com, Add New, Project, import the same repo.
2. **Root Directory**: `frontend`. Framework: Next.js (detected). Install: `pnpm install --frozen-lockfile`.
3. Environment variables (all environments). They are read at build time, so changing one needs a redeploy:

```
ALGEBRA_API_URL=https://<the Railway address from step 6>
PUBLIC_WEB_URL=https://<your project>.vercel.app
GEMINI_API_KEY=<your key>
SUPPORT_EMAIL=you@example.com
LEGAL_ENTITY_NAME=<your name or company>
LEGAL_ADDRESS=<city, country>
LEGAL_JURISDICTION=<your city>
GRIEVANCE_OFFICER_NAME=<your name>
GRIEVANCE_OFFICER_EMAIL=you@example.com
```

   `PUBLIC_WEB_URL` must be the exact address Vercel gives the project (Settings, Domains), or your own domain. Set a model spend limit in the model provider's console.
4. Deploy.

## 3. Close the loop

1. Railway: change `PUBLIC_WEB_URL` and `CORS_ALLOWED_ORIGINS` to the real Vercel address. The service redeploys.
2. Privy dashboard, your app: add the Vercel address under **Allowed origins**; enable your login methods and Solana wallets; turn on "Return user data in an identity token".
3. Vercel: if `PUBLIC_WEB_URL` was wrong the first time, fix it and redeploy.

### Your own domain

Adding a domain in Vercel changes the address the browser sends, and the API only accepts changes (anything but a read) from addresses it was told about. Until it is told, signing in still works and then every button fails with "cross-origin request rejected". When you add a domain:

1. See which form Vercel serves. It usually redirects one to the other (`example.com` to `www.example.com`): `curl -sI https://example.com | grep -i location`. The one it redirects **to** is the site's address.
2. Railway: `PUBLIC_WEB_URL=https://www.example.com` (that address) and `CORS_ALLOWED_ORIGINS=https://www.example.com,https://example.com` (both forms, comma-separated, no trailing slash). The service redeploys.
3. Vercel: `PUBLIC_WEB_URL` to the same address, then redeploy (it is read at build time).
4. Privy dashboard: both forms under **Allowed origins**.
5. Resend: verify the domain, then `EMAIL_FROM=Algebra <no-reply@example.com>` on Railway.

Check it without a browser: `curl -s -X POST https://www.example.com/api/v1/auth/logout -H "Origin: https://www.example.com" -H "Cookie: algebra_session=x"` answers `{"ok":true}` once the address is accepted, and "cross-origin request rejected" until then.

## 4. Check it

1. Open the Vercel address and sign in with Privy. Hover your wallet at the top right: address and QR.
2. Fund it from faucet.circle.com (Solana Devnet USDC) and faucet.solana.com (devnet SOL).
3. Spend passes: create a pass, then "Create with my account wallet" to make its vault.
4. Agent chat: "Find a devnet API for token prices and call it". Approve the card, then open the invoice and the Solscan link.

## Notes

- The API is reachable at its own Railway address too. Everything sensitive needs a session or a key, but if you want it private, move to the VM guide.
- Redis can be given as `host:port` or as a `redis://` / `rediss://` URL with a password (Railway, Upstash, Memorystore); the URL's password is never logged.
- To run the API without the demo providers (real catalogs only): `DEMO_PROVIDER=off`.
