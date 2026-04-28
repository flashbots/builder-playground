package playground

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	mevboostrelay "github.com/flashbots/builder-playground/mev-boost-relay"
	"github.com/flashbots/go-boost-utils/bls"
	"github.com/flashbots/go-boost-utils/utils"
)

var (
	defaultJWTToken          = "04592280e1778419b7aa954d43871cb2cfb2ebda754fb735e8adeb293a88f9bf"
	latestPlaygroundUtilsTag = "cc6f172493d7ef6b88a5b7895f4b8619806c99f9"
)

type RollupBoost struct {
	ELNode  string
	Builder string

	Flashblocks           bool
	FlashblocksBuilderURL string
}

func (r *RollupBoost) Apply(ctx *ExContext) *Component {
	component := NewComponent("rollup-boost")

	service := component.NewService("rollup-boost").
		WithImage("docker.io/flashbots/rollup-boost").
		WithTag("v0.7.12-rc1").
		WithArgs(
			"--rpc-host", "0.0.0.0",
			"--rpc-port", `{{Port "authrpc" 8551}}`,
			"--l2-jwt-path", "/data/jwtsecret",
			"--l2-url", Connect(r.ELNode, "authrpc"),
			"--builder-jwt-path", "/data/jwtsecret",
			"--builder-url", r.Builder,
		).WithArtifact("/data/jwtsecret", "jwtsecret")

	if r.Flashblocks {
		service.WithArgs(
			"--flashblocks",
			"--flashblocks-host", "0.0.0.0",
			"--flashblocks-port", `{{Port "flashblocks" 1112}}`,
		)
	}
	if r.FlashblocksBuilderURL != "" {
		service.WithArgs(
			"--flashblocks-builder-url", r.FlashblocksBuilderURL,
		)
	}

	return component
}

type OpRbuilder struct {
	Flashblocks bool
}

func (o *OpRbuilder) Apply(ctx *ExContext) *Component {
	component := NewComponent("op-rbuilder")

	service := component.NewService("op-rbuilder").
		WithImage("ghcr.io/flashbots/op-rbuilder").
		WithTag("v0.2.13").
		WithArgs(
			"node",
			"--authrpc.port", `{{Port "authrpc" 8551}}`,
			"--authrpc.addr", "0.0.0.0",
			"--authrpc.jwtsecret", "/data/jwtsecret",
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.port", `{{Port "http" 8545}}`,
			"--chain", "/data/l2-genesis.json",
			"--datadir", "/data_op_reth",
			"--color", "never",
			"--metrics", `0.0.0.0:{{Port "metrics" 9090}}`,
			"--port", `{{Port "rpc" 30303}}`,
			"--builder.enable-revert-protection",
			"--rollup.builder-secret-key", "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
		).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithVolume("data", "/data_op_reth").
		WithReady(ReadyCheck{
			QueryURL:    "http://localhost:8545",
			Interval:    1 * time.Second,
			Timeout:     10 * time.Second,
			Retries:     20,
			StartPeriod: 1 * time.Second,
		})

	if ctx.Bootnode != nil {
		service.WithArgs("--bootnodes", ctx.Bootnode.Connect(), "--nat", "none", "--rollup.discovery.v4")
	} else {
		service.WithArgs("--disable-discovery")
	}

	if o.Flashblocks {
		service.WithArgs(
			"--flashblocks.enabled",
			"--flashblocks.addr", "0.0.0.0",
			"--flashblocks.port", `{{Port "flashblocks" 1112}}`,
		)
	}

	return component
}

type FlashblocksRPC struct {
	FlashblocksWSService string
	BaseOverlay          bool
	UseWebsocketProxy    bool // Whether to add /ws path for websocket proxy
}

func (f *FlashblocksRPC) Apply(ctx *ExContext) *Component {
	component := NewComponent("flashblocks-rpc")

	websocketURL := ConnectWs(f.FlashblocksWSService, "flashblocks")
	if f.UseWebsocketProxy {
		websocketURL += "/ws"
	}

	var service *Service

	if f.BaseOverlay {
		service = component.NewService("flashblocks-rpc").
			WithImage("ghcr.io/base/node-reth-dev").
			WithTag("main").
			WithEntrypoint("/app/base-client").
			WithArgs(
				"node",
				"--websocket-url", websocketURL,
				"--enable-metering",
			)
	} else {
		service = component.NewService("flashblocks-rpc").
			WithImage("flashbots/flashblocks-rpc").
			WithTag("sha-7caffb9").
			WithArgs(
				"node",
				"--flashblocks.enabled",
				"--flashblocks.websocket-url", websocketURL,
			)
	}
	service.WithArgs(
		"--authrpc.port", `{{Port "authrpc" 8551}}`,
		"--authrpc.addr", "0.0.0.0",
		"--authrpc.jwtsecret", "/data/jwtsecret",
		"--http",
		"--http.addr", "0.0.0.0",
		"--http.port", `{{Port "http" 8545}}`,
		"--chain", "/data/l2-genesis.json",
		"--datadir", "/data_op_reth",
		"--color", "never",
		"--metrics", `0.0.0.0:{{Port "metrics" 9090}}`,
		"--port", `{{Port "rpc" 30303}}`,
	).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithVolume("data", "/data_flashblocks_rpc")

	if ctx.Bootnode != nil {
		service.WithArgs(
			"--bootnodes", ctx.Bootnode.Connect(),
			"--nat", "none",
			"--rollup.discovery.v4",
		)
	} else {
		service.WithArgs("--disable-discovery")
	}

	return component
}

