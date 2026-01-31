package playground

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
)

func Logs(ctx context.Context, sessionName, serviceName string, follow bool) error {
	client, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	containers, err := client.ContainerList(ctx, container.ListOptions{
		All: true,
	})
	if err != nil {
		return fmt.Errorf("error getting container list: %w", err)
	}

	for _, container := range containers {
		if sessionName != "" && container.Labels["playground.session"] != sessionName {
			continue
		}
		if container.Labels["playground"] == "true" &&
			container.Labels["com.docker.compose.service"] == serviceName {
			args := []string{"logs"}
			if follow {
				args = append(args, "-f", "--tail", "50")
			}
			args = append(args, container.ID)
			cmd := exec.CommandContext(ctx, "docker", args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
	}

	// Container not found - try to read from log file (for host services or failed services)
	logPath, err := findServiceLogFile(serviceName)
	if err != nil {
		return fmt.Errorf("no container or log file found for service %s", serviceName)
	}

	return readLogFile(ctx, logPath, follow)
}

// findServiceLogFile looks for a log file for the given service
func findServiceLogFile(serviceName string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Check the default devnet output directory (under .local/state)
	logPath := filepath.Join(homeDir, ".local", "state", "builder-playground", "devnet", "logs", serviceName+".log")
	if _, err := os.Stat(logPath); err == nil {
		return logPath, nil
	}

	// Fallback: legacy location
	logPath = filepath.Join(homeDir, "devnet", "logs", serviceName+".log")
	if _, err := os.Stat(logPath); err == nil {
		return logPath, nil
	}

	return "", fmt.Errorf("log file not found")
}

// readLogFile reads and displays a log file, optionally following it
func readLogFile(ctx context.Context, logPath string, follow bool) error {
	if follow {
		// Use tail -f to follow the log file
		cmd := exec.CommandContext(ctx, "tail", "-f", "-n", "50", logPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Just read and print the file
	file, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(os.Stdout, file)
	return err
}
