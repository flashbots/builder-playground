package mainctx

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSignalHandler_NewSignalHandler(t *testing.T) {
	r := require.New(t)
	sigCh := make(chan os.Signal, 1)
	sh := newSignalHandler(sigCh)

	r.NotNil(sh)
	r.NotNil(sh.gracefulCtx)
	r.NotNil(sh.forceKillCtx)
	r.Equal(0, sh.sigCount)

	// Contexts should not be cancelled initially
	select {
	case <-sh.gracefulCtx.Done():
		r.Fail("graceful context should not be cancelled initially")
	default:
	}

	select {
	case <-sh.forceKillCtx.Done():
		r.Fail("force kill context should not be cancelled initially")
	default:
	}
}

func TestSignalHandler_ContextsAreDifferent(t *testing.T) {
	r := require.New(t)
	// Send only one signal - graceful ctx should be cancelled, force kill should not
	sigCh := make(chan os.Signal, 1)
	sigCh <- testSignal{}
	close(sigCh)

	sh := newSignalHandler(sigCh)
	sh.handle()

	r.Equal(context.Canceled, sh.gracefulCtx.Err())
	r.NoError(sh.forceKillCtx.Err())
}

func TestSignalHandler_FirstSignalCancelsGracefulContext(t *testing.T) {
	r := require.New(t)
	sigCh := make(chan os.Signal, 1)
	sigCh <- testSignal{}
	close(sigCh)

	sh := newSignalHandler(sigCh)
	sh.handle()

	r.Equal(1, sh.sigCount)
	r.Equal(context.Canceled, sh.gracefulCtx.Err())
	r.NoError(sh.forceKillCtx.Err())
}

func TestSignalHandler_SecondSignalDoesNotCancelForceKill(t *testing.T) {
	r := require.New(t)
	sigCh := make(chan os.Signal, 2)
	sigCh <- testSignal{}
	sigCh <- testSignal{}
	close(sigCh)

	sh := newSignalHandler(sigCh)
	sh.handle()

	r.Equal(2, sh.sigCount)
	r.Equal(context.Canceled, sh.gracefulCtx.Err())
	r.NoError(sh.forceKillCtx.Err())
}

func TestSignalHandler_ThirdSignalCancelsForceKillContext(t *testing.T) {
	r := require.New(t)
	sigCh := make(chan os.Signal, 3)
	sigCh <- testSignal{}
	sigCh <- testSignal{}
	sigCh <- testSignal{}
	close(sigCh)

	sh := newSignalHandler(sigCh)
	sh.handle()

	r.Equal(3, sh.sigCount)
	r.Equal(context.Canceled, sh.gracefulCtx.Err())
	r.Equal(context.Canceled, sh.forceKillCtx.Err())
}

func TestGet(t *testing.T) {
	r := require.New(t)
	ctx := Get()
	r.NotNil(ctx)
}

func TestGetForceKillCtx(t *testing.T) {
	r := require.New(t)
	ctx := GetForceKillCtx()
	r.NotNil(ctx)
}

// testSignal implements os.Signal for testing purposes
type testSignal struct{}

func (testSignal) Signal()        {}
func (testSignal) String() string { return "test signal" }
