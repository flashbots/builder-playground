package utils

import (
	"os"
	"path/filepath"
)

// MustGetPlaygroundTempDir creates the temp dir for
func MustGetPlaygroundTempDir() string {
	return makeDir(TempPlaygroundDirPath())
}

// MustGetSessionTempDir creates and returns the temp dir for the session under
// the temp playground dir.
func MustGetTempSessionDir(sessionID string) string {
	return makeDir(MustGetPlaygroundTempDir(), sessionID)
}

// MustGetVolumeDir creates and returns the temp dir for bind mount volumes under
// the temp session dir.
func MustGetVolumeDir(sessionID, volumeName string) string {
	return makeDir(MustGetTempSessionDir(sessionID), "bind-mount-volumes", volumeName)
}

// GetSessionTempDirCount returns the number of session directories under the playground temp dir.
func GetSessionTempDirCount() int {
	playgroundDir := TempPlaygroundDirPath()
	entries, _ := os.ReadDir(playgroundDir)
	var count int
	for _, entry := range entries {
		if entry.IsDir() {
			count++
		}
	}
	return count
}

// TempPlaygroundDirPath returns the temp playground dir path.
func TempPlaygroundDirPath() string {
	return filepath.Join(os.TempDir(), "builder-playground")
}

func makeDir(segments ...string) string {
	absPath := filepath.Join(segments...)
	if err := os.MkdirAll(absPath, 0o755); err != nil {
		panic(err)
	}
	return absPath
}
