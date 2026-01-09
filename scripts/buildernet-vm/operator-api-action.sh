#!/usr/bin/env bash
# Execute an operator-api action
set -eu -o pipefail

OPERATOR_API_PORT=${OPERATOR_API_PORT:-13535}
OPERATOR_API_HOST=${OPERATOR_API_HOST:-localhost}

ACTION=${1:-}

if [ -z "$ACTION" ]; then
    echo "Usage: $0 <action>"
    echo ""
    echo "Available actions:"
    echo "  reboot                  - Reboot the system"
    echo "  rbuilder_restart        - Restart rbuilder-operator service"
    echo "  rbuilder_stop           - Stop rbuilder-operator service"
    echo "  fetch_config            - Fetch config from BuilderHub"
    echo "  rbuilder_bidding_restart - Restart rbuilder-bidding service"
    echo "  ssh_stop                - Stop SSH service"
    echo "  ssh_start               - Start SSH service"
    echo "  haproxy_restart         - Restart HAProxy service"
    exit 1
fi

curl -s -k "https://${OPERATOR_API_HOST}:${OPERATOR_API_PORT}/api/v1/actions/${ACTION}"
