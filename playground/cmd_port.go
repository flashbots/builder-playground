package playground

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
)

// GetServicePort returns the host port for a given service and port name.
// If portName is empty, returns a formatted string listing all available ports.
// If session is empty and there's exactly one session, it uses that session.
func GetServicePort(session, serviceName, portName string) (string, error) {
	client, err := newDockerClient()
	if err != nil {
		return "", fmt.Errorf("failed to create docker client: %w", err)
	}
	defer client.Close()

	if session == "" {
		sessions, err := GetLocalSessions()
		if err != nil {
			return "", fmt.Errorf("failed to get sessions: %w", err)
		}
		if len(sessions) == 0 {
			return "", fmt.Errorf("no running sessions found")
		}
		if len(sessions) > 1 {
			return "", fmt.Errorf("multiple sessions found, please specify a session name: %v", sessions)
		}
		session = sessions[0]
	}

	containers, err := client.ContainerList(context.Background(), container.ListOptions{
		All: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	for _, c := range containers {
		if c.Labels["playground"] != "true" ||
			c.Labels["playground.session"] != session ||
			c.Labels["com.docker.compose.service"] != serviceName {
			continue
		}

		// Build a map of port name -> host port
		portMap := make(map[string]int)
		for label, containerPortStr := range c.Labels {
			if !strings.HasPrefix(label, "port.") {
				continue
			}
			name := strings.TrimPrefix(label, "port.")
			containerPort, err := strconv.Atoi(containerPortStr)
			if err != nil {
				continue
			}
			for _, p := range c.Ports {
				if int(p.PrivatePort) == containerPort && p.PublicPort != 0 {
					portMap[name] = int(p.PublicPort)
					break
				}
			}
		}

		if portName == "" {
			if len(portMap) == 0 {
				return "", fmt.Errorf("no ports found for service '%s'", serviceName)
			}
			var names []string
			for name := range portMap {
				names = append(names, name)
			}
			sort.Strings(names)

			var lines []string
			for _, name := range names {
				lines = append(lines, fmt.Sprintf("%s: %d", name, portMap[name]))
			}
			return strings.Join(lines, "\n"), nil
		}

		if hostPort, ok := portMap[portName]; ok {
			return fmt.Sprintf("%d", hostPort), nil
		}

		var availablePorts []string
		for name := range portMap {
			availablePorts = append(availablePorts, name)
		}
		sort.Strings(availablePorts)
		if len(availablePorts) > 0 {
			return "", fmt.Errorf("port '%s' not found for service '%s'. Available ports: %v", portName, serviceName, availablePorts)
		}
		return "", fmt.Errorf("port '%s' not found for service '%s'", portName, serviceName)
	}

	return "", fmt.Errorf("service '%s' not found in session '%s'", serviceName, session)
}