type BProxy struct {
	TargetAuthrpc         string
	Peers                 []string
	Flashblocks           bool
	FlashblocksBuilderURL string
}

func (f *BProxy) Apply(ctx *ExContext) *Component {
	component := NewComponent("bproxy")

	peers := []string{}
	for _, peer := range f.Peers {
		peers = append(peers, Connect(peer, "authrpc"))
	}
	service := component.NewService("bproxy").
		WithImage("ghcr.io/flashbots/bproxy").
		WithTag("v0.1.2").
		WithArgs(
			"serve",
			"--authrpc-backend", f.TargetAuthrpc,
			"--authrpc-backend-timeout", "5s",
			"--authrpc-client-idle-connection-timeout", "15m",
			"--authrpc-deduplicate-fcus",
			"--authrpc-enabled",
			"--authrpc-listen-address", `0.0.0.0:{{Port "authrpc" 8651}}`,
			"--authrpc-log-requests",
			"--authrpc-log-responses",
			"--authrpc-max-backend-connections-per-host", "1",
			"--authrpc-max-request-size", "150",
			"--authrpc-max-response-size", "1150",
			"--authrpc-peers", strings.Join(peers, ","),
			"--authrpc-remove-backend-from-peers",
			"--authrpc-use-priority-queue",
		).
		WithArtifact("/data/jwtsecret", "jwtsecret")

	if f.Flashblocks {
		service.WithArgs(
			"--flashblocks-backend", f.FlashblocksBuilderURL,
			"--flashblocks-enabled",
			"--flashblocks-listen-address", `0.0.0.0:{{Port "flashblocks" 1114}}`,
			"--flashblocks-log-messages",
		)
	}

	return component
}

type WebsocketProxy struct {
	Upstream string
}

func (w *WebsocketProxy) Apply(ctx *ExContext) *Component {
	component := NewComponent("webproxy")

	component.NewService("websocket-proxy").
		WithImage("docker.io/mikawamp/websocket-rpc").
		WithTag("latest").
		WithArgs(
			"--listen-addr", `0.0.0.0:{{Port "flashblocks" 1115}}`,
			"--upstream-ws", ConnectWs(w.Upstream, "flashblocks"),
			"--enable-compression",
			"--client-ping-enabled",
		)

	return component
}

type ChainMonitor struct {
	L1RPC            string
	L2BlockTime      uint64
	L2BuilderAddress string
	L2RPC            string
}

func (c *ChainMonitor) Apply(ctx *ExContext) *Component {
	component := NewComponent("chain-monitor")

	component.NewService("chain-monitor").
		WithPort("metrics", 8080).
		WithImage("ghcr.io/flashbots/chain-monitor").
		WithTag("v0.0.54").
		WithArgs(
			"serve",
			"--l1-rpc", Connect(c.L1RPC, "http"),
			"--l2-block-time", fmt.Sprintf("%ds", c.L2BlockTime),
			"--l2-monitor-builder-address", c.L2BuilderAddress,
			"--l2-rpc", Connect(c.L2RPC, "http"),
		)

	return component
}

type OpBatcher struct {
	L1Node             string
	L2Node             string
	RollupNode         string
	MaxChannelDuration uint64
}

func (o *OpBatcher) Apply(ctx *ExContext) *Component {
	component := NewComponent("op-batcher")

	if o.MaxChannelDuration == 0 {
		o.MaxChannelDuration = 2
	}
	component.NewService("op-batcher").
		WithImage("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-batcher").
		WithTag("v1.16.3").
		WithEntrypoint("op-batcher").
		WithArgs(
			"--l1-eth-rpc", Connect(o.L1Node, "http"),
			"--l2-eth-rpc", Connect(o.L2Node, "http"),
			"--rollup-rpc", Connect(o.RollupNode, "http"),
			"--max-channel-duration="+strconv.FormatUint(o.MaxChannelDuration, 10),
			"--sub-safety-margin=4",
			"--poll-interval=1s",
			"--num-confirmations=1",
			"--private-key=0x2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6",
		)

	return component
}

type OpNode struct {
	L1Node   string
	L1Beacon string
	L2Node   string
}

func (o *OpNode) Apply(ctx *ExContext) *Component {
	component := NewComponent("op-node")

	component.NewService("op-node").
		WithImage("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-node").
		WithTag("v1.16.3").
		WithEntrypoint("op-node").
		WithEnv("A", "B"). // this is just a placeholder to make sure env works since we e2e test with the recipes
		WithArgs(
			"--l1", Connect(o.L1Node, "http"),
			"--l1.beacon", Connect(o.L1Beacon, "http"),
			"--l1.epoch-poll-interval", "12s",
			"--l1.http-poll-interval", "6s",
			"--l2", Connect(o.L2Node, "authrpc"),
			"--l2.jwt-secret", "/data/jwtsecret",
			"--metrics.enabled",
			"--metrics.addr", "0.0.0.0",
			"--metrics.port", `{{Port "metrics" 7300}}`,
			"--sequencer.enabled",
			"--sequencer.l1-confs", "0",
			"--verifier.l1-confs", "0",
			"--p2p.sequencer.key", "8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba",
			"--rollup.config", "/data/rollup.json",
			"--rollup.l1-chain-config", "/data/genesis.json",
			"--rpc.addr", "0.0.0.0",
			"--rpc.port", `{{Port "http" 8549}}`,
			"--p2p.listen.ip", "0.0.0.0",
			"--p2p.listen.tcp", `{{Port "p2p" 9003}}`,
			"--p2p.listen.udp", `{{PortUDP "p2p" 9003}}`,
			"--p2p.scoring.peers", "light",
			"--p2p.ban.peers", "true",
			"--pprof.enabled",
			"--rpc.enable-admin",
			"--safedb.path", "/data_db",
		).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/rollup.json", "rollup.json").
		WithArtifact("/data/genesis.json", "genesis.json").
		WithVolume("data", "/data_db")

	return component
}

