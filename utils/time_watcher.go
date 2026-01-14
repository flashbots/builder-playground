package utils

import (
	"log/slog"
	"time"
)

// StartTimer starts a timer with the given name and returns a function that prints the elapsed time when called.
// The elapsed time is only printed if debug or trace levels are used.
func StartTimer(name string) func() {
	start := time.Now()
	return func() {
		elapsed := time.Since(start)
		slog.Debug(name, "elapsed", elapsed)
	}
}
