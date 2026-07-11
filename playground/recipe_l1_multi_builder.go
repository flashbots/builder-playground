package playground

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	flag "github.com/spf13/pflag"
)

// blsCurveOrder is the order r of the BLS12-381 scalar field. A valid BLS
// secret key is a non-zero scalar in [1, r-1]; blst (the Rust implementation
// rbuilder uses) rejects raw bytes whose big-endian value is >= r.
var blsCurveOrder, _ = new(big.Int).SetString(
	"73eda753299d7d483339d80809a1d80553bda402fffe5bfeffffffff00000001", 16,
)

var _ Recipe = &L1MultiBuilderRecipe{}

const (
	defaultL1MultiBuilderCount = 2
	defaultL1MultiRelayCount   = 2
)

type L1MultiBuilderRecipe struct {
	latestFork           bool
	blockTime            time.Duration
	useRethForValidation bool
	builderCount         int
	relayCount           int
}

func (l *L1MultiBuilderRecipe) Name() string {
	return "l1-multi-builder"
}

func (l *L1MultiBuilderRecipe) Description() string {
	return "Deploy a full L1 stack with multiple builders and relays"
}

func (l *L1MultiBuilderRecipe) Flags() *flag.FlagSet {
	flags := flag.NewFlagSet("l1-multi-builder", flag.ContinueOnError)
	flags.BoolVar(&l.latestFork, "latest-fork", false, "use the latest fork")
	flags.DurationVar(&l.blockTime, "block-time", time.Duration(defaultL1BlockTimeSeconds)*time.Second, "Block time to use for the L1")
	flags.BoolVar(&l.useRethForValidation, "use-reth-for-validation", false, "use reth for validation")
	flags.IntVar(&l.builderCount, "builders", defaultL1MultiBuilderCount, "number of rbuilder instances to run; must be >= 1")
	flags.IntVar(&l.relayCount, "relays", defaultL1MultiRelayCount, "number of mev-boost-relay instances to run; must be >= 1")
	return flags
}

// Validate checks the recipe's flag-backed fields. Called from every entry
// point that consumes the recipe (cook/start, generate, validate) so that
// explicit zero/negative values fail loudly instead of being silently
// rewritten to defaults. GetBaseRecipes registers Flags() once on each
// instance so the struct zero never reaches Validate in normal usage.
func (l *L1MultiBuilderRecipe) Validate() error {
	if l.builderCount < 1 {
		return fmt.Errorf("--builders must be >= 1, got %d", l.builderCount)
	}
	if l.relayCount < 1 {
		return fmt.Errorf("--relays must be >= 1, got %d", l.relayCount)
	}
	if l.blockTime <= 0 {
		return fmt.Errorf("--block-time must be > 0, got %s", l.blockTime)
	}
	return nil
}

func (l *L1MultiBuilderRecipe) Artifacts() *ArtifactsBuilder {
	builder := NewArtifactsBuilder()
	builder.ApplyLatestL1Fork(l.latestFork)
	builder.L1BlockTime(max(1, uint64(l.blockTime.Seconds())))
	return builder
}

// indexedBLSSecret derives a deterministic BLS secret in hex (0x-prefixed) by
// hashing a domain-separated label and reducing modulo the BLS12-381 scalar
// field order so blst-based consumers (rbuilder, mev-boost-relay) accept it.
// Avoids visually trivial scalars like 0x...0001 that hint at low-entropy keys.
func indexedBLSSecret(label string, i int) string {
	h := crypto.Keccak256([]byte(fmt.Sprintf("%s-%d", label, i)))
	n := new(big.Int).SetBytes(h)
	n.Mod(n, blsCurveOrder)
	if n.Sign() == 0 {
		// Reduction landed on zero; bump to 1 so the scalar is non-zero. With a
		// uniform 256-bit input this branch is astronomically unlikely.
		n.SetInt64(1)
	}
	out := make([]byte, 32)
	n.FillBytes(out)
	return hexutil.Encode(out)
}

func (l *L1MultiBuilderRecipe) Apply(ctx *ExContext) *Component {
	component := NewComponent("l1-multi-builder-recipe")

	component.AddComponent(ctx, &RethEL{
		UseRethForValidation: l.useRethForValidation,
	})

	relayServices := make([]string, 0, l.relayCount)
	relaySecrets := make([]string, 0, l.relayCount)
	for i := 1; i <= l.relayCount; i++ {
		relayServices = append(relayServices, fmt.Sprintf("mev-boost-relay-%d", i))
		relaySecrets = append(relaySecrets, indexedBLSSecret("playground-relay", i))
	}

	component.AddComponent(ctx, &LighthouseBeaconNode{
		ExecutionNode: "el",
		MevBoostNode:  "mev-boost",
	})
	component.AddComponent(ctx, &LighthouseValidator{
		BeaconNode: "beacon",
	})

	mevBoostValidationServer := ""
	if l.useRethForValidation {
		mevBoostValidationServer = "el"
	}
	for i, relayService := range relayServices {
		component.AddComponent(ctx, &MevBoostRelay{
			ServiceName:      relayService,
			BeaconClient:     "beacon",
			ValidationServer: mevBoostValidationServer,
			SecretKey:        relaySecrets[i],
		})
	}

	mevBoostRelays := make([]MevBoostRelayEndpoint, 0, len(relayServices))
	for i, relayService := range relayServices {
		mevBoostRelays = append(mevBoostRelays, MevBoostRelayEndpoint{
			Service:   relayService,
			SecretKey: relaySecrets[i],
		})
	}
	component.AddComponent(ctx, &MevBoost{
		RelayEndpoints: mevBoostRelays,
	})

	for i := 1; i <= l.builderCount; i++ {
		component.AddComponent(ctx, &Rbuilder{
			ServiceName:    fmt.Sprintf("rbuilder-%d", i),
			RelayEndpoints: relayServices,
			RelaySecretKey: indexedBLSSecret("playground-rbuilder", i),
			ExtraData:      fmt.Sprintf("Playground Builder %d", i),
		})
	}

	component.RunContenderIfEnabled(ctx)
	return component
}

func (l *L1MultiBuilderRecipe) Output(manifest *Manifest) map[string]interface{} {
	return map[string]interface{}{}
}
