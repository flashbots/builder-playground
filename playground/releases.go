package playground

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type release struct {
	Name    string
	Repo    string
	Org     string
	Version string
	Arch    func(string, string) string
	// Format specifies the download format: "tar.gz" (default) or "binary"
	Format string
	// BaseURL overrides the default GitHub releases URL (for testing)
	BaseURL string
}

func DownloadRelease(outputFolder string, artifact *release) (string, error) {
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
	if err := os.MkdirAll(outputFolder, 0o755); err != nil {
		return "", fmt.Errorf("error creating output folder: %v", err)
	}

	archVersion := artifact.Arch(goos, goarch)

	baseURL := artifact.BaseURL
	if baseURL == "" {
		baseURL = "https://github.com"
	}

	repo := artifact.Repo
	if repo == "" {
		repo = artifact.Name
	}

	// Handle binary format (raw binary download)
	if artifact.Format == "binary" {
		releasesURL := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s", baseURL, artifact.Org, repo, artifact.Version, artifact.Name)
		log.Printf("Downloading binary %s: %s\n", outPath, releasesURL)

		if err := downloadBinary(releasesURL, outPath); err != nil {
			return "", fmt.Errorf("error downloading binary: %v", err)
		}
	} else if archVersion == "" {
		// Case 2. The architecture is not supported.
		log.Printf("unsupported OS/Arch: %s/%s\n", goos, goarch)
		if _, err := exec.LookPath(artifact.Name); err != nil {
			return "", fmt.Errorf("error looking up binary in PATH: %v", err)
		} else {
			outPath = artifact.Name
			log.Printf("Using %s from PATH\n", artifact.Name)
		}
	} else {
		// Case 3. Download the binary from the release page (tar.gz format)
		releasesURL := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s-%s-%s.tar.gz", baseURL, artifact.Org, repo, artifact.Version, artifact.Name, artifact.Version, archVersion)
		log.Printf("Downloading %s: %s\n", outPath, releasesURL)

		if err := downloadArtifact(releasesURL, artifact.Name, outPath); err != nil {
			return "", fmt.Errorf("error downloading artifact: %v", err)
		}
	}

	return outPath, nil
}

func downloadArtifact(url, expectedFile, outPath string) error {
	slog.Info("downloading artifact", "url", url, "expectedFile", expectedFile, "outPath", outPath)
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
			if err := os.Chmod(outPath, 0o755); err != nil {
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

func downloadBinary(url, outPath string) error {
	slog.Info("downloading binary", "url", url, "outPath", outPath)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("error downloading file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("error creating output file: %v", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return fmt.Errorf("error writing output file: %v", err)
	}

	if err := os.Chmod(outPath, 0o755); err != nil {
		return fmt.Errorf("error changing permissions: %v", err)
	}

	return nil
}
