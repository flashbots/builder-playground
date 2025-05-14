package playground

import (
	flag "github.com/spf13/pflag"
)

var _ Recipe = &OpRecipe{}

// OpRecipe is a recipe that deploys an OP stack
type OpRecipe struct {
	// externalBuilder is the URL of the external builder to use. If enabled, the recipe deploys
	// rollup-boost on the sequencer and uses this URL as the external builder.
	externalBuilder string

	// whether to enable the latest fork isthmus and when
	enableLatestFork *uint64

	// blockTime is the block time to use for the rollup
	// (default is 2 seconds)
	blockTime uint64

	// batcherMaxChannelDuration is the maximum channel duration to use for the batcher
	// (default is 2 seconds)
	batcherMaxChannelDuration uint64
}

func (o *OpRecipe) Name() string {
	return "opstack"
}

func (o *OpRecipe) Description() string {
	return "Deploy an OP stack"
}

func (o *OpRecipe) Flags() *flag.FlagSet {
	flags := flag.NewFlagSet("opstack", flag.ContinueOnError)
	flags.StringVar(&o.externalBuilder, "external-builder", "", "External builder URL")
	flags.Var(&nullableUint64Value{&o.enableLatestFork}, "enable-latest-fork", "Enable latest fork isthmus (nil or empty = disabled, otherwise enabled at specified block)")
	flags.Uint64Var(&o.blockTime, "block-time", defaultOpBlockTimeSeconds, "Block time to use for the rollup")
	flags.Uint64Var(&o.batcherMaxChannelDuration, "batcher-max-channel-duration", 2, "Maximum channel duration to use for the batcher")
	return flags
}

func (o *OpRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL2Fork(o.enableLatestFork)
	builder.OpBlockTime(o.blockTime)
	return builder
}

func (o *OpRecipe) Apply(ctx *ExContext, artifacts *Artifacts) (*Manifest, error) {
	svcManager := NewManifest(ctx, artifacts.Out)
	svcManager.AddService("el", &RethEL{})
	svcManager.AddService("beacon", &LighthouseBeaconNode{
		ExecutionNode: "el",
	})
	svcManager.AddService("validator", &LighthouseValidator{
		BeaconNode: "beacon",
	})

	externalBuilderRef := o.externalBuilder
	if o.externalBuilder == "op-reth" {
		// Add a new op-reth service and connect it to Rollup-boost
		svcManager.AddService("op-reth", &OpReth{})

		externalBuilderRef = Connect("op-reth", "authrpc")
	}

	elNode := "op-geth"
	if o.externalBuilder != "" {
		elNode = "rollup-boost"

		svcManager.AddService("rollup-boost", &RollupBoost{
			ELNode:  "op-geth",
			Builder: externalBuilderRef,
		})
	}
	svcManager.AddService("op-node", &OpNode{
		L1Node:   "el",
		L1Beacon: "beacon",
		L2Node:   elNode,
	})
	svcManager.AddService("op-geth", &OpGeth{})
	svcManager.AddService("op-batcher", &OpBatcher{
		L1Node:             "el",
		L2Node:             "op-geth",
		RollupNode:         "op-node",
		MaxChannelDuration: o.batcherMaxChannelDuration,
	})
	return svcManager, nil
}

func (o *OpRecipe) Output(manifest *Manifest) map[string]interface{} {
	/*
		opGeth := manifest.MustGetService("op-geth").component.(*OpGeth)
		if opGeth.Enode != "" {
			// Only output if enode was set
			return map[string]interface{}{
				"op-geth-enode": opGeth.Enode,
			}
		}
	*/
	return map[string]interface{}{}
}
