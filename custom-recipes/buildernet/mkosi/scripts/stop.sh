#!/usr/bin/env bash
# Stop the BuilderNet VM
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}/.."
PIDFILE="${PROJECT_DIR}/.runtime/qemu.pid"

if [[ ! -f "${PIDFILE}" ]]; then
    echo "No pidfile found"
    exit 0
fi

PID=$(cat "${PIDFILE}")
if kill -0 "${PID}" 2>/dev/null; then
    kill "${PID}"
    rm "${PIDFILE}"
    echo "VM stopped (PID: ${PID})"
else
    rm "${PIDFILE}"
    echo "Stale pidfile removed (process not running)"
fi