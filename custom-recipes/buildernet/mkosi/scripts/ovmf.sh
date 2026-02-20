#!/usr/bin/env bash
# Discover OVMF firmware files using QEMU firmware descriptors.
# Usage:
#   source ovmf.sh
#   # Sets OVMF_CODE and OVMF_VARS variables
set -eu -o pipefail

_discover_ovmf() {
    local search_dirs=(
        /usr/share/qemu/firmware
        /etc/qemu/firmware
    )

    for dir in "${search_dirs[@]}"; do
        [[ -d "$dir" ]] || continue
        for f in "$dir"/*.json; do
            [[ -f "$f" ]] || continue
            # Match x86_64 UEFI, non-secure-boot, 4m variant
            if jq -e '
                (.targets[] | select(.architecture == "x86_64")) and
                (.["interface-types"] | index("uefi")) and
                ((.features | index("secure-boot")) | not)
            ' "$f" >/dev/null 2>&1; then
                OVMF_CODE=$(jq -r '.mapping.executable.filename' "$f")
                OVMF_VARS=$(jq -r '.mapping."nvram-template".filename' "$f")
                if [[ -f "$OVMF_CODE" && -f "$OVMF_VARS" ]]; then
                    return 0
                fi
            fi
        done
    done

    # Fallback: check common hardcoded paths
    local known_paths=(
        "/usr/share/OVMF/x64"
        "/usr/share/edk2/x64"
        "/usr/share/OVMF"
        "/usr/share/edk2/ovmf"
        "/usr/share/edk2-ovmf/x64"
    )
    for dir in "${known_paths[@]}"; do
        if [[ -f "$dir/OVMF_CODE.4m.fd" && -f "$dir/OVMF_VARS.4m.fd" ]]; then
            OVMF_CODE="$dir/OVMF_CODE.4m.fd"
            OVMF_VARS="$dir/OVMF_VARS.4m.fd"
            return 0
        fi
    done

    echo "Error: Could not find OVMF firmware files." >&2
    echo "Install the edk2-ovmf package (or equivalent for your distro)." >&2
    return 1
}

_discover_ovmf
export OVMF_CODE OVMF_VARS
echo "OVMF_CODE=${OVMF_CODE}"
echo "OVMF_VARS=${OVMF_VARS}"
