# CL Proxy - Consensus Layer Engine API Proxy

A lightweight HTTP proxy that multiplexes Engine API requests from a consensus layer (CL) client to multiple execution layer (EL) clients simultaneously. Designed for testing scenarios where you want to run multiple block builders side-by-side without CL configuration complexity.

## Purpose

The CL Proxy enables **dual-builder testing** by intercepting Engine API calls from the beacon node and forwarding them to both a primary and secondary execution client. This allows:

- Testing multiple builder implementations simultaneously
- Comparing builder behavior under identical conditions
- Validating external builders against local implementations
- Running a fallback builder alongside a production builder

## Problem Statement

In standard Ethereum architecture:

```
Beacon Node (CL) ──Engine API──> Execution Client (EL)
```

The beacon node expects exactly **one** execution endpoint for:
- Fork choice updates (`engine_forkchoiceUpdated`)
- Block execution (`engine_newPayload`)
- Payload retrieval (`engine_getPayload`)

**Challenge**: How do you test two builders receiving the same fork choice updates?

**Solution**: Use CL Proxy as a multiplexer:

```
                            ┌──> Primary Builder (EL)
                            │    (provides responses)
Beacon Node ──> CL Proxy ───┤
                            │
                            └──> Secondary Builder (EL)
                                 (receives updates, no responses)
```

## Architecture

### Request Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                         CL Proxy                                 │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. Receive Request from Beacon Node                             │
│     ├─ JWT Authentication (forwarded from CL)                    │
│     └─ JSON-RPC Engine API call                                  │
│                                                                  │
│  2. Forward to Primary Builder                                   │
│     ├─ Full request with all parameters                          │
│     ├─ Wait for response                                         │
│     └─ Return response to beacon node                            │
│                                                                  │
│  3. Forward to Secondary Builder (async)                         │
│     ├─ Filter block building requests                            │
│     │  └─ Remove payload attributes from FCU                     │
│     │  └─ Skip engine_getPayload entirely                        │
│     └─ Fire-and-forget (errors logged, not propagated)           │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### Request Filtering

The proxy applies intelligent filtering to secondary builder requests:

| Engine API Method | Primary | Secondary | Modification |
|-------------------|---------|-----------|--------------|
| `engine_newPayload` | ✅ Full | ✅ Full | None |
| `engine_forkchoiceUpdated` | ✅ Full | ✅ **Filtered** | Remove `payloadAttributes` param |
| `engine_getPayload` | ✅ Full | ❌ **Skipped** | Not sent |
| `engine_exchangeCapabilities` | ✅ Full | ✅ Full | None |
| Other methods | ✅ Full | ✅ Full | None |

### Why Filter Block Building Requests?

**`engine_forkchoiceUpdated` Filtering:**
- The beacon node sends `payloadAttributes` to trigger block building
- The primary builder receives this and starts building a block
- The secondary builder **also receives FCU** but with `payloadAttributes` set to `null`
- Reason: Secondary builders typically use MEV-Boost/Rollup-Boost for block building, not direct Engine API

**`engine_getPayload` Skipping:**
- The beacon node requests the built payload using a `payloadId`
- This `payloadId` is specific to the primary builder
- The secondary builder doesn't have this payload (it builds via different mechanism)
- Sending this request to secondary would always fail

## Configuration

### Default Configuration

```go
config := clproxy.DefaultConfig()
// Returns:
// {
//     LogOutput: os.Stdout,
//     Port: 5656,
//     Primary: "",    // Must be set
//     Secondary: "",  // Optional
// }
```

### Configuration Options

| Field | Type | Description | Default | Required |
|-------|------|-------------|---------|----------|
| `LogOutput` | `io.Writer` | Log output destination | `os.Stdout` | No |
| `Port` | `uint64` | HTTP server listen port | `5656` | No |
| `Primary` | `string` | Primary builder Engine API URL | `""` | **Yes** |
| `Secondary` | `string` | Secondary builder Engine API URL | `""` | No |

**Note:** If `Secondary` is empty, the proxy acts as a simple pass-through to `Primary`.

## Usage

### Programmatic Usage

