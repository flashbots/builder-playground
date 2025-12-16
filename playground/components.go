package playground

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	mevboostrelay "github.com/flashbots/builder-playground/mev-boost-relay"
	"github.com/flashbots/go-boost-utils/bls"
	"github.com/flashbots/go-boost-utils/utils"
)

var defaultJWTToken = "04592280e1778419b7aa954d43871cb2cfb2ebda754fb735e8adeb293a88f9bf"

type RollupBoost struct {
	ELNode  string
	Builder string

	Flashblocks           bool
	FlashblocksBuilderURL string
}

func (r *RollupBoost) Apply(manifest *Manifest) {
	service := manifest.NewService("rollup-boost").
		WithImage("docker.io/flashbots/rollup-boost").
		WithTag("v0.7.5").
		DependsOnHealthy(r.ELNode).
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
}

type OpRbuilder struct {
	Flashblocks bool
}

func (o *OpRbuilder) Apply(manifest *Manifest) {
	service := manifest.NewService("op-rbuilder").
		WithImage("ghcr.io/flashbots/op-rbuilder").
		WithTag("v0.2.8").
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
			"--disable-discovery",
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

	if manifest.ctx.Bootnode != nil {
		service.WithArgs("--trusted-peers", manifest.ctx.Bootnode.Connect())
	}

	if o.Flashblocks {
		service.WithArgs(
			"--flashblocks.enabled",
			"--flashblocks.addr", "0.0.0.0",
			"--flashblocks.port", `{{Port "flashblocks" 1112}}`,
		)
	}
}

type FlashblocksRPC struct {
	FlashblocksWSService string
	BaseOverlay          bool
	UseWebsocketProxy    bool // Whether to add /ws path for websocket proxy
}

func (f *FlashblocksRPC) Apply(manifest *Manifest) {
	websocketURL := ConnectWs(f.FlashblocksWSService, "flashblocks")
	if f.UseWebsocketProxy {
		websocketURL += "/ws"
	}

	var service *Service

	if f.BaseOverlay {
		service = manifest.NewService("flashblocks-rpc").
			WithImage("ghcr.io/base/node-reth-dev").
			WithTag("main").
			WithEntrypoint("/app/base-reth-node").
			WithArgs(
				"node",
				"--websocket-url", websocketURL,
				"--enable-metering",
			)
	} else {
		service = manifest.NewService("flashblocks-rpc").
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
		"--disable-discovery",
		"--color", "never",
		"--metrics", `0.0.0.0:{{Port "metrics" 9090}}`,
		"--port", `{{Port "rpc" 30303}}`,
	).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithVolume("data", "/data_flashblocks_rpc")

	if manifest.ctx.Bootnode != nil {
		service.WithArgs(
			"--trusted-peers", manifest.ctx.Bootnode.Connect(),
		)
	}
}

type BProxy struct {
	TargetAuthrpc         string
	Peers                 []string
	Flashblocks           bool
	FlashblocksBuilderURL string
}

func (f *BProxy) Apply(manifest *Manifest) {
	peers := []string{}
	for _, peer := range f.Peers {
		peers = append(peers, Connect(peer, "authrpc"))
	}
	service := manifest.NewService("bproxy").
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
}

type WebsocketProxy struct {
	Upstream string
}

func (w *WebsocketProxy) Apply(manifest *Manifest) {
	manifest.NewService("websocket-proxy").
		WithImage("docker.io/mikawamp/websocket-rpc").
		WithTag("latest").
		WithArgs(
			"--listen-addr", `0.0.0.0:{{Port "flashblocks" 1115}}`,
			"--upstream-ws", ConnectWs(w.Upstream, "flashblocks"),
			"--enable-compression",
			"--client-ping-enabled",
		)
}

type ChainMonitor struct {
	L1RPC            string
	L2BlockTime      uint64
	L2BuilderAddress string
	L2RPC            string
}

