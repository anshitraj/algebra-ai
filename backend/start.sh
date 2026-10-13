#!/bin/sh
# Starts the devnet demo providers (when DEMO_PROVIDER is not "off"), then the
# API. The API listens where the host says (PORT), and is told where the demo
# providers are unless ECONOMIC_PROVIDERS is already set.
set -eu

: "${HTTP_ADDR:=:${PORT:-8080}}"
export HTTP_ADDR

# Cloud KMS signing off Google Cloud needs credentials as a file. If the host only
# offers environment variables, GOOGLE_CREDENTIALS_JSON (a service-account key that
# may only sign with one key version) is written to a private file and the variable
# is dropped before anything else starts. Prefer workload identity where available.
if [ -n "${GOOGLE_CREDENTIALS_JSON:-}" ] && [ -z "${GOOGLE_APPLICATION_CREDENTIALS:-}" ]; then
  ( umask 077; printf '%s' "$GOOGLE_CREDENTIALS_JSON" > /tmp/gcp-credentials.json )
  GOOGLE_APPLICATION_CREDENTIALS=/tmp/gcp-credentials.json
  export GOOGLE_APPLICATION_CREDENTIALS
fi
unset GOOGLE_CREDENTIALS_JSON

# A devnet payer, unless the operator brought a wallet: Algebra makes the key
# itself on first start and keeps it sealed in the database (back up
# ALGEBRA_MASTER_KEY: it opens it). Devnet only; real money needs a key held
# outside this server. And every payment comes from a person's Spend Pass vault,
# never from the payer's own funds. Both can be set in the service variables.
if [ -z "${SOLANA_CLUSTER:-}" ] && [ -z "${SOLANA_DEVNET_KEYPAIR:-}" ] && [ -z "${SOLANA_DEVNET_KEYPAIR_FILE:-}" ]; then
  : "${SOLANA_DEVNET_SIGNER:=generated}"
  export SOLANA_DEVNET_SIGNER
fi
: "${SPEND_PASS_REQUIRED:=devnet}"
export SPEND_PASS_REQUIRED

if [ "${DEMO_PROVIDER:-on}" != "off" ]; then
  if [ -z "${ECONOMIC_PROVIDERS:-}" ]; then
    ECONOMIC_PROVIDERS="$(/app/demo-provider -addr 127.0.0.1:8402 -print-config)"
    export ECONOMIC_PROVIDERS
  fi
  /app/demo-provider -addr 127.0.0.1:8402 &
  sleep 3
fi

exec /app/api
