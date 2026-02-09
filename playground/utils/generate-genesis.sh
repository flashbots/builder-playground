#!/bin/bash
# Generate genesis.json and rollup.json for OpStack using op-deployer.
# Usage: ./generate-genesis.sh [isthmus|jovian|all]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
L2_CHAIN_ID="${L2_CHAIN_ID:-13}"

# op-deployer versions per fork
declare -A VERSIONS=(
    [isthmus]="${OP_DEPLOYER_VERSION:-${OP_DEPLOYER_VERSION_ISTHMUS:-v0.4.7}}"
    [jovian]="${OP_DEPLOYER_VERSION:-${OP_DEPLOYER_VERSION_JOVIAN:-v0.5.2}}"
)

detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"
    [[ "$arch" == "x86_64" ]] && arch="amd64"
    [[ "$arch" == "aarch64" ]] && arch="arm64"
    echo "${os}_${arch}"
}

get_op_deployer() {
    local version="$1" platform="$2"
    local version_no_v="${version#v}"
    local binary="${SCRIPT_DIR}/op-deployer-${version_no_v}"

    if [[ -x "$binary" ]]; then
        echo "$binary"
        return
    fi

    local url="https://github.com/ethereum-optimism/optimism/releases/download/op-deployer/v${version_no_v}/op-deployer-${version_no_v}-${platform//_/-}.tar.gz"
    echo "Downloading op-deployer ${version}..." >&2

    local tmp_dir
    tmp_dir="$(mktemp -d)"
    curl -fsSL "$url" | tar -xz -C "$tmp_dir"
    mv "$(find "$tmp_dir" -name op-deployer -type f)" "$binary"
    rm -rf "$tmp_dir"
    chmod +x "$binary"
    echo "$binary"
}

generate_genesis() {
    local fork="$1" platform="$2"
    local version="${VERSIONS[$fork]}"
    local op_deployer
    op_deployer="$(get_op_deployer "$version" "$platform")"

    echo "=== Generating ${fork} genesis (op-deployer ${version}) ==="

    echo '{"version": 1}' > "${SCRIPT_DIR}/state.json"

    "$op_deployer" apply --workdir "$SCRIPT_DIR" --deployment-target genesis
    "$op_deployer" inspect genesis --workdir "$SCRIPT_DIR" --outfile "${SCRIPT_DIR}/genesis-${fork}.json" "$L2_CHAIN_ID"
    "$op_deployer" inspect rollup --workdir "$SCRIPT_DIR" --outfile "${SCRIPT_DIR}/rollup-${fork}.json" "$L2_CHAIN_ID"
    cp "${SCRIPT_DIR}/state.json" "${SCRIPT_DIR}/state-${fork}.json"

    echo "Enabled forks:"
    grep -oE '"[a-zA-Z]+Time": [0-9]+' "${SCRIPT_DIR}/genesis-${fork}.json" | sed 's/"//g; s/Time://' | column -t
}

main() {
    local fork="${1:-all}"
    local platform
    platform="$(detect_platform)"

    cd "$SCRIPT_DIR"

    case "$fork" in
        isthmus|jovian) generate_genesis "$fork" "$platform" ;;
        all) generate_genesis "isthmus" "$platform"; generate_genesis "jovian" "$platform" ;;
        *) echo "Usage: $0 [isthmus|jovian|all]" >&2; exit 1 ;;
    esac
}

main "$@"
