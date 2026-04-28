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
  rbuilder_1["rbuilder-1<br/>rpc:8645"]
  rbuilder_2["rbuilder-2<br/>rpc:8646"]

  el_healthmon -->|http| el
  beacon -->|authrpc| el
  beacon -->|http| mev_boost
  beacon_healthmon -->|http| beacon
  validator -->|http| beacon
  mev_boost_relay_1 -->|http| beacon
  mev_boost_relay_2 -->|http| beacon
  mev_boost -->|http| mev_boost_relay_1
  mev_boost -->|http| mev_boost_relay_2
  mev_boost_relay_1 -.->|depends_on| beacon
  mev_boost_relay_2 -.->|depends_on| beacon
  rbuilder_1 -.->|depends_on| el
  rbuilder_1 -.->|depends_on| beacon
  rbuilder_2 -.->|depends_on| el
  rbuilder_2 -.->|depends_on| beacon
```

