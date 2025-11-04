# In-Memory MEV-Boost Relay

An embedded, lightweight MEV-boost relay implementation designed for local testing and development. This is a wrapper around the [Flashbots MEV-Boost Relay](https://github.com/flashbots/mev-boost-relay) that replaces production dependencies with in-memory alternatives.

## Purpose

This package provides a **zero-configuration, self-contained MEV-boost relay** for use in the Builder Playground. It eliminates the need for external PostgreSQL and Redis instances by using in-memory implementations, making it ideal for:

- Local block builder development and testing
- Ephemeral test environments
- CI/CD pipelines
- Quick prototyping without infrastructure setup

## Key Differences from Production MEV-Boost Relay

| Feature | Production Relay | In-Memory Relay |
|---------|-----------------|-----------------|
| **Database** | PostgreSQL | In-memory map (not persistent) |
| **Redis** | External Redis server | miniredis (embedded) |
| **Block Validation** | External validation service | Mock service (always validates) |
| **Configuration** | Environment variables, config files | Simple struct-based config |
| **Persistence** | Data survives restarts | Data lost on restart |
| **Scalability** | Production-ready, multi-instance | Single instance, testing only |

## Core Components

### 1. In-Memory Database (`inmemoryDB`)

Extends `database.MockDB` with actual storage for:

**Validator Registry:**
- `SaveValidatorRegistration()` - Store validator registration entries
- `GetValidatorRegistration(pubkey)` - Retrieve registration by pubkey
- `GetLatestValidatorRegistrations()` - Get all registrations
- `NumRegisteredValidators()` - Count registered validators

**Delivered Payloads:**
- `SaveDeliveredPayload()` - Store payload delivery records
- `GetRecentDeliveredPayloads(filters)` - Query delivered payloads
- `GetNumDeliveredPayloads()` - Count delivered payloads

All data is stored in Go maps with mutex protection, lost on restart.

### 2. Embedded Redis (miniredis)

Uses [miniredis](https://github.com/alicebob/miniredis) for:
- Bid caching
- Builder state tracking
- Temporary storage for relay operations

Runs entirely in-memory, no disk I/O or external process required.

### 3. Mock Block Validation Service

A simple HTTP server that:
- Accepts all block validation requests
- Returns success for all blocks
- Useful for testing builder logic without execution validation

Can be replaced with a real validation service via `ValidationServerAddr` config.

### 4. API Service

Full MEV-boost relay API implementation (from upstream):
- **Builder API** - Submit blocks, register builders
- **Proposer API** - Get headers, unblind payloads
- **Data API** - Query delivered payloads, validator registrations

Automatically enabled with all endpoints.

### 5. Housekeeper Service

Background service that:
- Refreshes known validators from beacon chain
- Updates proposer duties for upcoming slots
- Maintains validator registry state

Triggered automatically at startup with forced registration.

## Configuration

### Default Configuration

```go
config := mevboostrelay.DefaultConfig()
// Returns:
// {
//     ApiListenAddr: "127.0.0.1",
//     ApiListenPort: 5555,
//     ApiSecretKey: "0x5eae315483f028b5cdd5d1090ff0c7618b18737ea9bf3c35047189db22835c48",
//     BeaconClientAddr: "http://localhost:3500",
//     LogOutput: os.Stdout,
//     ValidationServerAddr: "",
// }
```

### Configuration Options

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| `ApiListenAddr` | `string` | Relay API listen address | `127.0.0.1` |
| `ApiListenPort` | `uint64` | Relay API listen port | `5555` |
| `ApiSecretKey` | `string` | BLS secret key for signing relay responses | Default key (see code) |
| `BeaconClientAddr` | `string` | Beacon node HTTP endpoint | `http://localhost:3500` |
| `LogOutput` | `io.Writer` | Log output destination | `os.Stdout` |
| `ValidationServerAddr` | `string` | External block validation service (optional) | `""` (use mock) |

## Usage

### Programmatic Usage

```go
import mevboostrelay "github.com/flashbots/builder-playground/mev-boost-relay"

// Create relay with default config
config := mevboostrelay.DefaultConfig()
config.BeaconClientAddr = "http://localhost:3500"
config.ApiListenPort = 5555

relay, err := mevboostrelay.New(config)
if err != nil {
    log.Fatal(err)
}

// Start relay (blocks until error)
if err := relay.Start(); err != nil {
    log.Fatal(err)
}
```

### CLI Usage

```bash
# Build the binary
cd mev-boost-relay/cmd
go build -o local-mev-boost-relay

# Run with defaults
./local-mev-boost-relay

# Custom configuration
./local-mev-boost-relay \
  --api-listen-addr 0.0.0.0 \
  --api-listen-port 5555 \
  --beacon-client-addr http://localhost:3500 \
  --validation-server-addr http://localhost:8545
```

### Docker Usage (via Builder Playground)

```go
// In playground recipes
service := manifest.AddService("mev-boost-relay", &MevBoostRelay{
    BeaconClient: "beacon",
    ValidationServer: "", // Use mock
})
```

The relay is automatically included in L1 and OpStack recipes.

## API Endpoints

### Builder API

- `POST /relay/v1/builder/validators` - Register validators
- `GET /relay/v1/builder/validators` - Get known validators
- `POST /relay/v1/builder/blocks` - Submit block

### Proposer API

- `GET /eth/v1/builder/status` - Relay status
- `POST /eth/v1/builder/blinded_blocks` - Get execution payload

### Data API

- `GET /relay/v1/data/bidtraces/proposer_payload_delivered` - Delivered payloads
- `GET /relay/v1/data/validator_registration` - Validator registrations

Full API documentation: [MEV-Boost Relay Spec](https://flashbots.github.io/relay-specs/)

## Startup Behavior

### Initialization Sequence

1. **Beacon Client Sync** - Waits up to 10 seconds for beacon node to sync
2. **Network Detection** - Fetches spec and genesis to determine network (fork versions)
3. **Feature Flags** - Enables `ENABLE_BUILDER_CANCELLATIONS`
4. **Redis Start** - Launches miniredis on random port
5. **Database Setup** - Creates in-memory database
6. **Validator Refresh** - Loads initial validator set from beacon node
7. **Block Validation** - Starts mock service or connects to external
8. **Services Start** - Launches API and housekeeper in parallel
9. **Forced Registration** - Triggers initial proposer duty update

### Health Checks

The relay is considered healthy when:
- Beacon client is synced
- API server is listening
- Housekeeper is running
- At least one validator is known

## Feature Flags

The relay automatically enables:

- `ENABLE_BUILDER_CANCELLATIONS=1` - Allow builders to cancel submitted bids

Additional flags can be set via environment variables before calling `New()`.

## Network Support

Supports all Ethereum consensus forks:
- **Bellatrix** (Merge)
- **Capella** (Shanghai)
- **Deneb** (Cancun)
- **Electra** (Prague)

Fork versions are auto-detected from the beacon node's spec and genesis.

## Limitations

### Not Suitable For Production

⚠️ **This relay is for testing only.** Do not use in production because:

1. **No persistence** - All data lost on restart
2. **No redundancy** - Single point of failure
3. **Mock validation** - Blocks not actually validated (unless external service provided)
4. **No authentication** - No builder registration checks
5. **No rate limiting** - Vulnerable to abuse
6. **No metrics** - Limited observability
7. **Single instance** - Cannot scale horizontally

### Memory Constraints

All data is stored in RAM:
- Each validator registration: ~1 KB
- Each delivered payload: ~5 KB
- 10,000 validators ≈ 10 MB memory
- 1,000 payloads ≈ 5 MB memory

Monitor memory usage for long-running tests.

### Beacon Client Dependency

The relay **requires** a synced beacon node:
- Must respond to REST API requests
- Must have genesis and spec data
- Must provide validator duties

Startup will fail if beacon node is unreachable.

## Troubleshooting

### Relay Won't Start

**Symptom:** `beacon client failed to start` error

**Solutions:**
1. Verify beacon node is running: `curl http://localhost:3500/eth/v1/node/version`
2. Check beacon node is synced: `curl http://localhost:3500/eth/v1/node/syncing`
3. Increase timeout in code if beacon node is slow to sync

### No Validators Registered

**Symptom:** Builders cannot submit blocks, no validators shown

**Solutions:**
1. Wait for housekeeper to refresh (automatic after startup)
2. Check beacon node has active validators
3. Verify genesis delay allows validator set to be populated

### Validation Always Succeeds

**Expected behavior** - The mock validation service accepts all blocks.

To enable real validation:
```go
config.ValidationServerAddr = "http://my-validation-service:8545"
```

### Memory Leak

**Symptom:** Memory usage grows unbounded

**Solutions:**
1. The in-memory DB never prunes old data
2. For long-running tests, restart periodically
3. Or implement custom pruning logic in `inmemoryDB`

## Development

### Building

```bash
cd mev-boost-relay
go build -o relay .
```

### Testing

```bash
go test ./...
```

### Dependencies

- `github.com/flashbots/mev-boost-relay` - Upstream relay implementation
- `github.com/alicebob/miniredis` - Embedded Redis
- `github.com/flashbots/go-boost-utils` - BLS cryptography and types

## Integration with Builder Playground

The relay is used by:

### L1 Recipe
```go
manifest.AddService("mev-boost-relay", &MevBoostRelay{
    BeaconClient: "beacon",
})
```

Provides MEV-boost infrastructure for local builder testing.

### Component Definition
See [playground/components.go](../playground/components.go:952-972) for:
- Docker image: `flashbots/playground-utils:latest`
- Port: 5555 (HTTP)
- Entrypoint: `mev-boost-relay`

### Watchdog Integration
Implements `ServiceWatchdog` for monitoring:
- Validator registration count
- Known validator count
- Delivered payload count

## License

MIT License - Copyright (c) 2025 Flashbots

## Related Projects

- [MEV-Boost Relay](https://github.com/flashbots/mev-boost-relay) - Production relay
- [MEV-Boost](https://github.com/flashbots/mev-boost) - Proposer-side software
- [Builder Playground](https://github.com/flashbots/builder-playground) - Local testing framework
- [Rbuilder](https://github.com/flashbots/rbuilder) - Rust block builder

## Support

For issues specific to this in-memory relay:
- File issues at: https://github.com/flashbots/builder-playground/issues
- Tag with: `mev-boost-relay`

For MEV-Boost protocol questions:
- See: https://boost.flashbots.net/
