package playground

import (
	"context"
	"encoding/base64"
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

func (r *RollupBoost) Run(service *Service, ctx *ExContext) {
	service.
		WithImage("docker.io/flashbots/rollup-boost").
		WithTag("0.7.0").
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

func (r *RollupBoost) Name() string {
	return "rollup-boost"
}

type OpRbuilder struct {
	Flashblocks bool
}

func (o *OpRbuilder) Run(service *Service, ctx *ExContext) {
	service.WithImage("ghcr.io/flashbots/op-rbuilder").
		WithTag("sha-4f1931b").
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
		).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithVolume("data", "/data_op_reth")

	if ctx.Bootnode != nil {
		service.WithArgs("--trusted-peers", ctx.Bootnode.Connect())
	}

	if o.Flashblocks {
		service.WithArgs(
			"--flashblocks.enabled",
			"--flashblocks.addr", "0.0.0.0",
			"--flashblocks.port", `{{Port "flashblocks" 1112}}`,
		)
	}
}

func (o *OpRbuilder) Name() string {
	return "op-rbuilder"
}

type FlashblocksRPC struct {
	FlashblocksWSService string
	BaseOverlay bool
	UseWebsocketProxy bool  // Whether to add /ws path for websocket proxy
}

func (f *FlashblocksRPC) Run(service *Service, ctx *ExContext) {
	websocketURL := ConnectWs(f.FlashblocksWSService, "flashblocks")
	if f.UseWebsocketProxy {
		websocketURL += "/ws"
	}

	if f.BaseOverlay {
		// Base doesn't have built image, so we use mikawamp/base-reth-node
		service.WithImage("docker.io/mikawamp/base-reth-node").
			WithTag("latest").
			WithEntrypoint("/app/base-reth-node").
			WithArgs(
				"node",
				"--websocket-url", websocketURL,
			)
	} else {
		service.WithImage("flashbots/flashblocks-rpc").
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

	if ctx.Bootnode != nil {
		service.WithArgs(
			"--trusted-peers", ctx.Bootnode.Connect(),
		)
	}
}

func (f *FlashblocksRPC) Name() string {
	return "flashblocks-rpc"
}

type BProxy struct {
	TargetAuthrpc string
	Peers []string
	Flashblocks bool
	FlashblocksBuilderURL string
}

func (f* BProxy) Run(service *Service, ctx *ExContext) {
	peers := []string{}
	for _, peer := range f.Peers {
		peers = append(peers, Connect(peer, "authrpc"))
	}
	service.WithImage("ghcr.io/flashbots/bproxy").
		WithTag("v0.0.91").
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

func (f *BProxy) Name() string {
	return "bproxy"
}

type WebsocketProxy struct {
	Upstream string
}

func (w *WebsocketProxy) Run(service *Service, ctx *ExContext) {
	service.WithImage("docker.io/mikawamp/websocket-rpc").
		WithTag("latest").
		WithArgs(
			"--listen-addr", `0.0.0.0:{{Port "flashblocks" 1115}}`,
			"--upstream-ws", ConnectWs(w.Upstream, "flashblocks"),
			"--enable-compression",
			"--client-ping-enabled",
		)
}

func (w *WebsocketProxy) Name() string {
	return "websocket-proxy"
}

type OpBatcher struct {
	L1Node             string
	L2Node             string
	RollupNode         string
	MaxChannelDuration uint64
}

func (o *OpBatcher) Run(service *Service, ctx *ExContext) {
	if o.MaxChannelDuration == 0 {
		o.MaxChannelDuration = 2
	}
	service.
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

func (o *OpBatcher) Name() string {
	return "op-batcher"
}

type OpNode struct {
	L1Node   string
	L1Beacon string
	L2Node   string
}

func (o *OpNode) Run(service *Service, ctx *ExContext) {
	service.
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
			"--metrics.enabled",
			"--metrics.addr", "0.0.0.0",
			"--metrics.port", `{{Port "metrics" 7300}}`,
			"--pprof.enabled",
			"--rpc.enable-admin",
			"--safedb.path", "/data_db",
		).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/rollup.json", "rollup.json").
		WithVolume("data", "/data_db")
}

func (o *OpNode) Name() string {
	return "op-node"
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

func (o *OpGeth) Run(service *Service, ctx *ExContext) {
	o.Enode = ctx.Output.GetEnodeAddr()

	var trustedPeers string
	if ctx.Bootnode != nil {
		trustedPeers = fmt.Sprintf("--bootnodes %s ", ctx.Bootnode.Connect())
	}

	service.
		WithImage("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-geth").
		WithTag("v1.101503.2-rc.5").
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
		WithArtifact("/data/p2p_key.txt", o.Enode.Artifact)
}

func (o *OpGeth) Name() string {
	return "op-geth"
}

var _ ServiceWatchdog = &OpGeth{}

func (o *OpGeth) Watchdog(out io.Writer, instance *instance, ctx context.Context) error {
	gethURL := fmt.Sprintf("http://localhost:%d", instance.service.MustGetPort("http").HostPort)
	return watchChainHead(out, gethURL, 2*time.Second)
}

type RethEL struct {
	UseRethForValidation bool
	UseNativeReth        bool
}

func (r *RethEL) ReleaseArtifact() *release {
	return &release{
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

func (r *RethEL) Run(svc *Service, ctx *ExContext) {
	// start the reth el client
	svc.
		WithImage("ghcr.io/paradigmxyz/reth").
		WithTag("v1.4.8").
		WithEntrypoint("/usr/local/bin/reth").
		WithArgs(
			"node",
			"--chain", "/data/genesis.json",
			"--datadir", "/data_reth",
			"--color", "never",
			"--ipcpath", "/data_reth/reth.ipc",
			"--addr", "127.0.0.1",
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
			logLevelToRethVerbosity(ctx.LogLevel),
		).
		WithArtifact("/data/genesis.json", "genesis.json").
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithVolume("data", "/data_reth")

	if r.UseNativeReth {
		// we need to use this otherwise the db cannot be binded
		svc.UseHostExecution()
	}
}

func (r *RethEL) Name() string {
	return "reth"
}

var _ ServiceWatchdog = &RethEL{}

func (r *RethEL) Watchdog(out io.Writer, instance *instance, ctx context.Context) error {
	rethURL := fmt.Sprintf("http://localhost:%d", instance.service.MustGetPort("http").HostPort)
	return watchChainHead(out, rethURL, 12*time.Second)
}

type LighthouseBeaconNode struct {
	ExecutionNode string
	MevBoostNode  string
}

func (l *LighthouseBeaconNode) Run(svc *Service, ctx *ExContext) {
	svc.
		WithImage("sigp/lighthouse").
		WithTag("v7.0.0-beta.0").
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

func (l *LighthouseBeaconNode) Name() string {
	return "lighthouse-beacon-node"
}

type LighthouseValidator struct {
	BeaconNode string
}

func (l *LighthouseValidator) Run(service *Service, ctx *ExContext) {
	// start validator client
	service.
		WithImage("sigp/lighthouse").
		WithTag("v7.0.0-beta.0").
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
		WithArtifact("/data/testnet-dir", "testnet")
}

func (l *LighthouseValidator) Name() string {
	return "lighthouse-validator"
}

type ClProxy struct {
	PrimaryBuilder   string
	SecondaryBuilder string
}

func (c *ClProxy) Run(service *Service, ctx *ExContext) {
	service.
		WithImage("docker.io/flashbots/playground-utils").
		WithTag("latest").
		WithEntrypoint("cl-proxy").
		WithArgs(
			"--primary-builder", Connect(c.PrimaryBuilder, "authrpc"),
			"--secondary-builder", c.SecondaryBuilder,
			"--port", `{{Port "authrpc" 5656}}`,
		)
}

func (c *ClProxy) Name() string {
	return "cl-proxy"
}

type MevBoostRelay struct {
	BeaconClient     string
	ValidationServer string
}

func (m *MevBoostRelay) Run(service *Service, ctx *ExContext) {
	service.
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

func (m *MevBoostRelay) Name() string {
	return "mev-boost-relay"
}

var _ ServiceWatchdog = &MevBoostRelay{}

func (m *MevBoostRelay) Watchdog(out io.Writer, instance *instance, ctx context.Context) error {
	beaconNodeURL := fmt.Sprintf("http://localhost:%d", instance.service.MustGetPort("http").HostPort)

	watchGroup := newWatchGroup()
	watchGroup.watch(func() error {
		return watchProposerPayloads(beaconNodeURL)
	})
	watchGroup.watch(func() error {
		return validateProposerPayloads(out, beaconNodeURL)
	})

	return watchGroup.wait()
}

type BuilderHubPostgres struct {
}

func (b *BuilderHubPostgres) Run(service *Service, ctx *ExContext) {
	service.
		WithImage("docker.io/flashbots/builder-hub-db").
		WithTag("latest").
		WithPort("postgres", 5432).
		WithEnv("POSTGRES_USER", "postgres").
		WithEnv("POSTGRES_PASSWORD", "postgres").
		WithEnv("POSTGRES_DB", "postgres").
		WithReady(ReadyCheck{
			Test:        []string{"CMD-SHELL", "pg_isready -U postgres -d postgres"},
			Interval:    1 * time.Second,
			Timeout:     30 * time.Second,
			Retries:     3,
			StartPeriod: 1 * time.Second,
		})
}

func (b *BuilderHubPostgres) Name() string {
	return "builder-hub-postgres"
}

type BuilderHub struct {
	postgres string
}

func (b *BuilderHub) Run(service *Service, ctx *ExContext) {
	service.
		WithImage("docker.io/flashbots/builder-hub").
		WithTag("latest").
		WithEntrypoint("/app/builder-hub").
		WithEnv("POSTGRES_DSN", ConnectRaw(b.postgres, "postgres", "postgres", "postgres:postgres")+"/postgres?sslmode=disable").
		WithEnv("LISTEN_ADDR", "0.0.0.0:"+`{{Port "http" 8080}}`).
		WithEnv("ADMIN_ADDR", "0.0.0.0:"+`{{Port "admin" 8081}}`).
		WithEnv("INTERNAL_ADDR", "0.0.0.0:"+`{{Port "internal" 8082}}`).
		WithEnv("METRICS_ADDR", "0.0.0.0:"+`{{Port "metrics" 8090}}`).
		DependsOnHealthy(b.postgres)
}

func (b *BuilderHub) Name() string {
	return "builder-hub"
}

type BuilderHubMockProxy struct {
	TargetService string
}

func (b *BuilderHubMockProxy) Run(service *Service, ctx *ExContext) {
	service.
		WithImage("docker.io/flashbots/builder-hub-mock-proxy").
		WithTag("latest").
		WithPort("http", 8888)

	if b.TargetService != "" {
		service.DependsOnHealthy(b.TargetService)
	}
}

func (b *BuilderHubMockProxy) Name() string {
	return "builder-hub-mock-proxy"
}

type OpReth struct {
}

func (o *OpReth) Run(service *Service, ctx *ExContext) {
	service.WithImage("ghcr.io/paradigmxyz/op-reth").
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
			"--port", `{{Port "rpc" 30303}}`).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithVolume("data", "/data_op_reth")
}

func (o *OpReth) Name() string {
	return "op-reth"
}

func (o *OpReth) ReleaseArtifact() *release {
	return &release{
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
}

var _ ServiceWatchdog = &OpReth{}

func (p *OpReth) Watchdog(out io.Writer, instance *instance, ctx context.Context) error {
	rethURL := fmt.Sprintf("http://localhost:%d", instance.service.MustGetPort("http").HostPort)
	return watchChainHead(out, rethURL, 2*time.Second)
}

type MevBoost struct {
	RelayEndpoints []string
}

func (m *MevBoost) Run(service *Service, ctx *ExContext) {
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

	service.WithImage("flashbots/mev-boost").
		WithTag("latest").
		WithArgs(args...).
		WithEnv("GENESIS_FORK_VERSION", "0x20000089")
}

func (m *MevBoost) Name() string {
	return "mev-boost"
}

type nullService struct {
}

func (n *nullService) Run(service *Service, ctx *ExContext) {
}

func (n *nullService) Name() string {
	return "null"
}

type Contender struct {
}

func (c *Contender) Name() string {
	return "contender"
}

func (c *Contender) Run(service *Service, ctx *ExContext) {
	args := []string{
		"spam",
		"-l",                        // loop indefinitely
		"--min-balance", "10 ether", // give each spammer 10 ether (sender must have 100 ether because default number of spammers is 10)
		"-r", Connect("el", "http"), // connect to whatever EL node is available
		"--tps", "20", // send 20 txs per second
	}
	service.WithImage("flashbots/contender").
		WithTag("latest").
		WithArgs(args...).
		DependsOnHealthy("beacon")

}

type BlockExplorer struct {
	ChainNode string // The EL node to connect to (e.g., "el" for L1, "op-geth" for L2)
	Port      int    // The port number to connect to (e.g., 8545 for standard RPC)
}

func (b *BlockExplorer) Run(service *Service, ctx *ExContext) {
	// Simple blockchain explorer using nginx
	// Default port if not specified
	if b.Port == 0 {
		b.Port = 8545
	}
	
	// Use localhost since the browser will connect from the host machine
	rpcURL := fmt.Sprintf("http://localhost:%d", b.Port)
	
	// Create an enhanced blockchain explorer with immediate block list, table-based transactions, and URL navigation
	// Using string concatenation to build the HTML
	explorerHTML := `<!DOCTYPE html>
<html>
<head>
    <title>Blockchain Explorer</title>
    <script src="https://cdn.jsdelivr.net/npm/web3@1.10.0/dist/web3.min.js"></script>
    <style>
        * { box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 20px; background: #f5f7fa; }
        .container { max-width: 1400px; margin: 0 auto; }
        h1 { color: #2c3e50; margin: 0 0 20px; font-size: 28px; cursor: pointer; }
        h2 { color: #34495e; margin: 20px 0 15px; font-size: 20px; }
        .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; margin-bottom: 20px; }
        .stat-card { background: white; padding: 15px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
        .stat-label { color: #7f8c8d; font-size: 12px; text-transform: uppercase; margin-bottom: 5px; }
        .stat-value { color: #2c3e50; font-size: 18px; font-weight: bold; font-family: 'Monaco', 'Courier New', monospace; }
        .section { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); margin-bottom: 20px; }
        .block-list, .tx-table { width: 100%%; border-collapse: collapse; }
        .block-list th, .tx-table th { background: #ecf0f1; padding: 12px; text-align: left; font-weight: 600; color: #2c3e50; border-bottom: 2px solid #bdc3c7; }
        .block-list td, .tx-table td { padding: 12px; border-bottom: 1px solid #ecf0f1; }
        .block-list tr:hover, .tx-table tr:hover { background: #f8f9fa; cursor: pointer; }
        .block-hash, .tx-hash, .address { font-family: 'Monaco', 'Courier New', monospace; font-size: 12px; color: #3498db; }
        .clickable { color: #3498db; cursor: pointer; text-decoration: none; }
        .clickable:hover { text-decoration: underline; }
        .loading { text-align: center; color: #7f8c8d; padding: 40px; }
        .error { color: #e74c3c; padding: 20px; background: #ffe6e6; border-radius: 5px; margin: 20px 0; }
        .detail-grid { display: grid; grid-template-columns: auto 1fr; gap: 10px; }
        .detail-label { font-weight: 600; color: #7f8c8d; padding: 8px; background: #f8f9fa; }
        .detail-value { padding: 8px; font-family: 'Monaco', 'Courier New', monospace; word-break: break-all; }
        .tx-list { margin-top: 20px; }
        .back-button { background: #95a5a6; color: white; border: none; padding: 8px 16px; border-radius: 5px; cursor: pointer; margin-bottom: 15px; }
        .back-button:hover { background: #7f8c8d; }
        .hidden { display: none; }
        .tx-status { display: inline-block; padding: 2px 8px; border-radius: 3px; font-size: 11px; font-weight: 600; }
        .tx-status.success { background: #d4edda; color: #155724; }
        .tx-status.failed { background: #f8d7da; color: #721c24; }
        .value-cell { text-align: right; font-weight: 500; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Blockchain Explorer</h1>
        
        <div class="nav">
            <button id="nav-overview" class="active" onclick="showView('overview')">Overview</button>
            <button id="nav-blocks" onclick="showView('blocks')">Blocks</button>
        </div>

        <div id="loading" class="loading">Connecting to node...</div>
        <div id="error" class="error hidden"></div>
        
        <!-- Overview View -->
        <div id="overview-view" class="hidden">
            <div class="stats">
                <div class="stat-card">
                    <div class="stat-label">RPC Endpoint</div>
                    <div class="stat-value" style="font-size: 14px;">%s</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">Chain ID</div>
                    <div class="stat-value" id="chainId">-</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">Latest Block</div>
                    <div class="stat-value" id="latestBlock">-</div>
                </div>
                <div class="stat-card">
                    <div class="stat-label">Gas Price</div>
                    <div class="stat-value" id="gasPrice">-</div>
                </div>
            </div>
        </div>
        
        <!-- Blocks List View -->
        <div id="blocks-view" class="hidden">
            <div class="section">
                <h2>Recent Blocks</h2>
                <table class="block-list">
                    <thead>
                        <tr>
                            <th>Block</th>
                            <th>Hash</th>
                            <th>Parent</th>
                            <th>Gas Used / Limit</th>
                            <th>Base Fee</th>
                            <th>Time</th>
                            <th>Txns</th>
                            <th>Fee Recipient</th>
                        </tr>
                    </thead>
                    <tbody id="blocks-tbody">
                    </tbody>
                </table>
            </div>
        </div>
        
        <!-- Block Detail View -->
        <div id="block-detail-view" class="hidden">
            <button class="back-button" onclick="showView('blocks')">← Back to Blocks</button>
            <div class="section">
                <h2>Block Details</h2>
                <div class="detail-grid" id="block-details"></div>
                <div class="tx-list" id="block-transactions">
                    <h3>Transactions</h3>
                    <div id="tx-list-content"></div>
                </div>
            </div>
        </div>
        
        <!-- Transaction Detail View -->
        <div id="tx-detail-view" class="hidden">
            <button class="back-button" onclick="backToBlock()">← Back to Block</button>
            <div class="section">
                <h2>Transaction Details</h2>
                <div class="detail-grid" id="tx-details"></div>
            </div>
        </div>
    </div>
    
    <script>
        const web3 = new Web3('%s');
        let blocks = [];
        let currentBlock = null;
        let currentView = 'overview';
        
        function showView(view) {
            currentView = view;
            document.querySelectorAll('[id$="-view"]').forEach(v => v.classList.add('hidden'));
            document.querySelectorAll('.nav button').forEach(b => b.classList.remove('active'));
            
            if (view === 'overview') {
                document.getElementById('overview-view').classList.remove('hidden');
                document.getElementById('nav-overview').classList.add('active');
            } else if (view === 'blocks') {
                document.getElementById('blocks-view').classList.remove('hidden');
                document.getElementById('nav-blocks').classList.add('active');
                loadBlocks();
            } else if (view === 'block-detail') {
                document.getElementById('block-detail-view').classList.remove('hidden');
            } else if (view === 'tx-detail') {
                document.getElementById('tx-detail-view').classList.remove('hidden');
            }
        }
        
        async function initialize() {
            try {
                const [chainId, blockNumber, gasPrice] = await Promise.all([
                    web3.eth.getChainId(),
                    web3.eth.getBlockNumber(),
                    web3.eth.getGasPrice()
                ]);
                
                document.getElementById('chainId').textContent = chainId.toString();
                document.getElementById('latestBlock').textContent = blockNumber.toString();
                document.getElementById('gasPrice').textContent = web3.utils.fromWei(gasPrice.toString(), 'gwei') + ' gwei';
                
                document.getElementById('loading').classList.add('hidden');
                document.getElementById('overview-view').classList.remove('hidden');
                
                // Auto-refresh overview
                setInterval(async () => {
                    if (currentView === 'overview') {
                        const [blockNumber, gasPrice] = await Promise.all([
                            web3.eth.getBlockNumber(),
                            web3.eth.getGasPrice()
                        ]);
                        document.getElementById('latestBlock').textContent = blockNumber.toString();
                        document.getElementById('gasPrice').textContent = web3.utils.fromWei(gasPrice.toString(), 'gwei') + ' gwei';
                    }
                }, 3000);
            } catch (error) {
                showError('Failed to connect: ' + error.message);
            }
        }
        
        async function loadBlocks() {
            try {
                const latestBlockNumber = await web3.eth.getBlockNumber();
                const blockPromises = [];
                const latestNum = parseInt(latestBlockNumber.toString());
                const startBlock = Math.max(0, latestNum - 99);
                
                for (let i = latestNum; i >= startBlock; i--) {
                    // Pass block number as string to avoid precision issues
                    blockPromises.push(web3.eth.getBlock(i.toString()));
                }
                
                blocks = await Promise.all(blockPromises);
                renderBlocks();
            } catch (error) {
                showError('Failed to load blocks: ' + error.message);
            }
        }
        
        function renderBlocks() {
            const tbody = document.getElementById('blocks-tbody');
            tbody.innerHTML = '';
            
            blocks.forEach(block => {
                if (!block) return;
                const row = tbody.insertRow();
                row.onclick = () => showBlockDetail(block);
                
                row.insertCell().innerHTML = '<span class="clickable">' + block.number + '</span>';
                row.insertCell().innerHTML = '<span class="block-hash">' + block.hash.substring(0, 10) + '...</span>';
                row.insertCell().innerHTML = '<span class="block-hash">' + block.parentHash.substring(0, 10) + '...</span>';
                row.insertCell().textContent = block.gasUsed.toString() + ' / ' + block.gasLimit.toString();
                row.insertCell().textContent = block.baseFeePerGas ? web3.utils.fromWei(block.baseFeePerGas.toString(), 'gwei') + ' gwei' : 'N/A';
                row.insertCell().textContent = new Date(parseInt(block.timestamp.toString()) * 1000).toLocaleString();
                row.insertCell().textContent = block.transactions.length;
                row.insertCell().innerHTML = '<span class="block-hash">' + block.miner.substring(0, 10) + '...</span>';
            });
        }
        
        async function showBlockDetail(block) {
            currentBlock = block;
            showView('block-detail');
            
            const details = document.getElementById('block-details');
            details.innerHTML = '';
            
            const fields = [
                ['Block Number', block.number],
                ['Block Hash', block.hash],
                ['Parent Hash', block.parentHash],
                ['Timestamp', new Date(parseInt(block.timestamp.toString()) * 1000).toLocaleString() + ' (' + block.timestamp + ')'],
                ['Miner/Fee Recipient', block.miner],
                ['Gas Used', block.gasUsed.toString() + ' / ' + block.gasLimit.toString() + ' (' + ((parseInt(block.gasUsed.toString()) / parseInt(block.gasLimit.toString())) * 100).toFixed(2) + '%%)'],
                ['Base Fee Per Gas', block.baseFeePerGas ? web3.utils.fromWei(block.baseFeePerGas.toString(), 'gwei') + ' gwei' : 'N/A'],
                ['Difficulty', block.difficulty],
                ['Total Difficulty', block.totalDifficulty],
                ['Size', block.size + ' bytes'],
                ['Nonce', block.nonce || 'N/A'],
                ['Extra Data', block.extraData],
                ['Logs Bloom', block.logsBloom.substring(0, 66) + '...'],
                ['State Root', block.stateRoot],
                ['Transactions Root', block.transactionsRoot],
                ['Receipts Root', block.receiptsRoot]
            ];
            
            fields.forEach(([label, value]) => {
                const labelDiv = document.createElement('div');
                labelDiv.className = 'detail-label';
                labelDiv.textContent = label;
                details.appendChild(labelDiv);
                
                const valueDiv = document.createElement('div');
                valueDiv.className = 'detail-value';
                valueDiv.textContent = value;
                details.appendChild(valueDiv);
            });
            
            // Load transactions
            const txContent = document.getElementById('tx-list-content');
            txContent.innerHTML = '';
            
            if (block.transactions.length === 0) {
                txContent.innerHTML = '<p>No transactions in this block</p>';
            } else {
                for (let txHash of block.transactions) {
                    const tx = await web3.eth.getTransaction(txHash);
                    const txDiv = document.createElement('div');
                    txDiv.className = 'tx-item';
                    txDiv.innerHTML = '<div><strong>Hash:</strong> <span class="tx-hash">' + tx.hash + '</span></div>' +
                        '<div><strong>From:</strong> ' + tx.from + ' → <strong>To:</strong> ' + (tx.to || 'Contract Creation') + '</div>' +
                        '<div><strong>Value:</strong> ' + web3.utils.fromWei(tx.value.toString(), 'ether') + ' ETH</div>';
                    txDiv.onclick = () => showTxDetail(tx);
                    txContent.appendChild(txDiv);
                }
            }
        }
        
        async function showTxDetail(tx) {
            showView('tx-detail');
            
            const receipt = await web3.eth.getTransactionReceipt(tx.hash);
            const details = document.getElementById('tx-details');
            details.innerHTML = '';
            
            const fields = [
                ['Transaction Hash', tx.hash],
                ['Block Number', tx.blockNumber],
                ['From', tx.from],
                ['To', tx.to || 'Contract Creation'],
                ['Value', web3.utils.fromWei(tx.value.toString(), 'ether') + ' ETH'],
                ['Gas', tx.gas.toString()],
                ['Gas Price', web3.utils.fromWei(tx.gasPrice.toString(), 'gwei') + ' gwei'],
                ['Gas Used', receipt.gasUsed.toString()],
                ['Status', receipt.status ? 'Success' : 'Failed'],
                ['Nonce', tx.nonce],
                ['Transaction Index', tx.transactionIndex],
                ['Input Data', tx.input.length > 66 ? tx.input.substring(0, 66) + '...' : tx.input],
                ['Cumulative Gas Used', receipt.cumulativeGasUsed.toString()],
                ['Effective Gas Price', receipt.effectiveGasPrice ? web3.utils.fromWei(receipt.effectiveGasPrice.toString(), 'gwei') + ' gwei' : 'N/A']
            ];
            
            fields.forEach(([label, value]) => {
                const labelDiv = document.createElement('div');
                labelDiv.className = 'detail-label';
                labelDiv.textContent = label;
                details.appendChild(labelDiv);
                
                const valueDiv = document.createElement('div');
                valueDiv.className = 'detail-value';
                valueDiv.textContent = value;
                details.appendChild(valueDiv);
            });
        }
        
        function backToBlock() {
            if (currentBlock) {
                showBlockDetail(currentBlock);
            } else {
                showView('blocks');
            }
        }
        
        function showError(message) {
            const errorDiv = document.getElementById('error');
            errorDiv.textContent = message;
            errorDiv.classList.remove('hidden');
            document.getElementById('loading').classList.add('hidden');
        }
        
        // Initialize on load
        initialize();
    </script>
</body>
</html>`
	explorerHTML = fmt.Sprintf(explorerHTML, rpcURL, rpcURL)

	// Base64 encode the HTML to avoid shell escaping issues
	encodedHTML := base64.StdEncoding.EncodeToString([]byte(explorerHTML))
	
	// Configure nginx to listen on port 4000
	nginxConfig := `server {
		listen 4000;
		listen [::]:4000;
		server_name localhost;
		root /usr/share/nginx/html;
		index index.html;
		location / {
			try_files $uri $uri/ =404;
		}
	}`
	
	encodedConfig := base64.StdEncoding.EncodeToString([]byte(nginxConfig))
	
	service.
		WithImage("nginx").
		WithTag("alpine").
		WithPort("http", 4000).
		WithEntrypoint("/bin/sh").
		WithArgs(
			"-c",
			fmt.Sprintf(`echo '%s' | base64 -d > /usr/share/nginx/html/index.html && 
				echo '%s' | base64 -d > /etc/nginx/conf.d/default.conf && 
				exec nginx -g 'daemon off;'`, encodedHTML, encodedConfig),
		).
		WithReady(ReadyCheck{
			Test:        []string{"CMD-SHELL", "wget -q -O /dev/null http://localhost:4000 || exit 1"},
			Interval:    2 * time.Second,
			Timeout:     30 * time.Second,
			Retries:     5,
			StartPeriod: 3 * time.Second,
		}).
		DependsOnRunning(b.ChainNode)
}

func (b *BlockExplorer) Name() string {
	return "block-explorer"
}
