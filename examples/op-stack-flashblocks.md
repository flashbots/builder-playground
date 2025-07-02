# Op Stack Rollup Boost with Flashblocks

This example shows how to deploy an Op Stack with rollup-boost using Flashblocks for pre-confirmations.

## What is Flashblocks?

Flashblocks is a feature in rollup-boost that enables pre-confirmations by proposing incremental sections of blocks. This allows for faster transaction confirmations and improved user experience on L2 networks.

## Quick Start

Deploy the Op Stack with rollup-boost and Flashblocks enabled:

```bash
$ go run main.go cook opstack --external-builder op-rbuilder --flashblocks
```

This will deploy an Op Stack chain with:

- A complete L1 setup (CL/EL)
- A complete L2 sequencer (op-geth/op-node/op-batcher)
- Rollup-boost with Flashblocks enabled for pre-confirmations
- Op-rbuilder as the external block builder with Flashblocks support

The `--flashblocks` flag enables the Flashblocks feature, allowing the system to provide pre-confirmations for faster transaction processing.
