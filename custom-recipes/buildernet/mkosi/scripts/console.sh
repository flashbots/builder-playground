#!/usr/bin/env bash
# Connect to the BuilderNet VM console (auto-login with devtools profile)
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}/.."
CONSOLE_SOCK="${PROJECT_DIR}/.runtime/console.sock"

if [[ ! -S "${CONSOLE_SOCK}" ]]; then
    echo "Error: Console socket not found. Is the VM running?"
    echo "Run ./scripts/start.sh first."
    exit 1
fi

echo "Connecting to VM console... (Ctrl+] to exit)"
socat -,raw,echo=0,escape=0x1d UNIX-CONNECT:"${CONSOLE_SOCK}"

echo ""
echo "Disconnected from VM console."
