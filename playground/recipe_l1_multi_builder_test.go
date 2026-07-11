package playground

import (
	"fmt"
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

	el := manifest.MustGetService("el")
	require.Contains(t, strings.Join(el.Args, " "), "admin,eth,web3,net,txpool,rpc,mev,flashbots")

	relay1Args := strings.Join(manifest.MustGetService("mev-boost-relay-1").Args, " ")
	relay2Args := strings.Join(manifest.MustGetService("mev-boost-relay-2").Args, " ")
	require.Contains(t, relay1Args, "--api-secret-key")
	require.Contains(t, relay2Args, "--api-secret-key")
	require.NotEqual(t, relay1Args, relay2Args, "relays must run with distinct api secret keys")

	mevBoost := manifest.MustGetService("mev-boost")
	mevBoostArgs := strings.Join(mevBoost.Args, " ")
	require.Contains(t, mevBoostArgs, `{{Service "mev-boost-relay-1" "http" "http" "0x`)
	require.Contains(t, mevBoostArgs, `{{Service "mev-boost-relay-2" "http" "http" "0x`)

	for _, builder := range []struct {
		index int
		name  string
	}{
		{1, "rbuilder-1"},
		{2, "rbuilder-2"},
		{3, "rbuilder-3"},
	} {
		svc := manifest.MustGetService(builder.name)
		require.Equal(t, "service:el", svc.Pid)
		require.Equal(t, rbuilderJSONRPCPort, svc.MustGetPort("rpc").Port,
			"all rbuilders use the same in-container rpc port — host port allocator handles host-side collisions")
		require.ElementsMatch(t, []*DependsOn{
			{Name: "el", Condition: DependsOnConditionHealthy},
			{Name: "beacon", Condition: DependsOnConditionHealthy},
		}, svc.DependsOn)

		config, err := out.Read(builder.name + "-config.toml")
		require.NoError(t, err)
		require.Contains(t, config, `enabled_relays = ["mev-boost-relay-1", "mev-boost-relay-2"]`)
		require.Contains(t, config, fmt.Sprintf(`url = "http://mev-boost-relay-1:%d"`, mevBoostRelayHTTPPort))
		require.Contains(t, config, fmt.Sprintf(`url = "http://mev-boost-relay-2:%d"`, mevBoostRelayHTTPPort))
		require.Contains(t, config, fmt.Sprintf(`extra_data = "Playground Builder %d"`, builder.index))
		require.Contains(t, config, fmt.Sprintf(`relay_secret_key = "%s"`, indexedBLSSecret("playground-rbuilder", builder.index)))
	}
}

func TestL1MultiBuilderRecipeValidateRejectsZeroAndNegative(t *testing.T) {
	cases := []struct {
		name   string
		recipe *L1MultiBuilderRecipe
	}{
		{"zero builders", &L1MultiBuilderRecipe{blockTime: 12 * time.Second, builderCount: 0, relayCount: 1}},
		{"negative builders", &L1MultiBuilderRecipe{blockTime: 12 * time.Second, builderCount: -1, relayCount: 1}},
		{"zero relays", &L1MultiBuilderRecipe{blockTime: 12 * time.Second, builderCount: 1, relayCount: 0}},
		{"zero block time", &L1MultiBuilderRecipe{blockTime: 0, builderCount: 1, relayCount: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.recipe.Validate())
		})
	}
}

func TestL1MultiBuilderRecipeValidateAcceptsDefaults(t *testing.T) {
	recipe := &L1MultiBuilderRecipe{
		blockTime:    12 * time.Second,
		builderCount: defaultL1MultiBuilderCount,
		relayCount:   defaultL1MultiRelayCount,
	}
	require.NoError(t, recipe.Validate())
}

// TestRecipeToYAMLRunsValidate guards the `playground generate` path: a recipe
// configured with an explicitly invalid flag value must fail there too, not
// just on `playground cook`.
func TestRecipeToYAMLRunsValidate(t *testing.T) {
	recipe := &L1MultiBuilderRecipe{
		blockTime:    12 * time.Second,
		builderCount: -1,
		relayCount:   defaultL1MultiRelayCount,
	}
	_, err := RecipeToYAML(recipe)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--builders must be >= 1")
}
