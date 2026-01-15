#!/usr/bin/env bash
# Check builder-hub configuration
set -eu -o pipefail

echo "Builder Configuration:"
curl -s http://localhost:8888/api/l1-builder/v1/configuration | jq .
