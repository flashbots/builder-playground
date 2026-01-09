#!/usr/bin/env bash
# Configure builder-hub for the VM
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

BUILDER_HUB_ADMIN_URL="http://localhost:8082/api/admin/v1"
BUILDER_ID="playground_vm_builder"
BUILDER_IP="1.2.3.4"
MEASUREMENT_ID="test1"

#
# Get Reth and Lighthouse peer info from the playground services
#

# Reth and Lighthouse RPC endpoints (running in playground)
RETH_RPC_URL="http://localhost:8545"
LIGHTHOUSE_API_URL="http://localhost:3500"
# IP address the VM uses to reach the host (QEMU NAT)
VM_HOST_IP="10.0.2.2"

echo "Fetching peer info from playground services..."

# Get Reth enode and replace IP with VM-accessible address
ENODE_RAW=$(curl -s -X POST "${RETH_RPC_URL}" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"admin_nodeInfo","id":1}' | jq -r '.result.enode')
# Replace the IP in enode with VM_HOST_IP (enode://pubkey@IP:port -> enode://pubkey@10.0.2.2:port)
EL_BOOTNODE=$(echo "${ENODE_RAW}" | sed -E "s/@[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:/@${VM_HOST_IP}:/")
echo "EL bootnode: ${EL_BOOTNODE}"

# Get Lighthouse peer ID and build libp2p multiaddr for --libp2p-addresses
# The multiaddr format is: /ip4/<IP>/tcp/<PORT>/p2p/<PEER_ID>
# Note: Lighthouse P2P TCP is mapped to host port 9001 (see playground startup output)
CL_PEER_ID=$(curl -s "${LIGHTHOUSE_API_URL}/eth/v1/node/identity" | jq -r '.data.peer_id')
CL_LIBP2P_ADDR="/ip4/${VM_HOST_IP}/tcp/9001/p2p/${CL_PEER_ID}"
echo "CL libp2p address: ${CL_LIBP2P_ADDR}"

#
# Configure builder-hub
#

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

# Replace placeholders with actual values and send to builder-hub
cat "${SCRIPT_DIR}/builderhub-config.yaml" \
  | sed "s|{{EL_BOOTNODE}}|${EL_BOOTNODE}|g" \
  | sed "s|{{CL_LIBP2P_ADDR}}|${CL_LIBP2P_ADDR}|g" \
  | yaml2json \
  | curl --data-binary @- "${BUILDER_HUB_ADMIN_URL}/builders/secrets/${BUILDER_ID}"

echo "Enable Builder: ${BUILDER_ID}"
curl --request POST \
  --url "${BUILDER_HUB_ADMIN_URL}/builders/activation/${BUILDER_ID}" \
  --data '{"enabled": true}'

echo "Verify Builder Configuration for ${BUILDER_ID}:"
curl -s http://localhost:8888/api/l1-builder/v1/configuration | jq .
