package playground

import (
	"context"
	"testing"
	"time"

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
	callback := func(serviceName string, event TaskStatus) {
		numEvents++
	}

	cfg := &RunnerConfig{
		Manifest:  manifest,
		Callbacks: []Callback{callback},
	}
	runner, err := NewLocalRunner(cfg)
	require.NoError(t, err)

	err = runner.pullNotAvailableImages(context.Background())
	require.NoError(t, err)

	// 2 'pulling image' + 2 'image pulled'
	require.Equal(t, numEvents, 4)
}

func TestWaitForReady_Timeout(t *testing.T) {
	// Create a runner with a service that never becomes ready
	manifest := &Manifest{
		Services: []*Service{
			{Name: "never-ready"},
		},
	}

	cfg := &RunnerConfig{
		Manifest: manifest,
	}
	runner, err := NewLocalRunner(cfg)
	require.NoError(t, err)

	// Mark service as started but not ready
	runner.updateTaskStatus("never-ready", TaskStatusStarted)

	ctx := context.Background()
	err = runner.WaitForReady(ctx, 500*time.Millisecond)
	require.Error(t, err)
	require.Equal(t, "timeout", err.Error())
}

func TestWaitForReady_Success(t *testing.T) {
	// Create a runner with a service that becomes ready
	manifest := &Manifest{
		Services: []*Service{
			{Name: "ready-service"},
		},
	}

	cfg := &RunnerConfig{
		Manifest: manifest,
	}
	runner, err := NewLocalRunner(cfg)
	require.NoError(t, err)

	// Service becomes ready after a delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		runner.updateTaskStatus("ready-service", TaskStatusStarted)
	}()

	ctx := context.Background()
	err = runner.WaitForReady(ctx, 2*time.Second)
	require.NoError(t, err)
}
