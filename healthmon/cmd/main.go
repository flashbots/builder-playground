package main

import (
	"flag"

	"github.com/flashbots/builder-playground/healthmon"
)

func main() {
	var config healthmon.Config

	flag.StringVar(&config.Chain, "chain", "", "Type of ethereum chain to monitor (beacon or execution)")
	flag.StringVar(&config.URL, "url", "", "Full node URL (e.g., http://localhost:8545)")
	flag.StringVar(&config.Addr, "service.addr", "localhost:21171", "Address for the health check service to listen on (e.g., ':21171')")
	flag.IntVar(&config.BlockTimeSeconds, "blocktime", 0, "expected block time in seconds (optional)")
	flag.Parse()

	healthmon.Start(&config)
}
