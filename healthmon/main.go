package healthmon

import (
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/flashbots/mev-boost-relay/beaconclient"
	mevRCommon "github.com/flashbots/mev-boost-relay/common"
)

var chain string
var url string
var servicePort string
var serviceAddr string
var isHealthy atomic.Bool

func main() {
	flag.StringVar(&chain, "chain", "", "")
	flag.StringVar(&url, "url", "", "")
	flag.StringVar(&servicePort, "service.port", "", "")
	flag.StringVar(&serviceAddr, "service.addr", "", "")
	flag.Parse()

	updates := make(chan blockUpdate, 10)

	switch chain {
	case "beacon":
		go monitorBeacon(context.Background(), updates)
	case "execution":
		go monitorExecution(context.Background(), updates)
	default:
		log.Fatalf("chain '%s' not known", chain)
	}

	go monitor(context.Background(), updates)

	http.HandleFunc("/ready", statusHandler)
	http.ListenAndServe(serviceAddr, nil)
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

const maxBlockTime = 13 * time.Second

func monitor(ctx context.Context, updates <-chan blockUpdate) {

}

type blockUpdate struct {
	Number uint64
}

func monitorBeacon(ctx context.Context, updates chan<- blockUpdate) {
	log := mevRCommon.LogSetup(false, "info").WithField("context", "waitForChainAlive")
	beaconClient := beaconclient.NewProdBeaconInstance(log, url, url)

	var lastSlot uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
			sync, err := beaconClient.SyncStatus()
			if err != nil {
				continue
			}

			if sync.HeadSlot > lastSlot {
				lastSlot = sync.HeadSlot
				updates <- blockUpdate{Number: sync.HeadSlot}
			}
		}
	}
}

func monitorExecution(ctx context.Context, updates chan<- blockUpdate) {
	client, err := ethclient.Dial(url)
	if err != nil {
		log.Fatal(err)
	}

	var lastBlock uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
			num, err := client.BlockNumber(ctx)
			if err != nil {
				continue
			}
			if num > lastBlock {
				lastBlock = num
				updates <- blockUpdate{Number: num}
			}
		}
	}
}
