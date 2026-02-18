package playground

import (
	"archive/tar"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloadRelease_TarGz(t *testing.T) {
	tmpDir := t.TempDir()

	binaryContent := []byte("#!/bin/bash\necho hello")

	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path

		// Create tar.gz response
		gzWriter := gzip.NewWriter(w)
		defer gzWriter.Close()

		tarWriter := tar.NewWriter(gzWriter)
		defer tarWriter.Close()

		header := &tar.Header{
			Name: "test-binary",
			Mode: 0o755,
			Size: int64(len(binaryContent)),
		}
		tarWriter.WriteHeader(header)
		tarWriter.Write(binaryContent)
	}))
	defer server.Close()

	rel := &release{
		Name:    "test-binary",
		Org:     "test-org",
		Repo:    "test-repo",
		Version: "v1.0.0",
		BaseURL: server.URL,
		Arch: func(goos, goarch string) string {
			return "linux-amd64"
		},
	}

	outPath, err := DownloadRelease(tmpDir, rel)
	require.NoError(t, err)

	// Verify the request path matches expected GitHub releases format
	require.Equal(t, "/test-org/test-repo/releases/download/v1.0.0/test-binary-v1.0.0-linux-amd64.tar.gz", requestedPath)

	// Verify the downloaded file
	content, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, binaryContent, content)

	// Verify permissions
	info, err := os.Stat(outPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestDownloadRelease_Binary(t *testing.T) {
	tmpDir := t.TempDir()

	binaryContent := []byte("#!/bin/bash\necho hello")

	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Write(binaryContent)
	}))
	defer server.Close()

	rel := &release{
		Name:    "test-binary",
		Org:     "test-org",
		Repo:    "test-repo",
		Version: "v1.0.0",
		Format:  "binary",
		BaseURL: server.URL,
		Arch: func(goos, goarch string) string {
			return "linux-amd64"
		},
	}

	outPath, err := DownloadRelease(tmpDir, rel)
	require.NoError(t, err)

	// Verify the request path matches expected GitHub releases format for binary
	require.Equal(t, "/test-org/test-repo/releases/download/v1.0.0/test-binary", requestedPath)

	// Verify the downloaded file
	content, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, binaryContent, content)

	// Verify permissions
	info, err := os.Stat(outPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestDownloadRelease_RepoDefaultsToName(t *testing.T) {
	tmpDir := t.TempDir()

	binaryContent := []byte("#!/bin/bash\necho hello")

	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Write(binaryContent)
	}))
	defer server.Close()

	rel := &release{
		Name:    "my-tool",
		Org:     "my-org",
		Version: "v2.0.0",
		Format:  "binary",
		BaseURL: server.URL,
		Arch: func(goos, goarch string) string {
			return "linux-amd64"
		},
	}

	_, err := DownloadRelease(tmpDir, rel)
	require.NoError(t, err)

	// Repo should default to Name
	require.Equal(t, "/my-org/my-tool/releases/download/v2.0.0/my-tool", requestedPath)
}

func TestDownloadRelease_CachesExisting(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-create the binary file
	outPath := filepath.Join(tmpDir, "cached-binary-v1.0.0")
	err := os.WriteFile(outPath, []byte("cached content"), 0o755)
	require.NoError(t, err)

	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		w.Write([]byte("new content"))
	}))
	defer server.Close()

	rel := &release{
		Name:    "cached-binary",
		Version: "v1.0.0",
		Org:     "test-org",
		BaseURL: server.URL,
		Arch: func(goos, goarch string) string {
			return "linux-amd64"
		},
	}

	// Should return cached path without downloading
	result, err := DownloadRelease(tmpDir, rel)
	require.NoError(t, err)
	require.Equal(t, outPath, result)

	// Server should not have been called
	require.False(t, serverCalled, "server should not be called when file is cached")

	// Verify content wasn't changed
	content, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, []byte("cached content"), content)
}

func TestDownloadRelease_UnsupportedArch(t *testing.T) {
	tmpDir := t.TempDir()

	rel := &release{
		Name:    "nonexistent-binary-" + runtime.GOOS + "-" + runtime.GOARCH,
		Version: "v1.0.0",
		Org:     "test-org",
		Arch: func(goos, goarch string) string {
			return "" // Unsupported architecture
		},
	}

	// Should fail because binary is not in PATH
	_, err := DownloadRelease(tmpDir, rel)
	require.Error(t, err)
	require.Contains(t, err.Error(), "error looking up binary in PATH")
}

func TestDownloadRelease_CreatesOutputFolder(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "path", "to", "releases")

	binaryContent := []byte("#!/bin/bash\necho hello")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(binaryContent)
	}))
	defer server.Close()

	rel := &release{
		Name:    "test-binary",
		Org:     "test-org",
		Version: "v1.0.0",
		Format:  "binary",
		BaseURL: server.URL,
		Arch: func(goos, goarch string) string {
			return "linux-amd64"
		},
	}

	outPath, err := DownloadRelease(nestedDir, rel)
	require.NoError(t, err)

	// Verify the nested directory was created
	require.DirExists(t, nestedDir)

	// Verify the file was downloaded
	content, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, binaryContent, content)
}
