package playground

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"

	"github.com/flashbots/builder-playground/utils/mainctx"
)

// lifecycleContext holds shared state for lifecycle command execution
type lifecycleContext struct {
	svc       *Service
	dir       string
	logWriter io.Writer
	logPath   string
}

func (lc *lifecycleContext) newCmd(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = lc.dir
	cmd.Stdout = lc.logWriter
	cmd.Stderr = lc.logWriter
	return cmd
}

func (lc *lifecycleContext) logHeader(phase string, index int, command string) {
	if lc.logWriter == nil {
		return
	}
	if index >= 0 {
		fmt.Fprintf(lc.logWriter, "=== %s command %d: %s ===\n", phase, index, command)
	} else {
		fmt.Fprintf(lc.logWriter, "=== %s command: %s ===\n", phase, command)
	}
}

func (lc *lifecycleContext) formatError(phase, command string, err error) string {
	errMsg := fmt.Sprintf("service %s %s command failed:\n  Command: %s\n  Log file: %s\n  Exit error: %v",
		lc.svc.Name, phase, command, lc.logPath, err)
	if lastLines := readLastLines(lc.logPath, 10); lastLines != "" {
		errMsg += fmt.Sprintf("\n  Last output:\n%s", lastLines)
	}
	return errMsg
}

// startWithLifecycleHooks runs a service with lifecycle commands (init, start)
func (d *LocalRunner) startWithLifecycleHooks(ctx context.Context, svc *Service) error {
	if err := d.waitForDependencies(svc); err != nil {
		return fmt.Errorf("failed waiting for dependencies: %w", err)
	}

	logFile, err := d.out.LogOutput(svc.Name)
	var logPath string
	if err != nil {
		logFile = os.Stdout
		logPath = "" // Don't try to read stdout as a file
	} else {
		logPath = logFile.Name()
	}

	lc := &lifecycleContext{
		svc:       svc,
		dir:       d.out.dst,
		logWriter: logFile,
		logPath:   logPath,
	}

	d.lifecycleServices = append(d.lifecycleServices, svc)

	// Run init commands sequentially - each must return exit code 0
	for i, cmd := range svc.Init {
		slog.Info("Running lifecycle init command", "service", svc.Name, "command", cmd, "index", i)
		lc.logHeader("Init", i, cmd)

		if err := lc.newCmd(ctx, cmd).Run(); err != nil {
			return fmt.Errorf("%s", lc.formatError("init", cmd, err))
		}
	}

	if svc.Start == "" {
		return nil
	}

	// Run start command - may hang (long-running) or return 0
	slog.Info("Running lifecycle start command", "service", svc.Name, "command", svc.Start)
	lc.logHeader("Start", -1, svc.Start)

	startCmd := lc.newCmd(ctx, svc.Start)
	go func() {
		if err := startCmd.Run(); err != nil {
			if mainctx.IsExiting() {
				return
			}
			slog.Error("Lifecycle service failed", "service", svc.Name, "error", err)
			d.sendExitError(fmt.Errorf("%s", lc.formatError("start", svc.Start, err)))
		}
	}()

	d.handles = append(d.handles, startCmd)
	return nil
}

// runLifecycleStopCommands runs the stop commands for a lifecycle service
func (d *LocalRunner) runLifecycleStopCommands(svc *Service, logOutput io.Writer, logPath string) {
	if len(svc.Stop) == 0 {
		return
	}

	lc := &lifecycleContext{
		svc:       svc,
		dir:       d.out.dst,
		logWriter: logOutput,
		logPath:   logPath,
	}

	for i, stopCmd := range svc.Stop {
		slog.Info("Running lifecycle stop command", "service", svc.Name, "command", stopCmd, "index", i)
		lc.logHeader("Stop", i, stopCmd)

		if err := lc.newCmd(context.Background(), stopCmd).Run(); err != nil {
			slog.Warn("Lifecycle stop command failed (continuing)", "service", svc.Name, "command", stopCmd, "error", err)
		}
	}
}

// runAllLifecycleStopCommands runs stop commands for all lifecycle services
func (d *LocalRunner) runAllLifecycleStopCommands() {
	for _, svc := range d.lifecycleServices {
		logOutput, err := d.out.LogOutput(svc.Name)
		var logPath string
		if err != nil {
			logOutput = nil
			logPath = ""
		} else {
			logPath = logOutput.Name()
		}
		d.runLifecycleStopCommands(svc, logOutput, logPath)
	}
}
