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

	mevBoost := manifest.MustGetService("mev-boost")
	mevBoostArgs := strings.Join(mevBoost.Args, " ")
	require.Contains(t, mevBoostArgs, `{{Service "mev-boost-relay-1" "http" "http" "0x`)
	require.Contains(t, mevBoostArgs, `{{Service "mev-boost-relay-2" "http" "http" "0x`)

	rbuilder := manifest.MustGetService("rbuilder-2")
	require.Equal(t, "service:el", rbuilder.Pid)
	require.ElementsMatch(t, []*DependsOn{
		{Name: "el", Condition: DependsOnConditionHealthy},
		{Name: "beacon", Condition: DependsOnConditionHealthy},
	}, rbuilder.DependsOn)

	config, err := out.Read("rbuilder-2-config.toml")
	require.NoError(t, err)
	require.Contains(t, config, `enabled_relays = ["mev-boost-relay-1", "mev-boost-relay-2"]`)
	require.Contains(t, config, `url = "http://mev-boost-relay-1:5555"`)
	require.Contains(t, config, `url = "http://mev-boost-relay-2:5555"`)
	require.Contains(t, config, `relay_secret_key = "0x0000000000000000000000000000000000000000000000000000000000000002"`)
	require.Contains(t, config, `extra_data = "Playground Builder 2"`)
}
