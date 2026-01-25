package playground

import (
	"fmt"

	flag "github.com/spf13/pflag"
)

var _ Recipe = &OpRecipe{}

const defaultL2BuilderAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"

// OpRecipe is a recipe that deploys an OP stack
type OpRecipe struct {
	// externalBuilder is the URL of the external builder to use. If enabled, the recipe deploys
	// rollup-boost on the sequencer and uses this URL as the external builder.
	externalBuilder string

	// whether to enable the latest fork jovian and when
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

	// whether to enable chain-monitor
	enableChainMonitor bool

	// predeploysFile is the path to a JSON file containing additional contracts
	// to predeploy in the L2 genesis
	predeploysFile string

	// enableProxyd enables proxyd for routing transactions to ingress RPC
	enableProxyd bool

	// ingressRPC is the service name for the ingress RPC endpoint
	ingressRPC string
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
	flags.Var(&nullableUint64Value{&o.enableLatestFork}, "enable-latest-fork", "Enable Jovian fork: 0 = at genesis, N > 0 = at block N (default: Isthmus only)")
	flags.Uint64Var(&o.blockTime, "block-time", defaultOpBlockTimeSeconds, "Block time to use for the rollup")
	flags.Uint64Var(&o.batcherMaxChannelDuration, "batcher-max-channel-duration", 2, "Maximum channel duration to use for the batcher")
	flags.BoolVar(&o.flashblocks, "flashblocks", false, "Whether to enable flashblocks")
	flags.BoolVar(&o.baseOverlay, "base-overlay", false, "Whether to use base implementation for flashblocks-rpc")
	flags.StringVar(&o.flashblocksBuilderURL, "flashblocks-builder", "", "External URL of builder flashblocks stream")
	flags.BoolVar(&o.enableWebsocketProxy, "enable-websocket-proxy", false, "Whether to enable websocket proxy")
	flags.BoolVar(&o.enableChainMonitor, "chain-monitor", false, "Whether to enable chain-monitor")
	flags.StringVar(&o.predeploysFile, "use-predeploys", "", "Path to JSON file with additional contracts to predeploy in L2 genesis")
	flags.BoolVar(&o.enableProxyd, "proxyd", false, "Enable proxyd for routing eth_sendRawTransaction to ingress RPC")
	flags.StringVar(&o.ingressRPC, "ingress-rpc", "ingress-rpc", "Service name for ingress RPC endpoint")
	return flags
}

func (o *OpRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.WithL2()
	builder.ApplyLatestL2Fork(o.enableLatestFork)
	builder.OpBlockTime(o.blockTime)
	builder.PredeployFile(o.predeploysFile)

	// Generate proxyd config if proxyd is enabled
	if o.enableProxyd {
		// Use Docker service names for internal DNS resolution
		ingressURL := fmt.Sprintf("http://%s:8080", o.ingressRPC)
		standardELURL := "http://op-geth:8545"
		builder.WithProxyd(ingressURL, standardELURL)
	}

	return builder
}

func (o *OpRecipe) Apply(ctx *ExContext) *Component {
	component := NewComponent("op-recipe")

	component.AddComponent(ctx, &RethEL{})
	component.AddComponent(ctx, &LighthouseBeaconNode{
		ExecutionNode: "el",
	})
	component.AddComponent(ctx, &LighthouseValidator{
		BeaconNode: "beacon",
	})

	flashblocksBuilderURLRef := o.flashblocksBuilderURL
	externalBuilderRef := o.externalBuilder
	peers := []string{}

	opGeth := &OpGeth{}
	component.AddComponent(ctx, opGeth)

	ctx.Bootnode = &BootnodeRef{
		Service: "op-geth",
		ID:      opGeth.Enode.NodeID(),
	}

	if o.externalBuilder == "op-reth" {
		// Add a new op-reth service and connect it to Rollup-boost
		component.AddComponent(ctx, &OpReth{})

		externalBuilderRef = Connect("op-reth", "authrpc")
	} else if o.externalBuilder == "op-rbuilder" {
		component.AddComponent(ctx, &OpRbuilder{
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

	// Only enable bproxy if flashblocks is enabled (since flashblocks-rpc is the only service that needs it)
	if o.flashblocks {
		component.AddComponent(ctx, &BProxy{
			TargetAuthrpc:         externalBuilderRef,
			Peers:                 peers,
			Flashblocks:           o.flashblocks,
			FlashblocksBuilderURL: flashblocksBuilderURLRef,
		})
	}

	// Only enable websocket-proxy if the flag is set
	if o.enableWebsocketProxy {
		component.AddComponent(ctx, &WebsocketProxy{
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

		component.AddComponent(ctx, &RollupBoost{
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

		component.AddComponent(ctx, &FlashblocksRPC{
			FlashblocksWSService: flashblocksWSService,
			BaseOverlay:          o.baseOverlay,
			UseWebsocketProxy:    useWebsocketProxy,
		})
	}

	component.AddComponent(ctx, &OpNode{
		L1Node:   "el",
		L1Beacon: "beacon",
		L2Node:   elNode,
	})

	component.AddComponent(ctx, &OpBatcher{
		L1Node:             "el",
		L2Node:             "op-geth",
		RollupNode:         "op-node",
		MaxChannelDuration: o.batcherMaxChannelDuration,
	})

	if o.enableChainMonitor {
		component.AddComponent(ctx, &ChainMonitor{
			L1RPC:            "el",
			L2BlockTime:      o.blockTime,
			L2BuilderAddress: defaultL2BuilderAddress,
			L2RPC:            "op-geth",
		})
	}

	// If proxyd is enabled, add it
	if o.enableProxyd {
		elNode := "op-geth"
		if o.externalBuilder != "" {
			elNode = "rollup-boost"
		}

		component.AddComponent(ctx, &Proxyd{
			IngressRPC: o.ingressRPC,
			StandardEL: elNode,
		})
	}

	if ctx.Contender.TargetChain == "" {
		ctx.Contender.TargetChain = "op-geth"
	}
	component.RunContenderIfEnabled(ctx)

	return component
}

func (o *OpRecipe) Output(manifest *Manifest) map[string]interface{} {
	output := map[string]interface{}{}

	if o.enableProxyd {
		proxydService := manifest.MustGetService("proxyd")
		httpPort := proxydService.MustGetPort("http")
		output["proxyd-rpc"] = fmt.Sprintf("http://localhost:%d", httpPort.HostPort)
	}

	/*
		opGeth := manifest.MustGetService("op-geth").component.(*OpGeth)
		if opGeth.Enode != "" {
			// Only output if enode was set
			output["op-geth-enode"] = opGeth.Enode
		}
	*/

	return output
}
