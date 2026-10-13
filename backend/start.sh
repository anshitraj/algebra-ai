#!/bin/sh
# Starts the devnet demo providers (when DEMO_PROVIDER is not "off"), then the
# API. The API listens where the host says (PORT), and is told where the demo
# providers are unless ECONOMIC_PROVIDERS is already set.
set -eu

: "${HTTP_ADDR:=:${PORT:-8080}}"
export HTTP_ADDR

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
