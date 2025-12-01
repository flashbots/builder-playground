#!/usr/bin/env node

/**
 * Generate L2 predeploy JSON for EntryPoint v0.7
 *
 * This script extracts the runtime bytecode from the compiled EntryPoint contract
 * and outputs a JSON object suitable for use as an L2 predeploy definition.
 * It is only necessary to run this script when the EntryPoint contract is updated.
 *
 * Setup:
 * - Make sure that https://github.com/eth-infinitism/account-abstraction/tree/develop is cloned and at the same folder level as this repository.
 * - Run `yarn install` in the account-abstraction folder.
 * - Run `yarn compile` in the account-abstraction folder.
 * - Run this script: node ./scripts/generate-entrypoint-json.js
 * - This will overwrite the utils/entrypoint_v0.7.json file with the new predeploy JSON.
 */

const fs = require('fs');
const path = require('path');

// Canonical EntryPoint v0.7 address
// This is the deterministic singleton address deployed via CREATE2

const loaderConfig = {
  'v0.7': {
    address: '0x0000000071727De22E5E9d8BAf0edAc6f37da032',
    path: path.join(__dirname, '..', '..', 'account-abstraction', 'artifacts', 'contracts', 'core', 'EntryPoint.sol', 'EntryPoint.json'),
  },
}

function main() {
  // Read the compiled artifact
  for (const [version, config] of Object.entries(loaderConfig)) {
    // Avoid shadowing the Node.js `path` module by renaming the config field locally
    const { address, path: artifactPath } = config;
    if (!fs.existsSync(artifactPath)) {
      console.error(`Error: Artifact not found at ${artifactPath}`);
      console.error('Please run "yarn compile" first.');
      process.exit(1);
    }

    const artifact = JSON.parse(fs.readFileSync(artifactPath, 'utf8'));

  // Extract the deployed bytecode (runtime bytecode)
  // The artifact contains both "bytecode" (creation code) and "deployedBytecode" (runtime code)
  const runtimeBytecode = artifact.deployedBytecode;

  if (!runtimeBytecode || runtimeBytecode === '0x') {
    console.error('Error: No deployed bytecode found in artifact');
    process.exit(1);
  }

  // Construct the predeploy JSON object
  const predeploy = {
    address: address,
    balance: "0x0",
    nonce: "0x1",
    code: runtimeBytecode,
    storage: {}
  };

  // Output the JSON to the utils/entrypoint_v0.7.json file
  fs.writeFileSync(path.join(__dirname, '..', 'playground', 'utils', `entrypoint_${version}.json`), JSON.stringify(predeploy, null, 2));
  }
}

main();
