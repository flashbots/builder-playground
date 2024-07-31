# Builder Playground

The builder playground is a tool to deploy an end-to-end environment to locally test an Ethereum L1 builder. It deploys:

- A beacon node + validator client ([lighthouse](https://github.com/sigp/lighthouse)).
- An execution client ([reth](https://github.com/paradigmxyz/reth)).
- An in-memory [mev-boost-relay](https://github.com/flashbots/mev-boost-relay).

## Usage

Clone the repository and run the following command:

```bash
$ go run main.go
```

The playground performs the following steps:

1. It attempts to download the `lighthouse` and `reth` binaries from the GitHub releases page if they are not found locally.
2. It generates the genesis artifacts for the chain.
   - 100 validators with 32 ETH each.
   - 10 prefunded accounts with 100 ETH each, generated with the mnemonic `test test test test test test test test test test test junk`.
   - It enables the Deneb fork at startup.
3. It deploys the chain services and the relay.
   - `Reth` node.
   - `Lighthouse` beacon node.
   - `Lighthouse` validator client.
   - `Mev-boost-relay`.

To stop the playground, press `Ctrl+C`.

Options:

- `--output` (string): The directory where the chain data and artifacts are stored. It defaults to `$HOME/.playground/testnet`.
- `--continue` (bool): Whether to restart the chain from a previous run if the output folder is not empty. It defaults to `false`.
- `--use-bin-path` (bool): Whether to use the binaries from the local path instead of downloading them. It defaults to `false`.
- `--genesis-delay` (int): The delay in seconds before the genesis block is created. It is used to account for the delay between the creation of the artifacts and the running of the services. It defaults to `5` seconds.

Unless the `--continue` flag is set, the playground will delete the output directory and start a new chain from scratch on every run.
