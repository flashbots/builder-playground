
# Running Rbuilder

Run the L1 Playground recipe with:

```
go run main.go cook l1 --use-native-reth
```

## Why `--use-native-reth` is Required

Rbuilder needs to connect directly to the Reth database. Without the `--use-native-reth` flag, Reth runs inside a Docker container, which creates a critical compatibility problem on non-Linux systems like macOS.

Reth uses an MDBX database, which is not cross-platform compatible. When Reth runs in Docker, it runs in a Linux environment and creates an MDBX database with Linux-specific flags. If you're running on macOS, the MDBX database created inside the Linux container cannot be opened from the macOS host machine due to platform-specific differences in how MDBX is configured.

Since rbuilder runs on the host machine and requires access to the Reth database, it cannot open the Linux-configured MDBX database when running on macOS. The `--use-native-reth` flag solves this by running Reth directly on the host machine instead of in Docker. This creates an MDBX database with the host platform's native configurations, allowing rbuilder to successfully connect to and access the same database.

Then, run rbuilder:

```
cargo run --bin rbuilder run -- ./examples/rbuilder-config.toml
```

Send a transaction since Rbuilder will not mine empty blocks (no profit):

```bash
cast send 0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266 \
  --value 1ether \
  --private-key 0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d \
  --rpc-url http://localhost:8545
```

This sends a transaction from one funded account to the builder coinbase.

Query mev-boost-relay to see mined blocks:

```
curl http://localhost:5555/relay/v1/data/bidtraces/proposer_payload_delivered
```
