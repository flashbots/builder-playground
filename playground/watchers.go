package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/flashbots/mev-boost-relay/beaconclient"
	mevRCommon "github.com/flashbots/mev-boost-relay/common"
)

func isChainProducingBlocks(ctx context.Context, elURL string) (bool, error) {
	rpcClient, err := rpc.Dial(elURL)
	if err != nil {
		return false, err
	}
	defer rpcClient.Close()

	clt := ethclient.NewClient(rpcClient)
	num, err := clt.BlockNumber(ctx)
	if err != nil {
		return false, err
	}
	return num > 0, nil
}

func waitForFirstBlock(ctx context.Context, elURL string, timeout time.Duration) error {
	rpcClient, err := rpc.Dial(elURL)
	if err != nil {
		fmt.Printf("  [%s] Failed to connect: %v\n", elURL, err)
		return err
	}
	defer rpcClient.Close()

	clt := ethclient.NewClient(rpcClient)
	fmt.Printf("  [%s] Connected, waiting for first block...\n", elURL)

	timeoutCh := time.After(timeout)
	checkCount := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeoutCh:
			return fmt.Errorf("timeout waiting for first block on %s", elURL)
		case <-time.After(500 * time.Millisecond):
			num, err := clt.BlockNumber(ctx)
			checkCount++
			if err != nil {
				if checkCount%10 == 0 {
					fmt.Printf("  [%s] Error getting block number: %v\n", elURL, err)
				}
				continue
			}
			if num > 0 {
				fmt.Printf("  [%s] First block detected: %d\n", elURL, num)
				return nil
			}
			if checkCount%10 == 0 {
				fmt.Printf("  [%s] Block number: %d (waiting for > 0)\n", elURL, num)
			}
		}
	}
}

func waitForChainAlive(ctx context.Context, logOutput io.Writer, beaconNodeURL string, timeout time.Duration) error {
	// Test that blocks are being produced
	log := mevRCommon.LogSetup(false, "info").WithField("context", "waitForChainAlive")
	log.Logger.Out = logOutput

	clt := beaconclient.NewProdBeaconInstance(log, beaconNodeURL, beaconNodeURL)

	// Subscribe to head events right away even if the connection has not been established yet
	// That is handled internally in the function already.
	// Otherwise, if we connect only when the first head slot happens we might miss some initial slots.
	ch := make(chan beaconclient.PayloadAttributesEvent)
	go clt.SubscribeToPayloadAttributesEvents(ch)

	{
		// If the chain has not started yet, wait for it to start.
		// Otherwise, the subscription will not return any data.
		bClient := beaconclient.NewMultiBeaconClient(log, []beaconclient.IBeaconInstance{
			clt,
		})

		isReady := func() bool {
			sync, err := bClient.BestSyncStatus()
			if err != nil {
				return false
			}
			return sync.HeadSlot >= 1
		}

		if !isReady() {
			syncTimeoutCh := time.After(timeout)
			for {
				if isReady() {
					break
				}
				select {
				case <-syncTimeoutCh:
					return fmt.Errorf("beacon client failed to start")
				case <-ctx.Done():
					return fmt.Errorf("timeout waiting for chain to start")
				default:
					time.Sleep(1 * time.Second)
				}
			}
		}
	}

	return nil
}

