package mainctx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGet(t *testing.T) {
	r := require.New(t)
	ctx := Get()
	r.NotNil(ctx, "Get() returned nil context")

	// Verify the context is not already cancelled (before any signals)
	select {
	case <-ctx.Done():
		r.Fail("graceful context should not be cancelled initially")
	default:
		// expected
	}
}

func TestGetForceKillCtx(t *testing.T) {
	r := require.New(t)
	ctx := GetForceKillCtx()
	r.NotNil(ctx, "GetForceKillCtx() returned nil context")

	// Verify the context is not already cancelled (before any signals)
	select {
	case <-ctx.Done():
		r.Fail("force kill context should not be cancelled initially")
	default:
		// expected
	}
}

func TestContextsAreDifferent(t *testing.T) {
	r := require.New(t)
	graceful := Get()
	forceKill := GetForceKillCtx()

	r.NotEqual(graceful, forceKill, "graceful and force kill contexts should be different")
}

func TestContextsAreConsistent(t *testing.T) {
	r := require.New(t)
	// Multiple calls should return the same context
	ctx1 := Get()
	ctx2 := Get()
	r.Equal(ctx1, ctx2, "Get() should return the same context on multiple calls")

	fk1 := GetForceKillCtx()
	fk2 := GetForceKillCtx()
	r.Equal(fk1, fk2, "GetForceKillCtx() should return the same context on multiple calls")
}

func TestGracefulContextCancellation(t *testing.T) {
	r := require.New(t)
	// Send a signal to trigger graceful shutdown
	sigCh <- testSignal{}

	ctx := Get()
	select {
	case <-ctx.Done():
		r.Equal(context.Canceled, ctx.Err())
	case <-time.After(time.Second * 10):
		r.Fail("graceful context should be cancelled after first signal")
	}

	// Force kill context should still be active after first signal
	fkCtx := GetForceKillCtx()
	select {
	case <-fkCtx.Done():
		r.Fail("force kill context should not be cancelled after first signal")
	default:
		// expected
	}
}

func TestSecondSignalDoesNotCancelForceKill(t *testing.T) {
	r := require.New(t)
	// Send second signal
	sigCh <- testSignal{}
	sigCh <- testSignal{}

	// Force kill context should still be active after second signal
	fkCtx := GetForceKillCtx()
	select {
	case <-fkCtx.Done():
		r.Fail("force kill context should not be cancelled after second signal")
	default:
		// expected
	}
}

func TestForceKillContextCancellation(t *testing.T) {
	r := require.New(t)
	// Send third signal
	sigCh <- testSignal{}
	sigCh <- testSignal{}
	sigCh <- testSignal{}

	// Force kill context should be cancelled
	fkCtx := GetForceKillCtx()
	select {
	case <-fkCtx.Done():
		r.Equal(context.Canceled, fkCtx.Err())
	case <-time.After(time.Second * 10):
		r.Fail("force kill context should be cancelled after third signal")
	}
}

// testSignal implements os.Signal for testing purposes
type testSignal struct{}

func (testSignal) Signal()        {}
func (testSignal) String() string { return "test signal" }
