# opstack Recipe

Deploy an OP stack.

## Flags

- `base-overlay` (bool): Whether to use base implementation for flashblocks-rpc. Default to 'false'.
- `batcher-max-channel-duration` (uint64): Maximum channel duration to use for the batcher. Default to '2'.
- `block-time` (uint64): Block time to use for the rollup. Default to '2'.
- `chain-monitor` (bool): Whether to enable chain-monitor. Default to 'false'.
- `enable-latest-fork` (uint64): Enable latest fork isthmus (nil or empty = disabled, otherwise enabled at specified block). Default to 'nil'.
- `enable-websocket-proxy` (bool): Whether to enable websocket proxy. Default to 'false'.
- `external-builder` (string): External builder URL. Default to ''.
- `flashblocks` (bool): Whether to enable flashblocks. Default to 'false'.
- `flashblocks-builder` (string): External URL of builder flashblocks stream. Default to ''.

## Architecture Diagram

```dot
digraph G {
  rankdir=LR;
  node [shape=record];

  el [label="el|{rpc:30303|http:8545|ws:8546|authrpc:8551|metrics:9090}"];
  el_healthmon [label="el_healthmon"];
  beacon [label="beacon|{p2p:9000|p2p:9000|quic-p2p:9100|http:3500}"];
  beacon_healthmon [label="beacon_healthmon"];
  validator [label="validator"];
  op_geth [label="op-geth|{http:8545|ws:8546|authrpc:8551|rpc:30303|metrics:6061}"];
  op_geth_healthmon [label="op-geth_healthmon"];
  op_node [label="op-node|{metrics:7300|http:8549|p2p:9003|p2p:9003}"];
  op_batcher [label="op-batcher"];

  el_healthmon -> el [label="http"];
  beacon -> el [label="authrpc"];
  beacon_healthmon -> beacon [label="http"];
  validator -> beacon [label="http"];
  op_geth_healthmon -> op_geth [label="http"];
  op_node -> el [label="http"];
  op_node -> beacon [label="http"];
  op_node -> op_geth [label="authrpc"];
  op_batcher -> el [label="http"];
  op_batcher -> op_geth [label="http"];
  op_batcher -> op_node [label="http"];
}
```

