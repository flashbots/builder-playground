## Regenerating the Rollup Config

TODO: Rewrite these instructions since this doc is out of date


```
# Reset the state
rm state.json
echo '{"version": 1}' > state.json

# NOTE: op-deployer version must match the contract artifacts version in intent.toml
op-deployer apply --workdir . --deployment-target genesis

op-deployer inspect genesis --workdir . --outfile ./genesis.json 13

op-deployer inspect rollup --workdir . --outfile ./rollup.json 13
```

## Updating for New op-deployer Releases

When a new op-deployer version is released, follow these steps:

1. **Download the latest op-deployer:**
   ```bash
   # Check releases at: https://github.com/ethereum-optimism/optimism/releases
   # Download for your platform, e.g.:
   curl -L https://github.com/ethereum-optimism/optimism/releases/download/op-deployer/vX.Y.Z/op-deployer-X.Y.Z-darwin-arm64.tar.gz -o op-deployer.tar.gz
   tar -xzf op-deployer.tar.gz
   chmod +x op-deployer
   ```

2. **Find the latest stable contract artifacts hash:**
   ```bash
   # Browse the standard.go file from the release tag:
   curl -s https://raw.githubusercontent.com/ethereum-optimism/optimism/op-deployer/vX.Y.Z/op-deployer/pkg/deployer/standard/standard.go | grep -A 20 "taggedReleases"
   # Look for the latest stable version (avoid beta/rc tags) and get its ContentHash
   ```

3. **Update intent.toml with the new artifacts:**
   - Set `l1ContractsLocator` and `l2ContractsLocator` to:
     `https://storage.googleapis.com/oplabs-contract-artifacts/artifacts-v1-<ContentHash>.tar.gz`
   - Ensure `configType = "custom"` for HTTP URLs

4. **Deploy with the new version:**
   ```bash
   rm state.json
   echo '{"version": 1}' > state.json
   ./op-deployer apply --workdir . --deployment-target genesis
   ./op-deployer inspect genesis --workdir . --outfile ./genesis.json 13
   ./op-deployer inspect rollup --workdir . --outfile ./rollup.json 13
   ```

**Note:** The ContentHash (not ArtifactsHash) is used in the HTTP URL.
