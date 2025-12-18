# Writing Components

Components are the fundamental computational units in Builder Playground. Each component represents one or more Docker containers that work together as a single logical service.

A component is defined as a Go struct that implements the `ServiceGen` interface by providing an `Apply` method:

```go
type RethEL struct {
}

func (r *RethEL) Apply(manifest *Manifest) {
    svc := manifest.NewService("el").
        WithImage("ghcr.io/paradigmxyz/reth").
        WithTag("v1.8.2").
        WithArgs(
            "node",
            "--chain", "/data/genesis.json",
            "--http.port", `{{Port "http" 8545}}`,
            "--authrpc.jwtsecret", "/data/jwtsecret",
        ).
        WithArtifact("/data/genesis.json", "genesis.json").
        WithVolume("data", "/data_reth")
}
```

The `Service` struct in [playground/manifest.go](../../playground/manifest.go) provides the complete API for configuring containers, including image selection, arguments, environment variables, and more.

## Port Declarations

Ports can be explicitly declared using `WithPort`:

```go
WithPort("http", 8545)
WithPort("metrics", 9090)
WithPort("discovery", 30303, ProtocolUDP)
```

However, the recommended approach is to use the template syntax `{{Port}}` within arguments, which is syntactic sugar that automatically declares ports while referencing them:

```go
WithArgs(
    "--http.port", `{{Port "http" 8545}}`,
    "--metrics", `0.0.0.0:{{Port "metrics" 9090}}`,
    "--discovery-port", `{{PortUDP "discovery" 30303}}`,
)
```

Using the template syntax is preferred because it allows the playground to track where port numbers appear in the configuration and potentially remap them during execution if necessary. The template `{{Port "name" defaultPort}}` both declares the port and inserts the appropriate port number at that location.

**Note**: If a port is named `"metrics"`, it will be automatically recognized as a Prometheus endpoint and tracked by the telemetry stack. See [telemetry.md](../telemetry.md) for more information.

## Service Connections

Similar to port declarations, service connections use the template syntax `{{Service "name" "portLabel" "protocol" "user"}}` within arguments to define dependencies between services:

```go
manifest.NewService("beacon").
    WithArgs(
        "--execution-endpoint", `{{Service "el" "authrpc" "http" ""}}`,
        "--execution-jwt", "/data/jwtsecret",
    )
```

However, to make this syntax more readable, the `Connect` helper functions are provided:

```go
manifest.NewService("beacon").
    WithArgs(
        "--execution-endpoint", Connect("el", "authrpc"),
        "--execution-jwt", "/data/jwtsecret",
    )
```

The `Connect` function translates to the `{{Service}}` template under the hood.

This approach allows the playground to track exactly where in the arguments service connections are made, enabling automatic validation that the target service exists and exposes the referenced port at validation time rather than runtime. Additionally, connection strings can be adjusted if needed during execution (similar to port remapping), and the system can track dependencies between services.

Additional connection helpers include `ConnectWs` for WebSocket connections and `ConnectRaw` for custom protocols.

## Files and Volumes

Mount artifacts generated during the build phase:

```go
WithArtifact("/data/jwtsecret", "jwtsecret")
WithArtifact("/data/genesis.json", "genesis.json")
```

Mount persistent volumes for data storage:

```go
WithVolume("data", "/data_reth")
```

## Health Checks

Define readiness checks to determine when a service is healthy:

```go
WithReady(ReadyCheck{
    QueryURL:    "http://localhost:8545",
    Interval:    1 * time.Second,
    Timeout:     10 * time.Second,
    Retries:     20,
    StartPeriod: 1 * time.Second,
})
```

## Dependency Management

Use `DependsOn` to enforce startup ordering between services:

```go
service.DependsOnHealthy("beacon-node")  // Wait until healthy
service.DependsOnRunning("redis")        // Wait until running
```

It is recommended to only use `DependsOn` when a service would crash or fail to start if the dependency is not ready. If a service can gracefully handle the absence of dependencies through retries, reconnection logic, or exponential backoff, do not add `DependsOn` constraints. It is recommended to let services handle reconnections gracefully in order to speed up the overall startup process.
