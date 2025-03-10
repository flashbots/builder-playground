package internal

import (
	flag "github.com/spf13/pflag"
)

var _ Recipe = &OpRecipe{}

type OpRecipe struct {
}

func (o *OpRecipe) Name() string {
	return "opstack"
}

func (o *OpRecipe) Flags() *flag.FlagSet {
	flags := flag.NewFlagSet("opstack", flag.ContinueOnError)
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
	svcManager.AddService("op-node", &OpNode{
		L1Node:   "el",
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
