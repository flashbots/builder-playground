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

		// Extract PID (first field) and command (rest)
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

		// Check if the command is a playground start/cook invocation.
		// The command may be "builder-playground" or a full path like
		// "/usr/local/bin/builder-playground".
		command := strings.Join(fields[1:], " ")
		binary := fields[1]
		if idx := strings.LastIndex(binary, "/"); idx >= 0 {
			binary = binary[idx+1:]
		}
		if binary != "builder-playground" {
			continue
		}
		if !strings.Contains(command, " start ") && !strings.Contains(command, " cook ") {
			continue
		}

		count++
	}

	return count
}
