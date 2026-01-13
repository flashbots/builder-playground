package playground

import (
	"fmt"

	flag "github.com/spf13/pflag"
)

var _ Recipe = &BuilderNetRecipe{}

// BuilderNetRecipe is a recipe that extends the L1 recipe to include builder-hub
type BuilderNetRecipe struct {
	// Embed the L1Recipe to reuse its functionality
	l1Recipe L1Recipe

	builderConfig string
}

func (b *BuilderNetRecipe) Name() string {
	return "buildernet"
}

func (b *BuilderNetRecipe) Description() string {
	return "Deploy a full L1 stack with mev-boost and builder-hub"
}

func (b *BuilderNetRecipe) Flags() *flag.FlagSet {
	// Reuse the L1Recipe flags
	flags := b.l1Recipe.Flags()
	flags.StringVar(&b.builderConfig, "builder-config", "", "Builder config in YAML format")
	return flags
}

func (b *BuilderNetRecipe) Artifacts() *ArtifactsBuilder {
	// Reuse the L1Recipe artifacts builder
	return b.l1Recipe.Artifacts()
}

func (b *BuilderNetRecipe) Apply(svcManager *Manifest) {
	// Start with the L1Recipe manifest
	b.l1Recipe.Apply(svcManager)

	svcManager.AddComponent(&BuilderHub{
		BuilderConfig: b.builderConfig,
	})

	svcManager.RunContenderIfEnabled()
}

func (b *BuilderNetRecipe) Output(manifest *Manifest) map[string]interface{} {
	// Start with the L1Recipe output
	output := b.l1Recipe.Output(manifest)

	// Add builder-hub service info
	builderHubService := manifest.MustGetService("builder-hub-api")
	builderHubProxy := manifest.MustGetService("builder-hub-proxy")

	http := builderHubProxy.MustGetPort("http")
	admin := builderHubService.MustGetPort("admin")
	internal := builderHubService.MustGetPort("internal")

	output["builder-hub-proxy"] = fmt.Sprintf("http://localhost:%d", http.HostPort)
	output["builder-hub-admin"] = fmt.Sprintf("http://localhost:%d", admin.HostPort)
	output["builder-hub-internal"] = fmt.Sprintf("http://localhost:%d", internal.HostPort)

	return output
}
