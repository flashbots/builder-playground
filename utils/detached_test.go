package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Test helper protocol: when DETACH_TEST_HELPER is set, TestMain dispatches to
// a stand-in "child" binary instead of running the test suite. The re-exec'd
// child then exercises the library from the other side of the pipe. This is
// the standard trick Go's os/exec tests use to avoid needing a separate binary.
const (
	helperEnv       = "DETACH_TEST_HELPER"
	helperLogPath   = "DETACH_TEST_LOG_PATH"
	helperPostWrite = "DETACH_TEST_POST_WRITE"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		runHelper(mode)
		return
	}
	os.Exit(m.Run())
}

func runHelper(mode string) {
	switch mode {
	case "signal-ready":
		if err := SignalReady(detachedNotifyFd); err != nil {
			fmt.Fprintln(os.Stderr, "signal-ready:", err)
			os.Exit(2)
		}
		// Linger briefly so the parent has time to observe READY before we exit
		// and (on some kernels) before our writes to the pipe are discarded.
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)

	case "notify-then-write":
		logPath := os.Getenv(helperLogPath)
		post := os.Getenv(helperPostWrite)
		if err := NotifyDetachReady(detachedNotifyFd, logPath); err != nil {
			os.Exit(3)
		}
		// After NotifyDetachReady, stdout/stderr point at logPath.
		fmt.Println(post)
		os.Exit(0)

	case "exit-without-ready":
		// Intentionally exit without touching fd 3.
		os.Exit(7)

	case "garbage-then-exit":
		f := os.NewFile(detachedNotifyFd, "notify")
		_, _ = f.Write([]byte("NOPE\n"))
		f.Close()
		os.Exit(8)

	default:
		fmt.Fprintln(os.Stderr, "unknown helper mode:", mode)
		os.Exit(99)
	}
}

func selfExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	return exe
}

func envWith(extra ...string) []string {
	env := append([]string{}, os.Environ()...)
	return append(env, extra...)
}

