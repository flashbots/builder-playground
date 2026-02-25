#!/usr/bin/env bash
# Clean build artifacts and runtime files
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}/.."

FLASHBOTS_IMAGES_DIR="${PROJECT_DIR}/.flashbots-images"
RUNTIME_DIR="${PROJECT_DIR}/.runtime"
PIDFILE="${RUNTIME_DIR}/qemu.pid"

# Check if VM is running
if [[ -f "${PIDFILE}" ]] && kill -0 $(cat "${PIDFILE}") 2>/dev/null; then
    echo "Error: VM is still running. Run ./scripts/stop.sh first."
    exit 1
fi

if [[ -d "${FLASHBOTS_IMAGES_DIR}" ]]; then
    make -C "${FLASHBOTS_IMAGES_DIR}" clean
fi

rm -rf "${RUNTIME_DIR}"
