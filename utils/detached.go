package utils

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// ReadyMarker is the line a re-exec'd child writes to its notify fd to tell
// its parent that startup is complete and it is safe to detach.
const ReadyMarker = "READY"

// DefaultNotifyFlag is the CLI flag used to pass the notify fd to a re-exec'd
// child when DetachForkOptions.NotifyFlag is left empty.
const DefaultNotifyFlag = "--detached-notify-fd"

// detachedNotifyFd is the fd number the child sees the notify pipe on. It
// matches ExtraFiles[0] semantics for exec.Cmd (fd 3 in the child).
const detachedNotifyFd = 3

// DetachForkOptions configures DetachFork.
type DetachForkOptions struct {
	// Executable is the binary to launch. Required.
	Executable string

	// Args are the CLI args passed to the child (not including argv[0]).
	// Callers are responsible for stripping any flag that would cause the
	// child to recurse back into parent-mode (e.g. --detached).
	Args []string

	// NotifyFlag is the flag appended to Args telling the child which fd
	// carries the ready pipe. "=<fd>" is appended automatically. If empty,
	// DefaultNotifyFlag is used.
	NotifyFlag string

	// Env is the environment passed to the child. If nil, the child inherits
	// the current process's environment.
	Env []string

	// Stdout and Stderr are inherited by the child so the user sees startup
	// logs live. Default to os.Stdout and os.Stderr.
	Stdout *os.File
	Stderr *os.File

	// ForwardSignals lists signals forwarded to the child while the parent is
	// waiting for readiness, so Ctrl-C during startup still tears things down
	// cleanly. Defaults to SIGINT, SIGTERM, SIGHUP, SIGQUIT.
	ForwardSignals []os.Signal
}

// DetachedChild is the handle returned once the child has signaled readiness.
// The parent has already released the process (no reaping required) and can
// exit freely; the child keeps running in its own session.
type DetachedChild struct {
	PID int
}

// DetachFork re-execs Executable as a child process, attaches a one-shot
// readiness pipe to fd 3 in the child, and blocks until the child writes
// ReadyMarker to that pipe. On success the child is released and the returned
// DetachedChild reports its PID. If the child exits or closes the pipe before
// signaling ready, an error is returned.
func DetachFork(opts DetachForkOptions) (*DetachedChild, error) {
	if opts.Executable == "" {
		return nil, fmt.Errorf("detach: Executable is required")
	}
	notifyFlag := opts.NotifyFlag
	if notifyFlag == "" {
		notifyFlag = DefaultNotifyFlag
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	signals := opts.ForwardSignals
	if len(signals) == 0 {
		signals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
	}

	readyR, readyW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("detach: pipe: %w", err)
	}
	defer readyR.Close()

	args := append([]string{}, opts.Args...)
	args = append(args, fmt.Sprintf("%s=%d", notifyFlag, detachedNotifyFd))

	cmd := exec.Command(opts.Executable, args...)
	cmd.Stdin = nil
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.ExtraFiles = []*os.File{readyW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if opts.Env != nil {
		cmd.Env = opts.Env
	}

	if err := cmd.Start(); err != nil {
		readyW.Close()
		return nil, fmt.Errorf("detach: start child: %w", err)
	}
	// Close the parent's copy of the write end so the reader hits EOF if the
	// child exits without writing the ready marker.
	readyW.Close()

	stopForward := forwardSignalsTo(cmd.Process, signals)
	defer stopForward()

	if err := waitForReady(readyR); err != nil {
		waitErr := cmd.Wait()
		if waitErr != nil {
			return nil, fmt.Errorf("detach: child exited before ready: %w", waitErr)
		}
		return nil, fmt.Errorf("detach: %w", err)
	}

	// Capture the PID before Release — on unix, Release sets Pid to -1 to
	// mark the handle unusable.
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return nil, fmt.Errorf("detach: release child: %w", err)
	}

	return &DetachedChild{PID: pid}, nil
}

// StripFlag returns args with any occurrence of --flag, --flag=value, or the
// two-token form --flag value removed. It is intended for trimming a parent
// mode flag (e.g. --detached) out of os.Args before handing them to a child
// via DetachFork.
func StripFlag(args []string, flag string, takesValue bool) []string {
	prefix := flag + "="
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == flag {
			if takesValue && i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(a, prefix) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// SignalReady writes ReadyMarker (followed by a newline) to fd and closes it.
// The fd must be a writable pipe whose read end is held by the parent. After
// this call the caller must not use fd again.
func SignalReady(fd int) error {
	notify := os.NewFile(uintptr(fd), "detached-notify")
	if notify == nil {
		return fmt.Errorf("detach: invalid notify fd: %d", fd)
	}
	if _, err := notify.Write([]byte(ReadyMarker + "\n")); err != nil {
		notify.Close()
		return fmt.Errorf("detach: write ready: %w", err)
	}
	if err := notify.Close(); err != nil {
		return fmt.Errorf("detach: close notify fd: %w", err)
	}
	return nil
}

// RedirectStdio points the current process's stdin at /dev/null and stdout and
// stderr at logPath (opened in append mode, creating it if needed). If logPath
// is empty, stdout and stderr are redirected to /dev/null as well. Existing
// file descriptors for stdout/stderr are atomically replaced via dup2, so
// Go code that captured os.Stdout/os.Stderr before the call continues to
// write to the redirected target.
func RedirectStdio(logPath string) error {
	var out *os.File
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("detach: open %s: %w", logPath, err)
		}
		out = f
	} else {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("detach: open %s: %w", os.DevNull, err)
		}
		out = f
	}
	defer out.Close()

	if err := unix.Dup2(int(out.Fd()), int(os.Stdout.Fd())); err != nil {
		return fmt.Errorf("detach: redirect stdout: %w", err)
	}
	if err := unix.Dup2(int(out.Fd()), int(os.Stderr.Fd())); err != nil {
		return fmt.Errorf("detach: redirect stderr: %w", err)
	}
	if devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0); err == nil {
		_ = unix.Dup2(int(devNull.Fd()), int(os.Stdin.Fd()))
		devNull.Close()
	}
	return nil
}

// NotifyDetachReady is a convenience that calls SignalReady followed by
// RedirectStdio — the full sequence a re-exec'd child performs once it has
// finished bringing its services up.
func NotifyDetachReady(fd int, logPath string) error {
	if err := SignalReady(fd); err != nil {
		return err
	}
	return RedirectStdio(logPath)
}

func forwardSignalsTo(proc *os.Process, signals []os.Signal) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, signals...)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case s := <-sigCh:
				_ = proc.Signal(s)
			case <-stop:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(stop)
		wg.Wait()
	}
}

func waitForReady(r *os.File) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == ReadyMarker {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read ready pipe: %w", err)
	}
	return fmt.Errorf("child closed pipe without signaling %q", ReadyMarker)
}
