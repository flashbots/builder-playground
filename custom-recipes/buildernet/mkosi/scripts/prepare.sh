#!/usr/bin/env bash
# Extract VM image and create data disk
#
# Usage:
#   ./prepare.sh /path/to/image.qcow2         # Use local image
#   ./prepare.sh https://example.com/img.qcow2 # Download from URL
#
# Environment:
#   BUILDERNET_IMAGE  - Path or URL to VM image (overridden by $1 argument)
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}/.."

RUNTIME_DIR="${PROJECT_DIR}/.runtime"

VM_IMAGE="${RUNTIME_DIR}/buildernet-vm.qcow2"
VM_DATA_DISK="${RUNTIME_DIR}/persistent.raw"

# Determine source image: $1 > $BUILDERNET_IMAGE > error
SOURCE="${1:-${BUILDERNET_IMAGE:-}}"

if [[ -z "${SOURCE}" ]]; then
    echo "Error: no VM image specified."
    echo "Set BUILDERNET_IMAGE or pass a path/URL as argument."
    echo "Usage: ./scripts/prepare.sh [/path/to/image.qcow2 | https://url/to/image.qcow2]"
    exit 1
fi

echo "prepare.sh: PROJECT_DIR=${PROJECT_DIR}"
echo "prepare.sh: RUNTIME_DIR=${RUNTIME_DIR}"
echo "prepare.sh: SOURCE=${SOURCE}"

# Ensure the VM is stopped before replacing runtime files (PID file, disk images).
"${SCRIPT_DIR}/stop.sh"

rm -rf "${RUNTIME_DIR}"
mkdir -p "${RUNTIME_DIR}"

if [[ "${SOURCE}" =~ ^https?:// ]]; then
    echo "prepare.sh: downloading from URL..."
    TMP_IMAGE="${VM_IMAGE}.tmp"
    curl -fSL -o "${TMP_IMAGE}" "${SOURCE}"
    mv "${TMP_IMAGE}" "${VM_IMAGE}"
elif [[ -f "${SOURCE}" ]]; then
    echo "prepare.sh: copying local file ($(du -h "${SOURCE}" | cut -f1))..."
    cp --sparse=always "${SOURCE}" "${VM_IMAGE}"
else
    echo "Error: VM image not found: ${SOURCE}"
    exit 1
fi

echo "prepare.sh: creating data disk..."
qemu-img create -f raw "${VM_DATA_DISK}" 100G

echo "prepare.sh: runtime ready"
ls -lah "${RUNTIME_DIR}"
