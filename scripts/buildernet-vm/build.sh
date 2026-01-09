#!/usr/bin/env bash
# Build the BuilderNet VM image
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FLASHBOTS_IMAGES_DIR="${SCRIPT_DIR}/.flashbots-images"

if [[ ! -d "${FLASHBOTS_IMAGES_DIR}" ]]; then
    echo "Error: flashbots-images not found. Run ./sync.sh first."
    exit 1
fi

make -C "${FLASHBOTS_IMAGES_DIR}" build-playground
