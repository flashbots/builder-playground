package playground

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var rbuilderRelease = &release{
	Name:    "rbuilder",
	Org:     "flashbots",
	Version: "v1.3.5",
	Arch: func(goos, goarch string) (string, bool) {
		if goos == "linux" {
			return "", true
		}
		return "", false
	},
	URL: "https://github.com/{{.Org}}/{{.Repo}}/releases/download/{{.Version}}/{{.Name}}",
}

func TestReleases(t *testing.T) {
	out := newTestOutput(t)

	tests := []*release{
		rbuilderRelease,
		opRethRelease,
		rethELRelease,
	}

	for _, tc := range tests {
		binPath, err := DownloadRelease(out.dst, tc)
		if err != nil {
			if strings.Contains(err.Error(), "error looking up binary in PATH") {
				// TODO: This should be done better without the error string matching
				t.Logf("release '%s' does not have support in this arch", tc.Name)
				continue
			} else {
				t.Fatal(err)
			}
		}

		// Verify the binary is executable
		info, err := os.Stat(binPath)
		require.NoError(t, err)
		require.NotZero(t, info.Mode()&0o111, "binary should be executable")
	}
}
