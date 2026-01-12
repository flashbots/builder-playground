# buildernet Recipe

Deploy a full L1 stack with mev-boost and builder-hub.

## Flags

- `block-time` (duration): Block time to use for the L1. Default to '12s'.
- `builder-config` (string): Builder config in YAML format. Default to ''.
- `builder-ip` (string): IP address of the external builder to register in BuilderHub. Default to '127.0.0.1'.
- `latest-fork` (bool): use the latest fork. Default to 'false'.
- `secondary-el` (string): Address or port to use for the secondary EL (execution layer); Can be a port number (e.g., '8551') in which case the full URL is derived as `http://localhost:<port>` or a complete URL (e.g., `http://docker-container-name:8551`), use `http://host.docker.internal:<port>` to reach a secondary execution client that runs on your host and not within Docker.. Default to ''.
- `use-native-reth` (bool): use the native reth binary. Default to 'false'.
- `use-reth-for-validation` (bool): use reth for validation. Default to 'false'.
- `use-separate-mev-boost` (bool): use separate mev-boost and mev-boost-relay services. Default to 'false'.

## Architecture Diagram

```mermaid
graph LR
  el["el<br/>rpc:30303<br/>http:8545<br/>ws:8546<br/>authrpc:8551<br/>metrics:9090"]
  el_healthmon["el_healthmon"]
  beacon["beacon<br/>p2p:9000<br/>p2p:9000<br/>quic-p2p:9100<br/>http:3500"]
  beacon_healthmon["beacon_healthmon"]
  validator["validator"]
  mev_boost_relay["mev-boost-relay<br/>http:5555"]
  builder_hub_db["builder-hub-db<br/>postgres:5432"]
  builder_hub_api["builder-hub-api<br/>http:8080<br/>admin:8081<br/>internal:8082<br/>metrics:8090"]
  builder_hub_proxy["builder-hub-proxy<br/>http:8888"]

  el_healthmon -->|http| el
  beacon -->|authrpc| el
  beacon -->|http| mev_boost_relay
  beacon_healthmon -->|http| beacon
  validator -->|http| beacon
  mev_boost_relay -->|http| beacon
  builder_hub_api -->|postgres| builder_hub_db
  builder_hub_proxy -->|http| builder_hub_api
  mev_boost_relay -.->|depends_on| beacon
  builder_hub_api -.->|depends_on| builder_hub_db
  builder_hub_proxy -.->|depends_on| builder_hub_api
```

