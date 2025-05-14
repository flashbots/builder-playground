package playground

import (
	"fmt"

	flag "github.com/spf13/pflag"
)

var _ Recipe = &L1Recipe{}

type L1Recipe struct {
	// latestFork enables the use of the latest fork at startup
	latestFork bool

	// useRethForValidation signals mev-boost to use the Reth EL node for block validation
	useRethForValidation bool

	// secondaryELPort enables the use of a secondary EL connected to the validator beacon node
	// It is enabled through the use of the cl-proxy service
	secondaryELPort uint64

	// if useNativeReth is set to true, the Reth EL execution client for the validator beacon node
	// will run on the host machine. This is useful if you want to bind to the Reth database and you
	// are running a host machine (i.e Mac) that is differerent from the docker one (Linux)
	useNativeReth bool
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
	flags.Uint64Var(&l.secondaryELPort, "secondary-el", 0, "port to use for the secondary builder")
	flags.BoolVar(&l.useNativeReth, "use-native-reth", false, "use the native reth binary")
	return flags
}

func (l *L1Recipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL1Fork(l.latestFork)

	return builder
}

func (l *L1Recipe) Apply(ctx *ExContext, artifacts *Artifacts) (*Manifest, error) {
	svcManager := NewManifest(ctx, artifacts.Out)

	svcManager.AddService("el", &RethEL{
		UseRethForValidation: l.useRethForValidation,
		UseNativeReth:        l.useNativeReth,
	})

	var elService string
	if l.secondaryELPort != 0 {
		// we are going to use the cl-proxy service to connect the beacon node to two builders
		// one the 'el' builder and another one the remote one
		elService = "cl-proxy"
		svcManager.AddService("cl-proxy", &ClProxy{
			PrimaryBuilder:   "el",
			SecondaryBuilder: fmt.Sprintf("http://localhost:%d", l.secondaryELPort),
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
	return svcManager, nil
}

func (l *L1Recipe) Output(manifest *Manifest) map[string]interface{} {
	return map[string]interface{}{}
}