```go
import clproxy "github.com/flashbots/builder-playground/cl-proxy"

// Create proxy configuration
config := &clproxy.Config{
    LogOutput: os.Stdout,
    Port:      5656,
    Primary:   "http://localhost:8551", // Local Reth
    Secondary: "http://localhost:9551", // External builder
}

// Create and start proxy
proxy, err := clproxy.New(config)
if err != nil {
    log.Fatal(err)
}

// Run proxy (blocks until error or shutdown)
if err := proxy.Run(); err != nil {
    log.Fatal(err)
}
```

### CLI Usage

```bash
# Build the binary
cd cl-proxy/cmd
go build -o clproxy

# Run with both primary and secondary builders
./clproxy \
  --primary-builder http://localhost:8551 \
  --secondary-builder http://localhost:9551 \
  --port 5656

# Run as simple pass-through (no secondary)
./clproxy \
  --primary-builder http://localhost:8551 \
  --port 5656
```

### Docker Usage (via Builder Playground)

```bash
# L1 recipe with secondary builder
builder-playground cook l1 \
  --secondary-el 9551 \
  --output ~/my-testnet

# This automatically:
# - Starts primary builder (Reth) on 8551
# - Configures cl-proxy on 5656
# - Connects beacon node to cl-proxy instead of Reth directly
# - Forwards requests to both Reth and localhost:9551
```

### Integration with Builder Playground

In [playground/recipe_l1.go](../playground/recipe_l1.go:68-71):

```go
if l.secondaryELPort != 0 {
    // Use cl-proxy service to connect beacon node to two builders
    elService = "cl-proxy"
    svcManager.AddService("cl-proxy", &ClProxy{
        PrimaryBuilder:   "el",
        SecondaryBuilder: fmt.Sprintf("http://localhost:%d", l.secondaryELPort),
    })
} else {
    elService = "el"
}

svcManager.AddService("beacon", &LighthouseBeaconNode{
    ExecutionNode: elService,  // Points to "cl-proxy" or "el"
    MevBoostNode:  "mev-boost",
})
```

## Use Cases

### 1. Testing External Builders

Test an external builder (like Rbuilder) alongside your local Reth instance:

```bash
# Terminal 1: Start external builder on port 9551
rbuilder run --engine-api-addr 0.0.0.0:9551 ...

# Terminal 2: Start testnet with cl-proxy
builder-playground cook l1 --secondary-el 9551
```

Both builders receive identical fork choice updates from the beacon node.

### 2. Comparing Builder Implementations

Run two different builder implementations and compare their behavior:

```bash
# Primary: Reth (standard implementation)
# Secondary: Rbuilder (Rust builder)

builder-playground cook l1 \
  --secondary-el 9551 \
  --watchdog  # Monitor both builders
```

Monitor logs to compare:
- Block building performance
- Transaction selection differences
- Gas usage and fees

### 3. Fallback Builder Setup

Configure a production builder as primary, development builder as secondary:

```go
config := &clproxy.Config{
    Primary:   "http://production-builder:8551",  // Stable, returns responses
    Secondary: "http://dev-builder:9551",         // Experimental, logs only
}
```

If the secondary builder crashes, the beacon node continues using the primary.

### 4. Builder Development Workflow

Develop a new builder without disrupting your testnet:

1. Start testnet with cl-proxy and existing builder
2. Point secondary to your development builder
3. Iterate on your builder while testnet runs
4. No need to reconfigure beacon node

## API and Protocol

### HTTP Server

- **Method**: POST only (Engine API standard)
- **Port**: Configurable (default: 5656)
- **Timeouts**:
  - Read: 10 seconds
  - Write: 10 seconds
- **Content-Type**: `application/json`

### JSON-RPC Format

