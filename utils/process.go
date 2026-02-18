package utils

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// CountConcurrentPlaygroundSessions returns the number of other builder-playground
// start/cook processes currently running (excluding this process).
// This is used to offset ports for parallel sessions to avoid conflicts.
func CountConcurrentPlaygroundSessions() int {
	// Use ps with POSIX-compatible flags that work on both Linux and macOS
	cmd := exec.Command("ps", "-eo", "pid,command")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return getCountFromOutput(out, os.Getpid())
}

func getCountFromOutput(out []byte, myPid int) int {
	count := 0

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check if this line contains a playground start/cook command
		if !strings.Contains(line, "builder-playground") {
			continue
		}
		if !strings.Contains(line, "start") && !strings.Contains(line, "cook") {
			continue
		}

		// Extract PID (first field)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		// Skip our own process
		if pid == myPid {
			continue
		}

		count++
	}

	return count
}
