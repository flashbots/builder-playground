package mainctx

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
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
				forceKillContainers()
				os.Exit(1)
			}
		}
	}()
}

// forceKillContainers kills all playground Docker containers
func forceKillContainers() {
	// Find all playground containers
	cmd := exec.Command("docker", "ps", "-q", "--filter", "label=playground=true")
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		return
	}
	// Kill them forcefully
	killCmd := exec.Command("docker", "kill", "--signal", "KILL")
	killCmd.Args = append(killCmd.Args, string(output))
	// Use shell to handle the container IDs
	shellCmd := exec.Command("sh", "-c", "docker ps -q --filter label=playground=true | xargs -r docker kill")
	_ = shellCmd.Run()
}

// Get returns a context that is aware of the interruption/termination signals received by the process.
func Get() context.Context {
	return sigAwareCtx
}
