package internal

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

func downloadRelease(outputFolder string, artifact *release) (string, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	outPath := filepath.Join(outputFolder, artifact.Name+"-"+artifact.Version)
	_, err := os.Stat(outPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("error checking file existence: %v", err)
	}
	if err == nil {
		return outPath, nil
	}

	// create the output folder if it doesn't exist yet
	if err := os.MkdirAll(outputFolder, 0755); err != nil {
		return "", fmt.Errorf("error creating output folder: %v", err)
	}

	archVersion := artifact.Arch(goos, goarch)
	if archVersion == "" {
		// Case 2. The architecture is not supported.
		fmt.Printf("unsupported OS/Arch: %s/%s\n", goos, goarch)
		if _, err := exec.LookPath(artifact.Name); err != nil {
			return "", fmt.Errorf("error looking up binary in PATH: %v", err)
		} else {
			outPath = artifact.Name
			fmt.Printf("Using %s from PATH\n", artifact.Name)
		}
	} else {
		// Case 3. Download the binary from the release page
		releasesURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s-%s-%s.tar.gz", artifact.Org, artifact.Name, artifact.Version, artifact.Name, artifact.Version, archVersion)
		fmt.Printf("Downloading %s: %s\n", outPath, releasesURL)

		if err := downloadArtifact(releasesURL, artifact.Name, outPath); err != nil {
			return "", fmt.Errorf("error downloading artifact: %v", err)
		}
	}

	return outPath, nil
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
