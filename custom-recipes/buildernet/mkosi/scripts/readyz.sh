#!/usr/bin/env bash
# Poll /readyz on HAProxy until the VM is ready or timeout is reached
set -eu -o pipefail

TIMEOUT=${1:-300}
PORT=${HAPROXY_PORT:-10443}
HOST=${HAPROXY_HOST:-localhost}
URL="https://${HOST}:${PORT}/readyz"
START=$(date +%s)

echo "Waiting for ${URL}..."
while true; do
    if curl -sfk -m 2 -o /dev/null "${URL}" 2>/dev/null; then
        echo "OK ($(( $(date +%s) - START ))s)"
        exit 0
    fi
    elapsed=$(( $(date +%s) - START ))
    if [[ "${elapsed}" -ge "${TIMEOUT}" ]]; then
        echo "Timed out after ${elapsed}s"
        exit 1
    fi
    echo "  ${elapsed}s elapsed, retrying..."
    sleep 5
done
