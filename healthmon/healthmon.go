package healthmon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	mevboostrelay "github.com/flashbots/builder-playground/mev-boost-relay"
	"github.com/flashbots/go-template/common"
	"github.com/flashbots/mev-boost-relay/beaconclient"
	mevRCommon "github.com/flashbots/mev-boost-relay/common"
	"github.com/go-chi/httplog/v2"
)

var isHealthy atomic.Bool

type Config struct {
	Chain            string
	URL              string
	Addr             string
	BlockTimeSeconds int
}

func Start(config *Config) {
	log := common.SetupLogger(&common.LoggingOpts{
		Version: common.Version,
	})

	updates := make(chan blockUpdate, 10)
	log.Info("Started", "chain", config.Chain, "url", config.URL)

	switch config.Chain {
	case "beacon":
		go monitorBeacon(log, context.Background(), config.URL, updates)
	case "execution":
		go monitorExecution(log, context.Background(), config.URL, updates)
	default:
		log.Error("Unknown chain", "chain", config.Chain)
		os.Exit(1)
	}

	go monitor(log, config.BlockTimeSeconds, context.Background(), updates)

	log.Info("Starting service server", "addr", config.Addr)

	http.HandleFunc("/ready", statusHandler)
	http.ListenAndServe(config.Addr, nil)
}

func statusHandler(w http.ResponseWriter, req *http.Request) {
	if isHealthy.Load() {
		io.WriteString(w, "OK")
	} else {
		w.WriteHeader(503)
		io.WriteString(w, "NOT READY")
	}
}

func setHealthy(healthy bool) {
	isHealthy.Store(healthy)
}

type monitorState struct {
	log              *httplog.Logger
	firstBlockUpdate *blockUpdate
	blockTimeSeconds int
	blockTimer       *time.Timer
}

func newMonitorState(log *httplog.Logger, blockTimeSeconds int) *monitorState {
	// this timer will start after the blocks are received and we can figure out the block time
	blockTimer := time.NewTimer(0)
	blockTimer.Stop()

	return &monitorState{
		log:              log,
		firstBlockUpdate: nil,
		blockTimeSeconds: blockTimeSeconds,
		blockTimer:       blockTimer,
	}
}

var wiggleRoomSeconds = 1

func (m *monitorState) handleUpdate(update blockUpdate) {
	m.log.Info("Processing block update", "number", update.Number, "timestamp", update.Timestamp)

	if m.firstBlockUpdate == nil {
		m.firstBlockUpdate = &update
	}

	if m.blockTimeSeconds == 0 {
		// if block time is not known, either:
		// - use the block time provided in the update (beacon)
		// - use the difference between the first and current block (execution)
		if update.BlockTime != 0 {
			m.log.Info("Using block time from update", "block time seconds", update.BlockTime)
			m.blockTimeSeconds = update.BlockTime
		} else if m.firstBlockUpdate != nil && update.Number > m.firstBlockUpdate.Number {
			blocktime := update.Timestamp.Sub(m.firstBlockUpdate.Timestamp)
			m.log.Info("Calculated block time from timestamps", "block time seconds", blocktime)
			m.blockTimeSeconds = int(blocktime.Seconds())
		}
	}

	if m.blockTimeSeconds != 0 {
		m.log.Info("Resetting block timer", "blockTimeSeconds", m.blockTimeSeconds)
		m.blockTimer.Reset(time.Duration(m.blockTimeSeconds+wiggleRoomSeconds) * time.Second)
	}
}

func monitor(log *httplog.Logger, blockTimeSeconds int, ctx context.Context, updates <-chan blockUpdate) {
	state := newMonitorState(log, blockTimeSeconds)

	for {
		select {
		case <-ctx.Done():
			return
		case update := <-updates:
			// receiving a block always means healthy since the node is producing blocks
			// and the unhealthy state is set during the block timer timeout
			setHealthy(true)

			state.handleUpdate(update)

		case <-state.blockTimer.C:
			log.Warn("Block timer expired, setting unhealthy")
			setHealthy(false)
		}
	}
}

type blockUpdate struct {
	Number    uint64
	Timestamp time.Time
	BlockTime int
}

func monitorBeacon(log *httplog.Logger, ctx context.Context, url string, updates chan<- blockUpdate) {
	bLog := mevRCommon.LogSetup(false, "info")
	beaconClient := beaconclient.NewProdBeaconInstance(bLog, url, url)

	var lastSlot *uint64
	var blockTime int

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
			sync, err := beaconClient.SyncStatus()
			if err != nil {
				log.Error("Failed to get beacon sync status", "err", err)
				continue
			}

			if sync.IsSyncing {
				log.Debug("Beacon node is syncing", "headSlot", sync.HeadSlot)
				continue
			}

			if blockTime == 0 {
				spec, err := mevboostrelay.GetSpec(url)
				if err != nil {
					log.Error("Failed to get beacon spec", "err", err)
				} else {
					blockTime = int(spec.SecondsPerSlot)
					log.Info("Fetched beacon spec", "blockTime", blockTime)
				}
			}

			if lastSlot == nil || *lastSlot < sync.HeadSlot {
				lastSlot = &sync.HeadSlot
				log.Info("New beacon block received", "slot", sync.HeadSlot)
				updates <- blockUpdate{Number: sync.HeadSlot, BlockTime: blockTime}
			}
		}
	}
}

func monitorExecution(log *httplog.Logger, ctx context.Context, url string, updates chan<- blockUpdate) {
	client, err := ethclient.Dial(url)
	if err != nil {
		log.Error("Failed to connect to execution client", "err", err)
		os.Exit(1)
	}

	getLatestBlock := func() (*types.Header, error) {
		// We use a manual RPC call instead of the Geth SDK's HeaderByNumber because
		// we query both OP and normal L1 clients which have different transaction types
		// that cannot be decoded with a single Geth SDK. The Geth SDK only returns blocks
		// with transactions fully decoded (not just hashes), so we call the RPC directly
		// to avoid transaction decoding issues.
		var raw json.RawMessage
		if err := client.Client().CallContext(ctx, &raw, "eth_getBlockByNumber", "latest", false); err != nil {
			return nil, err
		}

		// Decode header and transactions.
		var head *types.Header
		if err := json.Unmarshal(raw, &head); err != nil {
			return nil, err
		}
		// When the block is not found, the API returns JSON null.
		if head == nil {
			return nil, ethereum.NotFound
		}
		return head, nil
	}

	var lastBlock *uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
			sync, err := client.SyncProgress(ctx)
			if err != nil {
				log.Error("Failed to get execution sync progress", "err", err)
				continue
			}

			if sync != nil && !sync.Done() {
				log.Debug("Execution node is syncing", "currentBlock", sync.CurrentBlock, "highestBlock", sync.HighestBlock)
				continue
			}
			header, err := getLatestBlock()
			if err != nil {
				log.Error("Failed to get execution block number", "err", err)
				continue
			}
			num := header.Number.Uint64()
			if lastBlock == nil || num > *lastBlock {
				lastBlock = &num
				timestamp := time.Unix(int64(header.Time), 0)

				log.Info("New execution block received", "number", num)
				updates <- blockUpdate{Number: num, Timestamp: timestamp}
			}
		}
	}
}
