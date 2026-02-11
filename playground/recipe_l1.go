package playground

import (
	"fmt"
	"regexp"
	"time"

	flag "github.com/spf13/pflag"
)

var _ Recipe = &L1Recipe{}

type L1Recipe struct {
	// latestFork enables the use of the latest fork at startup
	latestFork bool

	// blockTime is the block time to use for the L1 nodes
	// (default is 12 seconds)
	blockTime time.Duration

	// useRethForValidation signals mev-boost to use the Reth EL node for block validation
	useRethForValidation bool

	// secondaryEL enables the use of a secondary EL connected to the validator beacon node
	// It is enabled through the use of the cl-proxy service. If the input is a plain number, it is assumed
	// to be a port number and the secondary EL is assumed to be running on localhost at that port.
	// Otherwise, it is assumed to be a full address (e.g http://some-el:8551) where to reach the secondary EL,
	// use http://host.docker.internal:<port> to reach the host machine from within docker.
	secondaryEL string

	// if useNativeReth is set to true, the Reth EL execution client for the validator beacon node
	// will run on the host machine. This is useful if you want to bind to the Reth database and you
	// are running a host machine (i.e Mac) that is differerent from the docker one (Linux)
	useNativeReth bool

	useSeparateMevBoost bool

	withRbuilder bool
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
	flags.DurationVar(&l.blockTime, "block-time", time.Duration(defaultL1BlockTimeSeconds)*time.Second, "Block time to use for the L1")
	flags.BoolVar(&l.useRethForValidation, "use-reth-for-validation", false, "use reth for validation")
	flags.StringVar(&l.secondaryEL, "secondary-el", "", "Address or port to use for the secondary EL (execution layer); Can be a port number (e.g., '8551') in which case the full URL is derived as `http://localhost:<port>` or a complete URL (e.g., `http://docker-container-name:8551`), use `http://host.docker.internal:<port>` to reach a secondary execution client that runs on your host and not within Docker.")
	flags.BoolVar(&l.useNativeReth, "use-native-reth", false, "use the native reth binary")
	flags.BoolVar(&l.useSeparateMevBoost, "use-separate-mev-boost", false, "use separate mev-boost and mev-boost-relay services")
	flags.BoolVar(&l.withRbuilder, "rbuilder", false, "include rbuilder in the recipe")
	return flags
}

func (l *L1Recipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL1Fork(l.latestFork)
	builder.L1BlockTime(max(1, uint64(l.blockTime.Seconds())))

	return builder
}

var looksLikePortRegex = regexp.MustCompile(`^\d{2,5}$`)

func (l *L1Recipe) Apply(ctx *ExContext) *Component {
	component := NewComponent("l1-recipe")

	component.AddComponent(ctx, &RethEL{
		UseRethForValidation: l.useRethForValidation,
		UseNativeReth:        l.useNativeReth,
	})

	var elService string
	if l.secondaryEL != "" {
		address := l.secondaryEL
		if looksLikePortRegex.MatchString(l.secondaryEL) {
			address = fmt.Sprintf("http://localhost:%s", l.secondaryEL)
		}

		// we are going to use the cl-proxy service to connect the beacon node to two builders
		// one the 'el' builder and another one the remote one
		elService = "cl-proxy"
		component.AddComponent(ctx, &ClProxy{
			PrimaryBuilder:   "el",
			SecondaryBuilder: address,
		})
	} else {
		elService = "el"
	}

	var mevBoostNode string
	if l.useSeparateMevBoost {
		// use local mev-boost which connects to mev-boost-relay
		mevBoostNode = "mev-boost"
	} else {
		// connect directly to mev-boost-relay
		mevBoostNode = "mev-boost-relay"
	}

	component.AddComponent(ctx, &LighthouseBeaconNode{
		ExecutionNode: elService,
		MevBoostNode:  mevBoostNode,
	})
	component.AddComponent(ctx, &LighthouseValidator{
		BeaconNode: "beacon",
	})

	if l.useSeparateMevBoost {
		mevBoostValidationServer := ""
		if l.useRethForValidation {
			mevBoostValidationServer = "el"
		}

		component.AddComponent(ctx, &MevBoostRelay{
			BeaconClient:     "beacon",
			ValidationServer: mevBoostValidationServer,
		})

		component.AddComponent(ctx, &MevBoost{
			RelayEndpoints: []string{"mev-boost-relay"},
		})
	} else {
		// single-service setup
		mevBoostValidationServer := ""
		if l.useRethForValidation {
			mevBoostValidationServer = "el"
		}
		component.AddComponent(ctx, &MevBoostRelay{
			BeaconClient:     "beacon",
			ValidationServer: mevBoostValidationServer,
		})
	}

	component.AddComponent(ctx, &Fileserver{})

	if l.withRbuilder {
		component.AddComponent(ctx, &Rbuilder{})
	}

	component.RunContenderIfEnabled(ctx)
	return component
}

func (l *L1Recipe) Output(manifest *Manifest) map[string]interface{} {
	return map[string]interface{}{}
}
