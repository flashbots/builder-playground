package main

import (
	"fmt"
	"os"

	mevboostrelay "github.com/phylaxsystems/builder-playground/mev-boost-relay"
	"github.com/spf13/cobra"
)

var (
	apiListenAddr        string
	apiListenPort        uint64
	beaconClientAddr     string
	validationServerAddr string
)

var rootCmd = &cobra.Command{
	Use:   "local-mev-boost-relay",
	Short: "",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMevBoostRelay()
	},
}

func main() {
	rootCmd.Flags().StringVar(&apiListenAddr, "api-listen-addr", "127.0.0.1", "")
	rootCmd.Flags().Uint64Var(&apiListenPort, "api-listen-port", 5555, "")
	rootCmd.Flags().StringVar(&beaconClientAddr, "beacon-client-addr", "http://localhost:3500", "")
	rootCmd.Flags().StringVar(&validationServerAddr, "validation-server-addr", "", "")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runMevBoostRelay() error {
	cfg := mevboostrelay.DefaultConfig()
	cfg.ApiListenAddr = apiListenAddr
	cfg.ApiListenPort = apiListenPort
	cfg.BeaconClientAddr = beaconClientAddr
	cfg.ValidationServerAddr = validationServerAddr

	relay, err := mevboostrelay.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create relay: %w", err)
	}

	return relay.Start()
}
