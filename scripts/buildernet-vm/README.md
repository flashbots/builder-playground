# BuilderNet VM Scripts

Temporary bash scripts for running BuilderNet VM alongside builder-playground.
Feel free to translate them to Go and integrate into the CLI.

## Information:

Changes are made in two repos:
- [flashbots-images](https://github.com/flashbots/flashbots-images/tree/fryd/mkosi-playground) on `fryd/mkosi-playground` branch,
- [builder-playground](https://github.com/flashbots/builder-playground/tree/fryd/mkosi-playground) on `fryd/mkosi-playground` branch,

There are no plans of using [buildernet-playground](https://github.com/flashbots/buildernet-playground) repo.

## First Time Setup

```bash
./sync.sh      # Clone / fetch flashbots-images repo
./build.sh     # Build VM image with mkosi
./prepare.sh   # Extract image + create data disk
```

`sync.sh` clones/updates the `fryd/mkosi-playground` branch of flashbots-images.

## Run VM

```bash
./start.sh     # Start VM (background)
./ssh.sh       # SSH into running VM (requires SSH key setup)
./stop.sh      # Stop VM
```

## Builder Hub

```bash
./builderhub-configure.sh   # Register VM with builder-hub and update config for the VM
./builderhub-get-config.sh  # Get configuration for the VM
```

## Operator API

Scripts to interact with the operator-api service running inside the VM.

> **Note:** Actions and File Uploads could potentially be used for various things, like injecting genesis config instead of BuilderHub - still exploring this functionality.

```bash
./operator-api-health.sh              # Check if operator-api is healthy
./operator-api-logs.sh                # Get event logs
./operator-api-action.sh <action>     # Execute an action
```

Available actions:
- `reboot` - Reboot the system
- `rbuilder_restart` - Restart rbuilder-operator service
- `rbuilder_stop` - Stop rbuilder-operator service
- `fetch_config` - Fetch config from BuilderHub
- `rbuilder_bidding_restart` - Restart rbuilder-bidding service
- `ssh_stop` - Stop SSH service
- `ssh_start` - Start SSH service
- `haproxy_restart` - Restart HAProxy service

### File Uploads

Upload files to predefined paths. Only whitelisted names from `[file_uploads]` config are allowed:

```toml
[file_uploads]
rbuilder_blocklist = "/var/lib/persistent/rbuilder-operator/rbuilder.blocklist.json"
```

```bash
# Stores local blocklist.json content to the configured remote path
curl -k --data-binary "@blocklist.json" https://localhost:13535/api/v1/file-upload/rbuilder_blocklist
```

### Customization

To add more actions or file uploads, modify the config template:
https://github.com/flashbots/flashbots-images/blob/fryd/mkosi-playground/mkosi.profiles/playground/mkosi.extra/usr/lib/mustache-templates/etc/operator-api/config.toml.mustache

## Maintenance

```bash
./sync.sh      # Update flashbots-images to latest
./clean.sh     # Clean build artifacts + runtime files
```

## Ports

| Port | Service |
|------|---------|
| 2222 | SSH (maps to VM:40192) |
| 13535 | Operator API (maps to VM:3535) |
