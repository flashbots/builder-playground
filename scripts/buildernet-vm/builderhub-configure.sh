#!/usr/bin/env bash
# Configure builder-hub for the VM
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

BUILDER_HUB_ADMIN_URL="http://localhost:8082/api/admin/v1"
BUILDER_ID="playground_vm_builder"
BUILDER_IP="1.2.3.4"
MEASUREMENT_ID="test1"

echo "Create Allow-All Measurements: ${MEASUREMENT_ID}"
curl --request POST \
  --url "${BUILDER_HUB_ADMIN_URL}/measurements" \
  --data '{"measurement_id": "'${MEASUREMENT_ID}'","attestation_type": "test","measurements": {}}'

echo "Enable Measurements: ${MEASUREMENT_ID}"
curl --request POST \
  --url "${BUILDER_HUB_ADMIN_URL}/measurements/activation/${MEASUREMENT_ID}" \
  --data '{"enabled": true}'

echo "Create Builder: ${BUILDER_ID}"
curl --request POST \
  --url "${BUILDER_HUB_ADMIN_URL}/builders" \
  --data '{"name": "'${BUILDER_ID}'","ip_address": "'${BUILDER_IP}'", "network": "playground"}'

echo "Create Builder Configuration: ${BUILDER_ID}"
curl --url "${BUILDER_HUB_ADMIN_URL}/builders/configuration/${BUILDER_ID}" \
  --data-binary '{}'

echo "Set Secrets: ${BUILDER_ID}"
function yaml2json {
  python3 -c 'import sys,yaml,json; print(json.dumps(yaml.load(str(sys.stdin.read()),Loader=yaml.BaseLoader)))'
}

cat "${SCRIPT_DIR}/builderhub-config.yaml" | yaml2json | curl --data-binary @- "${BUILDER_HUB_ADMIN_URL}/builders/secrets/${BUILDER_ID}"

echo "Enable Builder: ${BUILDER_ID}"
curl --request POST \
  --url "${BUILDER_HUB_ADMIN_URL}/builders/activation/${BUILDER_ID}" \
  --data '{"enabled": true}'

echo "Verify Builder Configuration for ${BUILDER_ID}:"
curl -s http://localhost:8888/api/l1-builder/v1/configuration | jq .
