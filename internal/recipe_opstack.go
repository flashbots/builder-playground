package internal

import (
	flag "github.com/spf13/pflag"
)

var _ Recipe = &OpRecipe{}

type OpRecipe struct {
	externalBuilder string
}

func (o *OpRecipe) Name() string {
	return "opstack"
}

func (o *OpRecipe) Flags() *flag.FlagSet {
	flags := flag.NewFlagSet("opstack", flag.ContinueOnError)
	flags.StringVar(&o.externalBuilder, "external-builder", "", "External builder URL")
	return flags
}

func (o *OpRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	return builder
}

func (o *OpRecipe) Apply(artifacts *Artifacts) *Manifest {
	svcManager := NewManifest(artifacts.Out)
	svcManager.AddService("el", &RethEL{})
	svcManager.AddService("beacon", &LighthouseBeaconNode{
		ExecutionNode: "el",
	})
	svcManager.AddService("validator", &LighthouseValidator{
		BeaconNode: "beacon",
	})

	elNode := "el"
	if o.externalBuilder != "" {
		elNode = "rollup-boost"

		svcManager.AddService("rollup-boost", &RollupBoost{
			ELNode:  elNode,
			Builder: o.externalBuilder,
		})
	}
	svcManager.AddService("op-node", &OpNode{
		L1Node:   elNode,
		L1Beacon: "beacon",
		L2Node:   "op-geth",
	})
	svcManager.AddService("op-geth", &OpGeth{})
	svcManager.AddService("op-batcher", &OpBatcher{
		L1Node:     "el",
		L2Node:     "op-geth",
		RollupNode: "op-node",
	})
	return svcManager
}

func (o *OpRecipe) Watchdog(manifest *Manifest) error {
	return nil
}
