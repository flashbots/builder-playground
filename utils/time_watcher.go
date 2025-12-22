package utils

import (
	"log"
	"time"
)

var TraceMode bool

// StartTimer starts a timer with the given name and returns a function that prints the elapsed time when called.
// The elapsed time is only printed if TraceMode is enabled.
func StartTimer(name string) func() {
	start := time.Now()
	return func() {
		if TraceMode {
			elapsed := time.Since(start)
			log.Printf("[TRACE] %s: %v\n", name, elapsed)
		}
	}
}
