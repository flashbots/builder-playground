package internal

import (
	"fmt"

	flag "github.com/spf13/pflag"
)

var _ Recipe = &L1Recipe{}

type L1Recipe struct {
	latestFork           bool
	useRethForValidation bool
	secondaryBuilderPort uint64
}

func (l *L1Recipe) Name() string {
	return "l1"
}

func (l *L1Recipe) Flags() *flag.FlagSet {
	flags := flag.NewFlagSet("l1", flag.ContinueOnError)
	flags.BoolVar(&l.latestFork, "latest-fork", false, "use the latest fork")
	flags.BoolVar(&l.useRethForValidation, "use-reth-for-validation", false, "use reth for validation")
	flags.Uint64Var(&l.secondaryBuilderPort, "secondary-builder", 1234, "port to use for the secondary builder")
	return flags
}

func (l *L1Recipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL1Fork(l.latestFork)

	return builder
}

func (l *L1Recipe) Apply(artifacts *Artifacts) *Manifest {
	svcManager := NewManifest(artifacts.Out)

	svcManager.AddService("el", &RethEL{})

	var elService string
	if l.secondaryBuilderPort != 0 {
		// we are going to use the cl-proxy service to connect the beacon node to two builders
		// one the 'el' builder and another one the remote one
		elService = "cl-proxy"
		svcManager.AddService("cl-proxy", &ClProxy{
			PrimaryBuilder:   "el",
			SecondaryBuilder: fmt.Sprintf("http://localhost:%d", l.secondaryBuilderPort),
		})
	} else {
		elService = "el"
	}

	svcManager.AddService("beacon", &LighthouseBeaconNode{
		ExecutionNode: elService,
		MevBoostNode:  "mev-boost",
	})
	svcManager.AddService("validator", &LighthouseValidator{
		BeaconNode: "beacon",
	})

	mevBoostValidationServer := ""
	if l.useRethForValidation {
		mevBoostValidationServer = "el"
	}
	svcManager.AddService("mev-boost", &MevBoostRelay{
		BeaconClient:     "beacon",
		ValidationServer: mevBoostValidationServer,
	})
	return svcManager
}

func (l *L1Recipe) Watchdog(manifest *Manifest) error {
	beaconNode, ok := manifest.GetService("beacon")
	if !ok {
		return fmt.Errorf("beacon node not found")
	}

	port, ok := beaconNode.GetPort("http")
	if !ok {
		return fmt.Errorf("beacon node does not expose port http")
	}
	beaconNodeURL := fmt.Sprintf("http://localhost:%d", port.hostPort)

	go func() {
		watchProposerPayloads(beaconNodeURL)
	}()

	if err := validateProposerPayloads(beaconNodeURL); err != nil {
		return err
	}
	return nil
}
