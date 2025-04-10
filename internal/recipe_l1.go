package internal

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

	// bootnodePrivKey is the private key used by the bootnode for P2P discovery
	bootnodePrivKey string
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
	flags.StringVar(&l.bootnodePrivKey, "bootnode-privkey", "", "private key for the bootnode (optional)")
	return flags
}

func (l *L1Recipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL1Fork(l.latestFork)

	return builder
}

func (m *Manifest) AddServiceWithDeps(name string, service Service, deps ...string) {
	m.AddService(name, service)
	for _, dep := range deps {
		m.MustGetService(name).DependsOnHealthy(dep)
	}
}

func (l *L1Recipe) Apply(ctx *ExContext, artifacts *Artifacts) *Manifest {
	svcManager := NewManifest(ctx, artifacts.Out)

	// Register bootnode without dependency
	bootnode := &Bootnode{
		DiscoveryPort: 30301,
		PrivateKey:    l.bootnodePrivKey,
	}
	svcManager.AddService("bootnode", bootnode)

	// Register 'el' service with dependency on bootnode
	el := &RethEL{
		UseRethForValidation: l.useRethForValidation,
		UseNativeReth:        l.useNativeReth,
	}
	svcManager.AddServiceWithDeps("el", el, "bootnode")

	var elService string
	if l.secondaryELPort != 0 {
		// Use cl-proxy service to connect the beacon node to two builders
		elService = "cl-proxy"
		svcManager.AddService("cl-proxy", &ClProxy{
			PrimaryBuilder:   "el",
			SecondaryBuilder: fmt.Sprintf("http://localhost:%d", l.secondaryELPort),
		})
	} else {
		elService = "el"
	}

	// Register beacon with dependency on bootnode
	beacon := &LighthouseBeaconNode{
		ExecutionNode: elService,
		MevBoostNode:  "mev-boost",
	}
	svcManager.AddServiceWithDeps("beacon", beacon, "bootnode")

	// Register validator with dependency on beacon
	validator := &LighthouseValidator{
		BeaconNode: "beacon",
	}
	svcManager.AddServiceWithDeps("validator", validator, "beacon")

	mevBoostValidationServer := ""
	if l.useRethForValidation {
		mevBoostValidationServer = "el"
	}

	// Register mev-boost with dependency on beacon
	mevBoost := &MevBoostRelay{
		BeaconClient:     "beacon",
		ValidationServer: mevBoostValidationServer,
	}
	svcManager.AddServiceWithDeps("mev-boost", mevBoost, "beacon")

	return svcManager
}

func (l *L1Recipe) Output(manifest *Manifest) map[string]interface{} {
	return map[string]interface{}{}
}