type OpGeth struct {
	// outputs
	Enode *EnodeAddr
}

func logLevelToGethVerbosity(logLevel LogLevel) string {
	switch logLevel {
	case LevelTrace:
		return "5"
	case LevelDebug:
		return "4"
	case LevelInfo:
		return "3"
	case LevelWarn:
		return "2"
	case LevelError:
		return "1"
	default:
		return "3"
	}
}

func (o *OpGeth) Apply(ctx *ExContext) *Component {
	component := NewComponent("op-geth")
	o.Enode = ctx.Output.GetEnodeAddr()

	var trustedPeers string
	if ctx.Bootnode != nil {
		// TODO: Figure out the port dynamically.
		trustedPeers = fmt.Sprintf("--bootnodes enode://%s@$(getent hosts bootnode | awk '{print $1}'):30303 --discovery.v4 ", ctx.Bootnode.ID)
	} else {
		trustedPeers = "--nodiscover "
	}

	svc := component.NewService("op-geth").
		WithImage("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-geth").
		WithTag("v1.101604.0").
		WithEntrypoint("/bin/sh").
		WithLabel("metrics_path", "/debug/metrics/prometheus").
		WithArgs(
			"-c",
			"geth init --datadir /data_opgeth --state.scheme hash /data/l2-genesis.json && "+
				"exec geth "+
				"--datadir /data_opgeth "+
				"--verbosity "+logLevelToGethVerbosity(ctx.LogLevel)+" "+
				"--http "+
				"--http.corsdomain \"*\" "+
				"--http.vhosts \"*\" "+
				"--http.addr 0.0.0.0 "+
				"--http.port "+`{{Port "http" 8545}} `+
				"--http.api web3,debug,eth,txpool,net,engine,miner "+
				"--ws "+
				"--ws.addr 0.0.0.0 "+
				"--ws.port "+`{{Port "ws" 8546}} `+
				"--ws.origins \"*\" "+
				"--ws.api debug,eth,txpool,net,engine,miner "+
				"--syncmode full "+
				"--maxpeers 5 "+
				"--rpc.allow-unprotected-txs "+
				"--authrpc.addr 0.0.0.0 "+
				"--authrpc.port "+`{{Port "authrpc" 8551}} `+
				"--authrpc.vhosts \"*\" "+
				"--authrpc.jwtsecret /data/jwtsecret "+
				"--gcmode archive "+
				"--state.scheme hash "+
				"--port "+`{{Port "rpc" 30303}} `+
				"--nodekey /data/p2p_key.txt "+
				trustedPeers+
				"--metrics "+
				"--metrics.addr 0.0.0.0 "+
				"--metrics.port "+`{{Port "metrics" 6061}}`,
		).
		WithVolume("data", "/data_opgeth").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/p2p_key.txt", o.Enode.Artifact)

	UseHealthmon(component, svc, healthmonExecution)

	return component
}

type RethEL struct {
	UseRethForValidation bool
	UseNativeReth        bool
}

var rethELRelease = &release{
	Name:    "reth",
	Org:     "paradigmxyz",
	Version: "v1.9.3",
	Arch: func(goos, goarch string) string {
		if goos == "linux" {
			return "x86_64-unknown-linux-gnu"
		} else if goos == "darwin" && goarch == "arm64" { // Apple M1
			return "aarch64-apple-darwin"
		} else if goos == "darwin" && goarch == "amd64" {
			return "x86_64-apple-darwin"
		}
		return ""
	},
}

func logLevelToRethVerbosity(logLevel LogLevel) string {
	switch logLevel {
	case LevelTrace:
		return "-vvvvv"
	case LevelDebug:
		return "-vvvv"
	case LevelWarn:
		return "-vv"
	case LevelError:
		return "-v"
	case LevelInfo:
		fallthrough
	default:
		return "-vvv"
	}
}

func (r *RethEL) Apply(ctx *ExContext) *Component {
	component := NewComponent("reth")

	// start the reth el client
	svc := component.NewService("el").
		WithImage("ghcr.io/paradigmxyz/reth").
		WithTag("v1.9.3").
		WithEntrypoint("/usr/local/bin/reth").
		WithArgs(
			"node",
			"--chain", "/data/genesis.json",
			"--datadir", "/data_reth",
			"--color", "never",
			"--addr", "0.0.0.0",
			"--port", `{{Port "rpc" 30303}}`,
			"--ipcpath", "/data_reth/reth.ipc",
			// http config
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.api", "admin,eth,web3,net,txpool,rpc,mev,flashbots",
			"--http.port", `{{Port "http" 8545}}`,
			// websocket config
			"--ws",
			"--ws.addr", "0.0.0.0",
			"--ws.port", `{{Port "ws" 8546}}`,
			"--ws.api", "eth,web3,net,txpool,debug,trace",
			"--ws.origins", "*",
			"--authrpc.port", `{{Port "authrpc" 8551}}`,
			"--authrpc.addr", "0.0.0.0",
			"--authrpc.jwtsecret", "/data/jwtsecret",
			"--metrics", `0.0.0.0:{{Port "metrics" 9090}}`,
			// For reth version 1.2.0 the "legacy" engine was removed, so we now require these arguments:
			"--engine.persistence-threshold", "0", "--engine.memory-block-buffer-target", "0",
			logLevelToRethVerbosity(ctx.LogLevel),
		).
		WithRelease(rethELRelease).
		WithArtifact("/data/genesis.json", "genesis.json").
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithVolume("data", "/data_reth", true)

	if ctx.Bootnode != nil {
		svc.WithArgs("--bootnodes", ctx.Bootnode.Connect(), "--nat", "none")
	} else {
		svc.WithArgs("--disable-discovery")
	}

	UseHealthmon(component, svc, healthmonExecution)

	if r.UseNativeReth {
		// we need to use this otherwise the db cannot be binded
		svc.UseHostExecution()
	}

	return component
}

