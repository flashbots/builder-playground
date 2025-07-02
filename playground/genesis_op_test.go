package playground

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestOpGenesisIshtmus(t *testing.T) {
	data, err := os.ReadFile("./testcases/l2_genesis_ishtmus.json")
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	var opGenesisObj OpGenesis
	if err := json.Unmarshal(data, &opGenesisObj); err != nil {
		t.Fatalf("failed to unmarshal genesis: %v", err)
	}

	opBlock, err := toOpBlock(data)
	if err != nil {
		t.Fatalf("failed to convert to op block: %v", err)
	}

	expected := common.HexToHash("0x6c2f6ce3e748bd0b0717a48e5e3d223258a7d0135bc95f758fc90f6e44813ab9")
	if opBlock.Hash() != expected {
		t.Fatalf("expected hash %s, got %s", expected.Hex(), opBlock.Hash().Hex())
	}
}
