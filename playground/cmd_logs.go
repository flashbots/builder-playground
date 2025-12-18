package playground

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/docker/docker/api/types/container"
)

func Logs(ctx context.Context, sessionName string, serviceName string) error {
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
			cmd := exec.CommandContext(ctx, "docker", "logs", "-f", "--tail", "50", container.ID)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
	}

	return fmt.Errorf("no container found for service %s", serviceName)
}
