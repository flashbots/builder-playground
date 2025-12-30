package healthmon

import (
	"testing"
	"time"

	"github.com/flashbots/go-template/common"
	"github.com/go-chi/httplog/v2"
	"github.com/stretchr/testify/assert"
)

func TestHealthmonMonitor_BlockTimeInUpdate(t *testing.T) {
	// health monitor detects block time from difference between first and current block
	m := newMonitorState(testLogger(), 0)

	m.handleUpdate(blockUpdate{
		Number:    1,
		BlockTime: 2,
	})

	assert.Equal(t, m.blockTimeSeconds, 2)
	waitToTrigger(t, m)
}

func TestHealthmonMonitor_BlockTimeDiff(t *testing.T) {
	// health monitor detects block time from difference between first and current block
	m := newMonitorState(testLogger(), 0)

	now := time.Now()
	now1 := now.Add(2 * time.Second)

	m.handleUpdate(blockUpdate{
		Number:    1,
		Timestamp: now,
	})

	// monitor does not have yet enough info to calculate block time
	assert.Equal(t, m.blockTimeSeconds, 0)

	m.handleUpdate(blockUpdate{
		Number:    2,
		Timestamp: now1,
	})

	assert.Equal(t, m.blockTimeSeconds, 2)
	waitToTrigger(t, m)
}

func TestHealthmonMonitor_ResetTimer(t *testing.T) {
	m := newMonitorState(testLogger(), 2)

	m.handleUpdate(blockUpdate{})
	waitToTrigger(t, m)

	m.handleUpdate(blockUpdate{})
	waitToTrigger(t, m)
}

func testLogger() *httplog.Logger {
	logger := common.SetupLogger(&common.LoggingOpts{
		Version: common.Version,
	})
	return logger
}

func waitToTrigger(t *testing.T, m *monitorState) {
	// this functions waits for wathever block time is specified in the monitor state
	select {
	case <-m.blockTimer.C:
	case <-time.After(time.Duration(m.blockTimeSeconds+1) * time.Second):
		t.Fatal("timeout waiting for block timer to trigger")
	}
}
