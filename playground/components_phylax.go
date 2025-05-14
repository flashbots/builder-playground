package playground

import (
	"strconv"
)

type AssertionDA struct {
	DevMode bool
	Pk      string
}

func (a *AssertionDA) Run(service *Service, ctx *ExContext) {
	var name string
	if a.DevMode {
		name = "ghcr.io/phylaxsystems/assertion-da/assertion-da-dev"
	} else {
		name = "ghcr.io/phylaxsystems/assertion-da/assertion-da"
	}
	service.
		WithImage(name).
		WithTag("main").
		WithArgs("--listen-addr", "0.0.0.0:"+`{{Port "http" 5001}}`, "--private-key", a.Pk).
		WithAbsoluteVolume("/var/run/docker.sock", "/var/run/docker.sock").
		WithAbsoluteVolume("/tmp", "/tmp").
		WithPrivileged()
	if ctx.AlloyEnabled {
		service.WithEnv("OTEL_EXPORTER_OTLP_ENDPOINT", Connect("grafana-alloy", "otlp-http")).
			WithEnv("OTEL_ENVIRONMENT_NAME", "PCL_DA").
			WithEnv("OTEL_SERVICE_NAME", "ASSERTION_DA")
	}

}

func (a *AssertionDA) Name() string {
	return "assertion-da"
}

type Faucet struct {
	Rpc        string
	FaucetPk   string
	FaucetName string
	Symbol     string
}

func (f *Faucet) Name() string {
	return "eth-faucet"
}

func (f *Faucet) Run(service *Service, ctx *ExContext) {
	service.WithImage("chainflag/eth-faucet").
		WithTag("1.2.0").
		WithArgs(
			"--wallet.provider", f.Rpc,
			"--wallet.privkey", f.FaucetPk,
			"--faucet.name", f.FaucetName,
			"--faucet.symbol", f.Symbol,
			"--httpport", `{{Port "faucet" 6942}}`,
		)

	service.DependsOn = []DependsOn{
		{
			Name:      "op-talos",
			Condition: DependsOnConditionRunning,
		},
	}
	service.WithReady(ReadyCheck{
		QueryURL: f.Rpc,
	})
}

type OpTalos struct {
	AssertionDA    string
	AssexGasLimit  uint64
	OracleContract string
	ImageName      string
	ImageTag       string
	BlockTag       string
}

func (o *OpTalos) Run(service *Service, ctx *ExContext) {
	service.WithImage(o.ImageName).
		WithTag(o.ImageTag).
		WithArgs(
			"node",
			"--authrpc.port", `{{Port "authrpc" 8551}}`,
			"--authrpc.addr", "0.0.0.0",
			"--authrpc.jwtsecret", "/data/jwtsecret",
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.port", `{{Port "http" 8545}}`,
			"--ws",
			"--ws.origins", "*",
			"--ws.port", `{{Port "ws" 8546}}`,
			"--chain", "/data/l2-genesis.json",
			"--datadir", "/data_op_talos",
			"--disable-discovery",
			"--color", "never",
			"--metrics", `0.0.0.0:{{Port "metrics" 9090}}`,
			"--port", `{{Port "rpc" 30303}}`,
			"--ae.rpc_da_url", o.AssertionDA,
			"--ae.rpc_url", "ws://localhost:8546",
			"--ae.oracle_contract", o.OracleContract,
		).
		WithArtifact("/data/jwtsecret", "jwtsecret").
		WithArtifact("/data/l2-genesis.json", "l2-genesis.json").
		WithVolume("data", "/data_op_reth").
		WithEnv("AE_ASSERTION_GAS_LIMIT", strconv.FormatUint(o.AssexGasLimit, 10)).
		WithEnv("AE_BLOCK_TAG", o.BlockTag).
		WithEnv("RUST_LOG", logLevelToTalosVerbosity(ctx.LogLevel))
	if ctx.AlloyEnabled {
		service.WithEnv("OTEL_EXPORTER_OTLP_ENDPOINT", Connect("grafana-alloy", "otlp-http")).
			WithEnv("OTEL_ENVIRONMENT_NAME", "PCL_TALOS").
			WithEnv("OTEL_SERVICE_NAME", "OP_TALOS")
	}
}

func (o *OpTalos) Name() string {
	return "op-talos"
}

func logLevelToTalosVerbosity(logLevel LogLevel) string {
	switch logLevel {
	case LevelTrace:
		return "trace"
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}
