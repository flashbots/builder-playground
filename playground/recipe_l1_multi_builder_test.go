package playground

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestL1MultiBuilderRecipeWiresBuildersToRelays(t *testing.T) {
	out, err := NewOutput("test-l1-multi-builder", filepath.Join(t.TempDir(), "out"))
	require.NoError(t, err)

	recipe := &L1MultiBuilderRecipe{
		blockTime:    12 * time.Second,
		builderCount: 3,
		relayCount:   2,
	}
	component := recipe.Apply(&ExContext{
		Output:    out,
		Contender: &ContenderContext{},
	})
	manifest := NewManifest("test-l1-multi-builder", component)

	require.NotNil(t, manifest.MustGetService("mev-boost-relay-1"))
	require.NotNil(t, manifest.MustGetService("mev-boost-relay-2"))
	require.NotNil(t, manifest.MustGetService("rbuilder-1"))
	require.NotNil(t, manifest.MustGetService("rbuilder-2"))
	require.NotNil(t, manifest.MustGetService("rbuilder-3"))
	require.NotNil(t, manifest.MustGetService("flowproxy-1"))
	require.NotNil(t, manifest.MustGetService("flowproxy-2"))
	require.NotNil(t, manifest.MustGetService("flowproxy-3"))

	el := manifest.MustGetService("el")
	require.Contains(t, strings.Join(el.Args, " "), "admin,eth,web3,net,txpool,rpc,mev,flashbots")

	mevBoost := manifest.MustGetService("mev-boost")
	mevBoostArgs := strings.Join(mevBoost.Args, " ")
	require.Contains(t, mevBoostArgs, `{{Service "mev-boost-relay-1" "http" "http" "0x`)
	require.Contains(t, mevBoostArgs, `{{Service "mev-boost-relay-2" "http" "http" "0x`)

	rbuilder := manifest.MustGetService("rbuilder-2")
	require.Equal(t, "service:el", rbuilder.Pid)
	require.Equal(t, 8646, rbuilder.MustGetPort("rpc").Port)
	require.Equal(t, 6062, rbuilder.MustGetPort("redacted").Port)
	require.Equal(t, 6061, rbuilder.MustGetPort("full-metrics").Port)
	require.ElementsMatch(t, []*DependsOn{
		{Name: "el", Condition: DependsOnConditionHealthy},
		{Name: "beacon", Condition: DependsOnConditionHealthy},
	}, rbuilder.DependsOn)

	flowProxy := manifest.MustGetService("flowproxy-2")
	require.Equal(t, 28546, flowProxy.MustGetPort("http").Port)
	require.Equal(t, 29546, flowProxy.MustGetPort("system").Port)
	require.Equal(t, 29091, flowProxy.MustGetPort("metrics").Port)
	require.Equal(t, "true", flowProxy.Labels[useHostExecutionLabel])
	require.Contains(t, flowProxy.Args, "--disable-forwarding")
	require.Contains(t, flowProxy.Args, "Playground Builder 2")
	require.Contains(t, flowProxy.Args, `{{Service "rbuilder-2" "rpc" "http" ""}}`)
	require.Contains(t, flowProxy.Args, `{{Service "rbuilder-2" "redacted" "http" ""}}`)
	require.ElementsMatch(t, []*DependsOn{
		{Name: "rbuilder-2", Condition: DependsOnConditionRunning},
	}, flowProxy.DependsOn)

	config, err := out.Read("rbuilder-2-config.toml")
	require.NoError(t, err)
	require.Contains(t, config, `enabled_relays = ["mev-boost-relay-1", "mev-boost-relay-2"]`)
	require.Contains(t, config, `url = "http://mev-boost-relay-1:5555"`)
	require.Contains(t, config, `url = "http://mev-boost-relay-2:5555"`)
	require.Contains(t, config, `relay_secret_key = "0x0000000000000000000000000000000000000000000000000000000000000002"`)
	require.Contains(t, config, `extra_data = "Playground Builder 2"`)
}
