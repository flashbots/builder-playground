#!/usr/bin/env bash
# Extract VM image and create data disk
#
# Usage:
#   ./prepare.sh                              # Use default build output
#   ./prepare.sh /path/to/image.qcow2         # Use local image
#   ./prepare.sh https://example.com/img.qcow2 # Download from URL
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}/.."

FLASHBOTS_IMAGES_DIR="${PROJECT_DIR}/.flashbots-images"
RUNTIME_DIR="${PROJECT_DIR}/.runtime"

DEFAULT_QCOW2="${FLASHBOTS_IMAGES_DIR}/mkosi.output/buildernet-qemu_latest.qcow2"

VM_IMAGE="${RUNTIME_DIR}/buildernet-vm.qcow2"
VM_DATA_DISK="${RUNTIME_DIR}/persistent.raw"

# Determine source image
SOURCE="${1:-${DEFAULT_QCOW2}}"

rm -rf "${RUNTIME_DIR}"
mkdir -p "${RUNTIME_DIR}"

if [[ "${SOURCE}" =~ ^https?:// ]]; then
    echo "Downloading VM image: ${SOURCE}"
    curl -L -o "${VM_IMAGE}" "${SOURCE}"
elif [[ -f "${SOURCE}" ]]; then
    echo "Copying VM image: ${SOURCE}"
    cp --sparse=always "${SOURCE}" "${VM_IMAGE}"
else
    echo "Error: VM image not found: ${SOURCE}"
    if [[ "${SOURCE}" == "${DEFAULT_QCOW2}" ]]; then
        echo "Run './scripts/sync.sh && ./scripts/build.sh' first, or pass a path/URL as argument."
        echo "Usage: ./scripts/prepare.sh [/path/to/image.qcow2 | https://url/to/image.qcow2]"
    fi
    exit 1
fi

qemu-img create -f raw "${VM_DATA_DISK}" 100G

echo "Runtime ready: ${RUNTIME_DIR}"
ls -lah "${RUNTIME_DIR}"