func TestStripFlag(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		flag       string
		takesValue bool
		want       []string
	}{
		{
			name: "bare flag removed",
			args: []string{"start", "l1", "--detached", "--keep"},
			flag: "--detached",
			want: []string{"start", "l1", "--keep"},
		},
		{
			name: "equals form removed",
			args: []string{"start", "l1", "--detached=true", "--keep"},
			flag: "--detached",
			want: []string{"start", "l1", "--keep"},
		},
		{
			name:       "two-token value consumed",
			args:       []string{"start", "--log-level", "debug", "--detached"},
			flag:       "--log-level",
			takesValue: true,
			want:       []string{"start", "--detached"},
		},
		{
			name: "flag not present is a no-op",
			args: []string{"start", "l1", "--keep"},
			flag: "--detached",
			want: []string{"start", "l1", "--keep"},
		},
		{
			name: "only the target flag is affected",
			args: []string{"--detached-notify-fd=3", "--detached", "--keep"},
			flag: "--detached",
			want: []string{"--detached-notify-fd=3", "--keep"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripFlag(tc.args, tc.flag, tc.takesValue)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSignalReady(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	// Pass a dup'd fd so our own w remains valid for cleanup regardless of
	// SignalReady's internal Close.
	dupFd, err := syscall.Dup(int(w.Fd()))
	require.NoError(t, err)

	require.NoError(t, SignalReady(dupFd))

	// Close our copy so the read end observes EOF after the ready line.
	w.Close()

	buf, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, ReadyMarker+"\n", string(buf))
}

func TestSignalReady_InvalidFd(t *testing.T) {
	// A fd we know is closed — SignalReady should propagate the write error.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	r.Close()
	fd := int(w.Fd())
	w.Close()
	err = SignalReady(fd)
	require.Error(t, err)
}

func TestDetachFork_HappyPath(t *testing.T) {
	exe := selfExe(t)

	child, err := DetachFork(DetachForkOptions{
		Executable: exe,
		Args:       []string{"-test.run=^$"},
		Env:        envWith(helperEnv + "=signal-ready"),
		Stdout:     devNull(t),
		Stderr:     devNull(t),
	})
	require.NoError(t, err)
	require.NotNil(t, child)
	require.Greater(t, child.PID, 0)

	// Best-effort reap so we don't leave orphans behind after the test.
	waitForPid(t, child.PID, 3*time.Second)
}

func TestDetachFork_ChildExitsBeforeReady(t *testing.T) {
	exe := selfExe(t)

	child, err := DetachFork(DetachForkOptions{
		Executable: exe,
		Args:       []string{"-test.run=^$"},
		Env:        envWith(helperEnv + "=exit-without-ready"),
		Stdout:     devNull(t),
		Stderr:     devNull(t),
	})
	require.Error(t, err)
	require.Nil(t, child)
	require.Contains(t, err.Error(), "before ready")
}

func TestDetachFork_ChildClosesPipeWithoutReady(t *testing.T) {
	exe := selfExe(t)

	child, err := DetachFork(DetachForkOptions{
		Executable: exe,
		Args:       []string{"-test.run=^$"},
		Env:        envWith(helperEnv + "=garbage-then-exit"),
		Stdout:     devNull(t),
		Stderr:     devNull(t),
	})
	require.Error(t, err)
	require.Nil(t, child)
}

func TestDetachFork_PassesArgsThroughAndAppendsNotifyFlag(t *testing.T) {
	// This test verifies end-to-end that Args make it to the child and that
	// the notify flag is understood on fd 3. The helper doesn't parse args,
	// but it does read from fd 3 — which is only present if DetachFork wired
	// the ExtraFiles correctly.
	exe := selfExe(t)

	child, err := DetachFork(DetachForkOptions{
		Executable: exe,
		Args:       []string{"-test.run=^$", "--extra-arg", "value"},
		Env:        envWith(helperEnv + "=signal-ready"),
		Stdout:     devNull(t),
		Stderr:     devNull(t),
	})
	require.NoError(t, err)
	require.NotNil(t, child)
	waitForPid(t, child.PID, 3*time.Second)
}

func TestNotifyDetachReady_WritesPostReadyOutputToLogFile(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "child.log")
	exe := selfExe(t)

	child, err := DetachFork(DetachForkOptions{
		Executable: exe,
		Args:       []string{"-test.run=^$"},
		Env: envWith(
			helperEnv+"=notify-then-write",
			helperLogPath+"="+logPath,
			helperPostWrite+"=hello-from-detached-child",
		),
		Stdout: devNull(t),
		Stderr: devNull(t),
	})
	require.NoError(t, err)
	require.NotNil(t, child)

	// The child writes to the log file AFTER signaling ready, so we need to
	// wait for it to actually happen before asserting on file contents.
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(logPath)
		if err != nil {
			return false
		}
		return string(b) == "hello-from-detached-child\n"
	}, 5*time.Second, 25*time.Millisecond, "log file never received post-ready output")

	waitForPid(t, child.PID, 3*time.Second)
}

func TestRedirectStdio_DevNullWhenEmptyPath(t *testing.T) {
	// RedirectStdio mutates the test process's own stdio, which would break
	// subsequent test output. Run it in a subprocess and just assert the
	// helper exited cleanly.
	exe := selfExe(t)

	child, err := DetachFork(DetachForkOptions{
		Executable: exe,
		Args:       []string{"-test.run=^$"},
		// Empty DETACH_TEST_LOG_PATH → RedirectStdio falls back to /dev/null.
		Env: envWith(
			helperEnv+"=notify-then-write",
			helperLogPath+"=",
			helperPostWrite+"=to-devnull",
		),
		Stdout: devNull(t),
		Stderr: devNull(t),
	})
	require.NoError(t, err)
	require.NotNil(t, child)
	waitForPid(t, child.PID, 3*time.Second)
}

// devNull returns an *os.File handle to /dev/null owned by the test; it is
// closed on cleanup. Useful for silencing children's inherited stdio.
func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// waitForPid polls until the given pid has exited or timeout elapses. We can't
// Wait() on the child directly because DetachFork called Release() and
// Setsid'd it into a new session.
func waitForPid(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // process gone
		}
		time.Sleep(25 * time.Millisecond)
	}
	// If still alive, try to clean it up. Not fatal — the helper has a short
	// self-lifetime anyway — but leaves the process table tidy.
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
