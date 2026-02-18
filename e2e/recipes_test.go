package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

func TestL1(t *testing.T) {
	p := newPlaygroundInstance(t)
	defer p.cleanup()

	p.startPlayground("l1")

	httpPort := p.getServicePort("el", "http")
	rpcURL := fmt.Sprintf("http://localhost:%d", httpPort)
	p.waitForBlock(rpcURL, 1)
}

func TestL1WithNativeReth(t *testing.T) {
	p := newPlaygroundInstance(t)
	defer p.cleanup()

	p.runPlayground("l1", "--use-native-reth")
}

func TestL1WithPrometheus(t *testing.T) {
	p := newPlaygroundInstance(t)
	defer p.cleanup()

	p.runPlayground("l1", "--with-prometheus")
}

func TestOpstack(t *testing.T) {
	p := newPlaygroundInstance(t)
	defer p.cleanup()

	p.startPlayground("opstack")

	httpPort := p.getServicePort("op-geth", "http")
	rpcURL := fmt.Sprintf("http://localhost:%d", httpPort)

	client, err := ethclient.Dial(rpcURL)
	require.NoError(t, err, "failed to connect to op-geth")

	knownAddress := common.HexToAddress("0xf49Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	balance, err := client.BalanceAt(context.Background(), knownAddress, nil)
	require.NoError(t, err)
	require.NotEqual(t, balance, big.NewInt(0), "prefunded account should have non-zero balance")
}

func TestOpstackExternalBuilder(t *testing.T) {
	p := newPlaygroundInstance(t)
	defer p.cleanup()

	p.runPlayground("opstack", "--external-builder", "http://host.docker.internal:4444")
}

func TestOpstackEnableLatestFork0(t *testing.T) {
	p := newPlaygroundInstance(t)
	defer p.cleanup()

	p.runPlayground("opstack", "--enable-latest-fork=0")
}

func TestOpstackEnableLatestFork10(t *testing.T) {
	p := newPlaygroundInstance(t)
	defer p.cleanup()

	p.startPlayground("opstack", "--enable-latest-fork=10")

	httpPort := p.getServicePort("op-geth", "http")
	rpcURL := fmt.Sprintf("http://localhost:%d", httpPort)
	p.waitForBlock(rpcURL, 11)
}

func TestBuilderNet(t *testing.T) {
	p := newPlaygroundInstance(t)
	defer p.cleanup()

	p.startPlayground("buildernet")

	httpPort := p.getServicePort("builder-hub-api", "http")
	adminPort := p.getServicePort("builder-hub-api", "admin")
	internalPort := p.getServicePort("builder-hub-api", "internal")
	proxyPort := p.getServicePort("builder-hub-proxy", "http")

	httpEndpoint := fmt.Sprintf("http://localhost:%d", httpPort)
	admin := fmt.Sprintf("http://localhost:%d", adminPort)
	internal := fmt.Sprintf("http://localhost:%d", internalPort)
	proxy := fmt.Sprintf("http://localhost:%d", proxyPort)

	measurementPayload := `{
		"measurement_id": "test1",
		"attestation_type": "test",
		"measurements": {}
	}`
	resp, err := http.Post(admin+"/api/admin/v1/measurements", "application/json", strings.NewReader(measurementPayload))
	require.NoError(t, err)
	defer resp.Body.Close()

	activationPayload := `{"enabled": true}`
	resp, err = http.Post(admin+"/api/admin/v1/measurements/activation/test1", "application/json", strings.NewReader(activationPayload))
	require.NoError(t, err)
	defer resp.Body.Close()

	type measurementList []struct {
		MeasurementID string `json:"measurement_id"`
	}

	endpoints := []string{httpEndpoint, internal, proxy}
	for _, endpoint := range endpoints {
		var m measurementList
		resp, err = http.Get(endpoint + "/api/l1-builder/v1/measurements")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
		require.Equal(t, 1, len(m))
		require.Equal(t, "test1", m[0].MeasurementID)
	}

	require.Equal(t, http.StatusOK, resp.StatusCode)
}
