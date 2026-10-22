#!/bin/bash
# The Google Cloud resources Algebra runs on, as commands. This is the record of what was created on
# 2026-10-22 (project algebra-prod-7802b5); to build the same thing again in a new project, run it
# with a new PROJECT. It creates billable resources (about 35 USD a month: see README.md).
#
# Usage: PROJECT=algebra-prod-xxxxxx BILLING=XXXXXX-XXXXXX-XXXXXX deploy/gcp/provision.sh
# Needs: gcloud signed in as someone who may create projects and link this billing account.
set -euo pipefail

PROJECT=${PROJECT:?set PROJECT (new, globally unique, e.g. algebra-prod-$(openssl rand -hex 3))}
BILLING=${BILLING:?set BILLING (the billing account ID)}
REGION=${REGION:-asia-south1}
ZONE=${ZONE:-asia-south1-b}
SA="algebra-vm@$PROJECT.iam.gserviceaccount.com"
BUCKET="$PROJECT-backups"

echo "== project and billing"
gcloud projects create "$PROJECT" --name="Algebra"
gcloud billing projects link "$PROJECT" --billing-account="$BILLING"
gcloud services enable compute.googleapis.com iam.googleapis.com iamcredentials.googleapis.com \
  cloudkms.googleapis.com secretmanager.googleapis.com storage.googleapis.com logging.googleapis.com \
  monitoring.googleapis.com iap.googleapis.com oslogin.googleapis.com cloudresourcemanager.googleapis.com \
  serviceusage.googleapis.com billingbudgets.googleapis.com --project "$PROJECT"

echo "== network: SSH only from Google's identity-aware proxy; nothing else is let in"
gcloud compute networks create algebra-vpc --subnet-mode=custom --project "$PROJECT"
gcloud compute networks subnets create algebra-subnet --network=algebra-vpc --region="$REGION" \
  --range=10.10.0.0/24 --enable-private-ip-google-access --project "$PROJECT"
gcloud compute firewall-rules create algebra-allow-iap-ssh --network=algebra-vpc --direction=INGRESS \
  --priority=1000 --action=ALLOW --rules=tcp:22 --source-ranges=35.235.240.0/20 --target-tags=algebra-vm \
  --project "$PROJECT"

echo "== the VM's identity: logs and metrics, plus read access to exactly the secrets and key it needs"
gcloud iam service-accounts create algebra-vm --display-name="Algebra VM" --project "$PROJECT"
for role in roles/logging.logWriter roles/monitoring.metricWriter; do
  gcloud projects add-iam-policy-binding "$PROJECT" --member="serviceAccount:$SA" --role="$role" --condition=None --quiet >/dev/null
done

echo "== KMS key that wraps the master key"
gcloud kms keyrings create algebra --location="$REGION" --project "$PROJECT"
gcloud kms keys create master-wrap --keyring=algebra --location="$REGION" --purpose=encryption --project "$PROJECT"
gcloud kms keys add-iam-policy-binding master-wrap --keyring=algebra --location="$REGION" \
  --member="serviceAccount:$SA" --role=roles/cloudkms.cryptoKeyDecrypter --project "$PROJECT" --quiet >/dev/null

echo "== secrets (random values are generated here and never printed)"
mk() { local name=$1; shift; "$@" | gcloud secrets create "$name" --replication-policy=automatic --data-file=- --project "$PROJECT" >/dev/null; }
mk algebra-db-password openssl rand -hex 24
mk algebra-redis-password openssl rand -hex 24
mk algebra-edge-secret openssl rand -hex 32
mk algebra-operator-token openssl rand -hex 32
# The master key: 32 random bytes, stored only after KMS has wrapped them.
openssl rand 32 | gcloud kms encrypt --key=master-wrap --keyring=algebra --location="$REGION" \
  --plaintext-file=- --ciphertext-file=- --project "$PROJECT" | base64 -w0 | \
  gcloud secrets create algebra-master-key-wrapped --replication-policy=automatic --data-file=- --project "$PROJECT" >/dev/null
# Values only a person can supply: created empty. Add them with, in a terminal, after copying the value:
#   Get-Clipboard | gcloud secrets versions add NAME --project PROJECT --data-file=-       (PowerShell)
for name in algebra-resend-api-key algebra-cloudflare-tunnel-token; do
  gcloud secrets create "$name" --replication-policy=automatic --project "$PROJECT" >/dev/null
done
for name in algebra-db-password algebra-redis-password algebra-edge-secret algebra-operator-token \
            algebra-master-key-wrapped algebra-resend-api-key algebra-cloudflare-tunnel-token; do
  gcloud secrets add-iam-policy-binding "$name" --member="serviceAccount:$SA" \
    --role=roles/secretmanager.secretAccessor --project "$PROJECT" --quiet >/dev/null
done

echo "== backup bucket: private, versioned, each object kept 14 days, deleted after 45; the VM may only add"
gcloud storage buckets create "gs://$BUCKET" --location="$REGION" --uniform-bucket-level-access \
  --public-access-prevention --project "$PROJECT"
gcloud storage buckets update "gs://$BUCKET" --versioning --retention-period=14d --project "$PROJECT"
echo '{"rule":[{"action":{"type":"Delete"},"condition":{"age":45}}]}' > /tmp/algebra-lifecycle.json
gcloud storage buckets update "gs://$BUCKET" --lifecycle-file=/tmp/algebra-lifecycle.json --project "$PROJECT"
gcloud storage buckets add-iam-policy-binding "gs://$BUCKET" --member="serviceAccount:$SA" \
  --role=roles/storage.objectCreator --project "$PROJECT" --quiet >/dev/null

echo "== the VM: shielded boot, OS Login (no SSH keys in metadata), deletion protection"
gcloud compute instances create algebra-1 --project "$PROJECT" --zone "$ZONE" \
  --machine-type e2-medium --image-family debian-12 --image-project debian-cloud \
  --boot-disk-size 30GB --boot-disk-type pd-balanced \
  --subnet algebra-subnet --tags algebra-vm --labels app=algebra \
  --service-account "$SA" --scopes cloud-platform \
  --shielded-secure-boot --shielded-vtpm --shielded-integrity-monitoring \
  --metadata enable-oslogin=TRUE,block-project-ssh-keys=TRUE --deletion-protection

cat <<EOF

Next:
  1. Add the Resend key and the Cloudflare tunnel token (README.md, "First setup").
  2. Write /etc/algebra/deploy.conf on the VM and run: deploy/gcp/deploy.sh --setup
EOF
