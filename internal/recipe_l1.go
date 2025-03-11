package internal

import (
	"fmt"
	"time"

	flag "github.com/spf13/pflag"
)

var _ Recipe = &L1Recipe{}

type L1Recipe struct {
	latestFork           bool
	useRethForValidation bool
	secondaryBuilderPort uint64
	useNativeReth        bool
}

func (l *L1Recipe) Name() string {
	return "l1"
}

func (l *L1Recipe) Description() string {
	return "Deploy a full L1 stack with mev-boost"
}

func (l *L1Recipe) Flags() *flag.FlagSet {
	flags := flag.NewFlagSet("l1", flag.ContinueOnError)
	flags.BoolVar(&l.latestFork, "latest-fork", false, "use the latest fork")
	flags.BoolVar(&l.useRethForValidation, "use-reth-for-validation", false, "use reth for validation")
	flags.Uint64Var(&l.secondaryBuilderPort, "secondary-builder", 1234, "port to use for the secondary builder")
	flags.BoolVar(&l.useNativeReth, "use-native-reth", false, "use the native reth binary")
	return flags
}

func (l *L1Recipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL1Fork(l.latestFork)

	return builder
}

func (l *L1Recipe) Apply(artifacts *Artifacts) *Manifest {
	svcManager := NewManifest(artifacts.Out)

	svcManager.AddService("el", &RethEL{
		UseRethForValidation: l.useRethForValidation,
		UseNativeReth:        l.useNativeReth,
	})

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

func (l *L1Recipe) Watchdog(manifest *Manifest, out *output) error {
	beaconNode := manifest.MustGetService("beacon")
	beaconNodeEL := manifest.MustGetService("el")

	watchDogOut, err := out.LogOutput("watchdog")
	if err != nil {
		return err
	}

	beaconNodeURL := fmt.Sprintf("http://localhost:%d", beaconNode.MustGetPort("http").HostPort)
	if err := waitForChainAlive(watchDogOut, beaconNodeURL, 30*time.Second); err != nil {
		return err
	}
	beaconNodeELURL := fmt.Sprintf("http://localhost:%d", beaconNodeEL.MustGetPort("http").HostPort)

	watchGroup := newWatchGroup()
	watchGroup.watch(func() error {
		return watchProposerPayloads(beaconNodeURL)
	})
	watchGroup.watch(func() error {
		return validateProposerPayloads(watchDogOut, beaconNodeURL)
	})
	watchGroup.watch(func() error {
		return watchChainHead(watchDogOut, beaconNodeELURL, 12*time.Second)
	})

	if err := watchGroup.wait(); err != nil {
		return err
	}
	return nil
}
