#!/bin/bash
# Builds the API from the current commit and installs it on the Algebra VM.
#
#   deploy/gcp/deploy.sh            build, upload, install, restart, check
#   deploy/gcp/deploy.sh --setup    the same, after re-running vm-setup.sh (packages, units, settings)
#
# Needs gcloud signed in with access to the project (see README.md). The VM is reached through
# Google's identity-aware proxy: no port on it is open to the internet.
set -euo pipefail

PROJECT=${PROJECT:-algebra-prod-7802b5}
ZONE=${ZONE:-asia-south1-b}
VM=${VM:-algebra-1}
REGION=${ZONE%-*}
SSH_FLAGS=(--project "$PROJECT" --zone "$ZONE" --tunnel-through-iap --strict-host-key-checking=no --quiet)

ROOT=$(git rev-parse --show-toplevel)
COMMIT=$(git -C "$ROOT" rev-parse --short HEAD)
if ! git -C "$ROOT" diff --quiet HEAD -- backend deploy/gcp; then
  echo "deploy: backend/ or deploy/gcp has uncommitted changes; commit them first so the VM runs a known commit" >&2
  exit 1
fi

STAGE_DIR=$(mktemp -d)
trap 'rm -rf "$STAGE_DIR"' EXIT
NAME="algebra-stage-$COMMIT"
STAGE="$STAGE_DIR/$NAME"
mkdir -p "$STAGE/bin"

echo "== building $COMMIT for linux/amd64"
for cmd in api demo-provider solana-wallet; do
  (cd "$ROOT/backend" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$STAGE/bin/$cmd" "./cmd/$cmd")
done
cp -r "$ROOT/backend/migrations" "$STAGE/migrations"
cp -r "$ROOT/deploy/gcp" "$STAGE/gcp"
# Scripts must have Unix line ends on the VM, whatever this checkout has.
find "$STAGE/gcp" -type f ! -name '*.md' -exec sed -i 's/\r$//' {} +
(cd "$STAGE" && sha256sum bin/* > SHA256SUMS)
echo "$COMMIT" > "$STAGE/VERSION"

echo "== uploading"
# pscp (what gcloud uses on Windows) wants a Windows path, and copies the folder into /tmp/.
SRC=$STAGE
command -v cygpath >/dev/null 2>&1 && SRC=$(cygpath -m "$STAGE")
gcloud compute scp --recurse "${SSH_FLAGS[@]}" "$SRC" "$VM:/tmp/" >/dev/null

SETUP=0
[ "${1:-}" = "--setup" ] && SETUP=1

echo "== installing on $VM"
gcloud compute ssh "$VM" "${SSH_FLAGS[@]}" --command="
set -euo pipefail
cd /tmp/$NAME
sha256sum -c SHA256SUMS
sudo install -d -m 0755 /etc/algebra
printf 'PROJECT=%s\nKMS_KEY=%s\nBACKUP_BUCKET=%s\n' '$PROJECT' 'projects/$PROJECT/locations/$REGION/keyRings/algebra/cryptoKeys/master-wrap' '$PROJECT-backups' | sudo tee /etc/algebra/deploy.conf >/dev/null
if [ $SETUP = 1 ]; then sudo bash gcp/vm-setup.sh; fi
sudo install -d -m 0755 /opt/algebra/bin /opt/algebra/backend
[ -f /opt/algebra/bin/api ] && sudo cp -f /opt/algebra/bin/api /opt/algebra/bin/api.prev
sudo install -m 0755 bin/api bin/demo-provider bin/solana-wallet /opt/algebra/bin/
sudo rm -rf /opt/algebra/backend/migrations && sudo cp -r migrations /opt/algebra/backend/migrations
sudo install -m 0750 gcp/vm/algebra-fetch-secrets /usr/local/sbin/algebra-fetch-secrets
sudo install -m 0755 gcp/vm/algebra-api-start /usr/local/sbin/algebra-api-start
sudo install -m 0640 -o root -g algebra gcp/vm/api.conf /etc/algebra/api.conf
sudo install -m 0644 gcp/vm/*.service gcp/vm/*.timer /etc/systemd/system/
sudo systemctl daemon-reload
echo $COMMIT | sudo tee /opt/algebra/VERSION >/dev/null
sudo chown -R root:root /opt/algebra
sudo systemctl restart algebra-demo-provider algebra-api
sudo systemctl restart cloudflared 2>/dev/null || true
for i in \$(seq 1 40); do
  if curl -fsS http://127.0.0.1:8080/readyz 2>/dev/null; then echo; echo 'ready'; break; fi
  sleep 2
done
systemctl is-active algebra-demo-provider algebra-api cloudflared || true
rm -rf /tmp/$NAME
"
echo "== done: $COMMIT is running on $VM"