type LighthouseBeaconNode struct {
	ExecutionNode string
	MevBoostNode  string
}

func (l *LighthouseBeaconNode) Apply(ctx *ExContext) *Component {
	component := NewComponent("lighthouse-beacon-node")

	svc := component.NewService("beacon").
		WithImage("sigp/lighthouse").
		WithTag("v8.1.0").
		WithEntrypoint("lighthouse").
		WithArgs(
			"bn",
			"--datadir", "/data_beacon",
			"--testnet-dir", "/data/testnet-dir",
			"--enable-private-discovery",
			"--disable-peer-scoring",
			"--staking",
			"--enr-address", "127.0.0.1",
			"--enr-udp-port", `{{PortUDP "p2p" 9000}}`,
			"--enr-tcp-port", `{{Port "p2p" 9000}}`,
			"--enr-quic-port", `{{Port "quic-p2p" 9100}}`,
			"--port", `{{Port "p2p" 9000}}`,
			"--quic-port", `{{Port "quic-p2p" 9100}}`,
			"--http",
			"--http-port", `{{Port "http" 3500}}`,
			"--http-address", "0.0.0.0",
			"--http-allow-origin", "*",
			"--disable-packet-filter",
			"--target-peers", "0",
			"--execution-endpoint", Connect(l.ExecutionNode, "authrpc"),
			"--execution-jwt", "/data/jwtsecret",
			"--always-prepare-payload",
			"--prepare-payload-lookahead", "8000",
			"--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990",
		).
		WithArtifact("/data/testnet-dir", "testnet").
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithVolume("data", "/data_beacon")

	UseHealthmon(component, svc, healthmonBeacon)

	if l.MevBoostNode != "" {
		svc.WithArgs(
			"--builder", Connect(l.MevBoostNode, "http"),
			"--builder-fallback-epochs-since-finalization", "0",
			"--builder-fallback-disable-checks",
		)
	}

	return component
}

type LighthouseValidator struct {
	BeaconNode string
}

func (l *LighthouseValidator) Apply(ctx *ExContext) *Component {
	component := NewComponent("lighthouse-validator-node")

	// start validator client
	component.NewService("validator").
		WithImage("sigp/lighthouse").
		WithTag("v8.1.0").
		WithEntrypoint("lighthouse").
		WithArgs(
			"vc",
			"--datadir", "/data/validator",
			"--testnet-dir", "/data/testnet-dir",
			"--init-slashing-protection",
			"--beacon-nodes", Connect(l.BeaconNode, "http"),
			"--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990",
			"--builder-proposals",
			"--prefer-builder-proposals",
		).
		WithArtifact("/data/validator", "data_validator").
		WithArtifact("/data/testnet-dir", "testnet").
		// HACK: Mount a Docker-managed volume to avoid permission issues with removing logs.
		WithVolume("validator-logs", "/data/validator/validators/logs")

	return component
}

type ClProxy struct {
	PrimaryBuilder   string
	SecondaryBuilder string
}

func (c *ClProxy) Apply(ctx *ExContext) *Component {
	component := NewComponent("cl-proxy")

	component.NewService("cl-proxy").
		WithImage("docker.io/flashbots/playground-utils").
		WithTag(latestPlaygroundUtilsTag).
		WithEntrypoint("cl-proxy").
		WithArgs(
			"--primary-builder", Connect(c.PrimaryBuilder, "authrpc"),
			"--secondary-builder", c.SecondaryBuilder,
			"--port", `{{Port "authrpc" 5656}}`,
		)

	return component
}

type MevBoostRelay struct {
	ServiceName      string
	BeaconClient     string
	ValidationServer string
}

func (m *MevBoostRelay) serviceName() string {
	if m.ServiceName != "" {
		return m.ServiceName
	}
	return "mev-boost-relay"
}

func (m *MevBoostRelay) Apply(ctx *ExContext) *Component {
	serviceName := m.serviceName()
	component := NewComponent(serviceName)

	service := component.NewService(serviceName).
		WithImage("docker.io/flashbots/playground-utils").
		WithTag(latestPlaygroundUtilsTag).
		WithEnv("ALLOW_SYNCING_BEACON_NODE", "1").
		WithEntrypoint("mev-boost-relay").
		DependsOnHealthy(m.BeaconClient).
		WithArgs(
			"--api-listen-addr", "0.0.0.0",
			"--api-listen-port", `{{Port "http" 5555}}`,
			"--beacon-client-addr", Connect(m.BeaconClient, "http"),
		)

	if m.ValidationServer != "" {
		service.WithArgs("--validation-server-addr", Connect(m.ValidationServer, "http"))
	}

	return component
}

type OpReth struct{}

