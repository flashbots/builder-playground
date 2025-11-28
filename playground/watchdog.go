package playground

import (
	"context"
	"fmt"
)

func RunWatchdog(out *output, services []*Service) error {
	watchdogErr := make(chan error, len(services))

	output, err := out.LogOutput("watchdog")
	if err != nil {
		return fmt.Errorf("failed to create log output: %w", err)
	}

	for _, s := range services {
		if watchdogFn := s.watchdogFn; watchdogFn != nil {
			go func() {
				if err := watchdogFn(output, s, context.Background()); err != nil {
					watchdogErr <- fmt.Errorf("service %s watchdog failed: %w", s.Name, err)
				}
			}()
		}
	}

	// If any of the watchdogs fail, we return the error
	if err := <-watchdogErr; err != nil {
		return fmt.Errorf("failed to run watchdog: %w", err)
	}
	return nil
}
