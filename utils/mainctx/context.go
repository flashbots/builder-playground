package mainctx

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var (
	sigCh              = make(chan os.Signal, 1)
	gracefulCtx        context.Context
	gracefulCtxCancel  context.CancelFunc
	forceKillCtx       context.Context
	forceKillCtxCancel context.CancelFunc
)

func init() {
	gracefulCtx, gracefulCtxCancel = context.WithCancel(context.Background())
	forceKillCtx, forceKillCtxCancel = context.WithCancel(context.Background())
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
				slog.Warn("received signal, shutting down gracefully... (interrupt 2 more times force kill)", "signal", sig)
				gracefulCtxCancel()
			} else if sigCount == 2 {
				slog.Warn("received signal again (interrupt 1 more time to force kill)", "signal", sig)
			} else if sigCount >= 3 {
				slog.Warn("force killing...")
				forceKillCtxCancel()
			}
		}
	}()
}

// Get returns a context that is aware of the interruption/termination signals
// received by the process. It is intended for graceful exits.
func Get() context.Context {
	return gracefulCtx
}

func GetForceKillCtx() context.Context {
	return forceKillCtx
}
