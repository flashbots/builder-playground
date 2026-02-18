package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/flashbots/builder-playground/playground"
	"github.com/stretchr/testify/require"
)

// startupMu ensures only one playground starts at a time
var startupMu sync.Mutex

// lineBuffer captures output and allows checking for specific strings
type lineBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (b *lineBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, string(p))
	return len(p), nil
}

func (b *lineBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, "")
}

func (b *lineBuffer) Contains(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range b.lines {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

// playgroundInstance holds state for a single playground run
type playgroundInstance struct {
	t              *testing.T
	cmd            *exec.Cmd
	outputDir      string
	manifestPath   string
	manifest       *playground.Manifest
	manifestLoaded bool
	cmdErrChan     chan error
	outputBuffer   *lineBuffer
}

func getRepoRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filename))
}

func getBinaryPath() string {
	return filepath.Join(getRepoRoot(), "builder-playground")
}

func newPlaygroundInstance(t *testing.T) *playgroundInstance {
	if strings.ToLower(os.Getenv("E2E_TESTS")) != "true" {
		t.Skip("e2e tests not enabled")
	}
	t.Parallel()
	outputDir := filepath.Join("/tmp", "e2e-test", fmt.Sprintf("%d", time.Now().UnixNano()))
	return &playgroundInstance{
		t:            t,
		outputDir:    outputDir,
		manifestPath: filepath.Join(outputDir, "manifest.json"),
	}
}

func (p *playgroundInstance) cleanup() {
	// Dump buffered logs at the end of the test
	if p.outputBuffer != nil {
		p.t.Logf("=== Playground logs for %s ===\n%s", p.t.Name(), p.outputBuffer.String())
	}

	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Signal(os.Interrupt)
		if p.cmdErrChan != nil {
			select {
			case <-p.cmdErrChan:
			case <-time.After(10 * time.Second):
				p.cmd.Process.Kill()
			}
		}
	}
	if p.outputDir != "" {
		os.RemoveAll(p.outputDir)
	}
}

func (p *playgroundInstance) launchPlayground(args []string) {
	startupMu.Lock()

	cmdArgs := append([]string{"start"}, args...)
	cmdArgs = append(cmdArgs, "--output", p.outputDir)

	cmd := exec.Command(getBinaryPath(), cmdArgs...)
	cmd.Dir = getRepoRoot()

	p.outputBuffer = &lineBuffer{}
	cmd.Stdout = p.outputBuffer
	cmd.Stderr = p.outputBuffer

	err := cmd.Start()
	require.NoError(p.t, err, "failed to start playground")

	p.cmd = cmd

	p.cmdErrChan = make(chan error, 1)
	go func() {
		p.cmdErrChan <- cmd.Wait()
	}()

	// Wait until "Waiting for services to get healthy" appears - this means ports have been allocated
	p.waitForOutput("Waiting for services to get healthy", 60*time.Second)
	startupMu.Unlock()
}

func (p *playgroundInstance) runPlayground(args ...string) {
	p.launchPlayground(append(args, "--timeout", "10s"))

	// Wait for process to complete (it has --timeout so it will exit)
	err := <-p.cmdErrChan
	require.NoError(p.t, err, "playground exited with error")
}

func (p *playgroundInstance) startPlayground(args ...string) {
	p.launchPlayground(args)
	p.waitForReady()
}

func (p *playgroundInstance) waitForOutput(message string, timeout time.Duration) {
	timeoutCh := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-p.cmdErrChan:
			p.t.Fatalf("playground process exited before '%s': %v", message, err)
		case <-timeoutCh:
			p.t.Fatalf("timeout waiting for '%s' message", message)
		case <-ticker.C:
			if p.outputBuffer.Contains(message) {
				p.t.Logf("Found message: %s", message)
				return
			}
		}
	}
}

func (p *playgroundInstance) waitForReady() {
	p.t.Logf("Waiting for playground to be ready...")

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(90 * time.Second)

	for {
		select {
		case err := <-p.cmdErrChan:
			if err != nil {
				p.t.Fatalf("playground process exited with error: %v", err)
			}
			if !p.outputBuffer.Contains("All services are healthy") {
				p.t.Fatalf("playground process exited before services were ready")
			}
			return
		case <-timeout:
			p.t.Fatalf("timeout waiting for playground to be ready")
		case <-ticker.C:
			p.tryLoadManifest()
			if p.outputBuffer.Contains("All services are healthy") {
				p.t.Logf("Services are ready")
				return
			}
		}
	}
}

func (p *playgroundInstance) tryLoadManifest() {
	if p.manifestLoaded {
		return
	}
	if _, err := os.Stat(p.manifestPath); err != nil {
		return
	}
	data, err := os.ReadFile(p.manifestPath)
	if err != nil || len(data) == 0 {
		return
	}
	var manifest playground.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return
	}
	p.manifest = &manifest
	p.t.Logf("Manifest loaded with session ID: %s", manifest.ID)
	p.manifestLoaded = true
}

func (p *playgroundInstance) getServicePort(serviceName, portName string) int {
	require.NotNil(p.t, p.manifest, "manifest not loaded")

	var lastErr error
	for i := 0; i < 10; i++ {
		portStr, err := playground.GetServicePort(p.manifest.ID, serviceName, portName)
		if err == nil {
			port, err := strconv.Atoi(portStr)
			if err == nil {
				return port
			}
			lastErr = err
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	p.t.Fatalf("failed to get port %s on service %s: %v", portName, serviceName, lastErr)
	return 0
}

func (p *playgroundInstance) waitForBlock(rpcURL string, targetBlock uint64) {
	rpcClient, err := rpc.Dial(rpcURL)
	require.NoError(p.t, err, "failed to dial RPC")
	defer rpcClient.Close()

	clt := ethclient.NewClient(rpcClient)
	timeout := time.After(time.Minute)

	for {
		select {
		case <-timeout:
			p.t.Fatalf("timeout waiting for block %d on %s", targetBlock, rpcURL)
		case <-time.After(500 * time.Millisecond):
			num, err := clt.BlockNumber(context.Background())
			if err != nil {
				continue
			}
			if num >= targetBlock {
				return
			}
		}
	}
}
