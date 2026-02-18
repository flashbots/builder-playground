package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

// GetPlaygroundDir returns the base directory for builder-playground state.
// It follows XDG Base Directory Specification, defaulting to ~/.local/state/builder-playground.
func GetPlaygroundDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("error getting user home directory: %w", err)
	}

	// if legacy .playground dir is present, remove it
	if err := os.RemoveAll(filepath.Join(homeDir, ".playground")); err != nil {
		return "", err
	}

	stateHomeDir := os.Getenv("XDG_STATE_HOME")
	if stateHomeDir == "" {
		stateHomeDir = filepath.Join(homeDir, ".local", "state")
	}

	// Define the path for our custom home directory
	customHomeDir := filepath.Join(stateHomeDir, "builder-playground")

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(customHomeDir, 0o755); err != nil {
		return "", fmt.Errorf("error creating output directory: %v", err)
	}

	return customHomeDir, nil
}

// GetLogsDir returns the directory where service logs are stored.
func GetLogsDir(sessionID string) (string, error) {
	sessionsDir, err := GetSessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sessionsDir, sessionID, "logs"), nil
}

// GetSessionsDir returns the directory for all sessions.
func GetSessionsDir() (string, error) {
	playgroundDir, err := GetPlaygroundDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(playgroundDir, "sessions"), nil
}