func (c *ChainMonitor) Apply(manifest *Manifest) {
	manifest.NewService("chain-monitor").
		WithPort("metrics", 8080).
		WithImage("ghcr.io/flashbots/chain-monitor").
		WithTag("v0.0.54").
		DependsOnHealthy(c.L1RPC).
		DependsOnHealthy(c.L2RPC).
		WithArgs(
			"serve",
			"--l1-rpc", Connect(c.L1RPC, "http"),
			"--l2-block-time", fmt.Sprintf("%ds", c.L2BlockTime),
			"--l2-monitor-builder-address", c.L2BuilderAddress,
			"--l2-rpc", Connect(c.L2RPC, "http"),
		)
}

type OpBatcher struct {
	L1Node             string
	L2Node             string
	RollupNode         string
	MaxChannelDuration uint64
}

func (o *OpBatcher) Apply(manifest *Manifest) {
	if o.MaxChannelDuration == 0 {
		o.MaxChannelDuration = 2
	}
	manifest.NewService("op-batcher").
		WithImage("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-batcher").
		WithTag("v1.12.0-rc.1").
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
}

type OpNode struct {
	L1Node   string
	L1Beacon string
	L2Node   string
}

func (o *OpNode) Apply(manifest *Manifest) {
	manifest.NewService("op-node").
		WithImage("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-node").
		WithTag("v1.13.0-rc.1").
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
		WithVolume("data", "/data_db")
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

func (o *OpGeth) Apply(manifest *Manifest) {
	o.Enode = manifest.ctx.Output.GetEnodeAddr()

	var trustedPeers string
	if manifest.ctx.Bootnode != nil {
		trustedPeers = fmt.Sprintf("--bootnodes %s ", manifest.ctx.Bootnode.Connect())
	}

	manifest.NewService("op-geth").
		WithImage("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-geth").
		WithTag("v1.101503.2-rc.5").
		WithEntrypoint("/bin/sh").
		WithLabel("metrics_path", "/debug/metrics/prometheus").
		WithArgs(
			"-c",
			"geth init --datadir /data_opgeth --state.scheme hash /data/l2-genesis.json && "+
				"exec geth "+
				"--datadir /data_opgeth "+
				"--verbosity "+logLevelToGethVerbosity(manifest.ctx.LogLevel)+" "+
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
				"--nodiscover "+
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
		WithArtifact("/data/p2p_key.txt", o.Enode.Artifact).
		WithReady(ReadyCheck{
			Test:        ElWatch("http://localhost:8545", 2*time.Second),
			Interval:    1 * time.Second,
			Timeout:     10 * time.Second,
			Retries:     20,
			StartPeriod: 1 * time.Second,
		})
}

func opGethReadyFn(ctx context.Context, service *Service) error {
	opGethURL := fmt.Sprintf("http://localhost:%d", service.MustGetPort("http").HostPort)
	return waitForFirstBlock(ctx, opGethURL, 60*time.Second)
}

func opGethWatchdogFn(out io.Writer, service *Service, ctx context.Context) error {
	gethURL := fmt.Sprintf("http://localhost:%d", service.MustGetPort("http").HostPort)
	return watchChainHead(out, gethURL, 2*time.Second)
}

type RethEL struct {
	UseRethForValidation bool
	UseNativeReth        bool
}

var rethELRelease = &release{
	Name:    "reth",
	Org:     "paradigmxyz",
	Version: "v1.4.8",
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

func (r *RethEL) Apply(manifest *Manifest) {
	// start the reth el client
	svc := manifest.NewService("el").
		WithImage("ghcr.io/paradigmxyz/reth").
		WithTag("v1.8.2").
		WithEntrypoint("/usr/local/bin/reth").
		WithArgs(
			"node",
			"--chain", "/data/genesis.json",
			"--datadir", "/data_reth",
			"--color", "never",
			"--ipcdisable",
			"--addr", "0.0.0.0",
			"--port", `{{Port "rpc" 30303}}`,
			// "--disable-discovery",
			// http config
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.api", "admin,eth,web3,net,rpc,mev,flashbots",
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
			logLevelToRethVerbosity(manifest.ctx.LogLevel),
		).
		WithRelease(rethELRelease).
		WithArtifact("/data/genesis.json", "genesis.json").
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithVolume("data", "/data_reth").
		WithReady(ReadyCheck{
			Test:        ElWatch("http://localhost:8545", 12*time.Second),
			Interval:    1 * time.Second,
			Timeout:     10 * time.Second,
			Retries:     20,
			StartPeriod: 1 * time.Second,
		})

	if r.UseNativeReth {
		// we need to use this otherwise the db cannot be binded
		svc.UseHostExecution()
	}
}

type LighthouseBeaconNode struct {
	ExecutionNode string
	MevBoostNode  string
}

func (l *LighthouseBeaconNode) Apply(manifest *Manifest) {
	svc := manifest.NewService("beacon").
		WithImage("sigp/lighthouse").
		WithTag("v8.0.0-rc.2").
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
		WithVolume("data", "/data_beacon").
		WithReady(ReadyCheck{
			QueryURL:    "http://localhost:3500/eth/v1/node/syncing",
			Interval:    1 * time.Second,
			Timeout:     30 * time.Second,
			Retries:     3,
			StartPeriod: 1 * time.Second,
		})

	if l.MevBoostNode != "" {
		svc.WithArgs(
			"--builder", Connect(l.MevBoostNode, "http"),
			"--builder-fallback-epochs-since-finalization", "0",
			"--builder-fallback-disable-checks",
		)
	}
}

type LighthouseValidator struct {
	BeaconNode string
}

func (l *LighthouseValidator) Apply(manifest *Manifest) {
	// start validator client
	manifest.NewService("validator").
		WithImage("sigp/lighthouse").
		WithTag("v8.0.0-rc.2").
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
}

type ClProxy struct {
	PrimaryBuilder   string
	SecondaryBuilder string
}

func (c *ClProxy) Apply(manifest *Manifest) {
	manifest.NewService("cl-proxy").
		WithImage("docker.io/flashbots/playground-utils").
		WithTag("latest").
		WithEntrypoint("cl-proxy").
		WithArgs(
			"--primary-builder", Connect(c.PrimaryBuilder, "authrpc"),
			"--secondary-builder", c.SecondaryBuilder,
			"--port", `{{Port "authrpc" 5656}}`,
		)
}

type MevBoostRelay struct {
	BeaconClient     string
	ValidationServer string
}

func (m *MevBoostRelay) Apply(manifest *Manifest) {
	service := manifest.NewService("mev-boost-relay").
		WithImage("docker.io/flashbots/playground-utils").
		WithTag("latest").
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
}

func mevboostRelayWatchdogFn(out io.Writer, service *Service, ctx context.Context) error {
	beaconNodeURL := fmt.Sprintf("http://localhost:%d", service.MustGetPort("http").HostPort)

	watchGroup := newWatchGroup()
	watchGroup.watch(func() error {
		return watchProposerPayloads(beaconNodeURL)
	})
	watchGroup.watch(func() error {
		return validateProposerPayloads(out, beaconNodeURL)
	})

	return watchGroup.wait()
}

type OpReth struct{}

var opRethRelease = &release{
	Name:    "op-reth",
	Repo:    "reth",
	Org:     "paradigmxyz",
	Version: "v1.3.12",
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

func (o *OpReth) Apply(manifest *Manifest) {
	manifest.NewService("op-reth").
		WithImage("ghcr.io/paradigmxyz/op-reth").
		WithTag("nightly").
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
			"--disable-discovery",
			"--color", "never",
			"--metrics", `0.0.0.0:{{Port "metrics" 9090}}`,
			"--addr", "0.0.0.0",
			"--port", `{{Port "rpc" 30303}}`).
		WithRelease(opRethRelease).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithVolume("data", "/data_op_reth").
		WithReady(ReadyCheck{
			Test:        ElWatch("http://localhost:8545", 2*time.Second),
			Interval:    1 * time.Second,
			Timeout:     10 * time.Second,
			Retries:     20,
			StartPeriod: 1 * time.Second,
		})
}

type MevBoost struct {
	RelayEndpoints []string
}

func (m *MevBoost) Apply(manifest *Manifest) {
	args := []string{
		"--addr", "0.0.0.0:" + `{{Port "http" 18550}}`,
		"--loglevel", "info",
	}

	for _, endpoint := range m.RelayEndpoints {
		if endpoint == "mev-boost-relay" {
			// creating relay url with public key since mev-boost requires it
			envSkBytes, err := hexutil.Decode(mevboostrelay.DefaultSecretKey)
			if err != nil {
				continue
			}
			secretKey, err := bls.SecretKeyFromBytes(envSkBytes[:])
			if err != nil {
				continue
			}
			blsPublicKey, err := bls.PublicKeyFromSecretKey(secretKey)
			if err != nil {
				continue
			}
			publicKey, err := utils.BlsPublicKeyToPublicKey(blsPublicKey)
			if err != nil {
				continue
			}

			relayURL := ConnectRaw("mev-boost-relay", "http", "http", publicKey.String())
			args = append(args, "--relay", relayURL)
		} else {
			args = append(args, "--relay", Connect(endpoint, "http"))
		}
	}

	manifest.NewService("mev-boost").
		WithImage("flashbots/mev-boost").
		WithTag("latest").
		WithArgs(args...).
		WithEnv("GENESIS_FORK_VERSION", "0x20000089")
}

type nullService struct{}

func (n *nullService) Apply(manifest *Manifest) {
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

func (c *Contender) Apply(manifest *Manifest) {
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
		{name: "-l"},
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

	// Minimal conflict example: --loops overrides default "-l"
	conflict := func(flag string) bool {
		if seen[flag] {
			return true
		}
		if flag == "-l" && seen["--loops"] {
			return true
		}
		if flag == "-r" && seen["--rpc-url"] {
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

	service := manifest.NewService("contender").
		WithImage("flashbots/contender").
		WithTag("latest").
		WithArgs(args...).
		DependsOnHealthy("beacon")

	if c.TargetChain == "op-geth" {
		service.DependsOnRunning("op-node")
	}
}

type BuilderHub struct{}

func (b *BuilderHub) Apply(manifest *Manifest) {
	// Database service
	manifest.NewService("db").
		WithImage("docker.io/flashbots/builder-hub-db").
		WithTag("latest").
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

	// Web service
	manifest.NewService("web").
		WithImage("docker.io/flashbots/builder-hub").
		WithTag("latest").
		DependsOnHealthy("db").
		WithPort("http", 8080).
		WithPort("admin", 8081).
		WithPort("internal", 8082).
		WithPort("metrics", 8090).
		WithEnv("MOCK_SECRETS", "true").
		WithEnv("POSTGRES_DSN", ConnectRaw("db", "postgres", "postgres", "postgres:postgres")+"/postgres?sslmode=disable").
		WithEnv("LISTEN_ADDR", "0.0.0.0:"+`{{Port "http" 8080}}`).
		WithEnv("ADMIN_ADDR", "0.0.0.0:"+`{{Port "admin" 8081}}`).
		WithEnv("INTERNAL_ADDR", "0.0.0.0:"+`{{Port "internal" 8082}}`).
		WithEnv("METRICS_ADDR", "0.0.0.0:"+`{{Port "metrics" 8090}}`).
		WithEnv("DISABLE_ADMIN_AUTH", "1").
		WithReady(ReadyCheck{
			QueryURL:    "http://localhost:8080",
			Interval:    1 * time.Second,
			Timeout:     30 * time.Second,
			Retries:     3,
			StartPeriod: 1 * time.Second,
			UseNC:       true, // because the endpoint returns 404
		})

	// Proxy service
	manifest.NewService("proxy").
		WithImage("docker.io/flashbots/builder-hub-mock-proxy").
		WithTag("latest").
		WithPort("http", 8888).
		WithEnv("TARGET", Connect("web", "http")).
		DependsOnHealthy("web")
}
