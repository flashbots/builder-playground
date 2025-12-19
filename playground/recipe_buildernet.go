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
	return flags
}

func (b *BuilderNetRecipe) Artifacts() *ArtifactsBuilder {
	// Reuse the L1Recipe artifacts builder
	return b.l1Recipe.Artifacts()
}

func (b *BuilderNetRecipe) Apply(svcManager *Manifest) {
	// Start with the L1Recipe manifest
	b.l1Recipe.Apply(svcManager)

	svcManager.AddService(&BuilderHub{})

	svcManager.RunContenderIfEnabled()
}

func (b *BuilderNetRecipe) Output(manifest *Manifest) map[string]interface{} {
	// Start with the L1Recipe output
	output := b.l1Recipe.Output(manifest)

	// Add builder-hub service info
	builderHubService := manifest.MustGetService("builder-hub")
	http := builderHubService.MustGetPort("http")
	admin := builderHubService.MustGetPort("admin")
	internal := builderHubService.MustGetPort("internal")

	output["builder-hub-http"] = fmt.Sprintf("http://localhost:%d", http.HostPort)
	output["builder-hub-admin"] = fmt.Sprintf("http://localhost:%d", admin.HostPort)
	output["builder-hub-internal"] = fmt.Sprintf("http://localhost:%d", internal.HostPort)

	proxyService := manifest.MustGetService("builder-hub-proxy")
	proxyHTTP := proxyService.MustGetPort("http")
	output["builder-hub-proxy"] = fmt.Sprintf("http://localhost:%d", proxyHTTP.HostPort)

	return output
}
