#!/usr/bin/env bash
# Check operator-api health
set -eu -o pipefail

OPERATOR_API_PORT=${OPERATOR_API_PORT:-13535}
OPERATOR_API_HOST=${OPERATOR_API_HOST:-localhost}

# /livez returns HTTP 200 with empty body on success
HTTP_CODE=$(curl -s -k -o /dev/null -w "%{http_code}" "https://${OPERATOR_API_HOST}:${OPERATOR_API_PORT}/livez")

if [ "$HTTP_CODE" = "200" ]; then
    echo "OK"
    exit 0
else
    echo "FAIL (HTTP $HTTP_CODE)"
    exit 1
fi
