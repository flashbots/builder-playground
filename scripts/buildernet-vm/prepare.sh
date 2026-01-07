#!/usr/bin/env bash
# Extract VM image and create data disk
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FLASHBOTS_IMAGES_DIR="${SCRIPT_DIR}/.flashbots-images"
RUNTIME_DIR="${SCRIPT_DIR}/.runtime"

QEMU_RAW="${FLASHBOTS_IMAGES_DIR}/mkosi.output/buildernet-qemu_latest.raw"

VM_IMAGE="${RUNTIME_DIR}/buildernet-vm.raw"
VM_DATA_DISK="${RUNTIME_DIR}/persistent.raw"

if [[ ! -f "${QEMU_RAW}" ]]; then
    echo "Error: QEMU raw image not found: ${QEMU_RAW}"
    echo "Run ./build.sh first."
    exit 1
fi

rm -rf "${RUNTIME_DIR}"
mkdir -p "${RUNTIME_DIR}"

cp "${QEMU_RAW}" "${VM_IMAGE}"

qemu-img create -f raw "${VM_DATA_DISK}" 100G

echo "Runtime ready: ${RUNTIME_DIR}"
ls -lah "${RUNTIME_DIR}"
