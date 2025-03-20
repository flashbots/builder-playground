package internal

import (
	"fmt"
	"time"

	flag "github.com/spf13/pflag"
)

var _ Recipe = &OpRecipe{}

// OpRecipe is a recipe that deploys an OP stack
type OpRecipe struct {
	// externalBuilder is the URL of the external builder to use. If enabled, the recipe deploys
	// rollup-boost on the sequencer and uses this URL as the external builder.
	externalBuilder string
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
	return flags
}

func (o *OpRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
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
	svcManager.AddService("op-geth", &OpGeth{})
	svcManager.AddService("op-batcher", &OpBatcher{
		L1Node:     "el",
		L2Node:     "op-geth",
		RollupNode: "op-node",
	})
	return svcManager
}

func (o *OpRecipe) Watchdog(manifest *Manifest, out *output) error {
	beaconNode := manifest.MustGetService("beacon")
	beaconNodeEL := manifest.MustGetService("el")
	opNodeEL := manifest.MustGetService("op-geth")

	watchDogOut, err := out.LogOutput("watchdog")
	if err != nil {
		return err
	}

	beaconNodeURL := fmt.Sprintf("http://localhost:%d", beaconNode.MustGetPort("http").HostPort)
	if err := waitForChainAlive(watchDogOut, beaconNodeURL, 50*time.Second); err != nil {
		return err
	}

	watchGroup := newWatchGroup()

	beaconNodeELURL := fmt.Sprintf("http://localhost:%d", beaconNodeEL.MustGetPort("http").HostPort)
	watchGroup.watch(func() error {
		return watchChainHead(watchDogOut, beaconNodeELURL, 12*time.Second)
	})

	opNodeELURL := fmt.Sprintf("http://localhost:%d", opNodeEL.MustGetPort("http").HostPort)
	watchGroup.watch(func() error {
		return watchChainHead(watchDogOut, opNodeELURL, 2*time.Second)
	})

	if err := watchGroup.wait(); err != nil {
		return err
	}
	return nil
}
