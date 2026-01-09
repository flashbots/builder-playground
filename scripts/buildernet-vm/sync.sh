#!/usr/bin/env bash
# Clone or update flashbots-images repository
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

FLASHBOTS_IMAGES_DIR="${SCRIPT_DIR}/.flashbots-images"
FLASHBOTS_IMAGES_BRANCH="fryd/mkosi-playground"
FLASHBOTS_IMAGES_REPO="https://github.com/flashbots/flashbots-images.git"

if [[ ! -d "${FLASHBOTS_IMAGES_DIR}" ]]; then
    git clone --branch "${FLASHBOTS_IMAGES_BRANCH}" "${FLASHBOTS_IMAGES_REPO}" "${FLASHBOTS_IMAGES_DIR}"
else
    git -C "${FLASHBOTS_IMAGES_DIR}" fetch origin
    git -C "${FLASHBOTS_IMAGES_DIR}" checkout "${FLASHBOTS_IMAGES_BRANCH}"
    git -C "${FLASHBOTS_IMAGES_DIR}" pull origin "${FLASHBOTS_IMAGES_BRANCH}"
fi
