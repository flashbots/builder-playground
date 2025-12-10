package playground

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
)

func TestImagePuller_PullImage(t *testing.T) {
	ctx := context.Background()

	// Create docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer dockerClient.Close()

	puller := newImagePuller(dockerClient)
	imageName := "alpine:latest"

	// Ensure the image doesn't exist locally
	dockerClient.ImageRemove(ctx, imageName, image.RemoveOptions{
		Force:         true,
		PruneChildren: true,
	})

	// Pull the image
	err = puller.PullImage(ctx, imageName)
	require.NoError(t, err)

	// Verify the image exists
	_, err = dockerClient.ImageInspect(ctx, imageName)
	require.NoError(t, err)

	// Clean up
	_, err = dockerClient.ImageRemove(ctx, imageName, image.RemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	require.NoError(t, err)
}
