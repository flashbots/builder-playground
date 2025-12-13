package playground

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestOpGenesisJovian(t *testing.T) {
	data, err := os.ReadFile("./testcases/l2_genesis_jovian.json")
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

	expected := common.HexToHash("0x8b526e01dd4d0c4d2b9949bc88f7077171cd52481e64da8c54adde8c7e476e7a")
	if opBlock.Hash() != expected {
		t.Fatalf("expected hash %s, got %s", expected.Hex(), opBlock.Hash().Hex())
	}
}
