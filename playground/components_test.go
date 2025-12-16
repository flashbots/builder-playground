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

func TestRecipeOpstackSimple(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&OpRecipe{}, nil)
}

func TestRecipeOpstackExternalBuilder(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&OpRecipe{}, []string{
		"--external-builder", "http://host.docker.internal:4444",
	})
}

func TestRecipeOpstackEnableForkAfter(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	forkTime := uint64(10)
	manifest := tt.test(&OpRecipe{}, []string{
		"--enable-latest-fork", "10",
	})

	elService := manifest.MustGetService("op-geth")
	rethURL := fmt.Sprintf("http://localhost:%d", elService.MustGetPort("http").HostPort)
	require.NoError(t, waitForBlock(rethURL, forkTime+1, 1*time.Minute))
}

func TestRecipeL1Simple(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&L1Recipe{}, nil)
}

func TestRecipeL1UseNativeReth(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&L1Recipe{}, []string{
		"--use-native-reth",
	})
}

func TestComponentBuilderHub(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&BuilderHub{}, nil)

	// TODO: Calling the port directly on the host machine will not work once we have multiple
	// tests running in parallel
	resp, err := http.Get("http://localhost:8080/api/l1-builder/v1/measurements")
	require.NoError(t, err)
	require.Equal(t, resp.StatusCode, http.StatusOK)
}

type testFramework struct {
	t      *testing.T
	runner *LocalRunner
}

func newTestFramework(t *testing.T) *testFramework {
	return &testFramework{t: t}
}

func (tt *testFramework) test(s ServiceGen, args []string) *Manifest {
	t := tt.t

	// use the name of the repo and the current timestamp to generate
	// a name for the output folder of the test
	testName := toSnakeCase(t.Name())
	currentTime := time.Now().Format("2006-01-02-15-04")

	e2eTestDir := filepath.Join("../e2e-test/" + currentTime + "_" + testName)
	if err := os.MkdirAll(e2eTestDir, 0o755); err != nil {
		t.Fatal(err)
	}

	exCtx := &ExContext{
		LogLevel: LevelDebug,
		Contender: &ContenderContext{
			Enabled: false,
		},
	}

	o := &output{
		dst:     e2eTestDir,
		homeDir: filepath.Join(e2eTestDir, "artifacts"),
	}

	if recipe, ok := s.(Recipe); ok {
		// We have to parse the flags since they are used to set the
		// default values for the recipe inputs
		err := recipe.Flags().Parse(args)
		require.NoError(t, err)

		_, err = recipe.Artifacts().OutputDir(e2eTestDir).Build()
		require.NoError(t, err)
	}

	svcManager := NewManifest(exCtx, o)
	s.Apply(svcManager)

	require.NoError(t, svcManager.Validate())

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
	tt.runner = dockerRunner

	err = dockerRunner.Run(context.Background())
	require.NoError(t, err)

	require.NoError(t, dockerRunner.WaitForReady(context.Background(), 20*time.Second))
	return svcManager
}

func (tt *testFramework) Close() {
	if tt.runner != nil {
		if err := tt.runner.Stop(); err != nil {
			tt.t.Log(err)
		}
	}
}

func toSnakeCase(s string) string {
	// Insert underscore before uppercase letters
	re := regexp.MustCompile("([a-z0-9])([A-Z])")
	snake := re.ReplaceAllString(s, "${1}_${2}")

	// Convert to lowercase
	return strings.ToLower(snake)
}
