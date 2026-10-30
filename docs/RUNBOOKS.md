# Runbooks

What to do when something goes wrong in production, in the order to do it. The setup itself is in
[deploy/gcp/README.md](../deploy/gcp/README.md); the failure rules the coordinator follows are in
[ECONOMIC_COORDINATION.md](ECONOMIC_COORDINATION.md). Commands run from a checkout with `gcloud` signed in; the VM is
reached with `gcloud compute ssh algebra-1 --project algebra-prod-7802b5 --zone asia-south1-b --tunnel-through-iap`.

**None of these has been rehearsed yet.** The table at the end is for the date, the time it took and what went wrong; fill
it in the first time each one is run for practice, not in an emergency.

## Stop all spending now

From the weakest to the strongest. Each one is reversible.

1. **One agent:** the pass's switch in the console, or `POST /api/v1/me/passes/{id}/freeze {"frozen":true}`. A frozen pass releases
   nothing, even a payment already being signed.
2. **One person, every agent:** "Freeze every pass" in the console, or `POST /api/v1/me/killswitch {"frozen":true}`.
3. **Everything, from the server:** `sudo systemctl stop algebra-api` on the VM. The API is down, so nothing is paid and nothing
   is accepted; payments already in flight are settled from the chain by the reconciler when it starts again.
4. **Cut the internet off the VM:** `deploy/gcp/public-https.sh --close` (shuts ports 80 and 443, stops Caddy). Your own SSH
   through Google's IAP still works.

## A deploy went wrong

- **API:** `sudo cp /opt/algebra/bin/api.prev /opt/algebra/bin/api && sudo systemctl restart algebra-api` (migrations only go
  forward, so check that the previous binary still runs against the newer schema before relying on this). Check `curl -s http://127.0.0.1:8080/readyz`.
- **Web app:** Vercel, Deployments, the previous production deployment, Promote to Production. It carries the environment it was
  built with, so it also goes back to the API address it used. Or `vercel rollback` in a checkout linked to the project.
- **Which commit runs where:** `cat /opt/algebra/VERSION` on the VM; the footer-less way for the web app is the deployment's commit in Vercel.

## A secret may have leaked

1. Add a new version, then restart: `printf %s "$NEW" | gcloud secrets versions add NAME --project algebra-prod-7802b5 --data-file=-`
   and `sudo systemctl restart algebra-api` (and `cloudflared` for the tunnel token). Random ones: `openssl rand -hex 32`.
2. **`ALGEBRA_EDGE_SECRET` (the web app) must equal `algebra-edge-secret`:** set both, then redeploy the web app
   (`vercel env add ALGEBRA_EDGE_SECRET production --force`, then Promote or push a frontend commit).
3. **Resend:** revoke the key in Resend first, add the new one as a version of `algebra-resend-api-key`. Until it is added no
   sign-in code is sent.
4. **The master key** seals kept answers, agent tokens and the generated payer key, and derives the receipts' signing key. Replacing
   it makes those unreadable and old receipts stop verifying: with test money, start from a fresh database; with real money this
   needs a planned re-seal (deploy/gcp/README.md, "If something may have leaked").
5. Read Cloud Audit Logs for the project (Secret Manager access, KMS decrypts) for anything you did not do.

## The test payer is nearly empty

`sudo -u algebra bash -c 'set -a; . /etc/algebra/api.conf; . /run/algebra/api.env; /opt/algebra/bin/solana-wallet -generated'`
prints its address and balances. Fund it with devnet SOL (fees, which Algebra pays) and devnet USDC (what credits spend) from
faucet.solana.com and faucet.circle.com (a CAPTCHA: a person), or send from another devnet wallet you hold. Credits are a ledger
over this one wallet, bounded by the test-money guard (`TEST_MONEY_ACCOUNT_LIMIT`, `TEST_MONEY_DAY_LIMIT`); when the wallet is empty a Testnet
payment fails instead of being delivered, so fund it before it gets there.

## Restore from a backup

Nightly `pg_dump` files are in `gs://algebra-prod-7802b5-backups/pg/`. The VM can't read them (by design), so download one and put
it on the VM: the steps are in deploy/gcp/README.md, "Restore from a backup". Practise on a scratch database first.

## The API's name changes (a real hostname, or Cloudflare)

Point the new name at the VM's reserved address (`algebra-api-ip`), run `deploy/gcp/public-https.sh NEW_NAME OLD_NAME` so Caddy
serves both, set Vercel `ALGEBRA_API_URL=https://NEW_NAME`, and redeploy the web app. Drop the old name once nothing uses it.
With a Cloudflare tunnel carrying the traffic, run `public-https.sh --close`.

## The site is down

1. `curl -s -m 10 https://34-180-40-140.sslip.io/readyz` (the API) and `curl -s -o /dev/null -w '%{http_code}' https://www.usealgebra.xyz/` (the web app).
2. API down: `sudo systemctl status algebra-api caddy postgresql redis-server` and `sudo journalctl -u algebra-api -n 100 --no-pager`.
3. Web app down or wrong: Vercel's deployment list; promote the previous one.
4. The uptime check and CPU, memory and disk alerts mail the owner (`deploy/gcp/monitoring.sh`).

## Rehearsals

| Date | Scenario | Who | Time it took | What went wrong |
|---|---|---|---|---|
| | | | | |
