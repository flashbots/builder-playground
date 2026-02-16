#!/usr/bin/env bash
# Extract VM image and create data disk
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FLASHBOTS_IMAGES_DIR="${SCRIPT_DIR}/.flashbots-images"
RUNTIME_DIR="${SCRIPT_DIR}/.runtime"

QEMU_QCOW2="${FLASHBOTS_IMAGES_DIR}/mkosi.output/buildernet-qemu_latest.qcow2"

VM_IMAGE="${RUNTIME_DIR}/buildernet-vm.qcow2"
VM_DATA_DISK="${RUNTIME_DIR}/persistent.raw"

if [[ ! -f "${QEMU_QCOW2}" ]]; then
    echo "Error: QEMU qcow2 image not found: ${QEMU_QCOW2}"
    echo "Run ./build.sh first."
    exit 1
fi

rm -rf "${RUNTIME_DIR}"
mkdir -p "${RUNTIME_DIR}"

rm -f "${VM_IMAGE}"
cp --sparse=always "${QEMU_QCOW2}" "${VM_IMAGE}"

qemu-img create -f raw "${VM_DATA_DISK}" 100G

echo "Runtime ready: ${RUNTIME_DIR}"
ls -lah "${RUNTIME_DIR}"
