# Builder Playground

[![Goreport status](https://goreportcard.com/badge/github.com/flashbots/builder-playground)](https://goreportcard.com/report/github.com/flashbots/builder-playground)
[![Test status](https://github.com/flashbots/builder-playground/actions/workflows/checks.yaml/badge.svg?branch=main)](https://github.com/flashbots/builder-playground/actions?query=workflow%3A%22Checks%22)

Builder Playground is a CLI tool for spinning up self-contained Ethereum development networks for end-to-end block building. With a single command, it deploys complete L1 and L2 stacks—including EL and CL nodes, mev-boost, relays, and builder infrastructure. Designed for speed, determinism, and ease of use, it serves as the foundation for testing block-builder software, BuilderNet VM images, and integration tests across chains.

Recipes (e.g. `l1`, `opstack`) assemble opinionated components and pre-baked configs to bring a full blockchain stack online within seconds. This makes it ideal for:

- Developing and testing block-building pipelines
- Validating builder/relay behavior against real consensus flows
- Running repeatable CI and e2e scenarios
- Experimenting with fork configurations and client combinations

Quick start:

```bash
# L1 environment with mev-boost relay
builder-playground cook l1

# L2 OpStack with external builder support
builder-playground cook opstack --external-builder http://localhost:4444
```

## Getting started

Clone the repository and use the `cook` command to deploy a specific recipe:

```bash
$ builder-playground cook <recipe>
```

Currently available recipes:

### L1 Recipe

Deploys a complete L1 environment with:

- A beacon node + validator client ([lighthouse](https://github.com/sigp/lighthouse)).
- An execution client ([reth](https://github.com/paradigmxyz/reth)).
- An in-memory [mev-boost-relay](https://github.com/flashbots/mev-boost-relay).

```bash
$ builder-playground cook l1 [flags]
```

Flags:

- `--latest-fork`: Enable the latest fork at startup
- `--block-time`: Change the default block time (`12s`), to be provided in duration format (e.g. `--block-time=1s`)
- `--use-reth-for-validation`: Use Reth EL for block validation in mev-boost.
- `--secondary-el`: Host or port to use for a secondary el (enables the internal cl-proxy proxy). Can be a port number (e.g., '8551') in which case the full URL is derived as `http://localhost:<port>` or a complete URL (e.g., `http://remote-host:8551`), use `http://host.docker.internal:<port>` to reach a secondary execution client that runs on your host and not within Docker.
- `--use-native-reth`: Run the Reth EL binary on the host instead of docker (recommended to bind to the Reth DB)
- `--use-separate-mev-boost`: Spins a seperate service for mev-boost in addition with mev-boost-relay

### OpStack Recipe

Deploys an L2 environment with:

- Complete L1 setup (as above minus mev-boost)
- A complete sequencer with op-node, op-geth and op-batcher

```bash
$ builder-playground cook opstack [flags]
```

Flags:

- `--external-builder`: URL of an external builder to use (enables rollup-boost)
- `--enable-latest-fork` (int): Enables the latest fork (isthmus) at startup (0) or n blocks after genesis.

### Example Commands

Here's a complete example showing how to run the L1 recipe with the latest fork enabled and custom output directory:

```bash
$ builder-playground cook l1 --latest-fork --output ~/my-builder-testnet --genesis-delay 15 --log-level debug
```

### Generate transaction flow with contender

builder-playground can generate transaction flow to its nodes with [contender](https://github.com/flashbots/contender). Just pass the `--contender` flag to send spam transactions that fill each block:

```bash
go run main.go cook l1 --contender
```

The default contender flags are as follows:

- `--min-balance "10 ether"` -- gives each spammer account 10 ETH.
- `--tps 20` -- sends 20 transactions per second.
- `-l` -- runs spammer indefinitely (pass `-l <num>` to set a finite number of spam runs).

To add or modify contender flags, use `--contender.arg`:

```bash
# run the builtin erc20 scenario instead of the default "fill block" scenario, at 100 TPS
go run main.go cook l1 --contender \
  --contender.arg "--tps 100" \
  --contender.arg "erc20"
```

To read about more contender flag options, see the [contender CLI docs](https://github.com/flashbots/contender/blob/main/docs/cli.md). To see all available flags, [install contender](https://github.com/flashbots/contender/blob/main/docs/installation.md) or [run it in docker](https://github.com/flashbots/contender?tab=readme-ov-file#docker-instructions), and run `contender --help`.

To see what contender is doing internally, check its docker logs:

```bash
docker logs -f $(docker ps | grep contender | cut -d' ' -f1)
```

## Common Options

- `--output` (string): The directory where the chain data and artifacts are stored. Defaults to `$HOME/.playground/devnet`
- `--detached` (bool): Run the recipes in the background. Defaults to `false`.
- `--genesis-delay` (int): The delay in seconds before the genesis block is created. Defaults to `10` seconds
- `--watchdog` (bool): Enable the watchdog service to monitor the specific chain
- `--dry-run` (bool): Generates the artifacts and manifest but does not deploy anything (also enabled with the `--mise-en-place` flag)
- `--log-level` (string): Log level to use (debug, info, warn, error, fatal). Defaults to `info`.
- `--labels` (key=val): Custom labels to apply to your deployment.
- `--disable-logs` (bool): Disable the logs for the services. Defaults to `false`.
- `--contender` (bool): Enable [contender](https://github.com/flashbots/contender) spammer. Required to use other contender flags.
  - `--contender.arg` (string): Pass custom args to the contender CLI.
  Example: `--contender.arg "--tpb 20"`
  - `--contender.target` (string): Change the default target node to spam. On the `l1` recipe, the default is "el", and on `opstack` it's "op-geth".
- `--with-prometheus` (bool); Whether to deploy a Prometheus server and gather metrics. Defaults to `false`.

To stop the playground, press `Ctrl+C`.

## Inspect

Builder-playground supports inspecting the connection of a service to a specific port.

```bash
$ builder-playground inspect <service> <port>
```

Example:

```bash
$ builder-playground cook opstack
$ builder-playground inspect op-geth authrpc
```

This command starts a `tcpflow` container in the same network interface as the service and captures the traffic to the specified port.

## Clean

Removes a recipe running in the background

```bash
$ builder-playground clean [--output ./output]
```

## Telemetry

The Builder Playground includes built-in Prometheus metrics collection. When you run any recipe with the `--with-prometheus` flag, the system automatically deploys a Prometheus server and gathers metrics from all services in your deployment.

Prometheus automatically discovers services by looking for a port with the metrics label. You can define a metrics port in your component like this:

```go
WithArgs("--metrics", `0.0.0.0:{{Port "metrics" 9090}}`)
```

By default, Prometheus scrapes the `/metrics` path, but services can override this by specifying a custom path with `WithLabel("metrics_path", "/custom/path")`. All configured services are automatically registered as scrape targets.

### Usage
Enable Prometheus for any recipe:

```bash
$ builder-playground cook l1 --with-prometheus
$ builder-playground cook opstack --with-prometheus
```

## Internals

### Execution Flow

The playground executes in three main phases:

1. **Artifact Generation**: Creates all necessary files and configurations (genesis files, keys, etc.)
2. **Manifest Generation**: The recipe creates a manifest describing all services to be deployed, their ports, and configurations
3. **Deployment**: Uses Docker Compose to deploy the services described in the manifest

When running in dry-run mode (`--dry-run` flag), only the first two phases are executed. This is useful for alternative deployment targets - while the playground uses Docker Compose by default, the manifest could be used to deploy to other platforms like Kubernetes.

### System Architecture

The playground is structured in two main layers:

#### Components

Components are the basic building blocks of the system. Each component implements the `Service` interface:

```go
type Service interface {
    Run(service *service)
}
```

Components represent individual compute resources like:

- Execution clients (Reth)
- Consensus clients (Lighthouse)
- Sidecar applications (MEV-Boost Relay)

Each component, given its input parameters, outputs a Docker container description with its specific configuration.

#### Recipes

Recipes combine components in specific ways to create complete environments. They implement this interface:

```go
type Recipe interface {
   Apply(artifacts *Artifacts) *Manifest
}
```

The key output of a recipe is a `Manifest`, which represents a complete description of the environment to be deployed. A Manifest contains:

- A list of services to deploy
- Their interconnections and dependencies
- Port mappings and configurations
- Volume mounts and environment variables

While the current recipes (L1 and OpStack) are relatively simple, this architecture allows for more complex setups. For example, you could create recipes for:

- Multiple execution clients with a shared bootnode
- Testing specific MEV scenarios
- Interop L2 testing environments

The separation between components and recipes makes it easy to create new environments by reusing and combining existing components in different ways. The Manifest provides an abstraction between the recipe's description of what should be deployed and how it actually gets deployed (Docker Compose, Kubernetes, etc.).

## Design Philosophy

The Builder Playground is focused exclusively on block building testing and development. Unlike general-purpose tools like Kurtosis that support any Ethereum setup, we take an opinionated approach optimized for block building workflows.

We deliberately limit configuration options and client diversity to keep the tool simple and maintainable. This focused approach allows us to provide a smooth developer experience for block building testing scenarios while keeping the codebase simple and maintainable.

This means we make specific choices:

- Fixed client implementations (Lighthouse, Reth)
- Recipe-based rather than modular configurations
- Pre-backed configurations

For use cases outside our scope, consider using a more general-purpose tool like Kurtosis.
