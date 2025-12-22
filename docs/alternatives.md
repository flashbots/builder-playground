# Alternatives

## Kurtosis

[Kurtosis](https://www.kurtosis.com/) is an open-source platform for creating and managing containerized development environments for distributed systems. While it's designed as a general-purpose tool, Ethereum has become its primary use case.

The core challenge in blockchain devnet deployment isn't launching the computational resources themselves (like execution or consensus clients), but rather generating the necessary artifacts and configuration. For Ethereum, this includes computing the beacon genesis state, generating the execution layer genesis configuration, and setting up validator keystores. For L2 deployments like OP Stack, it requires running tools like [op-deployer](https://github.com/ethereum-optimism/optimism/tree/develop/op-deployer) to establish the initial chain state.

Kurtosis addresses this through a DAG-based pipeline system using Starlark, a Python-like language interpreted in Go. Starlark orchestrates these setup steps by calling Docker containers to perform the actual computations. Even simple operations like extracting values from JSON files are delegated to containerized tools. This creates a multi-layered architecture where Starlark acts as glue code between Docker-based execution stages.

Builder Playground takes a different path by being purpose-built for Ethereum use cases. Rather than orchestrating external containers, it implements the artifact generation logic directly in Go. This design choice offers significant performance improvements through native execution and simplifies debugging by reducing layers of abstraction. The tighter integration allows for faster iteration cycles when developing against Ethereum L1 and L2 networks.

Kurtosis's Starlark-based pipeline system provides theoretical modularity through its declarative approach. However, in practice, these pipelines can become complex and tightly coupled. Builder Playground prioritizes developer experience and execution speed for Ethereum-specific workflows over general-purpose flexibility.