// validateProposerPayloads validates that payload attribute events are being broadcasted by the beacon node
// in the correct order without any missing slots.
func validateProposerPayloads(logOutput io.Writer, beaconNodeURL string) error {
	// Test that blocks are being produced
	log := mevRCommon.LogSetup(false, "info").WithField("context", "validateProposerPayloads")
	log.Logger.Out = logOutput

	clt := beaconclient.NewProdBeaconInstance(log, beaconNodeURL, beaconNodeURL)

	// We run this after 'waitForChainAlive' to ensure that the beacon node is ready to receive payloads.
	ch := make(chan beaconclient.PayloadAttributesEvent)
	go clt.SubscribeToPayloadAttributesEvents(ch)

	log.Infof("Chain is alive. Subscribing to head events")

	var lastSlot uint64
	for {
		select {
		case head := <-ch:
			log.Infof("Slot: %d Parent block number: %d", head.Data.ProposalSlot, head.Data.ParentBlockNumber)

			// If we are being notified of a new slot, validate that the slots are contiguous
			// Note that lighthouse might send multiple updates for the same slot.
			if lastSlot != 0 && lastSlot != head.Data.ProposalSlot && lastSlot+1 != head.Data.ProposalSlot {
				return fmt.Errorf("slot mismatch, expected %d, got %d", lastSlot+1, head.Data.ProposalSlot)
			}
			// if the network did not miss any initial slots, lighthouse will send payload attribute updates
			// of the form: (slot = slot, parent block number = slot - 2), (slot, slot - 1).
			// The -2 is in case we want to handle reorgs in the chain.
			// We need to validate that at least the difference between the parent block number and the slot is 2.
			if head.Data.ProposalSlot-head.Data.ParentBlockNumber > 2 {
				return fmt.Errorf("parent block too big %d", head.Data.ParentBlockNumber)
			}

			lastSlot = head.Data.ProposalSlot
		case <-time.After(20 * time.Second):
			return fmt.Errorf("timeout waiting for block")
		}
	}
}

func watchProposerPayloads(beaconNodeURL string) error {
	getProposerPayloadDelivered := func() ([]*mevRCommon.BidTraceV2JSON, error) {
		resp, err := http.Get(fmt.Sprintf("%s/relay/v1/data/bidtraces/proposer_payload_delivered", beaconNodeURL))
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		var payloadDeliveredList []*mevRCommon.BidTraceV2JSON
		if err := json.Unmarshal(data, &payloadDeliveredList); err != nil {
			return nil, err
		}
		return payloadDeliveredList, nil
	}

	// Wait for at least 10 seconds for Mev-boost to start
	timerC := time.After(10 * time.Second)
LOOP:
	for {
		select {
		case <-timerC:
			break
		case <-time.After(2 * time.Second):
			if _, err := getProposerPayloadDelivered(); err == nil {
				break LOOP
			}
		}
	}

	// This is not the most efficient solution since we are querying the endpoint for the full list of payloads
	// every 2 seconds. It should be fine for the kind of workloads expected to run.

	lastSlot := uint64(0)

	for {
		time.Sleep(2 * time.Second)

		vals, err := getProposerPayloadDelivered()
		if err != nil {
			fmt.Println("Error getting proposer payloads:", err)
			continue
		}

		for _, val := range vals {
			if val.Slot <= lastSlot {
				continue
			}

			fmt.Printf("Block Proposed: Slot: %d, Builder: %s, Block: %d\n", val.Slot, val.BuilderPubkey, val.BlockNumber)
			lastSlot = val.Slot
		}
	}
}

// watchChainHead watches the chain head and ensures that it is advancing
func watchChainHead(logOutput io.Writer, elURL string, blockTime time.Duration) error {
	log := mevRCommon.LogSetup(false, "info").WithField("context", "watchChainHead").WithField("el", elURL)
	log.Logger.Out = logOutput

	// add some wiggle room to block time
	blockTime = blockTime + 1*time.Second

	rpcClient, err := rpc.Dial(elURL)
	if err != nil {
		return err
	}

	var latestBlock *uint64
	clt := ethclient.NewClient(rpcClient)

	timeout := time.NewTimer(blockTime)
	defer timeout.Stop()

	for {
		select {
		case <-time.After(500 * time.Millisecond):
			num, err := clt.BlockNumber(context.Background())
			if err != nil {
				return err
			}
			if latestBlock != nil && num <= *latestBlock {
				continue
			}
			log.Infof("Chain head: %d", num)
			latestBlock = &num

			// Reset timeout since we saw a new block
			if !timeout.Stop() {
				<-timeout.C
			}
			timeout.Reset(blockTime)

		case <-timeout.C:
			return fmt.Errorf("chain head for %s not advancing", elURL)
		}
	}
}

type watchGroup struct {
	errCh chan error
}

func newWatchGroup() *watchGroup {
	return &watchGroup{
		errCh: make(chan error, 1),
	}
}

func (wg *watchGroup) watch(watch func() error) {
	go func() {
		wg.errCh <- watch()
	}()
}

func (wg *watchGroup) wait() error {
	return <-wg.errCh
}
