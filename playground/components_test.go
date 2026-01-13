package playground

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

func TestRecipeOpstackSimple(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	m := tt.test(&OpRecipe{}, nil)

	httpPort := m.MustGetService("op-geth").MustGetPort("http")
	client, err := ethclient.Dial(fmt.Sprintf("http://localhost:%d", httpPort.HostPort))
	require.NoError(t, err)

	// validate that the default addresses are prefunded
	knownAddress := common.HexToAddress("0xf49Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	balance, err := client.BalanceAt(context.Background(), knownAddress, nil)
	require.NoError(t, err)
	require.NotEqual(t, balance, big.NewInt(0))
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

func TestRecipeBuilderHub(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&BuilderHub{}, nil)

	// TODO: Calling the port directly on the host machine will not work once we have multiple
	// tests running in parallel

	// Set measurements from the admin API.
	buf := bytes.NewBuffer([]byte(`
		{
			"measurement_id": "test1",
			"attestation_type": "test",
			"measurements": {}
		}
	`))
	resp, err := http.Post("http://localhost:8081/api/admin/v1/measurements", "application/json", buf)
	require.NoError(t, err)
	defer resp.Body.Close()

	buf = bytes.NewBuffer([]byte(`
		{
			"enabled": true
		}
	`))
	resp, err = http.Post("http://localhost:8081/api/admin/v1/measurements/activation/test1", "application/json", buf)
	require.NoError(t, err)
	defer resp.Body.Close()

	type measurementList []struct {
		MeasurementID string `json:"measurement_id"`
	}

	// Verify from all APIs that measurements are in place.
	ports := []string{"8080", "8082", "8888"}
	for _, port := range ports {
		var m measurementList
		resp, err = http.Get("http://localhost:" + port + "/api/l1-builder/v1/measurements")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
		require.Equal(t, 1, len(m))
		require.Equal(t, "test1", m[0].MeasurementID)
	}

	require.Equal(t, resp.StatusCode, http.StatusOK)
}

func TestRecipeBuilderHub_RegisterBuilder(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&BuilderHub{}, nil)
}

func TestRecipeBuilderNet(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&BuilderNetRecipe{}, []string{})
}

type testFramework struct {
	t      *testing.T
	runner *LocalRunner
}

func newTestFramework(t *testing.T) *testFramework {
	if strings.ToLower(os.Getenv("INTEGRATION_TESTS")) != "true" {
		t.Skip("integration tests not enabled")
	}
	return &testFramework{t: t}
}

func (tt *testFramework) test(component Component, args []string) *Manifest {
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

	if recipe, ok := component.(Recipe); ok {
		// We have to parse the flags since they are used to set the
		// default values for the recipe inputs
		err := recipe.Flags().Parse(args)
		require.NoError(t, err)

		err = recipe.Artifacts().Build(o)
		require.NoError(t, err)
	}

	svcManager := NewManifest(exCtx, o)
	component.Apply(svcManager)

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

	t.Cleanup(func() {
		require.NoError(t, dockerRunner.Stop(false))
	})

	tt.runner = dockerRunner

	err = dockerRunner.Run(context.Background())
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	require.NoError(t, dockerRunner.WaitForReady(waitCtx))
	return svcManager
}

func (tt *testFramework) Close() {
	if tt.runner != nil {
		if err := tt.runner.Stop(false); err != nil {
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

func waitForBlock(elURL string, targetBlock uint64, timeout time.Duration) error {
	rpcClient, err := rpc.Dial(elURL)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", elURL, err)
	}
	defer rpcClient.Close()

	clt := ethclient.NewClient(rpcClient)
	timeoutCh := time.After(timeout)

	for {
		select {
		case <-timeoutCh:
			return fmt.Errorf("timeout waiting for block %d on %s", targetBlock, elURL)
		case <-time.After(500 * time.Millisecond):
			num, err := clt.BlockNumber(context.Background())
			if err != nil {
				continue
			}
			if num >= targetBlock {
				return nil
			}
		}
	}
}
