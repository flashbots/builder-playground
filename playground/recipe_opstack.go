package playground

import (
	flag "github.com/spf13/pflag"
)

var _ Recipe = &OpRecipe{}

// OpRecipe is a recipe that deploys an OP stack
type OpRecipe struct {
	// externalBuilder is the URL of the external builder to use. If enabled, the recipe deploys
	// rollup-boost on the sequencer and uses this URL as the external builder.
	externalBuilder string

	// authrpcPeers is a list of additional peers to connect to the bproxy service.
	// Each peer can be specified multiple times using the --authrpc-peer flag.
	authrpcPeers []string

	// whether to enable the latest fork isthmus and when
	enableLatestFork *uint64

	// blockTime is the block time to use for the rollup
	// (default is 2 seconds)
	blockTime uint64

	// batcherMaxChannelDuration is the maximum channel duration to use for the batcher
	// (default is 2 seconds)
	batcherMaxChannelDuration uint64

	// whether to enable flashblocks in Rollup-boost. Note the internal builder **will not** be
	// using flashblocks. This is meant to be used with an external builder for now.
	flashblocks bool

	// flashblocksBuilderURL is the URL of the builder that returns the flashblocks. This is meant to be used
	// for external builders.
	flashblocksBuilderURL string

	// Indicates that flashblocks-rpc should use base image
	baseOverlay bool

	// whether to enable websocket proxy
	enableWebsocketProxy bool
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
	flags.StringSliceVar(&o.authrpcPeers, "authrpc-peers", []string{}, "Peers for bproxy (can be specified multiple times)")
	flags.Var(&nullableUint64Value{&o.enableLatestFork}, "enable-latest-fork", "Enable latest fork isthmus (nil or empty = disabled, otherwise enabled at specified block)")
	flags.Uint64Var(&o.blockTime, "block-time", defaultOpBlockTimeSeconds, "Block time to use for the rollup")
	flags.Uint64Var(&o.batcherMaxChannelDuration, "batcher-max-channel-duration", 2, "Maximum channel duration to use for the batcher")
	flags.BoolVar(&o.flashblocks, "flashblocks", false, "Whether to enable flashblocks")
	flags.BoolVar(&o.baseOverlay, "base-overlay", false, "Whether to use base implementation for flashblocks-rpc")
	flags.StringVar(&o.flashblocksBuilderURL, "flashblocks-builder", "", "External URL of builder flashblocks stream")
	flags.BoolVar(&o.enableWebsocketProxy, "enable-websocket-proxy", false, "Whether to enable websocket proxy")
	return flags
}

func (o *OpRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL2Fork(o.enableLatestFork)
	builder.OpBlockTime(o.blockTime)
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

	flashblocksBuilderURLRef := o.flashblocksBuilderURL
	externalBuilderRef := o.externalBuilder
	peers := []string{}

	opGeth := &OpGeth{}
	svcManager.AddService("op-geth", opGeth)

	ctx.Bootnode = &BootnodeRef{
		Service: "op-geth",
		ID:      opGeth.Enode.NodeID(),
	}

	if o.externalBuilder == "op-reth" {
		// Add a new op-reth service and connect it to Rollup-boost
		svcManager.AddService("op-reth", &OpReth{})

		externalBuilderRef = Connect("op-reth", "authrpc")
	} else if o.externalBuilder == "op-rbuilder" {
		svcManager.AddService("op-rbuilder", &OpRbuilder{
			Flashblocks: o.flashblocks,
		})
		externalBuilderRef = Connect("op-rbuilder", "authrpc")
	}

	if o.flashblocks && o.externalBuilder == "op-rbuilder" {
		// If flashblocks is enabled and using op-rbuilder, use it to deliver flashblocks
		flashblocksBuilderURLRef = ConnectWs("op-rbuilder", "flashblocks")
	}

	if o.flashblocks {
		peers = append(peers, "flashblocks-rpc")
	}

	peers = append(peers, o.authrpcPeers...)

	// Only enable bproxy if flashblocks is enabled (since flashblocks-rpc is the only service that needs it)
	if o.flashblocks {
		svcManager.AddService("bproxy", &BProxy{
			TargetAuthrpc:         externalBuilderRef,
			Peers:                 peers,
			Flashblocks:           o.flashblocks,
			FlashblocksBuilderURL: flashblocksBuilderURLRef,
		})
	}

	// Only enable websocket-proxy if the flag is set
	if o.enableWebsocketProxy {
		svcManager.AddService("websocket-proxy", &WebsocketProxy{
			Upstream: "rollup-boost",
		})
	}

	elNode := "op-geth"
	if o.externalBuilder != "" {
		elNode = "rollup-boost"

		// Use bproxy if flashblocks is enabled, otherwise use external builder directly
		builderRef := externalBuilderRef
		flashblocksBuilderRef := flashblocksBuilderURLRef
		if o.flashblocks {
			builderRef = Connect("bproxy", "authrpc")
			flashblocksBuilderRef = ConnectWs("bproxy", "flashblocks")
		}

		svcManager.AddService("rollup-boost", &RollupBoost{
			ELNode:                "op-geth",
			Builder:               builderRef,
			Flashblocks:           o.flashblocks,
			FlashblocksBuilderURL: flashblocksBuilderRef,
		})
	}

	if o.flashblocks {
		// Determine which service to use for flashblocks websocket connection
		flashblocksWSService := "rollup-boost"
		useWebsocketProxy := false
		if o.enableWebsocketProxy {
			flashblocksWSService = "websocket-proxy"
			useWebsocketProxy = true
		}

		svcManager.AddService("flashblocks-rpc", &FlashblocksRPC{
			FlashblocksWSService: flashblocksWSService,
			BaseOverlay:          o.baseOverlay,
			UseWebsocketProxy:    useWebsocketProxy,
		})
	}

	svcManager.AddService("op-node", &OpNode{
		L1Node:   "el",
		L1Beacon: "beacon",
		L2Node:   elNode,
	})

	svcManager.AddService("op-batcher", &OpBatcher{
		L1Node:             "el",
		L2Node:             "op-geth",
		RollupNode:         "op-node",
		MaxChannelDuration: o.batcherMaxChannelDuration,
	})

	if svcManager.ctx.Contender.TargetChain == "" {
		svcManager.ctx.Contender.TargetChain = "op-geth"
	}
	svcManager.RunContenderIfEnabled()

	return svcManager
}

func (o *OpRecipe) Output(manifest *Manifest) map[string]interface{} {
	/*
		opGeth := manifest.MustGetService("op-geth").component.(*OpGeth)
		if opGeth.Enode != "" {
			// Only output if enode was set
			return map[string]interface{}{
				"op-geth-enode": opGeth.Enode,
			}
		}
	*/
	return map[string]interface{}{}
}
