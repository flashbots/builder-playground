package playground

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/pathdb"
	"github.com/holiman/uint256"
)

var (
	// The L2 withdrawals contract predeploy address
	optimismL2ToL1MessagePasser = common.HexToAddress("0x4200000000000000000000000000000000000016")
)

type OpChainConfig struct {
	*params.ChainConfig

	IsthmusTime *uint64 `json:"isthmusTime,omitempty"` // Isthmus switch time (nil = no fork, 0 = already on Optimism Isthmus)
}

// isTimestampForked returns whether a fork scheduled at timestamp s is active
// at the given head timestamp. Whilst this method is the same as isBlockForked,
// they are explicitly separate for clearer reading.
func isTimestampForked(s *uint64, head uint64) bool {
	if s == nil {
		return false
	}
	return *s <= head
}

// IsOptimism returns whether the node is an optimism node or not.
func (c *OpChainConfig) IsOptimism() bool {
	return true
}

func (c *OpChainConfig) IsIsthmus(time uint64) bool {
	return isTimestampForked(c.IsthmusTime, time)
}

func (c *OpChainConfig) IsOptimismIsthmus(time uint64) bool {
	return c.IsOptimism() && c.IsIsthmus(time)
}

func (c *OpChainConfig) HasOptimismWithdrawalsRoot(blockTime uint64) bool {
	return c.IsOptimismIsthmus(blockTime)
}

// OpGenesis is the extension of the core.Genesis struct with additional fields
// for the OP stack. There are problems doing json.Unmarshal if we embed the core.Genesis struct
type OpGenesis struct {
	Config *OpChainConfig `json:"config"`
}

func toOpBlock(content []byte) (*types.Block, error) {
	var g core.Genesis
	if err := json.Unmarshal(content, &g); err != nil {
		return nil, err
	}
	var g1 OpGenesis
	if err := json.Unmarshal(content, &g1); err != nil {
		return nil, err
	}

	var stateRoot, storageRootMessagePasser common.Hash
	var err error
	if stateRoot, storageRootMessagePasser, err = hashAlloc(&g.Alloc, g.IsVerkle(), g1.Config.IsOptimismIsthmus(g.Timestamp)); err != nil {
		return nil, err
	}
	return g1.toBlockWithRoot(&g, stateRoot, storageRootMessagePasser), nil
}

