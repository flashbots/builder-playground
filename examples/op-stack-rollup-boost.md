# Op Stack Rollup Boost

This example shows how to deploy an Op Stack with rollup-boost with an external block builder (op-reth).

First, download the op-reth binary:

```bash
$ go run main.go artifacts op-reth
```

This will download the op-reth binary and save it under `$HOME/.playground/op-reth-v1.3.12`.

Second, we can deploy the Op Stack with rollup-boost:

```bash
$ go run main.go cook opstack --external-builder http://host.docker.internal:4444
```

This will deploy an Op Stack chain with:

- A complete L1 setup (CL/EL/Mev-boost)
- A complete L2 sequencer (op-geth/op-node/op-batcher)
- Rollup-boost to enable external block building

Note that we use `host.docker.internal` as the hostname because the Op Stack components run in Docker containers, while the external builder (op-reth) runs directly on the host machine.

By default, the EL node for the Op-stack is deployed with a deterministic P2P key, ensuring the enode address remains consistent across all runs. The enode address is:

`enode://3479db4d9217fb5d7a8ed4d61ac36e120b05d36c2eefb795dc42ff2e971f251a2315f5649ea1833271e020b9adc98d5db9973c7ed92d6b2f1f2223088c3d852f@127.0.0.1:30304`

You will see this enode address displayed in the output when running the Op-stack recipe.

The `--external-builder` flag is used to specify the URL of the external block builder. Even though the external builder is not active at this point, this does not affect the liveness of the system as the sequencer will continue to produce blocks normally.

Third, we can start the `op-reth` binary as the external block builder:

```bash
$ $HOME/.playground/op-reth-v1.3.12 node --http --http.port 2222 --authrpc.port 4444 --authrpc.jwtsecret $HOME/.playground/devnet/jwtsecret --chain $HOME/.playground/devnet/l2-genesis.json --datadir /tmp/builder --disable-discovery --port 30333 --trusted-peers enode://3479db4d9217fb5d7a8ed4d61ac36e120b05d36c2eefb795dc42ff2e971f251a2315f5649ea1833271e020b9adc98d5db9973c7ed92d6b2f1f2223088c3d852f@127.0.0.1:30304
```

The command above starts op-reth as an external block builder with the following key parameters:

- `--authrpc.port 4444`: Matches the port specified in the `--external-builder` flag earlier
- `--authrpc.jwtsecret`: Uses the JWT secret generated during Op Stack deployment
- `--trusted-peers`: Connects to our Op Stack's EL node using the deterministic enode address

Once op-reth is running, it will connect to the Op Stack and begin participating in block building. You can verify it's working by checking the logs of both the sequencer and op-reth for successful block proposals.

## Internal block builder

To use an internal `op-reth` as a block builder, run:

```
$ go run main.go cook opstack --external-builder op-reth
```
