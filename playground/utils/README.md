## Updating for New op-deployer Releases / Regenerating Rollup Config

When a new op-deployer version is released, follow these steps:

1. **Download the latest op-deployer:**
   ```bash
   # Check releases at: https://github.com/ethereum-optimism/optimism/releases?q=op-deployer
   # Download for your platform, e.g.:
   curl -L https://github.com/ethereum-optimism/optimism/releases/download/op-deployer/vX.Y.Z/op-deployer-X.Y.Z-darwin-arm64.tar.gz -o op-deployer.tar.gz
   tar -xzf op-deployer.tar.gz
   chmod +x op-deployer
   ```

2. **Find the latest stable contract artifacts hash:**
   In https://github.com/ethereum-optimism/optimism/op-deployer/vX.Y.Z/op-deployer/pkg/deployer/upgrade
   Look for the latest version and get its ContentHash from the ArtifactsURL in the upgrade.go file

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
