#!/usr/bin/env bash
# Extract VM image and create data disk
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FLASHBOTS_IMAGES_DIR="${SCRIPT_DIR}/.flashbots-images"
RUNTIME_DIR="${SCRIPT_DIR}/.runtime"

# TODO: adjust path based on actual mkosi output
TARBALL="${FLASHBOTS_IMAGES_DIR}/mkosi.output/buildernet-gcp_latest-import.tar.gz"

VM_IMAGE="${RUNTIME_DIR}/buildernet-vm.raw"
VM_DATA_DISK="${RUNTIME_DIR}/persistent.raw"

if [[ ! -f "${TARBALL}" ]]; then
    echo "Error: Tarball not found: ${TARBALL}"
    echo "Run ./build.sh first."
    exit 1
fi

rm -rf "${RUNTIME_DIR}"
mkdir -p "${RUNTIME_DIR}"

tar -xzf "${TARBALL}" -C "${RUNTIME_DIR}"
mv "${RUNTIME_DIR}/disk.raw" "${VM_IMAGE}"

qemu-img create -f raw "${VM_DATA_DISK}" 100G

echo "Runtime ready: ${RUNTIME_DIR}"
ls -lah "${RUNTIME_DIR}"
