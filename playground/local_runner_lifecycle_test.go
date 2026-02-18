package playground

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLocalRunner_LifecycleService_InitCommands(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lifecycle-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	out := &output{sessionDir: tmpDir}

	// Create a minimal LocalRunner - no Docker client needed for lifecycle
	runner := &LocalRunner{
		out:               out,
		lifecycleServices: []*lifecycleServiceInfo{},
	}

	// Create a service with init commands that create files
	testFile := filepath.Join(tmpDir, "init-test.txt")
	svc := &Service{
		Name:           "test-lifecycle",
		LifecycleHooks: true,
		Init: []string{
			"echo 'init1' > " + testFile,
			"echo 'init2' >> " + testFile,
		},
	}

	err = runner.startWithLifecycleHooks(context.Background(), svc)
	require.NoError(t, err)

	// Verify init commands ran
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "init1")
	require.Contains(t, string(content), "init2")

	// Verify service was tracked for stop commands
	require.Len(t, runner.lifecycleServices, 1)
	require.Equal(t, "test-lifecycle", runner.lifecycleServices[0].svc.Name)
}

func TestLocalRunner_LifecycleService_InitFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lifecycle-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	out := &output{sessionDir: tmpDir}

	runner := &LocalRunner{
		out:               out,
		lifecycleServices: []*lifecycleServiceInfo{},
	}

	svc := &Service{
		Name:           "test-lifecycle",
		LifecycleHooks: true,
		Init: []string{
			"exit 1", // This will fail
		},
	}

	err = runner.startWithLifecycleHooks(context.Background(), svc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "init command failed")
}

func TestLocalRunner_LifecycleService_StartCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lifecycle-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	out := &output{sessionDir: tmpDir}

	runner := &LocalRunner{
		out:               out,
		handles:           []*exec.Cmd{},
		lifecycleServices: []*lifecycleServiceInfo{},
	}

	startFile := filepath.Join(tmpDir, "start-ran.txt")
	svc := &Service{
		Name:           "test-lifecycle",
		LifecycleHooks: true,
		Start:          "echo 'started' > " + startFile,
	}

	err = runner.startWithLifecycleHooks(context.Background(), svc)
	require.NoError(t, err)

	// Give the goroutine time to run
	require.Eventually(t, func() bool {
		_, err := os.Stat(startFile)
		return err == nil
	}, 2*time.Second, 100*time.Millisecond)

	content, err := os.ReadFile(startFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "started")

	// Verify handle was tracked
	require.Len(t, runner.handles, 1)
}

func TestLocalRunner_LifecycleService_InitOnly(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lifecycle-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	out := &output{sessionDir: tmpDir}

	runner := &LocalRunner{
		out:               out,
		handles:           []*exec.Cmd{},
		lifecycleServices: []*lifecycleServiceInfo{},
	}

	initFile := filepath.Join(tmpDir, "init-only.txt")
	svc := &Service{
		Name:           "test-lifecycle",
		LifecycleHooks: true,
		Init: []string{
			"echo 'init-only' > " + initFile,
		},
	}

	err = runner.startWithLifecycleHooks(context.Background(), svc)
	require.NoError(t, err)

	// Verify init ran
	content, err := os.ReadFile(initFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "init-only")

	// No start command, so no handle should be tracked
	require.Len(t, runner.handles, 0)
}

func TestLocalRunner_StopCommands(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lifecycle-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	out := &output{sessionDir: tmpDir}

	stopFile := filepath.Join(tmpDir, "stop.txt")
	svc := &Service{
		Name:           "test-lifecycle",
		LifecycleHooks: true,
		Init:           []string{"echo init"},
		Stop: []string{
			"echo 'stop1' > " + stopFile,
			"echo 'stop2' >> " + stopFile,
		},
	}

	runner := &LocalRunner{
		out:               out,
		lifecycleServices: []*lifecycleServiceInfo{{svc: svc}},
	}

	// Run all stop commands
	runner.runAllLifecycleStopCommands()

	// Verify stop commands ran
	content, err := os.ReadFile(stopFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "stop1")
	require.Contains(t, string(content), "stop2")
}

func TestLocalRunner_StopCommands_ContinueOnError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lifecycle-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	out := &output{sessionDir: tmpDir}

	stopFile := filepath.Join(tmpDir, "stop.txt")
	svc := &Service{
		Name:           "test-lifecycle",
		LifecycleHooks: true,
		Init:           []string{"echo init"},
		Stop: []string{
			"exit 1",                         // This fails
			"echo 'continued' > " + stopFile, // But this should still run
		},
	}

	runner := &LocalRunner{
		out:               out,
		lifecycleServices: []*lifecycleServiceInfo{{svc: svc}},
	}

	// Run all stop commands - should not panic or stop on error
	runner.runAllLifecycleStopCommands()

	// Verify second stop command still ran despite first failing
	content, err := os.ReadFile(stopFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "continued")
}
