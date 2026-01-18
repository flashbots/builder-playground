package playground

import (
	"fmt"
	"regexp"
	"time"
)

var _ Recipe = &L1Recipe{}

type L1Recipe struct {
	// latestFork enables the use of the latest fork at startup
	LatestFork bool `flag:"latest-fork" description:"use the latest fork" default:"false"`

	// blockTime is the block time to use for the L1 nodes
	// (default is 12 seconds)
	BlockTime time.Duration `flag:"block-time" description:"Block time to use for the L1" default:"12s"`

	// useRethForValidation signals mev-boost to use the Reth EL node for block validation
	UseRethForValidation bool `flag:"use-reth-for-validation" description:"use reth for validation" default:"false"`

	// secondaryEL enables the use of a secondary EL connected to the validator beacon node
	// It is enabled through the use of the cl-proxy service. If the input is a plain number, it is assumed
	// to be a port number and the secondary EL is assumed to be running on localhost at that port.
	// Otherwise, it is assumed to be a full address (e.g http://some-el:8551) where to reach the secondary EL,
	// use http://host.docker.internal:<port> to reach the host machine from within docker.
	SecondaryEL string `flag:"secondary-el" description:"Address or port to use for the secondary EL (execution layer); Can be a port number (e.g., '8551') in which case the full URL is derived as http://localhost:<port> or a complete URL (e.g., http://docker-container-name:8551), use http://host.docker.internal:<port> to reach a secondary execution client that runs on your host and not within Docker."`

	// if useNativeReth is set to true, the Reth EL execution client for the validator beacon node
	// will run on the host machine. This is useful if you want to bind to the Reth database and you
	// are running a host machine (i.e Mac) that is differerent from the docker one (Linux)
	UseNativeReth bool `flag:"use-native-reth" description:"use the native reth binary" default:"false"`

	UseSeparateMevBoost bool `flag:"use-separate-mev-boost" description:"use separate mev-boost and mev-boost-relay services" default:"false"`
}

func (l *L1Recipe) Name() string {
	return "l1"
}

func (l *L1Recipe) Description() string {
	return "Deploy a full L1 stack with mev-boost"
}

func (l *L1Recipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL1Fork(l.LatestFork)
	builder.L1BlockTime(max(1, uint64(l.BlockTime.Seconds())))

	return builder
}

var looksLikePortRegex = regexp.MustCompile(`^\d{2,5}$`)

func (l *L1Recipe) Apply(svcManager *Manifest) {
	svcManager.AddComponent(&RethEL{
		UseRethForValidation: l.UseRethForValidation,
		UseNativeReth:        l.UseNativeReth,
	})

	var elService string
	if l.SecondaryEL != "" {
		address := l.SecondaryEL
		if looksLikePortRegex.MatchString(l.SecondaryEL) {
			address = fmt.Sprintf("http://localhost:%s", l.SecondaryEL)
		}

		// we are going to use the cl-proxy service to connect the beacon node to two builders
		// one the 'el' builder and another one the remote one
		elService = "cl-proxy"
		svcManager.AddComponent(&ClProxy{
			PrimaryBuilder:   "el",
			SecondaryBuilder: address,
		})
	} else {
		elService = "el"
	}

	var mevBoostNode string
	if l.UseSeparateMevBoost {
		// use local mev-boost which connects to mev-boost-relay
		mevBoostNode = "mev-boost"
	} else {
		// connect directly to mev-boost-relay
		mevBoostNode = "mev-boost-relay"
	}

	svcManager.AddComponent(&LighthouseBeaconNode{
		ExecutionNode: elService,
		MevBoostNode:  mevBoostNode,
	})
	svcManager.AddComponent(&LighthouseValidator{
		BeaconNode: "beacon",
	})

	if l.UseSeparateMevBoost {
		mevBoostValidationServer := ""
		if l.UseRethForValidation {
			mevBoostValidationServer = "el"
		}

		svcManager.AddComponent(&MevBoostRelay{
			BeaconClient:     "beacon",
			ValidationServer: mevBoostValidationServer,
		})

		svcManager.AddComponent(&MevBoost{
			RelayEndpoints: []string{"mev-boost-relay"},
		})
	} else {
		// single-service setup
		mevBoostValidationServer := ""
		if l.UseRethForValidation {
			mevBoostValidationServer = "el"
		}
		svcManager.AddComponent(&MevBoostRelay{
			BeaconClient:     "beacon",
			ValidationServer: mevBoostValidationServer,
		})
	}

	svcManager.RunContenderIfEnabled()
}

func (l *L1Recipe) Output(manifest *Manifest) map[string]interface{} {
	return map[string]interface{}{}
}
