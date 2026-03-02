# BuilderNet VM for Playground

Run a BuilderNet VM alongside builder-playground's L1 network. The playground manages the L1 stack (Reth, Lighthouse, mev-boost-relay, builder-hub) in Docker, while a BuilderNet VM runs in QEMU and proposes blocks to the network.

> **Important:** The VM image must be built with the `playground` mkosi profile. Production images are hardened and will not work with this setup.

## Prerequisites

- **Linux with KVM** (macOS is not supported)
- **Docker** (for the L1 network stack)
- **QEMU** with KVM support (`qemu-system-x86_64`, `qemu-utils`)
- **UEFI firmware** (`edk2-ovmf` or equivalent)
- **socat** (for VM console access)
- **jq**, **curl**, **unzip**

Verify KVM is available:

```bash
ls /dev/kvm
```

## Quick Start

```bash
# 1. Install builder-playground
curl -sSfL https://raw.githubusercontent.com/flashbots/builder-playground/main/install.sh | bash

# 2. Create project dir and generate recipe files
mkdir buildernet-dev && cd buildernet-dev
builder-playground generate buildernet/mkosi

# 3. Point to the VM image
#    - you can point to a local file or URL
#    - alternatively you can set this in playground.yaml (see below)
export BUILDERNET_IMAGE=/path/to/buildernet-qemu.qcow2

# 4. Start (runs L1 Docker stack + VM in the background)
builder-playground start playground.yaml --bind-external --detached

# 5. Wait for the VM to boot (~60-90s) then check readiness
./scripts/operator-api-health.sh    # repeat until you see "OK"

# 6. Verify by sending a transaction
builder-playground test --rpc http://localhost:18645 --el-rpc http://localhost:8545
```

If the test transaction is included in a block, the full pipeline is working: transaction reaches rbuilder inside the VM, rbuilder builds a block, and it lands on the L1 chain.

You can also set `BUILDERNET_IMAGE` in `playground.yaml` instead of using an environment variable:

```yaml
# In playground.yaml, under builder > env:
env:
  BUILDERNET_IMAGE: "/path/to/buildernet-qemu.qcow2"
```

## Building Your Own Image

For developers working on [flashbots-images](https://github.com/flashbots/flashbots-images) who want to test VM changes against a local network.

Build the image in your flashbots-images checkout using the **playground** mkosi profile, then point `BUILDERNET_IMAGE` to the output:

```bash
export BUILDERNET_IMAGE=/path/to/flashbots-images/mkosi.output/buildernet-qemu_latest.qcow2
```

See the [flashbots-images](https://github.com/flashbots/flashbots-images) repository for build environment setup, available profiles, and customization options. You can also look at [`./scripts/build.sh`](scripts/build.sh) for a reference on how to clone and build the image.

## VM Management

### Lifecycle

```bash
./scripts/stop.sh                        # Stop the VM (Docker L1 stack keeps running)
./scripts/prepare.sh <path-or-url>       # Reset VM to fresh state
./scripts/start.sh                       # Start the VM
```

### Access

```bash
./scripts/console.sh    # Serial console (exit: Ctrl+])
```

### Logs

VM serial output (kernel, systemd, service logs) is captured to `.runtime/console.log`:

```bash
tail -f .runtime/console.log
```

For operator-api event logs (rbuilder lifecycle, config fetches, etc.):

```bash
./scripts/operator-api-logs.sh
```

For Docker service logs (Reth, Lighthouse, mev-boost-relay, etc.), use `builder-playground logs`.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `BUILDERNET_IMAGE` | *(set in playground.yaml)* | Path or URL to the VM qcow2 image |
| `QEMU_CPU` | `8` | Number of CPU cores |
| `QEMU_RAM` | `32G` | Memory allocation |
| `QEMU_ACCEL` | `kvm` | Acceleration (`kvm` or `tcg`) |

Environment variables override values defined in `playground.yaml`.

## Operator API

The operator-api runs inside the VM and is exposed on port 13535:

```bash
./scripts/operator-api-health.sh            # Health check
./scripts/operator-api-logs.sh              # Event logs
./scripts/operator-api-action.sh <action>   # Execute an action
```

Available actions: `reboot`, `rbuilder_restart`, `rbuilder_stop`, `fetch_config`, `rbuilder_bidding_restart`, `ssh_stop`, `ssh_start`, `haproxy_restart`.

## Stopping

```bash
# Stop just the VM (Docker keeps running)
./scripts/stop.sh

# Stop everything (use `builder-playground list` to find the session name)
builder-playground stop <session-name>

# Full cleanup
builder-playground clean <session-name>
./scripts/clean.sh
```

## Ports

### VM (QEMU host forwarding)

| Host Port | VM Port | Service |
|-----------|---------|---------|
| 2222 | 40192 | SSH |
| 13535 | 3535 | Operator API |
| 18645 | 8645 | rbuilder JSON-RPC |
| 10080 | 80 | HAProxy HTTP |
| 10443 | 443 | HAProxy HTTPS |

### Playground (Docker)

Use `builder-playground port <service> <port-name>` to look up host ports. Common services: `el` (Reth), `beacon` (Lighthouse), `mev-boost-relay`.

## File Structure

After `builder-playground generate buildernet/mkosi`:

```
buildernet-dev/
├── playground.yaml           # Recipe (L1 stack + VM lifecycle hooks)
├── config/                   # configs used by the BuilderNet setup
└── scripts/
    ├── prepare.sh            # Download/copy image + create data disk
    ├── start.sh              # Start the QEMU VM
    ├── stop.sh               # Stop the QEMU VM
    ├── console.sh            # Serial console
    ├── clean.sh              # Remove runtime files
    └── operator-api-*.sh     # Operator API helpers
```

## Troubleshooting

**"KVM not available"** — Ensure `/dev/kvm` exists. You may need: `sudo usermod -aG kvm $USER`

**"OVMF not found"** — Install UEFI firmware: `sudo apt install ovmf` (Debian/Ubuntu) or `sudo dnf install edk2-ovmf` (Fedora)

**VM boots but doesn't connect** — Ensure playground was started with `--bind-external` so the VM can reach Docker services via `10.0.2.2` (QEMU user-mode networking gateway).

**Console shows login prompt** — The image was not built with the `playground` mkosi profile. Rebuild with the profile enabled.