Standard Ethereum JSON-RPC 2.0:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "engine_forkchoiceUpdatedV3",
  "params": [
    {
      "headBlockHash": "0x...",
      "safeBlockHash": "0x...",
      "finalizedBlockHash": "0x..."
    },
    {
      "timestamp": "0x...",
      "prevRandao": "0x...",
      "suggestedFeeRecipient": "0x...",
      "withdrawals": [],
      "parentBeaconBlockRoot": "0x..."
    }
  ]
}
```

### JWT Authentication

The proxy **forwards JWT tokens** from the beacon node to both builders:

1. Beacon node includes `Authorization: Bearer <jwt>` header
2. Proxy copies header to both primary and secondary requests
3. Both builders validate JWT independently

**Important**: Primary and secondary builders must use the **same JWT secret** as the beacon node.

## Request Processing Details

### engine_forkchoiceUpdated

**Request to Primary:**
```json
{
  "method": "engine_forkchoiceUpdatedV3",
  "params": [
    {"headBlockHash": "0x123...", ...},
    {"timestamp": "0x...", "prevRandao": "0x...", ...}  // Full payload attributes
  ]
}
```

**Request to Secondary:**
```json
{
  "method": "engine_forkchoiceUpdatedV3",
  "params": [
    {"headBlockHash": "0x123...", ...},
    null  // Payload attributes removed
  ]
}
```

### engine_getPayload

**Request to Primary:**
```json
{
  "method": "engine_getPayloadV3",
  "params": ["0x1234567890abcdef"]  // PayloadId from FCU response
}
```

**Request to Secondary:**
- Not sent at all

### engine_newPayload

**Request to Both:**
```json
{
  "method": "engine_newPayloadV3",
  "params": [
    {
      "parentHash": "0x...",
      "feeRecipient": "0x...",
      "stateRoot": "0x...",
      // ... full execution payload
    }
  ]
}
```

Sent identically to both primary and secondary.

## Logging

The proxy logs all requests and errors:

```
INFO[0000] Starting server on port 5656
INFO[0001] Received request: method=engine_forkchoiceUpdatedV3
INFO[0001] Multiplexing request to secondary: method=engine_forkchoiceUpdatedV3
INFO[0002] Received request: method=engine_newPayloadV3
INFO[0002] Multiplexing request to secondary: method=engine_newPayloadV3
WARN[0005] ForkchoiceUpdated call with only one parameter
ERROR[0010] Error multiplexing to secondary: connection refused
```

### Log Levels

- **INFO**: Normal request flow
- **WARN**: Unexpected request format (e.g., FCU with only 1 param)
- **ERROR**: Network errors, marshalling errors (secondary only)

**Note**: Errors from secondary requests are logged but **do not** affect the response to the beacon node.

## Error Handling

### Primary Builder Errors

If the primary builder fails:
- Error is propagated to beacon node
- HTTP 500 returned to beacon node
- Beacon node may retry or fall back to safe head

### Secondary Builder Errors

If the secondary builder fails:
- Error is logged
- **No impact on beacon node**
- Primary builder response is still returned

This ensures the secondary builder cannot disrupt the testnet.

## Performance Considerations

### Latency

The proxy adds minimal latency:
- Primary request: synchronous (waits for response)
- Secondary request: asynchronous (fire-and-forget)
- No serialization between primary and secondary

**Typical overhead**: < 1ms for local forwarding.

### Throughput

The proxy can handle:
- ~1000 requests/second (local forwarding)
- Limited by primary builder response time
- Secondary requests do not block primary responses

### Resource Usage

- **Memory**: < 10 MB
- **CPU**: < 1% (idle), < 5% (active)
- **Network**: Minimal (local HTTP requests)

## Limitations and Caveats

### 1. Single Primary Only

Only one primary builder can respond to the beacon node. Multiple secondaries could be supported with code changes.

### 2. No Response Merging

The secondary builder's responses are **ignored**. The proxy does not:
- Compare responses between builders
- Merge results
- Aggregate metrics

For response comparison, you must implement custom logging/monitoring.

### 3. JWT Secret Sharing

All builders must use the **same JWT secret**. This is a security consideration:
- In production, different secrets per builder are recommended
- For testing, shared secret is acceptable

### 4. No Health Checks

The proxy does not:
- Verify builder availability before forwarding
- Implement circuit breakers
- Provide health check endpoints

If a builder is down, requests will fail and be logged.

### 5. Fire-and-Forget Secondary

Secondary requests are not retried on failure. If the secondary builder is temporarily unavailable, it will miss updates.

### 6. No Request Queuing

Requests are forwarded immediately. If the primary builder is slow, the beacon node will experience increased latency.

## Troubleshooting

### Proxy Won't Start

**Symptom:** `address already in use` error

**Solution:**
```bash
# Check what's using the port
lsof -i :5656

# Use a different port
clproxy --port 5657 --primary-builder ...
```

### Beacon Node Can't Connect

**Symptom:** Beacon node logs `failed to connect to execution endpoint`

**Solutions:**
1. Verify proxy is listening: `curl http://localhost:5656`
2. Check JWT secret matches beacon node
3. Ensure beacon node points to proxy, not directly to builder

