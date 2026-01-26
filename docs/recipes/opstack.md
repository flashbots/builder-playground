# opstack Recipe

Deploy an OP stack.

## Flags

- `base-overlay` (bool): Whether to use base implementation for flashblocks-rpc. Default to 'false'.
- `batcher-max-channel-duration` (uint64): Maximum channel duration to use for the batcher. Default to '2'.
- `block-time` (uint64): Block time to use for the rollup. Default to '2'.
- `chain-monitor` (bool): Whether to enable chain-monitor. Default to 'false'.
- `enable-latest-fork` (uint64): Enable Jovian fork: 0 = at genesis, N > 0 = at block N (default: Isthmus only). Default to 'nil'.
- `enable-websocket-proxy` (bool): Whether to enable websocket proxy. Default to 'false'.
- `external-builder` (string): External builder URL. Default to ''.
- `flashblocks` (bool): Whether to enable flashblocks. Default to 'false'.
- `flashblocks-builder` (string): External URL of builder flashblocks stream. Default to ''.

## Architecture Diagram

```mermaid
graph LR
  bootnode["bootnode<br/>rpc:30303"]
  el["el<br/>rpc:30303<br/>http:8545<br/>ws:8546<br/>authrpc:8551<br/>metrics:9090"]
  el_healthmon["el_healthmon"]
  beacon["beacon<br/>p2p:9000<br/>p2p:9000<br/>quic-p2p:9100<br/>http:3500"]
  beacon_healthmon["beacon_healthmon"]
  validator["validator"]
  op_geth["op-geth<br/>http:8545<br/>ws:8546<br/>authrpc:8551<br/>rpc:30303<br/>metrics:6061"]
  op_geth_healthmon["op-geth_healthmon"]
  op_node["op-node<br/>metrics:7300<br/>http:8549<br/>p2p:9003<br/>p2p:9003"]
  op_batcher["op-batcher"]

  el -->|rpc| bootnode
  el_healthmon -->|http| el
  beacon -->|authrpc| el
  beacon_healthmon -->|http| beacon
  validator -->|http| beacon
  op_geth -->|rpc| bootnode
  op_geth_healthmon -->|http| op_geth
  op_node -->|http| el
  op_node -->|http| beacon
  op_node -->|authrpc| op_geth
  op_batcher -->|http| el
  op_batcher -->|http| op_geth
  op_batcher -->|http| op_node
```

