#!/usr/bin/env bash
# Clone flashbots-images (if needed) and build the BuilderNet VM image
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}/.."

FLASHBOTS_IMAGES_DIR="${PROJECT_DIR}/.flashbots-images"
FLASHBOTS_IMAGES_REPO="https://github.com/flashbots/flashbots-images.git"
FLASHBOTS_IMAGES_BRANCH="${FLASHBOTS_IMAGES_BRANCH:-fryd/mkosi-playground}"

if [[ ! -d "${FLASHBOTS_IMAGES_DIR}" ]]; then
    echo "Cloning flashbots-images (branch: ${FLASHBOTS_IMAGES_BRANCH})..."
    git clone --branch "${FLASHBOTS_IMAGES_BRANCH}" "${FLASHBOTS_IMAGES_REPO}" "${FLASHBOTS_IMAGES_DIR}"
fi

# Setup mkosi if needed
MKOSI_COMMIT=$(cat "${FLASHBOTS_IMAGES_DIR}/.mkosi_version")
VENV="${FLASHBOTS_IMAGES_DIR}/.venv"
MKOSI="${VENV}/bin/mkosi"

if [[ ! -x "${MKOSI}" ]]; then
    echo "Setting up mkosi (commit: ${MKOSI_COMMIT})..."
    python3 -m venv "${VENV}"
    "${VENV}/bin/pip" install -q --upgrade pip
    "${VENV}/bin/pip" install -q "git+https://github.com/systemd/mkosi.git@${MKOSI_COMMIT}"
    echo "Installed: $(${MKOSI} --version)"
fi

echo "Building playground image..."
cd "${FLASHBOTS_IMAGES_DIR}"
${MKOSI} --force -I buildernet.conf --profile="devtools,playground"
