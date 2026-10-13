#!/bin/sh
# Starts the devnet demo providers (when DEMO_PROVIDER is not "off"), then the
# API. The API listens where the host says (PORT), and is told where the demo
# providers are unless ECONOMIC_PROVIDERS is already set.
set -eu

: "${HTTP_ADDR:=:${PORT:-8080}}"
export HTTP_ADDR

if [ "${DEMO_PROVIDER:-on}" != "off" ]; then
  if [ -z "${ECONOMIC_PROVIDERS:-}" ]; then
    ECONOMIC_PROVIDERS="$(/app/demo-provider -addr 127.0.0.1:8402 -print-config)"
    export ECONOMIC_PROVIDERS
  fi
  /app/demo-provider -addr 127.0.0.1:8402 &
  sleep 3
fi

exec /app/api
