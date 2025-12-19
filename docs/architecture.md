# Architecture

Builder Playground is designed specifically for Ethereum chain deployments, particularly for block building use cases. This focus on a concrete problem space allowed us to optimize the architecture accordingly.

The internal architecture is split into three distinct phases:

- **Artifact generation**: Creates all the configuration files, genesis states, and materials needed to bootstrap the network.
- **Topology generation**: Produces a description of the services to run and how they connect to each other.
- **Execution**: Deploys the topology to a runtime. Currently, this only supports Docker Compose.
