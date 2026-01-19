package playground

import (
	"encoding/json"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

func toOpBlock(content []byte) (*types.Block, error) {
	var g core.Genesis
	if err := json.Unmarshal(content, &g); err != nil {
		return nil, err
	}
	// Use op-geth's built-in ToBlock() which handles all Optimism-specific fields
	return g.ToBlock(), nil
}
