#!/bin/bash
# Gives the API a public HTTPS address without Cloudflare.
#
#   deploy/gcp/public-https.sh                 use <address>.sslip.io (a public name that resolves to the VM's address)
#   deploy/gcp/public-https.sh api.example.com [more names]   use these names (each needs an A record to the address)
#   deploy/gcp/public-https.sh --close         shut ports 80 and 443 again and stop Caddy
#
# Caddy on the VM takes the TLS handshake (it gets and renews its own certificate) and hands requests to the API on
# 127.0.0.1:8080. Only ports 80 and 443 are opened, only to this VM, and SSH stays reachable through Google's
# identity-aware proxy alone. This is the way to be reachable before a Cloudflare tunnel exists: it exposes the API
# itself to the internet (no WAF, no DDoS shield in front of it), so it fits a Testnet launch, not real money. The
# designed path in README.md ("Cutover from Railway") closes these ports again once the tunnel carries the traffic.
set -euo pipefail

PROJECT=${PROJECT:-algebra-prod-7802b5}
ZONE=${ZONE:-asia-south1-b}
VM=${VM:-algebra-1}
REGION=${ZONE%-*}
NETWORK=${NETWORK:-algebra-vpc}
TAG=${TAG:-algebra-vm}
G=(--project "$PROJECT")
SSH_FLAGS=(--project "$PROJECT" --zone "$ZONE" --tunnel-through-iap --strict-host-key-checking=no --quiet)

if [ "${1:-}" = "--close" ]; then
  gcloud compute ssh "$VM" "${SSH_FLAGS[@]}" --command="sudo systemctl disable --now caddy" || true
  gcloud compute firewall-rules delete algebra-allow-https "${G[@]}" --quiet || true
  echo "closed: ports 80 and 443 are shut and Caddy is stopped"
  exit 0
fi

IP=$(gcloud compute instances describe "$VM" --zone "$ZONE" "${G[@]}" --format='value(networkInterfaces[0].accessConfigs[0].natIP)')
[ -n "$IP" ] || { echo "public-https: $VM has no external address" >&2; exit 1; }
# sslip.io answers every name like 34-180-40-140.sslip.io with that address, so no DNS record of ours is needed.
# (Google's own name for the address, N.N.N.N.bc.googleusercontent.com, cannot be used: its CAA record forbids Let's Encrypt.)
DEFAULT_HOST="${IP//./-}.sslip.io"
HOSTS=("$@")
[ ${#HOSTS[@]} -eq 0 ] && HOSTS=("$DEFAULT_HOST")
SITES=$(IFS=,; echo "${HOSTS[*]}" | sed 's/,/, /g')
PRIMARY=${HOSTS[0]}

echo "== keeping $IP (a stopped VM would otherwise get a new address, and the names below would point nowhere)"
gcloud compute addresses describe algebra-api-ip --region "$REGION" "${G[@]}" >/dev/null 2>&1 \
  || gcloud compute addresses create algebra-api-ip --addresses "$IP" --region "$REGION" "${G[@]}"

echo "== opening ports 80 and 443 to this VM only"
gcloud compute firewall-rules describe algebra-allow-https "${G[@]}" >/dev/null 2>&1 \
  || gcloud compute firewall-rules create algebra-allow-https "${G[@]}" --network "$NETWORK" --direction INGRESS \
       --allow tcp:80,tcp:443 --source-ranges 0.0.0.0/0 --target-tags "$TAG" \
       --description "Public HTTPS for the API through Caddy (deploy/gcp/public-https.sh). Delete with --close."

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
cat > "$STAGE/Caddyfile" <<EOF
# Written by deploy/gcp/public-https.sh. Caddy gets and renews the certificates by itself.
{
	servers {
		timeouts {
			read_header 10s
			idle 2m
		}
	}
}

$SITES {
	# Streams (the MCP endpoint, long answers) must not wait for a buffer to fill.
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
EOF
SRC=$STAGE/Caddyfile
command -v cygpath >/dev/null 2>&1 && SRC=$(cygpath -m "$STAGE/Caddyfile")

echo "== installing Caddy on $VM (Debian's own package)"
gcloud compute scp "${SSH_FLAGS[@]}" "$SRC" "$VM:/tmp/Caddyfile" >/dev/null
gcloud compute ssh "$VM" "${SSH_FLAGS[@]}" --command="
set -euo pipefail
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y caddy >/dev/null
sudo install -m 0644 /tmp/Caddyfile /etc/caddy/Caddyfile
rm -f /tmp/Caddyfile
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null
sudo systemctl enable caddy >/dev/null 2>&1
sudo systemctl restart caddy
systemctl is-active caddy
exit 0
"

echo "== waiting for the certificate and the first answer from https://$PRIMARY"
for i in $(seq 1 40); do
  if out=$(curl -fsS -m 10 "https://$PRIMARY/readyz" 2>/dev/null); then echo "$out"; echo "ready: ALGEBRA_API_URL=https://$PRIMARY"; exit 0; fi
  sleep 3
done
echo "public-https: https://$PRIMARY did not answer in two minutes; read the log: sudo journalctl -u caddy -n 60 --no-pager" >&2
exit 1
