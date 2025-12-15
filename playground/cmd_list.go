package playground

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
)

func List(ctx context.Context) error {
	client, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	// TODO: Filter by session ID when we introduce multiple sessions soon.
	containers, err := client.ContainerList(ctx, container.ListOptions{
		All: true,
	})
	if err != nil {
		return fmt.Errorf("error getting container list: %w", err)
	}

	for _, container := range containers {
		if container.Labels["playground"] != "true" {
			continue
		}
		fmt.Println(container.Labels["com.docker.compose.service"])
	}

	return nil
}