var opRethRelease = &release{
	Name:    "op-reth",
	Repo:    "reth",
	Org:     "paradigmxyz",
	Version: "v1.9.3",
	Arch: func(goos, goarch string) string {
		if goos == "linux" {
			return "x86_64-unknown-linux-gnu"
		} else if goos == "darwin" && goarch == "arm64" { // Apple M1
			return "aarch64-apple-darwin"
		} else if goos == "darwin" && goarch == "amd64" {
			return "x86_64-apple-darwin"
		}
		return ""
	},
}

func (o *OpReth) Apply(ctx *ExContext) *Component {
	component := NewComponent("op-reth")

	svc := component.NewService("op-reth").
		WithImage("ghcr.io/paradigmxyz/op-reth").
		WithTag("v1.9.3").
		WithEntrypoint("op-reth").
		WithArgs(
			"node",
			"--authrpc.port", `{{Port "authrpc" 8551}}`,
			"--authrpc.addr", "0.0.0.0",
			"--authrpc.jwtsecret", "/data/jwtsecret",
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.port", `{{Port "http" 8545}}`,
			"--chain", "/data/l2-genesis.json",
			"--datadir", "/data_op_reth",
			"--color", "never",
			"--metrics", `0.0.0.0:{{Port "metrics" 9090}}`,
			"--addr", "0.0.0.0",
			"--port", `{{Port "rpc" 30303}}`).
		WithRelease(opRethRelease).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithVolume("data", "/data_op_reth")

	if ctx.Bootnode != nil {
		svc.WithArgs("--bootnodes", ctx.Bootnode.Connect(), "--nat", "none", "--rollup.discovery.v4")
	} else {
		svc.WithArgs("--disable-discovery")
	}

	UseHealthmon(component, svc, healthmonExecution)

	return component
}

type MevBoost struct {
	RelayEndpoints []string
}

func localMevBoostRelayURL(endpoint string) (string, bool) {
	envSkBytes, err := hexutil.Decode(mevboostrelay.DefaultSecretKey)
	if err != nil {
		return "", false
	}
	secretKey, err := bls.SecretKeyFromBytes(envSkBytes[:])
	if err != nil {
		return "", false
	}
	blsPublicKey, err := bls.PublicKeyFromSecretKey(secretKey)
	if err != nil {
		return "", false
	}
	publicKey, err := utils.BlsPublicKeyToPublicKey(blsPublicKey)
	if err != nil {
		return "", false
	}

	return ConnectRaw(endpoint, "http", "http", publicKey.String()), true
}

func (m *MevBoost) Apply(ctx *ExContext) *Component {
	component := NewComponent("mev-boost")

	args := []string{
		"--addr", "0.0.0.0:" + `{{Port "http" 18550}}`,
		"--loglevel", "info",
	}

	for _, endpoint := range m.RelayEndpoints {
		if strings.Contains(endpoint, "://") {
			args = append(args, "--relay", endpoint)
		} else if relayURL, ok := localMevBoostRelayURL(endpoint); ok {
			args = append(args, "--relay", relayURL)
		} else {
			args = append(args, "--relay", Connect(endpoint, "http"))
		}
	}

	component.NewService("mev-boost").
		WithImage("flashbots/mev-boost").
		WithTag("latest").
		WithArgs(args...).
		WithEnv("GENESIS_FORK_VERSION", "0x20000089")

	return component
}

const defaultRbuilderRelaySecretKey = "0x25295f0d1d592a90b333e26e85149708208e9f8e8bc18f6c77bd62f8ad7a6866"

//go:embed utils/rbuilder-config.toml.tmpl
var defaultRbuilderConfigToml string

type Rbuilder struct {
	ServiceName     string
	BeaconNode      string
	ExecutionNode   string
	RelayEndpoints  []string
	RelaySecretKey  string
	ConfigArtifact  string
	ExtraData       string
	JSONRPCPort     int
	RedactedPort    int
	FullMetricsPort int
}

func (r *Rbuilder) serviceName() string {
	if r.ServiceName != "" {
		return r.ServiceName
	}
	return "rbuilder"
}

func (r *Rbuilder) beaconNode() string {
	if r.BeaconNode != "" {
		return r.BeaconNode
	}
	return "beacon"
}

func (r *Rbuilder) executionNode() string {
	if r.ExecutionNode != "" {
		return r.ExecutionNode
	}
	return "el"
}

func (r *Rbuilder) configArtifact() string {
	if r.ConfigArtifact != "" {
		return r.ConfigArtifact
	}
	if r.ServiceName != "" {
		return r.ServiceName + "-config.toml"
	}
	return "rbuilder-config.toml"
}

func (r *Rbuilder) relaySecretKey() string {
	if r.RelaySecretKey != "" {
		return r.RelaySecretKey
	}
	return defaultRbuilderRelaySecretKey
}

func (r *Rbuilder) relayEndpoints() []string {
	if len(r.RelayEndpoints) > 0 {
		return r.RelayEndpoints
	}
	return []string{"mev-boost-relay"}
}

func (r *Rbuilder) extraData() string {
	if r.ExtraData != "" {
		return r.ExtraData
	}
	return "Playground Builder"
}

func (r *Rbuilder) jsonRPCPort() int {
	if r.JSONRPCPort != 0 {
		return r.JSONRPCPort
	}
	return 8645
}

func (r *Rbuilder) redactedPort() int {
	if r.RedactedPort != 0 {
		return r.RedactedPort
	}
	return 6061
}

func (r *Rbuilder) fullMetricsPort() int {
	if r.FullMetricsPort != 0 {
		return r.FullMetricsPort
	}
	return 6060
}

