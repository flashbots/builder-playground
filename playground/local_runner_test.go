package playground

import (
	"context"
	"os"
	"os/exec"
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

func TestCheckAndUpdateReadiness_MultipleCallsNoPanic(t *testing.T) {
	manifest := &Manifest{
		Services: []*Service{
			{Name: "test-service"},
		},
	}

	cfg := &RunnerConfig{
		Manifest: manifest,
	}
	runner, err := NewLocalRunner(cfg)
	require.NoError(t, err)

	runner.updateTaskStatus("test-service", TaskStatusStarted)

	require.NotPanics(t, func() {
		runner.checkAndUpdateReadiness()
	})
}

func TestHostServiceDependency_WithHealthCheck(t *testing.T) {
	// Test scenario:
	// - Service A (host): Python HTTP server on port 18080 with health check
	// - Service B (docker): Nginx HTTP server on port 80 with health check
	// - Service C (host): Sleep command that depends on BOTH A and B being healthy
	//
	// This test verifies:
	// 1. Host services with ReadyCheck get health tracked via trackHostServiceHealth
	// 2. Docker services get health tracked via Docker events
	// 3. waitForDependencies polls task status for both host and docker services
	// 4. Service C waits for both A and B to be healthy before starting

	// Find python3 binary
	python3Path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found, skipping test")
	}

	tmpDir := os.TempDir()
	out := &output{dst: tmpDir, homeDir: tmpDir}

	// Service A: Host HTTP server on port 18080
	serviceA := &Service{
		Name:     "host-server",
		HostPath: python3Path,
		Args:     []string{"-m", "http.server", "18080"},
		Ports: []*Port{
			{Name: "http", Port: 18080, HostPort: 18080, Protocol: ProtocolTCP},
		},
		ReadyCheck: &ReadyCheck{
			QueryURL: "http://localhost:18080",
			Interval: 500 * time.Millisecond,
			Timeout:  2 * time.Second,
		},
	}

	// Service B: Docker nginx server with health check
	serviceB := &Service{
		Name:  "docker-server",
		Image: "nginx",
		Tag:   "alpine",
		Ports: []*Port{
			{Name: "http", Port: 80, Protocol: ProtocolTCP},
		},
		ReadyCheck: &ReadyCheck{
			QueryURL: "http://localhost:80",
			Interval: 1 * time.Second,
			Timeout:  2 * time.Second,
		},
	}

	// Service C: Host service that depends on both A and B
	serviceC := &Service{
		Name:     "client",
		HostPath: "/bin/sleep",
		Args:     []string{"10"},
		DependsOn: []*DependsOn{
			{Name: "host-server", Condition: DependsOnConditionHealthy},
			{Name: "docker-server", Condition: DependsOnConditionHealthy},
		},
	}

	manifest := &Manifest{
		ID:       "test",
		Services: []*Service{serviceA, serviceB, serviceC},
	}

	// Validate manifest to create healthmon sidecars for docker services
	err = manifest.Validate(out)
	require.NoError(t, err)

	cfg := &RunnerConfig{
		Out:      out,
		Manifest: manifest,
	}
	runner, err := NewLocalRunner(cfg)
	require.NoError(t, err)
	defer runner.Stop(false)

	ctx := context.Background()
	err = runner.Run(ctx)
	require.NoError(t, err)

	// Wait for all services to be ready (healthy)
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err = runner.WaitForReady(waitCtx)
	require.NoError(t, err, "all services should become ready")

	runner.tasksMtx.Lock()
	defer runner.tasksMtx.Unlock()

	for name, task := range runner.tasks {
		require.Equal(t, TaskStatusStarted, task.status, "task %s should be started", name)
	}
}