// toBlockWithRoot constructs the genesis block with the given genesis state root.
func (g1 *OpGenesis) toBlockWithRoot(g *core.Genesis, stateRoot, storageRootMessagePasser common.Hash) *types.Block {
	opConfig := g1.Config

	head := &types.Header{
		Number:     new(big.Int).SetUint64(g.Number),
		Nonce:      types.EncodeNonce(g.Nonce),
		Time:       g.Timestamp,
		ParentHash: g.ParentHash,
		Extra:      g.ExtraData,
		GasLimit:   g.GasLimit,
		GasUsed:    g.GasUsed,
		BaseFee:    g.BaseFee,
		Difficulty: g.Difficulty,
		MixDigest:  g.Mixhash,
		Coinbase:   g.Coinbase,
		Root:       stateRoot,
	}
	if g.GasLimit == 0 {
		head.GasLimit = params.GenesisGasLimit
	}
	if g.Difficulty == nil {
		if g.Config != nil && g.Config.Ethash == nil {
			head.Difficulty = big.NewInt(0)
		} else if g.Mixhash == (common.Hash{}) {
			head.Difficulty = params.GenesisDifficulty
		}
	}
	if g.Config != nil && g.Config.IsLondon(common.Big0) {
		if g.BaseFee != nil {
			head.BaseFee = g.BaseFee
		} else {
			head.BaseFee = new(big.Int).SetUint64(params.InitialBaseFee)
		}
	}
	var withdrawals []*types.Withdrawal
	if conf := g.Config; conf != nil {
		num := big.NewInt(int64(g.Number))
		if conf.IsShanghai(num, g.Timestamp) {
			head.WithdrawalsHash = &types.EmptyWithdrawalsHash
			withdrawals = make([]*types.Withdrawal, 0)
		}
		if conf.IsCancun(num, g.Timestamp) {
			// EIP-4788: The parentBeaconBlockRoot of the genesis block is always
			// the zero hash. This is because the genesis block does not have a parent
			// by definition.
			head.ParentBeaconRoot = new(common.Hash)
			// EIP-4844 fields
			head.ExcessBlobGas = g.ExcessBlobGas
			head.BlobGasUsed = g.BlobGasUsed
			if head.ExcessBlobGas == nil {
				head.ExcessBlobGas = new(uint64)
			}
			if head.BlobGasUsed == nil {
				head.BlobGasUsed = new(uint64)
			}
		}
		if conf.IsPrague(num, g.Timestamp) {
			head.RequestsHash = &types.EmptyRequestsHash
		}
		// If Isthmus is active at genesis, set the WithdrawalRoot to the storage root of the L2ToL1MessagePasser contract.
		if opConfig.IsOptimismIsthmus(g.Timestamp) {
			if storageRootMessagePasser == (common.Hash{}) {
				// if there was no MessagePasser contract storage, set the WithdrawalsHash to the empty hash
				storageRootMessagePasser = types.EmptyWithdrawalsHash
			}
			head.WithdrawalsHash = &storageRootMessagePasser
		}
	}

	// keep a copy of the withdrawals hash because it gets overwritten in the next step
	withdrawalsHash := common.Hash{}
	copy(withdrawalsHash[:], head.WithdrawalsHash[:])

	block := types.NewBlock(head, &types.Body{Withdrawals: withdrawals}, nil, trie.NewStackTrie(nil))

	{
		// add the Ishtmus changes
		header := block.Header()
		if opConfig.HasOptimismWithdrawalsRoot(header.Time) {
			if withdrawals == nil || len(withdrawals) > 0 {
				panic(fmt.Sprintf("expected non-nil empty withdrawals operation list in Isthmus"))
			}
			header.WithdrawalsHash = &withdrawalsHash
		} else if withdrawals == nil {
			// pre-Canyon
			header.WithdrawalsHash = nil
		} else if len(withdrawals) == 0 {
			header.WithdrawalsHash = &types.EmptyWithdrawalsHash
		} else {
			hash := types.DeriveSha(types.Withdrawals(withdrawals), trie.NewStackTrie(nil))
			header.WithdrawalsHash = &hash
		}
		block = types.NewBlockWithHeader(header)
	}

	return block
}

// hashAlloc returns the following:
// * computed state root according to the genesis specification.
// * storage root of the L2ToL1MessagePasser contract.
// * error if any, when committing the genesis state (if so, state root and storage root will be empty).
func hashAlloc(ga *types.GenesisAlloc, isVerkle, isIsthmus bool) (common.Hash, common.Hash, error) {
	// If a genesis-time verkle trie is requested, create a trie config
	// with the verkle trie enabled so that the tree can be initialized
	// as such.
	var config *triedb.Config
	if isVerkle {
		config = &triedb.Config{
			PathDB:   pathdb.Defaults,
			IsVerkle: true,
		}
	}
	// Create an ephemeral in-memory database for computing hash,
	// all the derived states will be discarded to not pollute disk.
	emptyRoot := types.EmptyRootHash
	if isVerkle {
		emptyRoot = types.EmptyVerkleHash
	}
	db := rawdb.NewMemoryDatabase()
	statedb, err := state.New(emptyRoot, state.NewDatabase(triedb.NewDatabase(db, config), nil))
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	for addr, account := range *ga {
		if account.Balance != nil {
			statedb.AddBalance(addr, uint256.MustFromBig(account.Balance), tracing.BalanceIncreaseGenesisBalance)
		}
		statedb.SetCode(addr, account.Code)
		statedb.SetNonce(addr, account.Nonce, tracing.NonceChangeGenesis)
		for key, value := range account.Storage {
			statedb.SetState(addr, key, value)
		}
	}

	stateRoot, err := statedb.Commit(0, false, false)
	if err != nil {
		return common.Hash{}, common.Hash{}, err
	}
	// get the storage root of the L2ToL1MessagePasser contract
	var storageRootMessagePasser common.Hash
	if isIsthmus {
		storageRootMessagePasser = statedb.GetStorageRoot(optimismL2ToL1MessagePasser)
	}

	return stateRoot, storageRootMessagePasser, nil
}
