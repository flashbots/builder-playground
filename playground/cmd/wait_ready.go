package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/flashbots/builder-playground/playground"
	"github.com/spf13/cobra"
)

var waitReadyURL string
var waitReadyTimeout time.Duration
var waitReadyInterval time.Duration

var WaitReadyCmd = &cobra.Command{
	Use:   "wait-ready",
	Short: "Wait for the network to be ready for transactions",
	RunE: func(cmd *cobra.Command, args []string) error {
		return waitForReady()
	},
}

func InitWaitReadyCmd() {
	WaitReadyCmd.Flags().StringVar(&waitReadyURL, "url", "http://localhost:8080/readyz", "readyz endpoint URL")
	WaitReadyCmd.Flags().DurationVar(&waitReadyTimeout, "timeout", 60*time.Second, "maximum time to wait")
	WaitReadyCmd.Flags().DurationVar(&waitReadyInterval, "interval", 1*time.Second, "poll interval")
}

func waitForReady() error {
	fmt.Printf("Waiting for %s (timeout: %s, interval: %s)\n", waitReadyURL, waitReadyTimeout, waitReadyInterval)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-sig
		cancel()
	}()

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	deadline := time.Now().Add(waitReadyTimeout)
	attempt := 0

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("interrupted")
		default:
		}

		attempt++
		elapsed := time.Since(deadline.Add(-waitReadyTimeout))

		resp, err := client.Get(waitReadyURL)
		if err != nil {
			fmt.Printf("  [%s] Attempt %d: connection error: %v\n", elapsed.Truncate(time.Second), attempt, err)
			time.Sleep(waitReadyInterval)
			continue
		}

		var readyzResp playground.ReadyzResponse
		if err := json.NewDecoder(resp.Body).Decode(&readyzResp); err != nil {
			resp.Body.Close()
			fmt.Printf("  [%s] Attempt %d: failed to parse response: %v\n", elapsed.Truncate(time.Second), attempt, err)
			time.Sleep(waitReadyInterval)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK && readyzResp.Ready {
			fmt.Printf("  [%s] Ready! (200 OK)\n", elapsed.Truncate(time.Second))
			return nil
		}

		errMsg := ""
		if readyzResp.Error != "" {
			errMsg = fmt.Sprintf(" - %s", readyzResp.Error)
		}
		fmt.Printf("  [%s] Attempt %d: %d %s%s\n", elapsed.Truncate(time.Second), attempt, resp.StatusCode, http.StatusText(resp.StatusCode), errMsg)
		time.Sleep(waitReadyInterval)
	}

	return fmt.Errorf("timeout waiting for readyz after %s", waitReadyTimeout)
}
