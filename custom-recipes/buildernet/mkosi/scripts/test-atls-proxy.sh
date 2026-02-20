#!/usr/bin/env bash
# Spins up an atls-proxy client, curls through it, tears it down.
# Usage: ./test-atls-proxy.sh [server:port] [path]
set -euo pipefail

IMAGE="ghcr.io/flashbots/attested-tls-proxy:1.0.1"
NAME="atls-proxy-client-test"
PORT=6000
SERVER="${1:-localhost:7000}"
PATH_="${2:-/api/l1-builder/v1/configuration}"

trap 'docker rm -f $NAME >/dev/null 2>&1 || true' EXIT
docker rm -f "$NAME" >/dev/null 2>&1 || true

echo "-> client connecting to ${SERVER}"
docker run -d --name "$NAME" --network host "$IMAGE" \
  client --listen-addr "0.0.0.0:${PORT}" \
  --client-attestation-type none \
  --allowed-remote-attestation-type none \
  --allow-self-signed \
  "$SERVER" >/dev/null

for i in $(seq 1 10); do
  curl -s -o /dev/null "http://127.0.0.1:${PORT}/" 2>/dev/null && break
  [ "$i" -eq 10 ] && { echo "client failed to start:"; docker logs "$NAME"; exit 1; }
  sleep 0.5
done

echo "-> GET ${PATH_}"
curl -s -w "\n--- %{http_code}\n" "http://127.0.0.1:${PORT}${PATH_}"