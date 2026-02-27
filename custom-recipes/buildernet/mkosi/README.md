# BuilderNet VM for Playground

Run a BuilderNet VM alongside builder-playground's L1 network. The playground manages the L1 stack (Reth, Lighthouse, mev-boost-relay, builder-hub) in Docker, while a BuilderNet VM runs in QEMU and proposes blocks to the network.

> **Important:** The VM image must be built with the `playground` mkosi profile. Production images are hardened and will not work with this setup.

## Prerequisites

- **Linux with KVM** (macOS is not supported)
- **Docker** (for the L1 network stack)
- **QEMU** with KVM support (`qemu-system-x86_64`)
- **UEFI firmware** (`edk2-ovmf` or equivalent)
- **socat** (for VM console access)
- **jq**, **curl**

Verify KVM is available:

```bash
ls /dev/kvm
```

## Quick Start

```bash
# 1. Install builder-playground
curl -sSfL https://raw.githubusercontent.com/flashbots/builder-playground/main/install.sh | bash

# 2. Create project directory and generate recipe files
mkdir buildernet-dev && cd buildernet-dev
builder-playground generate buildernet/mkosi

# 3. Download the VM image and prepare runtime
./scripts/prepare.sh https://example.com/buildernet-playground.qcow2

# 4. Start everything
builder-playground start playground.yaml --bind-external --detached

# 5. Send a test transaction through rbuilder
builder-playground test --rpc http://localhost:18645 --el-rpc http://localhost:8545
```

If the test transaction is included in a block, the full pipeline is working: transaction reaches rbuilder inside the VM, rbuilder builds a block, and it lands on the L1 chain.

## Using Your Own Image

For developers working on [flashbots-images](https://github.com/flashbots/flashbots-images) who want to test VM changes against a local network.

Build the image in your flashbots-images checkout using the **playground** mkosi profile.

Then pass the image path to prepare:

```bash
./scripts/prepare.sh /path/to/buildernet-qemu_latest.qcow2
```

See the [flashbots-images](https://github.com/flashbots/flashbots-images) repository for build environment setup, available profiles, and customization options.

## VM Management

### Lifecycle

```bash
./scripts/stop.sh       # Stop the VM (Docker L1 stack keeps running)
./scripts/prepare.sh <url-or-path>  # Reset VM to fresh state
./scripts/start.sh      # Start the VM
```

### Access

```bash
./scripts/console.sh    # Serial console (exit: Ctrl+])
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `QEMU_CPU` | `8` | Number of CPU cores |
| `QEMU_RAM` | `32G` | Memory allocation |
| `QEMU_ACCEL` | `kvm` | Acceleration (`kvm` or `tcg`) |

## Development Workflow

The Docker L1 stack stays running while you iterate on the VM image:

```bash
# 1. Stop the VM
./scripts/stop.sh

# 2. Rebuild the image (in your flashbots-images checkout)
#    IMPORTANT: use `playground` mkosi profile when building

# 3. Prepare and restart
./scripts/prepare.sh /path/to/buildernet-qemu_latest.qcow2
./scripts/start.sh

# 4. Verify
builder-playground test --rpc http://localhost:18645 --el-rpc http://localhost:8545
```

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

# Stop everything
builder-playground stop <session-name>

# Full cleanup
builder-playground clean <session-name>
./scripts/clean.sh
```

Use `builder-playground list` to find the session name.

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
