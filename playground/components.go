package playground

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"
)

var defaultJWTToken = "04592280e1778419b7aa954d43871cb2cfb2ebda754fb735e8adeb293a88f9bf"

type RollupBoost struct {
	ELNode  string
	Builder string
}

func (r *RollupBoost) Run(service *Service, ctx *ExContext) {
	service.
		WithImage("docker.io/flashbots/rollup-boost").
		WithTag("0.4rc1").
		WithArgs(
			"--rpc-port", `{{Port "authrpc" 8551}}`,
			"--l2-jwt-path", "/data/jwtsecret",
			"--l2-url", Connect(r.ELNode, "authrpc"),
			"--builder-jwt-path", "/data/jwtsecret",
			"--builder-url", r.Builder,
		).WithArtifact("/data/jwtsecret", "jwtsecret")
	if ctx.AlloyEnabled {
		service.
			WithArgs(
				"--tracing",
				"--otlp-endpoint", Connect("grafana-alloy", "otlp-http"),
			)
	}
}

func (r *RollupBoost) Name() string {
	return "rollup-boost"
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
		Version: "v1.3.1",
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
		WithTag("v1.3.1").
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

type nullService struct {
}

func (n *nullService) Run(service *Service, ctx *ExContext) {
}

func (n *nullService) Name() string {
	return "null"
}
