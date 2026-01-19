
# Running Rbuilder

Run the L1 Playground recipe with:

```
go run main.go cook l1 --use-native-reth
```

This runs the Reth EL instance on the host machine. Rbuilder binds to the Reth database - running both on the host avoids potential cross-platform issues.

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
