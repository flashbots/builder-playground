package utils

import (
	"os"
	"path/filepath"
)

// MustGetPlaygroundTempDir creates the temp dir for
func MustGetPlaygroundTempDir() string {
	return makeDir(os.TempDir(), "builder-playground")
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

func makeDir(segments ...string) string {
	absPath := filepath.Join(segments...)
	if err := os.MkdirAll(absPath, 0o755); err != nil {
		panic(err)
	}
	return absPath
}
