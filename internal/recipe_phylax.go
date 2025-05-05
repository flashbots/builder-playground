package internal

import (
	flag "github.com/spf13/pflag"
)

var _ Recipe = &OpTalosRecipe{}

// OpRecipe is a recipe that deploys an OP stack
type OpTalosRecipe struct {
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

	// externalDA is the URL of the external DA to use. If enabled, the recipe deploys
	// a new assertion-da service and connects it to the external DA.
	externalDA string

	// assexGasLimit is the gas limit of the Assertion Execution
	assexGasLimit uint64
}

func (o *OpTalosRecipe) Name() string {
	return "pcl"
}

func (o *OpTalosRecipe) Description() string {
	return "Deploy OP Talos"
}

func (o *OpTalosRecipe) Flags() *flag.FlagSet {
	flags := flag.NewFlagSet("pcl", flag.ContinueOnError)
	flags.StringVar(&o.externalBuilder, "external-builder", "", "External builder URL")
	flags.StringVar(&o.externalDA, "external-da", "", "External DA URL")
	flags.Var(&nullableUint64Value{&o.enableLatestFork}, "enable-latest-fork", "Enable latest fork isthmus (nil or empty = disabled, otherwise enabled at specified block)")
	flags.Uint64Var(&o.blockTime, "block-time", defaultOpBlockTimeSeconds, "Block time to use for the rollup")
	flags.Uint64Var(&o.batcherMaxChannelDuration, "batcher-max-channel-duration", 2, "Maximum channel duration to use for the batcher")
	flags.Uint64Var(&o.assexGasLimit, "assex-gas-limit", 30000000, "Gas limit of the Assertion Execution")
	return flags
}

func (o *OpTalosRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL2Fork(o.enableLatestFork)
	builder.OpBlockTime(o.blockTime)
	return builder
}

func (o *OpTalosRecipe) Apply(ctx *ExContext, artifacts *Artifacts) *Manifest {
	svcManager := NewManifest(ctx, artifacts.Out)
	svcManager.AddService("el", &RethEL{})
	svcManager.AddService("beacon", &LighthouseBeaconNode{
		ExecutionNode: "el",
	})
	svcManager.AddService("validator", &LighthouseValidator{
		BeaconNode: "beacon",
	})

	externalDaRef := o.externalDA
	if o.externalDA == "" || o.externalDA == "dev" {
		svcManager.AddService("assertion-da", &AssertionDA{
			DevMode: o.externalDA == "dev",
			Pk:      "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
		})
		externalDaRef = Connect("assertion-da", "http")
	}

	externalBuilderRef := o.externalBuilder
	if o.externalBuilder == "" {
		// Add a new op-reth service and connect it to Rollup-boost
		svcManager.AddService("op-talos", &OpTalos{
			AssertionDA:   externalDaRef,
			AssexGasLimit: o.assexGasLimit,
		})
		externalBuilderRef = Connect("op-talos", "authrpc")
	}

	elNode := "rollup-boost"

	svcManager.AddService("rollup-boost", &RollupBoost{
		ELNode:  "op-geth",
		Builder: externalBuilderRef,
	})

	svcManager.AddService("op-node", &OpNode{
		L1Node:   "el",
		L1Beacon: "beacon",
		L2Node:   elNode,
	})
	svcManager.AddService("op-geth", &OpGeth{
		UseDeterministicP2PKey: o.externalBuilder != "",
	})
	svcManager.AddService("op-batcher", &OpBatcher{
		L1Node:             "el",
		L2Node:             "op-geth",
		RollupNode:         "op-node",
		MaxChannelDuration: o.batcherMaxChannelDuration,
	})
	return svcManager
}

func (o *OpTalosRecipe) Output(manifest *Manifest) map[string]interface{} {
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
