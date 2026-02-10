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

// runLifecycleService runs a service with lifecycle commands (init, start, stop)
func (d *LocalRunner) runLifecycleService(ctx context.Context, svc *Service) error {
	if err := d.waitForDependencies(svc); err != nil {
		return fmt.Errorf("failed waiting for dependencies: %w", err)
	}

	logFile, err := d.out.LogOutput(svc.Name)
	if err != nil {
		logFile = os.Stdout
	}

	lc := &lifecycleContext{
		svc:       svc,
		dir:       d.out.dst,
		logWriter: logFile,
		logPath:   logFile.Name(),
	}

	d.lifecycleServices = append(d.lifecycleServices, svc)

	// Run init commands sequentially - each must return exit code 0
	for i, cmd := range svc.Lifecycle.Init {
		slog.Info("Running lifecycle init command", "service", svc.Name, "command", cmd, "index", i)
		lc.logHeader("Init", i, cmd)

		if err := lc.newCmd(ctx, cmd).Run(); err != nil {
			d.runLifecycleStopCommands(svc, logFile)
			return fmt.Errorf("%s", lc.formatError("init", cmd, err))
		}
	}

	if svc.Lifecycle.Start == "" {
		return nil
	}

	// Run start command - may hang (long-running) or return 0
	slog.Info("Running lifecycle start command", "service", svc.Name, "command", svc.Lifecycle.Start)
	lc.logHeader("Start", -1, svc.Lifecycle.Start)

	startCmd := lc.newCmd(ctx, svc.Lifecycle.Start)
	go func() {
		if err := startCmd.Run(); err != nil {
			if mainctx.IsExiting() {
				return
			}
			slog.Error("Lifecycle service failed", "service", svc.Name, "error", err)
			d.runLifecycleStopCommands(svc, logFile)
			d.sendExitError(fmt.Errorf("%s", lc.formatError("start", svc.Lifecycle.Start, err)))
		}
	}()

	d.handles = append(d.handles, startCmd)
	return nil
}

// runLifecycleStopCommands runs the stop commands for a lifecycle service
func (d *LocalRunner) runLifecycleStopCommands(svc *Service, logOutput io.Writer) {
	if svc.Lifecycle == nil || len(svc.Lifecycle.Stop) == 0 {
		return
	}

	lc := &lifecycleContext{
		svc:       svc,
		dir:       d.out.dst,
		logWriter: logOutput,
	}

	for i, stopCmd := range svc.Lifecycle.Stop {
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
		if err != nil {
			logOutput = nil
		}
		d.runLifecycleStopCommands(svc, logOutput)
	}
}
