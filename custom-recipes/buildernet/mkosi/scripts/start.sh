#!/usr/bin/env bash
# Start the BuilderNet VM
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}/.."

RUNTIME_DIR="${PROJECT_DIR}/.runtime"
VM_IMAGE="${RUNTIME_DIR}/buildernet-vm.qcow2"
VM_DATA_DISK="${RUNTIME_DIR}/persistent.raw"
PIDFILE="${RUNTIME_DIR}/qemu.pid"
CONSOLE_LOG="${RUNTIME_DIR}/console.log"
CONSOLE_SOCK="${RUNTIME_DIR}/console.sock"

CPU="${QEMU_CPU:-8}"
RAM="${QEMU_RAM:-32G}"
SSH_PORT=2222
OPERATOR_API_PORT=13535
RBUILDER_RPC_PORT=18645
HAPROXY_HTTP_PORT=10080
HAPROXY_HTTPS_PORT=10443

if [[ ! -f "${VM_IMAGE}" ]]; then
    echo "Error: VM image not found. Run ./scripts/prepare.sh first."
    exit 1
fi

if [[ -f "${PIDFILE}" ]] && kill -0 $(cat "${PIDFILE}") 2>/dev/null; then
    echo "Error: VM already running (PID: $(cat ${PIDFILE}))"
    exit 1
fi

# Determine acceleration mode
ACCEL="${QEMU_ACCEL:-kvm}"
if [[ "${ACCEL}" == "kvm" ]]; then
    if [[ ! -e /dev/kvm ]]; then
        echo "Error: KVM is not available (/dev/kvm not found)."
        echo "Options:"
        echo "  - Enable KVM on this host (load kvm kernel module)"
        echo "  - Use software emulation: QEMU_ACCEL=tcg ./scripts/start.sh"
        echo "    (TCG is ~10-20x slower but works anywhere)"
        exit 1
    fi
    QEMU_ACCEL_ARGS="-enable-kvm -cpu host"
elif [[ "${ACCEL}" == "tcg" ]]; then
    QEMU_ACCEL_ARGS="-accel tcg -cpu max"
else
    echo "Error: Unknown QEMU_ACCEL value: ${ACCEL} (expected 'kvm' or 'tcg')"
    exit 1
fi

echo "Starting VM..."
echo "  Accel: ${ACCEL}"
echo "  CPU: ${CPU} cores, RAM: ${RAM}"
echo "  SSH: localhost:${SSH_PORT}"
echo "  Operator API: localhost:${OPERATOR_API_PORT}"
echo "  rbuilder RPC: localhost:${RBUILDER_RPC_PORT}"
echo "  HAProxy HTTP: localhost:${HAPROXY_HTTP_PORT}"
echo "  HAProxy HTTPS: localhost:${HAPROXY_HTTPS_PORT}"
echo "  Console log: ${CONSOLE_LOG}"
echo "  Console socket: ${CONSOLE_SOCK}"

source "${SCRIPT_DIR}/ovmf.sh"

echo "start.sh: launching qemu-system-x86_64..."
echo "start.sh: QEMU_ACCEL_ARGS=${QEMU_ACCEL_ARGS}"
echo "start.sh: VM_IMAGE=${VM_IMAGE} ($(du -h "${VM_IMAGE}" | cut -f1))"
echo "start.sh: VM_DATA_DISK=${VM_DATA_DISK}"

# exec replaces this shell with QEMU so the playground can track and kill
# the process directly. QEMU runs in the foreground (no -daemonize).
exec qemu-system-x86_64 \
  -pidfile "${PIDFILE}" \
  -serial file:"${CONSOLE_LOG}" \
  -name buildernet-playground \
  -drive if=pflash,format=raw,readonly=on,file="${OVMF_CODE}" \
  -drive if=pflash,format=raw,readonly=on,file="${OVMF_VARS}" \
  -drive format=qcow2,if=none,cache=none,id=osdisk,file="${VM_IMAGE}" \
  -device nvme,drive=osdisk,serial=nvme-os,bootindex=0 \
  ${QEMU_ACCEL_ARGS} -m "${RAM}" -smp "${CPU}" -display none \
  -device virtio-scsi-pci,id=scsi0 \
  -drive file="${VM_DATA_DISK}",format=raw,if=none,id=datadisk \
  -device nvme,id=nvme0,serial=nvme-data \
  -device nvme-ns,drive=datadisk,bus=nvme0,nsid=12 \
  -nic user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:${SSH_PORT}-:40192,hostfwd=tcp:127.0.0.1:${OPERATOR_API_PORT}-:3535,hostfwd=tcp:127.0.0.1:${RBUILDER_RPC_PORT}-:8645,hostfwd=tcp:127.0.0.1:${HAPROXY_HTTP_PORT}-:80,hostfwd=tcp:127.0.0.1:${HAPROXY_HTTPS_PORT}-:443 \
  -chardev socket,id=virtcon,path="${CONSOLE_SOCK}",server=on,wait=off \
  -device virtio-serial-pci \
  -device virtconsole,chardev=virtcon,name=org.qemu.console.0
