package internal

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// Inspect incldues the logic for the inspect command
func Inspect(ctx context.Context, serviceName, portName string) error {
	client, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	serviceID, portNum, err := retrieveContainerDetails(client, serviceName, portName)
	if err != nil {
		return fmt.Errorf("failed to retrieve container details: %w", err)
	}

	return runTcpFlow(ctx, client, serviceID, portNum)
}

func retrieveContainerDetails(client *client.Client, serviceName, portName string) (string, string, error) {
	// Get the service by name
	containers, err := client.ContainerList(context.Background(), container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", "service="+serviceName)),
		All:     true,
	})
	if err != nil {
		return "", "", fmt.Errorf("error getting container list: %w", err)
	}

	size := len(containers)
	if size == 0 {
		return "", "", fmt.Errorf("no containers found for service %s", serviceName)
	} else if size > 1 {
		return "", "", fmt.Errorf("multiple containers found for service %s", serviceName)
	}

	container := containers[0]

	// Get the container details to find the port mapping in the labels as port.<name>
	containerDetails, err := client.ContainerInspect(context.Background(), container.ID)
	if err != nil {
		return "", "", fmt.Errorf("error inspecting container %s: %w", container.ID, err)
	}

	// Check if the port name is in the labels
	portLabel := fmt.Sprintf("port.%s", portName)
	portNum, ok := containerDetails.Config.Labels[portLabel]
	if !ok {
		return "", "", fmt.Errorf("port %s not found in container %s", portName, container.ID)
	}

	return container.ID, portNum, nil
}

func runTcpFlow(ctx context.Context, client *client.Client, containerID, portName string) error {
	// Create container config for tcpflow
	config := &container.Config{
		Image:        "appropriate/tcpflow:latest",
		Cmd:          []string{"-c", "-p", "-i", "eth0", "port", portName},
		Tty:          true,
		AttachStdout: true,
		AttachStderr: true,
	}

	// Host config with network mode and capabilities
	hostConfig := &container.HostConfig{
		NetworkMode: container.NetworkMode("container:" + containerID),
		CapAdd:      []string{"NET_ADMIN"},
	}

	// Create the container
	resp, err := client.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Start the container
	if err := client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Get container logs and stream them
	logOptions := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: false,
	}

	logs, err := client.ContainerLogs(ctx, resp.ID, logOptions)
	if err != nil {
		return fmt.Errorf("failed to get container logs: %w", err)
	}
	defer logs.Close()

	// Start copying logs to stdout
	go func() {
		_, err := io.Copy(os.Stdout, logs)
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "Error copying logs: %v\n", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()

	// Cleanup: stop and remove the container
	timeout := 5
	if err := client.ContainerStop(context.Background(), resp.ID, container.StopOptions{Timeout: &timeout}); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping container: %v\n", err)
	}

	if err := client.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true}); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing container: %v\n", err)
	}

	return nil
}
