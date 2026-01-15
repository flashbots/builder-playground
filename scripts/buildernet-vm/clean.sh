#!/usr/bin/env bash
# Clean build artifacts and runtime files
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FLASHBOTS_IMAGES_DIR="${SCRIPT_DIR}/.flashbots-images"
RUNTIME_DIR="${SCRIPT_DIR}/.runtime"
PIDFILE="${RUNTIME_DIR}/qemu.pid"

# Check if VM is running
if [[ -f "${PIDFILE}" ]] && kill -0 $(cat "${PIDFILE}") 2>/dev/null; then
    echo "Error: VM is still running. Run ./stop.sh first."
    exit 1
fi

if [[ -d "${FLASHBOTS_IMAGES_DIR}" ]]; then
    make -C "${FLASHBOTS_IMAGES_DIR}" clean
fi

rm -rf "${RUNTIME_DIR}"