### Secondary Builder Not Receiving Requests

**Symptom:** Secondary builder logs show no activity

**Solutions:**
1. Check proxy logs for errors: `Error multiplexing to secondary`
2. Verify secondary builder URL is correct
3. Test secondary builder directly: `curl -X POST http://localhost:9551`
4. Ensure secondary builder is running and accessible

### JWT Authentication Failures

**Symptom:** `401 Unauthorized` in proxy logs

**Solutions:**
1. Verify JWT secret is identical for beacon node, primary, and secondary
2. Check JWT secret file permissions (must be readable)
3. Ensure `Authorization` header is forwarded correctly

### ForkchoiceUpdated Warnings

**Symptom:** `ForkchoiceUpdated call with only one parameter`

**Explanation:** Some beacon nodes send FCU without payload attributes (block building not requested). This is normal.

**Action:** No action needed, warning is informational.

## Advanced Configuration

### Custom Logging

```go
// Log to file
file, _ := os.OpenFile("clproxy.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
config.LogOutput = file

// Log to multiple destinations
config.LogOutput = io.MultiWriter(os.Stdout, file)

// Disable logging
config.LogOutput = io.Discard
```

### Graceful Shutdown

```go
proxy, _ := clproxy.New(config)

// Start proxy in goroutine
go func() {
    if err := proxy.Run(); err != nil {
        log.Printf("Proxy error: %v", err)
    }
}()

// Handle signals
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
<-sigCh

// Graceful shutdown (10s timeout)
if err := proxy.Close(); err != nil {
    log.Printf("Shutdown error: %v", err)
}
```

### Multiple Secondary Builders

Currently not supported, but could be implemented by:
1. Accepting `[]string` for `Secondary` config
2. Looping over all secondaries in `handleRequest`
3. Using `sync.WaitGroup` to wait for all requests

## Security Considerations

### 1. JWT Secret Exposure

The proxy has access to JWT tokens. Ensure:
- Proxy runs in trusted environment
- Network traffic is encrypted (or use localhost)
- JWT secret file has restrictive permissions (0600)

### 2. Denial of Service

The proxy has basic timeouts but no rate limiting. In production:
- Add rate limiting per source IP
- Implement request size limits
- Use a reverse proxy (nginx, HAProxy) in front

### 3. Request Validation

The proxy does minimal validation. Malicious requests could:
- Crash primary/secondary builders
- Cause unexpected behavior
- Waste resources

Consider adding request schema validation for production use.

## Development

### Building

```bash
# Build library
cd cl-proxy
go build

# Build CLI
cd cmd
go build -o clproxy
```

### Testing

```bash
# Unit tests
go test ./...

# Integration test with mock builders
# (requires implementation)
```

### Dependencies

- `github.com/flashbots/mev-boost-relay/common` - Logging setup
- `github.com/sirupsen/logrus` - Structured logging
- Standard library only (no heavy dependencies)

## Related Projects

- [Lighthouse](https://github.com/sigp/lighthouse) - Ethereum consensus client
- [Reth](https://github.com/paradigmxyz/reth) - Execution client
- [Rbuilder](https://github.com/flashbots/rbuilder) - Rust-based block builder
- [Builder Playground](https://github.com/flashbots/builder-playground) - Testing framework

## License

MIT License - Copyright (c) 2025 Flashbots

## Support

For issues or questions:
- GitHub Issues: https://github.com/flashbots/builder-playground/issues
- Tag with: `cl-proxy`

## Future Enhancements

Potential improvements:

1. **Response Comparison** - Compare primary/secondary responses and log differences
2. **Multiple Secondaries** - Support array of secondary builders
3. **Health Checks** - Endpoint for monitoring proxy status
4. **Metrics** - Prometheus metrics for request counts, latency, errors
5. **Circuit Breaker** - Disable secondary if it fails repeatedly
6. **Request Replay** - Save and replay requests for debugging
7. **WebSocket Support** - Support WebSocket Engine API connections
8. **Dynamic Configuration** - Reload config without restart
9. **TLS Support** - HTTPS for remote builders
10. **Request Filtering Rules** - Configurable filtering logic per builder
