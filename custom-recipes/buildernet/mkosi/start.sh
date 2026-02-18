#!/usr/bin/env bash
# Start the BuilderNet VM
set -eu -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RUNTIME_DIR="${SCRIPT_DIR}/.runtime"
VM_IMAGE="${RUNTIME_DIR}/buildernet-vm.qcow2"
VM_DATA_DISK="${RUNTIME_DIR}/persistent.raw"
PIDFILE="${RUNTIME_DIR}/qemu.pid"
CONSOLE_LOG="${RUNTIME_DIR}/console.log"
CONSOLE_SOCK="${RUNTIME_DIR}/console.sock"

CPU=8
RAM=32G
SSH_PORT=2222
OPERATOR_API_PORT=13535
RBUILDER_RPC_PORT=18645
HAPROXY_HTTP_PORT=10080
HAPROXY_HTTPS_PORT=10443

if [[ ! -f "${VM_IMAGE}" ]]; then
    echo "Error: VM image not found. Run ./prepare.sh first."
    exit 1
fi

if [[ -f "${PIDFILE}" ]] && kill -0 $(cat "${PIDFILE}") 2>/dev/null; then
    echo "Error: VM already running (PID: $(cat ${PIDFILE}))"
    exit 1
fi

echo "Starting VM..."
echo "  SSH: localhost:${SSH_PORT}"
echo "  Operator API: localhost:${OPERATOR_API_PORT}"
echo "  rbuilder RPC: localhost:${RBUILDER_RPC_PORT}"
echo "  HAProxy HTTP: localhost:${HAPROXY_HTTP_PORT}"
echo "  HAProxy HTTPS: localhost:${HAPROXY_HTTPS_PORT}"
echo "  Console log: ${CONSOLE_LOG}"
echo "  Console socket: ${CONSOLE_SOCK}"

source "${SCRIPT_DIR}/ovmf.sh"

qemu-system-x86_64 \
  -daemonize \
  -pidfile "${PIDFILE}" \
  -serial file:"${CONSOLE_LOG}" \
  -name buildernet-playground \
  -drive if=pflash,format=raw,readonly=on,file="${OVMF_CODE}" \
  -drive if=pflash,format=raw,readonly=on,file="${OVMF_VARS}" \
  -drive format=qcow2,if=none,cache=none,id=osdisk,file="${VM_IMAGE}" \
  -device nvme,drive=osdisk,serial=nvme-os,bootindex=0 \
  -enable-kvm -cpu host -m "${RAM}" -smp "${CPU}" -display none \
  -device virtio-scsi-pci,id=scsi0 \
  -drive file="${VM_DATA_DISK}",format=raw,if=none,id=datadisk \
  -device nvme,id=nvme0,serial=nvme-data \
  -device nvme-ns,drive=datadisk,bus=nvme0,nsid=12 \
  -nic user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:${SSH_PORT}-:40192,hostfwd=tcp:127.0.0.1:${OPERATOR_API_PORT}-:3535,hostfwd=tcp:127.0.0.1:${RBUILDER_RPC_PORT}-:8645,hostfwd=tcp:127.0.0.1:${HAPROXY_HTTP_PORT}-:80,hostfwd=tcp:127.0.0.1:${HAPROXY_HTTPS_PORT}-:443 \
  -chardev socket,id=virtcon,path="${CONSOLE_SOCK}",server=on,wait=off \
  -device virtio-serial-pci \
  -device virtconsole,chardev=virtcon,name=org.qemu.console.0

echo "VM started (PID: $(cat ${PIDFILE}))"
echo "Use './stop.sh' to stop, './console.sh' to connect"
echo "Use 'tail -f ${CONSOLE_LOG}' to watch console output"


# TRIED TO DISABLE SERVICES - DID NOT WORK
# error:
#   qemu-system-x86_64: -append only allowed with -kernel option

# PLAYGROUND_DISABLE_SERVICES=(
#   reth-sync                    # Downloads Reth snapshot from S3 bucket
#   acme-le                      # Issues Let's Encrypt TLS certificates
#   acme-le-renewal              # Renews Let's Encrypt certificates
#   rbuilder-bidding-downloader  # Downloads binary from private GitHub repo
#   vector                       # Observability pipeline (logs/metrics)
#   rbuilder-rebalancer          # ETH balance rebalancing across wallets
#   operator-api                 # Management API for node operators
#   config-watchdog              # Watches and reloads rbuilder config
# )

# mask_args() {
#   [[ $# -gt 0 ]] && printf "systemd.mask=%s.service " "$@"
# }
# # # add argument to qemu-system-x86_64:
# # \
# #   -append "console=ttyS0 $(mask_args "${PLAYGROUND_DISABLE_SERVICES[@]}")"
