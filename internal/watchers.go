package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/flashbots/mev-boost-relay/beaconclient"
	mevRCommon "github.com/flashbots/mev-boost-relay/common"
)

// validateProposerPayloads validates that payload attribute events are being broadcasted by the beacon node
// in the correct order without any missing slots.
func validateProposerPayloads(beaconNodeURL string) error {
	// Test that blocks are being produced
	log := mevRCommon.LogSetup(false, "info")
	log.Logger.Out = io.Discard

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
			syncTimeoutCh := time.After(30 * time.Second)
			for {
				if isReady() {
					break
				}
				select {
				case <-syncTimeoutCh:
					return fmt.Errorf("beacon client failed to start")
				default:
					time.Sleep(1 * time.Second)
				}
			}
		}
	}

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

func watchProposerPayloads(beaconNodeURL string) {
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
