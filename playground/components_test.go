package playground

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuilderHub2(t *testing.T) {
	testComponent(t, &BuilderHub2{})

	// TODO: Calling the port directly on the host machine will not work once we have multiple
	// tests running in parallel
	resp, err := http.Get("http://localhost:8080/api/l1-builder/v1/measurements")
	require.NoError(t, err)
	require.Equal(t, resp.StatusCode, http.StatusOK)
}

func testComponent(t *testing.T, s ServiceGen) {
	// use the name of the repo and the current timestamp to generate
	// a name for the output folder of the test
	testName := toSnakeCase(t.Name())
	currentTime := time.Now().Format("2006-02-01-15-04")

	e2eTestDir := filepath.Join("../e2e-test/" + currentTime + "_" + testName)
	if err := os.MkdirAll(e2eTestDir, 0755); err != nil {
		t.Fatal(err)
	}

	exCtx := &ExContext{
		LogLevel: LevelDebug,
		Contender: &ContenderContext{
			Enabled: false,
		},
	}

	o := &output{
		dst: e2eTestDir,
	}
	svcManager := NewManifest(exCtx, o)
	s.Apply(svcManager)

	if err := svcManager.Validate(); err != nil {
		t.Fatal(err)
	}

	// Generate random network name with "testing-" prefix
	networkName := fmt.Sprintf("testing-%d", rand.Int63())

	cfg := &RunnerConfig{
		Out:           o,
		Manifest:      svcManager,
		NetworkName:   networkName,
		Labels:        map[string]string{"e2e": "true"},
		LogInternally: true,
	}
	dockerRunner, err := NewLocalRunner(cfg)
	require.NoError(t, err)
	dockerRunner.cleanupNetwork = true

	defer func() {
		if err := dockerRunner.Stop(); err != nil {
			t.Log(err)
		}
	}()

	err = dockerRunner.Run()
	require.NoError(t, err)

	require.NoError(t, dockerRunner.WaitForReady(context.Background(), 20*time.Second))
	require.NoError(t, CompleteReady(dockerRunner.Instances()))
}

func toSnakeCase(s string) string {
	// Insert underscore before uppercase letters
	re := regexp.MustCompile("([a-z0-9])([A-Z])")
	snake := re.ReplaceAllString(s, "${1}_${2}")

	// Convert to lowercase
	return strings.ToLower(snake)
}
