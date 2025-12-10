package playground

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogReleases(t *testing.T) {
	out := newTestOutput(t)

	tests := []*release{
		opRethRelease,
		rethELRelease,
	}

	for _, tc := range tests {
		_, err := DownloadRelease(out.dst, tc)
		require.NoError(t, err)
	}
}
