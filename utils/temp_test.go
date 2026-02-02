package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMustGetPlaygroundTempDir(t *testing.T) {
	r := require.New(t)
	dir := MustGetPlaygroundTempDir()

	// Should be under system temp dir
	r.True(strings.HasPrefix(dir, os.TempDir()), "expected dir to be under %s, got %s", os.TempDir(), dir)

	// Should end with builder-playground
	r.Equal("builder-playground", filepath.Base(dir))

	// Directory should exist
	info, err := os.Stat(dir)
	r.NoError(err)
	r.True(info.IsDir(), "expected %s to be a directory", dir)
}

func TestMustGetTempSessionDir(t *testing.T) {
	r := require.New(t)
	sessionID := "test-session-123"
	dir := MustGetTempSessionDir(sessionID)

	// Should be under playground temp dir
	playgroundDir := MustGetPlaygroundTempDir()
	r.True(strings.HasPrefix(dir, playgroundDir), "expected dir to be under %s, got %s", playgroundDir, dir)

	// Should end with session ID
	r.Equal(sessionID, filepath.Base(dir))

	// Directory should exist
	info, err := os.Stat(dir)
	r.NoError(err)
	r.True(info.IsDir(), "expected %s to be a directory", dir)

	// Cleanup
	os.RemoveAll(dir)
}

func TestMustGetVolumeDir(t *testing.T) {
	r := require.New(t)
	sessionID := "test-session-456"
	volumeName := "test-volume"
	dir := MustGetVolumeDir(sessionID, volumeName)

	// Should be under session temp dir
	sessionDir := MustGetTempSessionDir(sessionID)
	r.True(strings.HasPrefix(dir, sessionDir), "expected dir to be under %s, got %s", sessionDir, dir)

	// Should contain bind-mount-volumes in path
	r.Contains(dir, "bind-mount-volumes")

	// Should end with volume name
	r.Equal(volumeName, filepath.Base(dir))

	// Directory should exist
	info, err := os.Stat(dir)
	r.NoError(err)
	r.True(info.IsDir(), "expected %s to be a directory", dir)

	// Cleanup
	os.RemoveAll(sessionDir)
}

func TestMakeDir(t *testing.T) {
	r := require.New(t)
	tempDir := t.TempDir()
	testPath := filepath.Join(tempDir, "a", "b", "c")

	result := makeDir(tempDir, "a", "b", "c")

	r.Equal(testPath, result)

	// Directory should exist
	info, err := os.Stat(result)
	r.NoError(err)
	r.True(info.IsDir(), "expected %s to be a directory", result)
}

func TestMakeDirIdempotent(t *testing.T) {
	r := require.New(t)
	tempDir := t.TempDir()

	// Call makeDir twice with same path
	result1 := makeDir(tempDir, "idempotent-test")
	result2 := makeDir(tempDir, "idempotent-test")

	r.Equal(result1, result2)

	// Directory should still exist
	info, err := os.Stat(result1)
	r.NoError(err)
	r.True(info.IsDir(), "expected %s to be a directory", result1)
}
