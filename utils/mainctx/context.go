package mainctx

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var (
	sigCh       = make(chan os.Signal, 1)
	sigAwareCtx context.Context
)

func init() {
	var cancel context.CancelFunc
	sigAwareCtx, cancel = context.WithCancel(context.Background())
	signal.Notify(sigCh,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		sig := <-sigCh
		slog.Warn("received signal", "signal", sig)
		cancel()
	}()
}

// Get returns a context that is aware of the interruption/termination signals received by the process.
func Get() context.Context {
	return sigAwareCtx
}
