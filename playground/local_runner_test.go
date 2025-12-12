package playground

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"
)

func TestRunnerPullImages(t *testing.T) {
	imageName, tag := "alpine", "latest"

	client, err := newDockerClient()
	require.NoError(t, err)

	removeImage := func() {
		client.ImageRemove(context.Background(), imageName+":"+tag, image.RemoveOptions{
			Force:         true,
			PruneChildren: true,
		})
	}

	// Ensure the image doesn't exist locally
	removeImage()

	manifest := &Manifest{
		Services: []*Service{
			{Image: imageName, Tag: tag},
			{Image: imageName, Tag: tag},
		},
	}

	numEvents := 0
	callback := func(serviceName, event string) {
		numEvents++
	}

	cfg := &RunnerConfig{
		Manifest: manifest,
		Callback: callback,
	}
	runner, err := NewLocalRunner(cfg)
	require.NoError(t, err)

	err = runner.pullNotAvailableImages(context.Background())
	require.NoError(t, err)

	// 2 'pulling image' + 2 'image pulled'
	require.Equal(t, numEvents, 4)
}
