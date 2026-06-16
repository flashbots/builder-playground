#!/usr/bin/env bash
# spam the playground el with value transfers so blocks have real bodies.
# usage: ./spam.sh [txs_per_batch] [sleep_between_batches_s]
set -u

RPC="${RPC:-http://localhost:8545}"
# prefunded mnemonic account 1 (not the builder coinbase, which is account 0)
KEY="${KEY:-0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d}"
FROM="0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
BATCH="${1:-20}"
SLEEP="${2:-1}"

nonce=$(cast nonce --rpc-url "$RPC" "$FROM") || exit 1
echo "spamming from $FROM starting at nonce $nonce (batch=$BATCH sleep=${SLEEP}s)"

while true; do
  for _ in $(seq 1 "$BATCH"); do
    # vary the recipient so account trie leaves differ
    to=$(printf '0x%040x' $((RANDOM * RANDOM)))
    # real priority fee so the block has coinbase profit (the builder silently
    # drops zero-profit blocks with ProfitTooLow)
    cast send --async --rpc-url "$RPC" --private-key "$KEY" \
      --nonce "$nonce" --gas-limit 21000 \
      --gas-price 10gwei --priority-gas-price 2gwei \
      "$to" --value 1gwei >/dev/null &
    nonce=$((nonce + 1))
  done
  wait
  sleep "$SLEEP"
done
