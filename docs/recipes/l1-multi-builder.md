# l1-multi-builder Recipe

Deploy a full L1 stack with multiple builders and relays.

## Flags

- `block-time` (duration): Block time to use for the L1. Default to '12s'.
- `builders` (int): number of rbuilder instances to run. Default to '2'.
- `latest-fork` (bool): use the latest fork. Default to 'false'.
- `relays` (int): number of mev-boost-relay instances to run. Default to '2'.
- `use-reth-for-validation` (bool): use reth for validation. Default to 'false'.

## Architecture Diagram

```mermaid
graph LR
  el["el<br/>rpc:30303<br/>http:8545<br/>ws:8546<br/>authrpc:8551<br/>metrics:9090"]
  el_healthmon["el_healthmon"]
  beacon["beacon<br/>p2p:9000<br/>p2p:9000<br/>quic-p2p:9100<br/>http:3500"]
  beacon_healthmon["beacon_healthmon"]
  validator["validator"]
  mev_boost_relay_1["mev-boost-relay-1<br/>http:5555"]
  mev_boost_relay_2["mev-boost-relay-2<br/>http:5555"]
  mev_boost["mev-boost<br/>http:18550"]
  rbuilder_1["rbuilder-1<br/>rpc:8645<br/>redacted:6061<br/>full-metrics:6060"]
  flowproxy_1["flowproxy-1<br/>http:28545<br/>system:29545<br/>metrics:29090"]
  rbuilder_2["rbuilder-2<br/>rpc:8646<br/>redacted:6062<br/>full-metrics:6061"]
  flowproxy_2["flowproxy-2<br/>http:28546<br/>system:29546<br/>metrics:29091"]

  el_healthmon -->|http| el
  beacon -->|authrpc| el
  beacon -->|http| mev_boost
  beacon_healthmon -->|http| beacon
  validator -->|http| beacon
  mev_boost_relay_1 -->|http| beacon
  mev_boost_relay_2 -->|http| beacon
  mev_boost -->|http| mev_boost_relay_1
  mev_boost -->|http| mev_boost_relay_2
  flowproxy_1 -->|rpc| rbuilder_1
  flowproxy_1 -->|redacted| rbuilder_1
  flowproxy_2 -->|rpc| rbuilder_2
  flowproxy_2 -->|redacted| rbuilder_2
  mev_boost_relay_1 -.->|depends_on| beacon
  mev_boost_relay_2 -.->|depends_on| beacon
  rbuilder_1 -.->|depends_on| el
  rbuilder_1 -.->|depends_on| beacon
  flowproxy_1 -.->|depends_on| rbuilder_1
  rbuilder_2 -.->|depends_on| el
  rbuilder_2 -.->|depends_on| beacon
  flowproxy_2 -.->|depends_on| rbuilder_2
```

