package main

import (
	"fmt"
	"os"

	clproxy "github.com/phylaxsystems/builder-playground/cl-proxy"
	"github.com/spf13/cobra"
)

var (
	primaryBuilder   string
	secondaryBuilder string
	port             int
)

var rootCmd = &cobra.Command{
	Use:   "clproxy",
	Short: "",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCLProxy()
	},
}

func main() {
	rootCmd.Flags().StringVar(&primaryBuilder, "primary-builder", "http://localhost:8551", "")
	rootCmd.Flags().StringVar(&secondaryBuilder, "secondary-builder", "", "")
	rootCmd.Flags().IntVar(&port, "port", 5656, "")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runCLProxy() error {
	// Start the cl proxy
	cfg := &clproxy.Config{
		LogOutput: os.Stdout,
		Port:      uint64(port),
		Primary:   primaryBuilder,
		Secondary: secondaryBuilder,
	}

	clproxy, err := clproxy.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create cl proxy: %w", err)
	}
	return clproxy.Run()
}
