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

	// Add mock proxy for testing
	includeMockProxy bool
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

	// Add a flag to enable/disable the mock proxy
	flags.BoolVar(&b.includeMockProxy, "mock-proxy", false, "include a mock proxy for builder-hub with attestation headers")

	return flags
}

func (b *BuilderNetRecipe) Artifacts() *ArtifactsBuilder {
	// Reuse the L1Recipe artifacts builder
	return b.l1Recipe.Artifacts()
}

func (b *BuilderNetRecipe) Apply(ctx *ExContext, artifacts *Artifacts) (*Manifest, error) {
	// Start with the L1Recipe manifest
	svcManager, err := b.l1Recipe.Apply(ctx, artifacts)
	if err != nil {
		return nil, fmt.Errorf("failed to apply L1 recipe for BuilderNet: %w", err)
	}

	// Add builder-hub-postgres service (now includes migrations)
	svcManager.AddService("builder-hub-postgres", &BuilderHubPostgres{})

	// Add the builder-hub service
	svcManager.AddService("builder-hub", &BuilderHub{
		postgres: "builder-hub-postgres",
	})

	// Optionally add mock proxy for testing
	if b.includeMockProxy {
		svcManager.AddService("builder-hub-proxy", &BuilderHubMockProxy{
			TargetService: "builder-hub",
		})
	}

	return svcManager, nil
}

func (b *BuilderNetRecipe) Output(manifest *Manifest) map[string]interface{} {
	// Start with the L1Recipe output
	output := b.l1Recipe.Output(manifest)

	// Add builder-hub service info
	builderHubService, ok := manifest.GetService("builder-hub")
	if ok {
		http := builderHubService.MustGetPort("http")
		admin := builderHubService.MustGetPort("admin")
		internal := builderHubService.MustGetPort("internal")

		output["builder-hub-http"] = fmt.Sprintf("http://localhost:%d", http.HostPort)
		output["builder-hub-admin"] = fmt.Sprintf("http://localhost:%d", admin.HostPort)
		output["builder-hub-internal"] = fmt.Sprintf("http://localhost:%d", internal.HostPort)
	}

	if b.includeMockProxy {
		proxyService, ok := manifest.GetService("builder-hub-proxy")
		if ok {
			http := proxyService.MustGetPort("http")
			output["builder-hub-proxy"] = fmt.Sprintf("http://localhost:%d", http.HostPort)
		}
	}

	return output
}
