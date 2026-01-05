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
			{Name: "never-ready", ReadyCheck: &ReadyCheck{}},
		},
	}

	cfg := &RunnerConfig{
		Manifest: manifest,
	}
	runner, err := NewLocalRunner(cfg)
	require.NoError(t, err)

	// Mark service as started but not ready
	runner.updateTaskStatus("never-ready", TaskStatusStarted)

	waitCtx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	err = runner.WaitForReady(waitCtx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWaitForReady_Success(t *testing.T) {
	// Create a runner with a service that becomes ready
	manifest := &Manifest{
		Services: []*Service{
			{Name: "always-ready", ReadyCheck: &ReadyCheck{}},
		},
	}

	cfg := &RunnerConfig{
		Manifest: manifest,
	}
	runner, err := NewLocalRunner(cfg)
	require.NoError(t, err)

	runner.updateTaskStatus("always-ready", TaskStatusStarted)
	runner.updateTaskStatus("always-ready", TaskStatusHealthy)

	waitCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = runner.WaitForReady(waitCtx)
	require.NoError(t, err)
}
