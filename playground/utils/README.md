# L2 Genesis Files

Genesis configuration files for the OpStack recipe, generated using [op-deployer](https://github.com/ethereum-optimism/optimism/releases?q=op-deployer).

## Usage

```bash
./generate-genesis.sh              # Generate both isthmus and jovian
./generate-genesis.sh isthmus      # Generate isthmus only
./generate-genesis.sh jovian       # Generate jovian only
```

## Files

- `intent.toml` - op-deployer configuration (input)
- `genesis-{fork}.json` - L2 genesis (output)
- `rollup-{fork}.json` - Rollup config (output)
- `state-{fork}.json` - L1 contract state (output)

## Configuration

| Setting | Value |
|---------|-------|
| op-deployer (isthmus) | v0.4.7 |
| op-deployer (jovian) | v0.5.2 |
| L2 Chain ID | 13 |

Override via environment: `OP_DEPLOYER_VERSION`, `L2_CHAIN_ID`
