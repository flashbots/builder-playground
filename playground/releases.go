package playground

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

var ErrPlatformNotSupported = errors.New("platform not supported")

type ArchiveFormat string

const (
	FormatTarGz  ArchiveFormat = "tar.gz"
	FormatZip    ArchiveFormat = "zip"
	FormatBinary ArchiveFormat = "binary"
)

type release struct {
	Name    string
	Repo    string
	Org     string
	Version string
	Arch    func(string, string) (string, bool)

	// URL template for download. Variables: .Name, .Repo, .Org, .Version, .Arch, .GOOS, .GOARCH
	URL string
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

	archVersion, ok := artifact.Arch(goos, goarch)
	if !ok {
		log.Printf("unsupported OS/Arch: %s/%s\n", goos, goarch)
		if _, err := exec.LookPath(artifact.Name); err != nil {
			return "", fmt.Errorf("error looking up binary in PATH: %v", err)
		}
		outPath = artifact.Name
		log.Printf("Using %s from PATH\n", artifact.Name)
		return outPath, nil
	}

	// Build URL from template
	repo := artifact.Repo
	if repo == "" {
		repo = artifact.Name
	}

	tmpl, err := template.New("url").Parse(artifact.URL)
	if err != nil {
		return "", fmt.Errorf("error parsing URL template: %v", err)
	}

	var urlBuf bytes.Buffer
	templateData := map[string]string{
		"Name":    artifact.Name,
		"Repo":    repo,
		"Org":     artifact.Org,
		"Version": artifact.Version,
		"Arch":    archVersion,
		"GOOS":    goos,
		"GOARCH":  goarch,
	}
	if err := tmpl.Execute(&urlBuf, templateData); err != nil {
		return "", fmt.Errorf("error executing URL template: %v", err)
	}

	downloadURL := urlBuf.String()
	log.Printf("Downloading %s: %s\n", outPath, downloadURL)

	if err := downloadFile(downloadURL, outPath, ""); err != nil {
		return "", fmt.Errorf("error downloading file: %v", err)
	}

	return outPath, nil
}

// detectFormat determines the archive format from the URL or returns the provided format.
// If format is empty, it attempts to detect from the URL extension.
// Returns one of FormatTarGz, FormatZip, or FormatBinary (default if no format detected)
func detectFormat(url string, format ArchiveFormat) ArchiveFormat {
	if format != "" {
		return format
	}
	if strings.HasSuffix(url, ".tar.gz") {
		return FormatTarGz
	}
	if strings.HasSuffix(url, ".zip") {
		return FormatZip
	}
	return FormatBinary
}

// downloadFile downloads a file from url and extracts it based on the format.
// format can be FormatTarGz, FormatZip, or FormatBinary (or empty to auto-detect).
// For archives, extracts the first regular file found.
func downloadFile(url, outPath string, format ArchiveFormat) error {
	format = detectFormat(url, format)
	slog.Info("downloading file", "url", url, "format", format, "outPath", outPath)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("error downloading file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	switch format {
	case FormatTarGz:
		return extractTarGz(resp.Body, outPath)
	case FormatZip:
		return extractZip(resp.Body, outPath)
	case FormatBinary:
		return writeBinary(resp.Body, outPath)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func extractTarGz(r io.Reader, outPath string) error {
	gzipReader, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("error creating gzip reader: %v", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	// Extract the first regular file found
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %v", err)
		}

		if header.Typeflag == tar.TypeReg {
			outFile, err := os.Create(outPath)
			if err != nil {
				return fmt.Errorf("error creating output file: %v", err)
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, tarReader); err != nil {
				return fmt.Errorf("error writing output file: %v", err)
			}

			if err := os.Chmod(outPath, 0o755); err != nil {
				return fmt.Errorf("error changing permissions: %v", err)
			}
			return nil
		}
	}

	return fmt.Errorf("no regular file found in archive")
}

func extractZip(r io.Reader, outPath string) error {
	// Read the entire response into memory (required for zip.NewReader)
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("error reading response: %v", err)
	}

	zipReader, err := zip.NewReader(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		return fmt.Errorf("error creating zip reader: %v", err)
	}

	// Extract the first regular file found
	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("error opening file in zip: %v", err)
		}
		defer rc.Close()

		outFile, err := os.Create(outPath)
		if err != nil {
			return fmt.Errorf("error creating output file: %v", err)
		}
		defer outFile.Close()

		if _, err := io.Copy(outFile, rc); err != nil {
			return fmt.Errorf("error writing output file: %v", err)
		}

		if err := os.Chmod(outPath, 0o755); err != nil {
			return fmt.Errorf("error changing permissions: %v", err)
		}
		return nil
	}

	return fmt.Errorf("no regular file found in archive")
}

func writeBinary(r io.Reader, outPath string) error {
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("error creating output file: %v", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, r); err != nil {
		return fmt.Errorf("error writing output file: %v", err)
	}

	if err := os.Chmod(outPath, 0o755); err != nil {
		return fmt.Errorf("error changing permissions: %v", err)
	}

	return nil
}
