package mainctx

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var (
	sigCh            = make(chan os.Signal, 1)
	sigAwareCtx      context.Context
	forceKillHandler func()
)

// RegisterForceKillHandler sets a callback that will be invoked on the third interrupt signal.
// This allows session-specific cleanup (e.g., killing only containers from current session).
func RegisterForceKillHandler(handler func()) {
	forceKillHandler = handler
}

func init() {
	var cancel context.CancelFunc
	sigAwareCtx, cancel = context.WithCancel(context.Background())
	signal.Notify(sigCh,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		sigCount := 0
		for sig := range sigCh {
			sigCount++
			if sigCount == 1 {
				slog.Warn("received signal, shutting down gracefully... (interrupt 2 more times to force kill)", "signal", sig)
				cancel()
			} else if sigCount == 2 {
				slog.Warn("received signal again (interrupt 1 more time to force kill)", "signal", sig)
			} else if sigCount >= 3 {
				slog.Warn("force killing containers and exiting...")
				if forceKillHandler != nil {
					forceKillHandler()
				}
				os.Exit(1)
			}
		}
	}()
}

// Get returns a context that is aware of the interruption/termination signals received by the process.
func Get() context.Context {
	return sigAwareCtx
}