func (r *Rbuilder) configTOML() string {
	relayEndpoints := r.relayEndpoints()
	relayNames := make([]string, 0, len(relayEndpoints))
	for _, relay := range relayEndpoints {
		relayNames = append(relayNames, strconv.Quote(relay))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "log_json = false\n")
	fmt.Fprintf(&b, "log_level = \"info,rbuilder=debug\"\n")
	fmt.Fprintf(&b, "redacted_telemetry_server_port = %d\n", r.redactedPort())
	fmt.Fprintf(&b, "redacted_telemetry_server_ip = \"0.0.0.0\"\n")
	fmt.Fprintf(&b, "full_telemetry_server_port = %d\n", r.fullMetricsPort())
	fmt.Fprintf(&b, "full_telemetry_server_ip = \"0.0.0.0\"\n\n")
	fmt.Fprintf(&b, "chain = \"/data/genesis.json\"\n")
	fmt.Fprintf(&b, "reth_datadir = \"/data_reth\"\n")
	fmt.Fprintf(&b, "el_node_ipc_path = \"/data_reth/reth.ipc\"\n")
	fmt.Fprintf(&b, "coinbase_secret_key = \"0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80\"\n")
	fmt.Fprintf(&b, "relay_secret_key = %s\n", strconv.Quote(r.relaySecretKey()))
	fmt.Fprintf(&b, "cl_node_url = [%s]\n", strconv.Quote(fmt.Sprintf("http://%s:3500", r.beaconNode())))
	fmt.Fprintf(&b, "jsonrpc_server_port = %d\n", r.jsonRPCPort())
	fmt.Fprintf(&b, "jsonrpc_server_ip = \"0.0.0.0\"\n")
	fmt.Fprintf(&b, "extra_data = %s\n\n", strconv.Quote(r.extraData()))
	fmt.Fprintf(&b, "ignore_cancellable_orders = true\n")
	fmt.Fprintf(&b, "root_hash_use_sparse_trie = true\n")
	fmt.Fprintf(&b, "root_hash_compare_sparse_trie = false\n")
	fmt.Fprintf(&b, "slot_delta_to_start_bidding_ms = -20000\n\n")
	fmt.Fprintf(&b, "live_builders = [\"mp-ordering\"]\n")
	fmt.Fprintf(&b, "enabled_relays = [%s]\n\n", strings.Join(relayNames, ", "))

	for i, relay := range relayEndpoints {
		fmt.Fprintf(&b, "[[relays]]\n")
		fmt.Fprintf(&b, "name = %s\n", strconv.Quote(relay))
		fmt.Fprintf(&b, "url = %s\n", strconv.Quote(fmt.Sprintf("http://%s:5555", relay)))
		fmt.Fprintf(&b, "priority = %d\n", i)
		fmt.Fprintf(&b, "use_ssz_for_submit = false\n")
		fmt.Fprintf(&b, "use_gzip_for_submit = false\n")
		fmt.Fprintf(&b, "mode = \"full\"\n\n")
	}

	fmt.Fprintf(&b, "[[builders]]\n")
	fmt.Fprintf(&b, "name = \"mp-ordering\"\n")
	fmt.Fprintf(&b, "algo = \"ordering-builder\"\n")
	fmt.Fprintf(&b, "discard_txs = true\n")
	fmt.Fprintf(&b, "sorting = \"max-profit\"\n")
	fmt.Fprintf(&b, "failed_order_retries = 1\n")
	fmt.Fprintf(&b, "drop_failed_orders = true\n")

	return b.String()
}

func (r *Rbuilder) Apply(ctx *ExContext) *Component {
	serviceName := r.serviceName()
	configArtifact := r.configArtifact()
	component := NewComponent(serviceName)

	// TODO: Handle error
	config := defaultRbuilderConfigToml
	if r.ServiceName != "" || len(r.RelayEndpoints) > 0 || r.BeaconNode != "" || r.ExecutionNode != "" ||
		r.RelaySecretKey != "" || r.ExtraData != "" || r.JSONRPCPort != 0 || r.RedactedPort != 0 || r.FullMetricsPort != 0 {
		config = r.configTOML()
	}
	ctx.Output.WriteFile(configArtifact, config)

	service := component.NewService(serviceName).
		WithImage("ghcr.io/flashbots/rbuilder").
		WithTag("sha-7efdc0b").
		WithArtifact("/data/rbuilder-config.toml", configArtifact).
		WithArtifact("/data/genesis.json", "genesis.json").
		WithPort("rpc", r.jsonRPCPort()).
		WithPort("redacted", r.redactedPort()).
		WithPort("full-metrics", r.fullMetricsPort()).
		WithVolume("shared:el-data", "/data_reth", true).
		DependsOnHealthy(r.executionNode()).
		DependsOnHealthy(r.beaconNode()).
		WithArgs(
			"run", "/data/rbuilder-config.toml",
		)
	service.Pid = "service:" + r.executionNode()

	return component
}

var flowProxyRelease = &release{
	Name:    "flowproxy",
	Repo:    "FlowProxy",
	Org:     "BuilderNet",
	Version: "v2.1.2",
	Arch: func(goos, goarch string) string {
		return ""
	},
	Format: "binary",
}

type FlowProxy struct {
	ServiceName    string
	BuilderService string
	BuilderName    string
	UserPort       int
	SystemPort     int
	MetricsPort    int
}

func (f *FlowProxy) serviceName() string {
	if f.ServiceName != "" {
		return f.ServiceName
	}
	return "flowproxy"
}

func (f *FlowProxy) builderService() string {
	if f.BuilderService != "" {
		return f.BuilderService
	}
	return "rbuilder"
}

