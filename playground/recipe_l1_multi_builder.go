package playground

import (
	"fmt"
	"time"

	flag "github.com/spf13/pflag"
)

var _ Recipe = &L1MultiBuilderRecipe{}

const (
	defaultL1MultiBuilderCount = 2
	defaultL1MultiRelayCount   = 2
)

type L1MultiBuilderRecipe struct {
	latestFork           bool
	blockTime            time.Duration
	useRethForValidation bool
	builderCount         int
	relayCount           int
}

func (l *L1MultiBuilderRecipe) Name() string {
	return "l1-multi-builder"
}

func (l *L1MultiBuilderRecipe) Description() string {
	return "Deploy a full L1 stack with multiple builders and relays"
}

func (l *L1MultiBuilderRecipe) Flags() *flag.FlagSet {
	flags := flag.NewFlagSet("l1-multi-builder", flag.ContinueOnError)
	flags.BoolVar(&l.latestFork, "latest-fork", false, "use the latest fork")
	flags.DurationVar(&l.blockTime, "block-time", time.Duration(defaultL1BlockTimeSeconds)*time.Second, "Block time to use for the L1")
	flags.BoolVar(&l.useRethForValidation, "use-reth-for-validation", false, "use reth for validation")
	flags.IntVar(&l.builderCount, "builders", defaultL1MultiBuilderCount, "number of rbuilder instances to run")
	flags.IntVar(&l.relayCount, "relays", defaultL1MultiRelayCount, "number of mev-boost-relay instances to run")
	return flags
}

func (l *L1MultiBuilderRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL1Fork(l.latestFork)
	builder.L1BlockTime(max(1, uint64(l.normalizedBlockTime().Seconds())))

	return builder
}

func (l *L1MultiBuilderRecipe) normalizedBlockTime() time.Duration {
	if l.blockTime > 0 {
		return l.blockTime
	}
	return time.Duration(defaultL1BlockTimeSeconds) * time.Second
}

func (l *L1MultiBuilderRecipe) normalizedBuilderCount() int {
	if l.builderCount > 0 {
		return l.builderCount
	}
	return defaultL1MultiBuilderCount
}

func (l *L1MultiBuilderRecipe) normalizedRelayCount() int {
	if l.relayCount > 0 {
		return l.relayCount
	}
	return defaultL1MultiRelayCount
}

func (l *L1MultiBuilderRecipe) Apply(ctx *ExContext) *Component {
	component := NewComponent("l1-multi-builder-recipe")

	component.AddComponent(ctx, &RethEL{
		UseRethForValidation: l.useRethForValidation,
	})

	relayServices := make([]string, 0, l.normalizedRelayCount())
	for i := 1; i <= l.normalizedRelayCount(); i++ {
		relayServices = append(relayServices, fmt.Sprintf("mev-boost-relay-%d", i))
	}

	component.AddComponent(ctx, &LighthouseBeaconNode{
		ExecutionNode: "el",
		MevBoostNode:  "mev-boost",
	})
	component.AddComponent(ctx, &LighthouseValidator{
		BeaconNode: "beacon",
	})

	mevBoostValidationServer := ""
	if l.useRethForValidation {
		mevBoostValidationServer = "el"
	}
	for _, relayService := range relayServices {
		component.AddComponent(ctx, &MevBoostRelay{
			ServiceName:      relayService,
			BeaconClient:     "beacon",
			ValidationServer: mevBoostValidationServer,
		})
	}

	component.AddComponent(ctx, &MevBoost{
		RelayEndpoints: relayServices,
	})

	for i := 1; i <= l.normalizedBuilderCount(); i++ {
		component.AddComponent(ctx, &Rbuilder{
			ServiceName:     fmt.Sprintf("rbuilder-%d", i),
			RelayEndpoints:  relayServices,
			RelaySecretKey:  fmt.Sprintf("0x%064x", i),
			ExtraData:       fmt.Sprintf("Playground Builder %d", i),
			JSONRPCPort:     8645 + i - 1,
			RedactedPort:    6061 + i - 1,
			FullMetricsPort: 6060 + i - 1,
		})
	}

	component.RunContenderIfEnabled(ctx)
	return component
}

func (l *L1MultiBuilderRecipe) Output(manifest *Manifest) map[string]interface{} {
	return map[string]interface{}{}
}
