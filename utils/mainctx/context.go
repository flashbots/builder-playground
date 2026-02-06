package mainctx

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

type signalHandler struct {
	sigCh              chan os.Signal
	sigCount           int
	gracefulCtx        context.Context
	gracefulCtxCancel  context.CancelFunc
	forceKillCtx       context.Context
	forceKillCtxCancel context.CancelFunc
}

func newSignalHandler(sigCh chan os.Signal) *signalHandler {
	gracefulCtx, gracefulCtxCancel := context.WithCancel(context.Background())
	forceKillCtx, forceKillCtxCancel := context.WithCancel(context.Background())
	return &signalHandler{
		sigCh:              sigCh,
		sigCount:           0,
		gracefulCtx:        gracefulCtx,
		gracefulCtxCancel:  gracefulCtxCancel,
		forceKillCtx:       forceKillCtx,
		forceKillCtxCancel: forceKillCtxCancel,
	}
}

func (sh *signalHandler) handle() {
	for sig := range sh.sigCh {
		sh.sigCount++
		switch sh.sigCount {
		case 1:
			slog.Warn("received signal, shutting down gracefully... (interrupt 2 more times force kill)", "signal", sig)
			sh.gracefulCtxCancel()
		case 2:
			slog.Warn("received signal again (interrupt 1 more time to force kill)", "signal", sig)
		case 3:
			slog.Warn("force killing...")
			sh.forceKillCtxCancel()
		}
	}
}

var sigHandler *signalHandler

func init() {
	sigCh := make(chan os.Signal, 1)
	sigHandler = newSignalHandler(sigCh)
	signal.Notify(sigCh,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go sigHandler.handle()
}

// Get returns a context that is aware of the interruption/termination signals
// received by the process. It is intended for graceful exits.
func Get() context.Context {
	return sigHandler.gracefulCtx
}

// GetForceKillCtx returns the context that is aware of the interruption/termination signals
// received by the process. It is intended for force kills.
func GetForceKillCtx() context.Context {
	return sigHandler.forceKillCtx
}

// IsExiting returns true if the process has received an exit signal.
func IsExiting() bool {
	select {
	case <-sigHandler.gracefulCtx.Done():
		return true
	case <-sigHandler.forceKillCtx.Done():
		return true
	default:
		return false
	}
}
