# Multi-rbuilder Flowproxy Scenario

This custom recipe extends the `l1` playground recipe with:

- three `rbuilder` services using a local wrapper built from the sibling `rbuilder-prism` checkout;
- one `bidding-gateway` service; `rbuilder-1` sends compressed signed submissions to it, and the gateway forwards to `mev-boost-relay:5555` without the relay-delay proxy;
- one local `flowproxy` container per rbuilder;
- relay-delay HTTP proxies for the non-gateway builders, adding `LATENCY_MS=50` before forwarding to `mev-boost-relay:5555`;
- three `flashbots/contender:0.7.2` services that send transfer load to each builder EL RPC from a prefunded devnet key.

Run it from the `builder-playground` repository:

```bash
./builder-playground start custom-recipes/multi-rbuilder/flowproxy/playground.yaml
```

Use a `builder-playground` binary built from this checkout. The recipe relies on YAML `pid` support so each rbuilder can share its paired Reth PID namespace and open the MDBX database safely.

The recipe setup builds:

- `builder-playground/relay-delay-proxy:local` from this directory;
- `builder-playground/rbuilder-wrapper:local` from `../../../../rbuilder-prism` using this directory's Dockerfile;
- `builder-playground/flowproxy-build:local` from the sibling `flowproxy-private` repository;
- `builder-playground/flowproxy:local` from this directory.

If the images are already built, append `--skip-setup`.

Useful checks after startup:

```bash
./builder-playground list
./builder-playground logs rbuilder-1
./builder-playground logs bidding-gateway
./builder-playground logs rbuilder-2
./builder-playground logs rbuilder-3
curl "http://localhost:$(./builder-playground port mev-boost-relay http)/relay/v1/data/bidtraces/builder_blocks_received"
curl "http://localhost:$(./builder-playground port mev-boost-relay http)/relay/v1/data/bidtraces/proposer_payload_delivered"
```
