#!/usr/bin/env bash

FROM_ADDR="0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"

CHAIN_ID_HEX=$(curl -s -X POST http://localhost:8545 \
	-H "Content-Type: application/json" \
	-d '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}' | jq -r '.result')
CHAIN_ID=$((CHAIN_ID_HEX))

NONCE_HEX=$(curl -s -X POST http://localhost:8545 \
	-H "Content-Type: application/json" \
	-d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getTransactionCount\",\"params\":[\"$FROM_ADDR\",\"pending\"],\"id\":1}" | jq -r '.result')
NONCE=$((NONCE_HEX))

TX_PAYLOAD=$(cast mktx \
	--private-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
	0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
	--value 0.1ether --nonce "$NONCE" --gas-limit 21000 --gas-price 1gwei --chain "$CHAIN_ID")

# Change this to set the target. 8545 for playground reth, 18645 for buildernet vm rbuilder.
date +%H:%M:%S
SEND_RESULT=$(curl -s --fail-with-body -X POST http://localhost:18645 \
	-H "Content-Type: application/json" \
	-d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_sendRawTransaction\",\"params\":[\"$TX_PAYLOAD\"],\"id\":1}")
CURL_EXIT=$?

if [ $CURL_EXIT -ne 0 ]; then
	echo "Error: curl failed with exit code $CURL_EXIT"
	exit 1
fi

echo "Send result: $SEND_RESULT"

TX_HASH=$(echo "$SEND_RESULT" | jq -r '.result')

if [ -z "$TX_HASH" ] || [ "$TX_HASH" = "null" ]; then
	echo "Error: Failed to get transaction hash"
	echo "$SEND_RESULT" | jq .
	exit 1
fi

echo "TX_HASH: $TX_HASH"

echo "Waiting for receipt..."
while true; do
	RECEIPT=$(curl -s -X POST http://localhost:8545 \
		-H "Content-Type: application/json" \
		-d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getTransactionReceipt\",\"params\":[\"$TX_HASH\"],\"id\":1}")

	RESULT=$(echo "$RECEIPT" | jq -r '.result')
	if [ "$RESULT" != "null" ]; then
		echo "Receipt: $RECEIPT"
		break
	fi
	sleep 1
done
