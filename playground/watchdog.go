package playground

import (
	"context"
	"fmt"
	"io"
)

type ServiceWatchdog interface {
	Watchdog(out io.Writer, instance *instance, ctx context.Context) error
}

func RunWatchdog(out *output, instances []*instance) error {
	watchdogErr := make(chan error, len(instances))

	output, err := out.LogOutput("watchdog")
	if err != nil {
		return fmt.Errorf("failed to create log output: %w", err)
	}

	for _, s := range instances {
		if watchdogFn, ok := s.component.(ServiceWatchdog); ok {
			go func() {
				if err := watchdogFn.Watchdog(output, s, context.Background()); err != nil {
					watchdogErr <- fmt.Errorf("service %s watchdog failed: %w", s.service.Name, err)
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
