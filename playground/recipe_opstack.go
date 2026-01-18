package playground

var _ Recipe = &OpRecipe{}

const defaultL2BuilderAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"

// OpRecipe is a recipe that deploys an OP stack
type OpRecipe struct {
	// externalBuilder is the URL of the external builder to use. If enabled, the recipe deploys
	// rollup-boost on the sequencer and uses this URL as the external builder.
	ExternalBuilder string `flag:"external-builder" description:"External builder URL"`

	// whether to enable the latest fork jovian and when
	EnableLatestFork *uint64 `flag:"enable-latest-fork" description:"Enable Jovian fork: 0 = at genesis, N > 0 = at block N (default: Isthmus only)"`

	// blockTime is the block time to use for the rollup
	// (default is 2 seconds)
	BlockTime uint64 `flag:"block-time" description:"Block time to use for the rollup" default:"2"`

	// batcherMaxChannelDuration is the maximum channel duration to use for the batcher
	// (default is 2 seconds)
	BatcherMaxChannelDuration uint64 `flag:"batcher-max-channel-duration" description:"Maximum channel duration to use for the batcher" default:"2"`

	// whether to enable flashblocks in Rollup-boost. Note the internal builder **will not** be
	// using flashblocks. This is meant to be used with an external builder for now.
	Flashblocks bool `flag:"flashblocks" description:"Whether to enable flashblocks" default:"false"`

	// flashblocksBuilderURL is the URL of the builder that returns the flashblocks. This is meant to be used
	// for external builders.
	FlashblocksBuilderURL string `flag:"flashblocks-builder" description:"External URL of builder flashblocks stream"`

	// Indicates that flashblocks-rpc should use base image
	BaseOverlay bool `flag:"base-overlay" description:"Whether to use base implementation for flashblocks-rpc" default:"false"`

	// whether to enable websocket proxy
	EnableWebsocketProxy bool `flag:"enable-websocket-proxy" description:"Whether to enable websocket proxy" default:"false"`

	// whether to enable chain-monitor
	EnableChainMonitor bool `flag:"chain-monitor" description:"Whether to enable chain-monitor" default:"false"`
}

func (o *OpRecipe) Name() string {
	return "opstack"
}

func (o *OpRecipe) Description() string {
	return "Deploy an OP stack"
}

func (o *OpRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.WithL2()
	builder.ApplyLatestL2Fork(o.EnableLatestFork)
	builder.OpBlockTime(o.BlockTime)
	return builder
}

func (o *OpRecipe) Apply(svcManager *Manifest) {
	svcManager.AddComponent(&RethEL{})
	svcManager.AddComponent(&LighthouseBeaconNode{
		ExecutionNode: "el",
	})
	svcManager.AddComponent(&LighthouseValidator{
		BeaconNode: "beacon",
	})

	flashblocksBuilderURLRef := o.FlashblocksBuilderURL
	externalBuilderRef := o.ExternalBuilder
	peers := []string{}

	opGeth := &OpGeth{}
	svcManager.AddComponent(opGeth)

	svcManager.ctx.Bootnode = &BootnodeRef{
		Service: "op-geth",
		ID:      opGeth.Enode.NodeID(),
	}

	if o.ExternalBuilder == "op-reth" {
		// Add a new op-reth service and connect it to Rollup-boost
		svcManager.AddComponent(&OpReth{})

		externalBuilderRef = Connect("op-reth", "authrpc")
	} else if o.ExternalBuilder == "op-rbuilder" {
		svcManager.AddComponent(&OpRbuilder{
			Flashblocks: o.Flashblocks,
		})
		externalBuilderRef = Connect("op-rbuilder", "authrpc")
	}

	if o.Flashblocks && o.ExternalBuilder == "op-rbuilder" {
		// If flashblocks is enabled and using op-rbuilder, use it to deliver flashblocks
		flashblocksBuilderURLRef = ConnectWs("op-rbuilder", "flashblocks")
	}

	if o.Flashblocks {
		peers = append(peers, "flashblocks-rpc")
	}

	// Only enable bproxy if flashblocks is enabled (since flashblocks-rpc is the only service that needs it)
	if o.Flashblocks {
		svcManager.AddComponent(&BProxy{
			TargetAuthrpc:         externalBuilderRef,
			Peers:                 peers,
			Flashblocks:           o.Flashblocks,
			FlashblocksBuilderURL: flashblocksBuilderURLRef,
		})
	}

	// Only enable websocket-proxy if the flag is set
	if o.EnableWebsocketProxy {
		svcManager.AddComponent(&WebsocketProxy{
			Upstream: "rollup-boost",
		})
	}

	elNode := "op-geth"
	if o.ExternalBuilder != "" {
		elNode = "rollup-boost"

		// Use bproxy if flashblocks is enabled, otherwise use external builder directly
		builderRef := externalBuilderRef
		flashblocksBuilderRef := flashblocksBuilderURLRef
		if o.Flashblocks {
			builderRef = Connect("bproxy", "authrpc")
			flashblocksBuilderRef = ConnectWs("bproxy", "flashblocks")
		}

		svcManager.AddComponent(&RollupBoost{
			ELNode:                "op-geth",
			Builder:               builderRef,
			Flashblocks:           o.Flashblocks,
			FlashblocksBuilderURL: flashblocksBuilderRef,
		})
	}

	if o.Flashblocks {
		// Determine which service to use for flashblocks websocket connection
		flashblocksWSService := "rollup-boost"
		useWebsocketProxy := false
		if o.EnableWebsocketProxy {
			flashblocksWSService = "websocket-proxy"
			useWebsocketProxy = true
		}

		svcManager.AddComponent(&FlashblocksRPC{
			FlashblocksWSService: flashblocksWSService,
			BaseOverlay:          o.BaseOverlay,
			UseWebsocketProxy:    useWebsocketProxy,
		})
	}

	svcManager.AddComponent(&OpNode{
		L1Node:   "el",
		L1Beacon: "beacon",
		L2Node:   elNode,
	})

	svcManager.AddComponent(&OpBatcher{
		L1Node:             "el",
		L2Node:             "op-geth",
		RollupNode:         "op-node",
		MaxChannelDuration: o.BatcherMaxChannelDuration,
	})

	if o.EnableChainMonitor {
		svcManager.AddComponent(&ChainMonitor{
			L1RPC:            "el",
			L2BlockTime:      o.BlockTime,
			L2BuilderAddress: defaultL2BuilderAddress,
			L2RPC:            "op-geth",
		})
	}

	if svcManager.ctx.Contender.TargetChain == "" {
		svcManager.ctx.Contender.TargetChain = "op-geth"
	}
	svcManager.RunContenderIfEnabled()
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
