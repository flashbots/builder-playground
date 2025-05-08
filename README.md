# Builder Playground

[![Goreport status](https://goreportcard.com/badge/github.com/phylaxsystems/builder-playground)](https://goreportcard.com/report/github.com/phylaxsystems/builder-playground)
[![Test status](https://github.com/phylaxsystems/builder-playground/actions/workflows/checks.yaml/badge.svg?branch=main)](https://github.com/phylaxsystems/builder-playground/actions?query=workflow%3A%22Checks%22)

The builder playground is a tool to deploy an end-to-end environment to locally test EVM block builders.

## Usage

Clone the repository and use the `cook` command to deploy a specific recipe:

```bash
$ builder-playground cook <recipe>
```

Currently available recipes:

### Phylax Credible Layer (PCL) Recipe

Deploys a complete OP Stack Credible Layer environment with:

- Complete L1 setup (beacon node, validator, and execution client)
- A complete sequencer with op-node, op-geth and op-batcher
- Rollup-Boost
- OP-Talos (either external or part of the playground)
- Assertion-DA (either external or part of the playground)

```bash
$ builder-playground cook pcl [flags]
```

Flags:

- `--external-builder`: URL of an external builder to use
- `--external-da`: URL of an external DA to use (use "dev" for development mode)
- `--enable-latest-fork`: Enable the latest fork (isthmus) at startup (0) or n blocks after genesis
- `--block-time`: Block time to use for the rollup (default: 2 seconds)
- `--with-grafana-alloy`: Enable grafana alloy and initialize from `.env.grafana` (default: false)
- `--batcher-max-channel-duration`: Maximum channel duration to use for the batcher (default: 2 seconds)

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
- `--use-reth-for-validation`: Use Reth EL for block validation in mev-boost.
- `--secondary-el`: Port to use for a secondary el (enables the internal cl-proxy proxy)
- `--use-native-reth`: Run the Reth EL binary on the host instead of docker (recommended to bind to the Reth DB)

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

## Common Options

- `--output` (string): The directory where the chain data and artifacts are stored. Defaults to `$HOME/.playground/devnet`
- `--genesis-delay` (int): The delay in seconds before the genesis block is created. Defaults to `10` seconds
- `--watchdog` (bool): Enable the watchdog service to monitor the specific chain
- `--dry-run` (bool): Generates the artifacts and manifest but does not deploy anything (also enabled with the `--mise-en-place` flag)
- `--log-level` (string): Log level to use (debug, info, warn, error, fatal). Defaults to `info`.
- `--labels` (key=val): Custom labels to apply to your deployment.
- `--disable-logs` (bool): Disable the logs for the services. Defaults to `false`.

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
