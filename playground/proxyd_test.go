package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

// TestProxydConfigGeneration tests that proxyd TOML config is generated correctly
func TestProxydConfigGeneration(t *testing.T) {
	o := newTestOutput(t)

	b := NewArtifactsBuilder()
	b.WithL2()
	b.WithProxyd("http://ingress:8080", "http://op-geth:8545")

	require.NoError(t, b.Build(o))

	// Read the generated proxyd config
	config, err := o.Read("proxyd-config.toml")
	require.NoError(t, err)
	require.NotEmpty(t, config)

	// Verify key config sections exist
	require.Contains(t, config, "[server]")
	require.Contains(t, config, "rpc_port = 8545")
	require.Contains(t, config, "[backends.ingress]")
	require.Contains(t, config, `rpc_url = "http://ingress:8080"`)
	require.Contains(t, config, "[backends.standard]")
	require.Contains(t, config, `rpc_url = "http://op-geth:8545"`)

	// Verify transaction submission methods route to ingress
	require.Contains(t, config, `eth_sendRawTransaction = "ingress"`)
	require.Contains(t, config, `eth_sendBundle = "ingress"`)
	require.Contains(t, config, `eth_sendBackrunBundle = "ingress"`)
	require.Contains(t, config, `eth_cancelBundle = "ingress"`)
	require.Contains(t, config, `eth_sendUserOperation = "ingress"`)

	// Verify standard methods route to standard backend
	require.Contains(t, config, `eth_chainId = "standard"`)
	require.Contains(t, config, `eth_call = "standard"`)
	require.Contains(t, config, `eth_blockNumber = "standard"`)
	require.Contains(t, config, `eth_getBalance = "standard"`)

	// Verify Base-specific methods route to standard backend
	require.Contains(t, config, `base_transactionStatus = "standard"`)
	require.Contains(t, config, `base_meterBundle = "standard"`)
	require.Contains(t, config, `base_meteredPriorityFeePerGas = "standard"`)
}

// TestProxydConfigWithFlashblocks tests that flashblocks-rpc is used as standard backend
func TestProxydConfigWithFlashblocks(t *testing.T) {
	o := newTestOutput(t)

	b := NewArtifactsBuilder()
	b.WithL2()
	b.WithProxyd("http://ingress:8080", "http://flashblocks-rpc:8545")

	require.NoError(t, b.Build(o))

	config, err := o.Read("proxyd-config.toml")
	require.NoError(t, err)

	// Verify flashblocks-rpc is the standard backend
	require.Contains(t, config, `rpc_url = "http://flashblocks-rpc:8545"`)

	// Verify base_meter* methods still route to standard (which is flashblocks-rpc)
	require.Contains(t, config, `base_meterBundle = "standard"`)
}

// TestProxydConfigWithCustomMethods tests custom method routing
func TestProxydConfigWithCustomMethods(t *testing.T) {
	o := newTestOutput(t)

	b := NewArtifactsBuilder()
	b.WithL2()
	b.WithProxyd("http://ingress:8080", "http://op-geth:8545")
	b.ProxydIngressMethods([]string{"eth_customSubmit", "eth_batchSubmit"})
	b.ProxydStandardMethods([]string{"debug_customTrace", "trace_customBlock"})

	require.NoError(t, b.Build(o))

	config, err := o.Read("proxyd-config.toml")
	require.NoError(t, err)

	// Verify custom methods are routed correctly
	require.Contains(t, config, `eth_customSubmit = "ingress"`)
	require.Contains(t, config, `eth_batchSubmit = "ingress"`)
	require.Contains(t, config, `debug_customTrace = "standard"`)
	require.Contains(t, config, `trace_customBlock = "standard"`)
}

// TestRecipeOpstackWithProxyd tests the opstack recipe with proxyd enabled
func TestRecipeOpstackWithProxyd(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	manifest := tt.test(&OpRecipe{}, []string{
		"--proxyd",
		"--ingress-rpc", "http://op-geth:8545", // Use op-geth as mock ingress for this test
	})

	// Verify proxyd service exists
	proxydService := manifest.MustGetService("proxyd")
	require.NotNil(t, proxydService)

	// Verify proxyd has http port exposed
	httpPort := proxydService.MustGetPort("http")
	require.NotNil(t, httpPort)
	require.Equal(t, 8545, httpPort.Port)

	// Test that proxyd can handle standard RPC methods
	client, err := ethclient.Dial(fmt.Sprintf("http://localhost:%d", httpPort.HostPort))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// eth_chainId should route to standard backend (op-geth)
	chainID, err := client.ChainID(ctx)
	require.NoError(t, err)
	require.NotNil(t, chainID)
}

// TestRecipeOpstackWithProxydAndFlashblocks tests proxyd with flashblocks enabled
func TestRecipeOpstackWithProxydAndFlashblocks(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	manifest := tt.test(&OpRecipe{}, []string{
		"--proxyd",
		"--flashblocks",
		"--external-builder", "op-rbuilder",
		"--ingress-rpc", "http://flashblocks-rpc:8545", // Use flashblocks-rpc as mock ingress for this test
	})

	// Verify proxyd service exists
	proxydService := manifest.MustGetService("proxyd")
	require.NotNil(t, proxydService)

	// Verify flashblocks-rpc service exists
	flashblocksService := manifest.MustGetService("flashblocks-rpc")
	require.NotNil(t, flashblocksService)

	// Test that proxyd routes to flashblocks-rpc
	proxydPort := proxydService.MustGetPort("http")
	client, err := ethclient.Dial(fmt.Sprintf("http://localhost:%d", proxydPort.HostPort))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Standard method should work via flashblocks-rpc
	chainID, err := client.ChainID(ctx)
	require.NoError(t, err)
	require.NotNil(t, chainID)
}

// TestRecipeOpstackWithProxydCustomMethods tests custom method routing flags
func TestRecipeOpstackWithProxydCustomMethods(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	tt.test(&OpRecipe{}, []string{
		"--proxyd",
		"--ingress-rpc", "http://op-geth:8545",
		"--proxyd-ingress-methods", "eth_customMethod1,eth_customMethod2",
		"--proxyd-standard-methods", "debug_customMethod",
	})

	// Read the generated config to verify custom methods
	config, err := os.ReadFile(tt.runner.config.Out.dst + "/proxyd-config.toml")
	require.NoError(t, err)

	configStr := string(config)
	require.Contains(t, configStr, `eth_customMethod1 = "ingress"`)
	require.Contains(t, configStr, `eth_customMethod2 = "ingress"`)
	require.Contains(t, configStr, `debug_customMethod = "standard"`)
}

// TestProxydOutput tests that proxyd RPC endpoint is in recipe output
func TestProxydOutput(t *testing.T) {
	tt := newTestFramework(t)
	defer tt.Close()

	manifest := tt.test(&OpRecipe{}, []string{
		"--proxyd",
		"--ingress-rpc", "http://op-geth:8545",
	})

	recipe := &OpRecipe{
		enableProxyd: true,
	}

	output := recipe.Output(manifest)
	require.Contains(t, output, "proxyd-rpc")

	proxydRPC, ok := output["proxyd-rpc"].(string)
	require.True(t, ok)
	require.Contains(t, proxydRPC, "http://localhost:")

	// Verify the proxyd endpoint is accessible
	resp, err := http.Get(proxydRPC)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should get a JSON-RPC error for GET request (requires POST)
	// but confirms the endpoint is listening
	var jsonResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&jsonResp)
	// Either we get a valid JSON-RPC response or the endpoint is accessible
	// (some implementations return error for GET, others accept it)
	require.True(t, err == nil || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusMethodNotAllowed)
}
