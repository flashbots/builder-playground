package internal

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
	return flags
}

func (o *OpRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL2Fork(o.enableLatestFork)
	return builder
}

func (o *OpRecipe) Apply(ctx *ExContext, artifacts *Artifacts) *Manifest {
	svcManager := NewManifest(ctx, artifacts.Out)
	svcManager.AddService("el", &RethEL{})
	svcManager.AddService("beacon", &LighthouseBeaconNode{
		ExecutionNode: "el",
	})
	svcManager.AddService("validator", &LighthouseValidator{
		BeaconNode: "beacon",
	})

	elNode := "op-geth"
	if o.externalBuilder != "" {
		elNode = "rollup-boost"

		svcManager.AddService("rollup-boost", &RollupBoost{
			ELNode:  "op-geth",
			Builder: o.externalBuilder,
		})
	}
	svcManager.AddService("op-node", &OpNode{
		L1Node:   "el",
		L1Beacon: "beacon",
		L2Node:   elNode,
	})
	svcManager.AddService("op-geth", &OpGeth{
		UseDeterministicP2PKey: o.externalBuilder != "",
	})
	svcManager.AddService("op-batcher", &OpBatcher{
		L1Node:     "el",
		L2Node:     "op-geth",
		RollupNode: "op-node",
	})
	return svcManager
}

func (o *OpRecipe) Output(manifest *Manifest) map[string]interface{} {
	opGeth := manifest.MustGetService("op-geth").component.(*OpGeth)
	if opGeth.Enode != "" {
		// Only output if enode was set
		return map[string]interface{}{
			"op-geth-enode": opGeth.Enode,
		}
	}
	return map[string]interface{}{}
}