func (f *FlowProxy) builderName() string {
	if f.BuilderName != "" {
		return f.BuilderName
	}
	return f.builderService()
}

func (f *FlowProxy) userPort() int {
	if f.UserPort != 0 {
		return f.UserPort
	}
	return 28545
}

func (f *FlowProxy) systemPort() int {
	if f.SystemPort != 0 {
		return f.SystemPort
	}
	return 29545
}

func (f *FlowProxy) metricsPort() int {
	if f.MetricsPort != 0 {
		return f.MetricsPort
	}
	return 29090
}

func (f *FlowProxy) Apply(ctx *ExContext) *Component {
	serviceName := f.serviceName()
	builderService := f.builderService()
	component := NewComponent(serviceName)

	component.NewService(serviceName).
		WithRelease(flowProxyRelease).
		UseHostExecution().
		WithArgs(
			"--user-listen-addr", fmt.Sprintf(`0.0.0.0:{{Port "http" %d}}`, f.userPort()),
			"--system-listen-addr", fmt.Sprintf(`0.0.0.0:{{Port "system" %d}}`, f.systemPort()),
			"--builder-name", f.builderName(),
			"--builder-url", Connect(builderService, "rpc"),
			"--builder-ready-endpoint", Connect(builderService, "redacted"),
			"--metrics", fmt.Sprintf(`0.0.0.0:{{Port "metrics" %d}}`, f.metricsPort()),
			"--disable-forwarding",
		).
		WithPort("http", f.userPort()).
		WithPort("system", f.systemPort()).
		WithPort("metrics", f.metricsPort()).
		DependsOnRunning(builderService)

	return component
}

type nullService struct{}

func (n *nullService) Apply(ctx *ExContext) *Component {
	return nil
}

type Contender struct {
	ExtraArgs   []string
	TargetChain string // defaults to "el", may be any chain name in a recipe's spec
}

// Converts a `ContenderContext` into a `Contender` service. `Enabled` is ignored.
func (cc *ContenderContext) Contender() *Contender {
	return &Contender{
		ExtraArgs:   cc.ExtraArgs,
		TargetChain: cc.TargetChain,
	}
}

// parse "key=value" OR "key value"; remainder after first space is the value (may contain spaces)
func parseKV(s string) (name, val string, hasVal, usedEq bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false, false
	}
	eq := strings.IndexByte(s, '=')
	ws := indexWS(s)

	// prefer '=' if it appears before any whitespace
	if eq > 0 && (ws == -1 || eq < ws) {
		return strings.TrimSpace(s[:eq]), strings.TrimSpace(s[eq+1:]), true, true
	}
	if ws == -1 {
		return s, "", false, false
	}
	return strings.TrimSpace(s[:ws]), strings.TrimSpace(s[ws+1:]), true, false
}

func indexWS(s string) int {
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return i
		}
	}
	return -1
}

func (c *Contender) Apply(ctx *ExContext) *Component {
	type opt struct {
		name   string
		val    string
		hasVal bool
	}

	targetChain := "el"
	if c.TargetChain != "" {
		targetChain = c.TargetChain
	}

	defaults := []opt{
		{name: "--forever"},
		{name: "--optimistic-nonces"},
		{name: "--min-balance", val: "10 ether", hasVal: true},
		{name: "-r", val: Connect(targetChain, "http"), hasVal: true},
		{name: "--tps", val: "20", hasVal: true},
	}

	// Parse extras and track seen flags
	type extra struct {
		name   string
		val    string
		hasVal bool
		usedEq bool
	}
	var extras []extra
	seen := map[string]bool{}

	for _, s := range c.ExtraArgs {
		name, val, hasVal, usedEq := parseKV(s)
		if name == "" {
			continue
		}
		extras = append(extras, extra{name, val, hasVal, usedEq})
		seen[name] = true
	}

	// Minimal conflict example: -r overrides default -r
	conflict := func(flag string) bool {
		if seen[flag] {
			return true
		}
		if (flag == "--infinite" || flag == "--indefinite" || flag == "--indefinitely") && seen["--forever"] {
			return true
		}
		if (flag == "-r" && seen["--rpc-url"]) || (flag == "--rpc-url" && seen["-r"]) {
			return true
		}
		if (flag == "--tpb" || flag == "--txs-per-second" || flag == "--tps" || flag == "--txs-per-block") &&
			(seen["--tpb"] || seen["--tps"] || seen["--txs-per-second"] || seen["--txs-per-block"]) {
			return true
		}
		return false
	}

	args := []string{"spam"}

	// Add defaults unless overridden
	for _, d := range defaults {
		if conflict(d.name) {
			continue
		}
		args = append(args, d.name)
		if d.hasVal {
			args = append(args, d.val)
		}
	}

	// Append extras verbatim, preserving "=" vs space
	for _, e := range extras {
		if !e.hasVal {
			args = append(args, e.name)
			continue
		}
		if e.usedEq {
			args = append(args, e.name+"="+e.val)
		} else {
			args = append(args, e.name, e.val)
		}
	}

	component := NewComponent("contender")
	service := component.NewService("contender").
		WithImage("flashbots/contender").
		WithTag("0.7.2").
		WithArgs(args...).
		DependsOnHealthy("beacon")

	if c.TargetChain == "op-geth" {
		service.DependsOnRunning("op-node")
	}

	return component
}

type BuilderHub struct{}

