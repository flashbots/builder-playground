package artifacts

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type release struct {
	Name    string
	Org     string
	Version string
	Arch    func(string, string) string
}

func DownloadArtifacts() (map[string]string, error) {
	var artifacts = []release{
		{
			Name:    "reth",
			Org:     "paradigmxyz",
			Version: "v1.0.2",
			Arch: func(goos, goarch string) string {
				if goos == "linux" {
					return "x86_64-unknown-linux-gnu"
				} else if goos == "darwin" && goarch == "arm64" { // Apple M1
					return "aarch64-apple-darwin"
				} else if goos == "darwin" && goarch == "amd64" {
					return "x86_64-apple-darwin"
				}
				return ""
			},
		},
		{
			Name:    "lighthouse",
			Org:     "sigp",
			Version: "v5.2.1",
			Arch: func(goos, goarch string) string {
				if goos == "linux" {
					return "x86_64-unknown-linux-gnu"
				} else if goos == "darwin" && goarch == "arm64" { // Apple M1
					return "x86_64-apple-darwin-portable"
				} else if goos == "darwin" && goarch == "amd64" {
					return "x86_64-apple-darwin"
				}
				return ""
			},
		},
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("error getting user home directory: %w", err)
	}

	// Define the path for our custom home directory
	customHomeDir := filepath.Join(homeDir, ".playground")

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(customHomeDir, 0755); err != nil {
		return nil, fmt.Errorf("error creating output directory: %v", err)
	}

	goos := runtime.GOOS
	goarch := runtime.GOARCH

	fmt.Printf("Architecture detected: %s/%s\n", goos, goarch)

	// Try to download the release binaries for 'reth' and 'lighthouse'. It works as follows:
	// 1. Check under $HOME/.playground if the binary-<version> exists. If exists, use it.
	// 2. If the binary does not exists, use the arch and os to download the binary from the release page.
	// 3. If the architecture is not supported, check if the binary is found in PATH.
	releases := make(map[string]string)
	for _, artifact := range artifacts {
		outPath := filepath.Join(customHomeDir, artifact.Name+"-"+artifact.Version)
		_, err := os.Stat(outPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("error checking file existence: %v", err)
		}

		if err != nil {
			archVersion := artifact.Arch(goos, goarch)
			if archVersion == "" {
				// Case 2. The architecture is not supported.
				fmt.Printf("unsupported OS/Arch: %s/%s\n", goos, goarch)
				if _, err := exec.LookPath(artifact.Name); err != nil {
					return nil, fmt.Errorf("error looking up binary in PATH: %v", err)
				} else {
					outPath = artifact.Name
					fmt.Printf("Using %s from PATH\n", artifact.Name)
				}
			} else {
				// Case 3. Download the binary from the release page
				releasesURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s-%s-%s.tar.gz", artifact.Org, artifact.Name, artifact.Version, artifact.Name, artifact.Version, archVersion)
				fmt.Printf("Downloading %s: %s\n", outPath, releasesURL)

				if err := downloadArtifact(releasesURL, artifact.Name, outPath); err != nil {
					return nil, fmt.Errorf("error downloading artifact: %v", err)
				}
			}
		} else {
			// Case 1. Use the binary in $HOME/.playground
			fmt.Printf("%s already exists, skipping download\n", outPath)
		}

		releases[artifact.Name] = outPath
	}

	return releases, nil
}

func downloadArtifact(url string, expectedFile string, outPath string) error {
	// Download the file
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("error downloading file: %v", err)
	}
	defer resp.Body.Close()

	// Create a gzip reader
	gzipReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("error creating gzip reader: %v", err)
	}
	defer gzipReader.Close()

	// Create a tar reader
	tarReader := tar.NewReader(gzipReader)

	// Extract the file
	var found bool
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %v", err)
		}

		if header.Typeflag == tar.TypeReg {
			if header.Name != expectedFile {
				return fmt.Errorf("unexpected file in archive: %s", header.Name)
			}
			outFile, err := os.Create(outPath)
			if err != nil {
				return fmt.Errorf("error creating output file: %v", err)
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, tarReader); err != nil {
				return fmt.Errorf("error writing output file: %v", err)
			}

			// change permissions
			if err := os.Chmod(outPath, 0755); err != nil {
				return fmt.Errorf("error changing permissions: %v", err)
			}
			found = true
			break // Assuming there's only one file per repo
		}
	}

	if !found {
		return fmt.Errorf("file not found in archive: %s", expectedFile)
	}
	return nil
}
