# Run Algebra on one Google Cloud VM (devnet)

One Ubuntu or Debian VM runs everything: Postgres, Redis, the API, the web app, the devnet demo providers, and Caddy for HTTPS. This is a **devnet** deployment. Mainnet stays off.

**Not run by its author.** These files and steps follow how the stack runs from source on a laptop; no VM was available to try them on. If a step fails, the error is in the unit's log: `sudo journalctl -u algebra-api -n 80 --no-pager`.

You need: the VM's external IP, a hostname that points to it (a domain, or `<ip-with-dashes>.sslip.io`, which needs no purchase), a Privy app, one Gemini (or Anthropic/OpenAI) key, and your devnet payer key file.

## 1. Network and hostname (Cloud console, once)

1. VM instance, then Edit: tick **Allow HTTP traffic** and **Allow HTTPS traffic** (opens 80 and 443). Open nothing else: 3000, 8080, 8402, 5432 and 6379 stay closed.
2. VPC network, External IP addresses: make the VM's IP **static** (Reserve).
3. Hostname: either a DNS **A record** for your domain to that IP, or use `34-123-45-67.sslip.io` for IP `34.123.45.67`.
4. Privy dashboard, your app, Allowed origins: add `https://YOUR_HOSTNAME`. Under Login methods, enable email, Google (if used) and Solana wallets. Turn on "Return user data in an identity token".

## 2. Install the tools on the VM

```bash
sudo apt-get update
sudo apt-get install -y postgresql redis-server caddy git curl build-essential openssl
# Go 1.26 (check go.mod for the exact version) and Node 22
curl -fsSL https://go.dev/dl/go1.26.3.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
sudo ln -sf /usr/local/go/bin/go /usr/local/bin/go
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs
sudo corepack enable
# The web build needs memory: add swap on a small VM
sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
```

If the Caddy package is missing on your image, follow Caddy's own apt instructions (caddyserver.com/docs/install).

## 3. Database

```bash
DBPASS=$(openssl rand -hex 24); echo "$DBPASS"      # keep this for api.env
sudo -u postgres psql -c "CREATE USER algebra WITH PASSWORD '$DBPASS';"
sudo -u postgres psql -c "CREATE DATABASE algebra OWNER algebra;"
sudo systemctl enable --now postgresql redis-server
```

Postgres on Debian and Ubuntu listens on localhost only and has TLS on by default, which `APP_ENV=production` requires. Redis also listens on localhost only.

## 4. Get the code and build

```bash
sudo useradd --system --home /opt/algebra --shell /usr/sbin/nologin algebra
sudo mkdir -p /opt/algebra /var/lib/algebra /etc/algebra
sudo chown "$USER" /opt/algebra
git clone --branch main https://github.com/anshitraj/algebra-ai.git /opt/algebra
# Until PRs #4 and #5 are merged, use the branch: git -C /opt/algebra checkout fix/account-wallet

cd /opt/algebra/backend
mkdir -p ../bin
go build -trimpath -o ../bin/api ./cmd/api
go build -trimpath -o ../bin/demo-provider ./cmd/demo-provider

cd /opt/algebra/frontend
export ALGEBRA_API_URL=http://127.0.0.1:8080 PUBLIC_WEB_URL=https://YOUR_HOSTNAME
export SUPPORT_EMAIL=you@example.com LEGAL_ENTITY_NAME="Your name or company" LEGAL_ADDRESS="City, Country" LEGAL_JURISDICTION="Your city" \
       GRIEVANCE_OFFICER_NAME="Your name" GRIEVANCE_OFFICER_EMAIL=you@example.com
pnpm install --frozen-lockfile
pnpm build
cp -r .next/static .next/standalone/.next/static
cp -r public .next/standalone/public 2>/dev/null || true
```

## 5. Secrets and settings

```bash
sudo cp /opt/algebra/deploy/vm/api.env.example /etc/algebra/api.env
sudo cp /opt/algebra/deploy/vm/web.env.example /etc/algebra/web.env
sudo chmod 600 /etc/algebra/*.env
sudo nano /etc/algebra/api.env     # fill every PASTE_/CHANGE_ value; replace app.example.com
sudo nano /etc/algebra/web.env     # your model key
```

- `ALGEBRA_MASTER_KEY`: `openssl rand -base64 32`. Make a **new** one; never reuse your laptop's. Losing it makes sealed data unreadable, so keep a copy in Secret Manager.
- `RESEND_API_KEY`: required to start in production. Without a verified sending domain, email sign-up and reset can't send; Privy sign-in works.
- The devnet payer key: from your laptop, `gcloud compute scp .data\solana-devnet.json VM_NAME:/tmp/solana-devnet.json`, then on the VM:
  `sudo mv /tmp/solana-devnet.json /var/lib/algebra/ && sudo chown algebra: /var/lib/algebra/solana-devnet.json && sudo chmod 600 /var/lib/algebra/solana-devnet.json`.
  It is a devnet wallet, but keep it off GitHub and out of chat. Or make a fresh one on the VM and fund it from the faucets.

## 6. Start it

```bash
sudo chown -R algebra: /var/lib/algebra
sudo cp /opt/algebra/deploy/vm/algebra-*.service /etc/systemd/system/
# 1) the demo providers first: they create their wallet and fund it from the faucet
sudo systemctl daemon-reload && sudo systemctl enable --now algebra-demo-provider
sleep 20 && sudo journalctl -u algebra-demo-provider -n 20 --no-pager
# 2) put its config into api.env
sudo -u algebra /opt/algebra/bin/demo-provider -addr 127.0.0.1:8402 -print-config
sudo nano /etc/algebra/api.env     # paste that line as ECONOMIC_PROVIDERS=...
# 3) the API (runs the migrations), then the web app
sudo systemctl enable --now algebra-api && sleep 8
curl -s http://127.0.0.1:8080/readyz && echo
sudo systemctl enable --now algebra-web
# 4) HTTPS
sudo cp /opt/algebra/deploy/vm/Caddyfile /etc/caddy/Caddyfile
sudo sed -i 's/app.example.com/YOUR_HOSTNAME/' /etc/caddy/Caddyfile
sudo systemctl restart caddy
```

The devnet faucet is rate-limited; if the demo provider can't fund itself, send its address a little devnet SOL and USDC from the faucets.

## 7. Check it

1. `https://YOUR_HOSTNAME` shows the site. `curl -sI https://YOUR_HOSTNAME | head` shows HTTP/2 and the security headers.
2. Sign in with Privy. Hover your wallet in the top bar: address and QR.
3. Fund that wallet from faucet.circle.com (Solana Devnet USDC) and faucet.solana.com (devnet SOL).
4. Spend passes: create a pass, then "Create with my account wallet" to make its vault.
5. Agent chat: "Find a devnet API for token prices and call it". Approve the card. Open the invoice and the Solscan link.

## Updating

```bash
cd /opt/algebra && git pull
cd backend && go build -trimpath -o ../bin/api ./cmd/api && sudo systemctl restart algebra-api
cd ../frontend && pnpm install --frozen-lockfile && pnpm build && cp -r .next/static .next/standalone/.next/static && sudo systemctl restart algebra-web
```

Migrations run when the API starts. Back up Postgres before a deploy: `sudo -u postgres pg_dump algebra | gzip > ~/algebra-$(date +%F).sql.gz`.

## Before real users

Cloud SQL and Memorystore instead of the VM's own Postgres and Redis; the master key in Secret Manager or KMS; a log sink and an alert on `/readyz`; and the rest of [docs/PRODUCTION.md](../../docs/PRODUCTION.md) §5. None of that is needed to show a devnet demo.