func (b *BuilderHub) Apply(exCtx *ExContext) *Component {
	component := NewComponent("builder-hub")

	// Database service
	component.NewService("builder-hub-db").
		WithImage("docker.io/flashbots/builder-hub-db").
		WithTag("0.3.1-alpha1").
		WithPort("postgres", 5432).
		WithEnv("PGUSER", "postgres").
		WithEnv("POSTGRES_DB", "postgres").
		WithEnv("POSTGRES_USER", "postgres").
		WithEnv("POSTGRES_PASSWORD", "postgres").
		WithReady(ReadyCheck{
			Test:        []string{"CMD-SHELL", "pg_isready"},
			Interval:    5 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     5,
			StartPeriod: 2 * time.Second,
		})

		// API service
	component.NewService("builder-hub-api").
		WithImage("docker.io/flashbots/builder-hub").
		WithTag("0.3.1-alpha1").
		DependsOnHealthy("builder-hub-db").
		WithPort("http", 8080).
		WithPort("admin", 8081).
		WithPort("internal", 8082).
		WithPort("metrics", 8090).
		WithEnv("MOCK_SECRETS", "true").
		WithEnv("POSTGRES_DSN", ConnectRaw("builder-hub-db", "postgres", "postgres", "postgres:postgres")+"/postgres?sslmode=disable").
		WithEnv("LISTEN_ADDR", "0.0.0.0:"+`{{Port "http" 8080}}`).
		WithEnv("ADMIN_ADDR", "0.0.0.0:"+`{{Port "admin" 8081}}`).
		WithEnv("INTERNAL_ADDR", "0.0.0.0:"+`{{Port "internal" 8082}}`).
		WithEnv("METRICS_ADDR", "0.0.0.0:"+`{{Port "metrics" 8090}}`).
		WithEnv("DISABLE_ADMIN_AUTH", "1").
		WithEnv("ALLOW_EMPTY_MEASUREMENTS", "1").
		WithReady(ReadyCheck{
			QueryURL:    "http://localhost:8081/readyz",
			Interval:    1 * time.Second,
			Timeout:     30 * time.Second,
			Retries:     3,
			StartPeriod: 1 * time.Second,
		})

	// Proxy service
	component.NewService("builder-hub-proxy").
		WithImage("docker.io/flashbots/builder-hub-mock-proxy").
		WithTag("0.3.1-alpha1").
		WithPort("http", 8888).
		WithEnv("TARGET", Connect("builder-hub-api", "http")).
		DependsOnHealthy("builder-hub-api").
		WithReady(ReadyCheck{
			QueryURL:    "http://localhost:8888",
			Interval:    1 * time.Second,
			Timeout:     30 * time.Second,
			Retries:     3,
			StartPeriod: 1 * time.Second,
		})

	return component
}

type Bootnode struct {
	Enode *EnodeAddr
}

func (b *Bootnode) Apply(ctx *ExContext) *Component {
	component := NewComponent("bootnode")

	b.Enode = ctx.Output.GetEnodeAddr()
	component.NewService("bootnode").
		WithImage("ghcr.io/paradigmxyz/reth").
		WithTag("v1.9.3").
		WithEntrypoint("/usr/local/bin/reth").
		WithArgs(
			"p2p", "bootnode",
			"--addr", `0.0.0.0:{{Port "rpc" 30303}}`,
			"--p2p-secret-key", "/data/p2p_key.txt",
			"-vvvv",
			"--color", "never",
			"--nat", "none",
			"--v5",
		).
		WithArtifact("/data/p2p_key.txt", b.Enode.Artifact)

	// Mutate the execution context by setting the bootnode.
	ctx.Bootnode = &BootnodeRef{
		Service: "bootnode",
		ID:      b.Enode.NodeID(),
	}

	return component
}

const (
	healthmonBeacon    = "beacon"
	healthmonExecution = "execution"
)

// DefaultHealthmonReadyCheck returns the standard ReadyCheck used by healthmon sidecars.
func DefaultHealthmonReadyCheck() ReadyCheck {
	return ReadyCheck{
		Test:        []string{"CMD", "wget", "--spider", "--quiet", "http://127.0.0.1:21171/ready"},
		Interval:    1 * time.Second,
		Timeout:     30 * time.Minute,
		Retries:     20,
		StartPeriod: 1 * time.Second,
	}
}

func UseHealthmon(component *Component, s *Service, chain string) {
	healthmonName := s.Name + "_healthmon"

	s.WithLabel(healthCheckSidecarLabel, healthmonName)
	component.NewService(healthmonName).
		WithImage("docker.io/flashbots/playground-utils").
		WithTag(latestPlaygroundUtilsTag).
		WithEntrypoint("healthmon").
		WithArgs("--chain", chain, "--url", Connect(s.Name, "http")).
		WithReady(DefaultHealthmonReadyCheck())
}

// Fileserver serves genesis and testnet files over HTTP using Caddy.
// This allows VMs or external clients to fetch configuration files.
type Fileserver struct{}

func (f *Fileserver) Apply(ctx *ExContext) *Component {
	component := NewComponent("fileserver")

	component.NewService("server").
		WithImage("caddy").
		WithTag("2-alpine").
		WithArgs(
			"caddy", "file-server",
			"--root", "/data",
			"--listen", `:{{Port "http" 8100}}`,
			"--browse",
		).
		WithArtifact("/data/genesis.json", "genesis.json").
		WithArtifact("/data/testnet", "testnet").
		WithReady(ReadyCheck{
			QueryURL:    "http://localhost:8100",
			Interval:    1 * time.Second,
			Timeout:     30 * time.Second,
			Retries:     3,
			StartPeriod: 1 * time.Second,
		})

	return component
}
