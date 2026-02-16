#!/usr/bin/env bash
# Get operator-api event logs
set -eu -o pipefail

OPERATOR_API_PORT=${OPERATOR_API_PORT:-13535}
OPERATOR_API_HOST=${OPERATOR_API_HOST:-localhost}

curl -s -k "https://${OPERATOR_API_HOST}:${OPERATOR_API_PORT}/logs"
