package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/flashbots/mev-boost-relay/beaconclient"
	mevRCommon "github.com/flashbots/mev-boost-relay/common"
	"golang.org/x/mod/semver"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	ecrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/ferranbt/builder-playground/artifacts"
	clproxy "github.com/ferranbt/builder-playground/cl-proxy"
	mevboostrelay "github.com/ferranbt/builder-playground/mev-boost-relay"

	"github.com/hashicorp/go-uuid"
	"github.com/prysmaticlabs/prysm/v5/config/params"
	"github.com/prysmaticlabs/prysm/v5/crypto/bls/common"
	"github.com/prysmaticlabs/prysm/v5/runtime/interop"
	"github.com/prysmaticlabs/prysm/v5/runtime/version"
	"github.com/spf13/cobra"
	keystorev4 "github.com/wealdtech/go-eth2-wallet-encryptor-keystorev4"
	"gopkg.in/yaml.v2"
)

//go:embed utils/rollup.json
var opRollupConfig []byte

//go:embed utils/genesis.json
var opGenesis []byte

//go:embed utils/state.json
var opState []byte

//go:embed config.yaml.tmpl
var clConfigContent []byte

var defaultJWTToken = "04592280e1778419b7aa954d43871cb2cfb2ebda754fb735e8adeb293a88f9bf"

var (
	defaultRethDiscoveryPrivKey    = "a11ac89899cd86e36b6fb881ec1255b8a92a688790b7d950f8b7d8dd626671fb"
	defaultRethDiscoveryPrivKeyLoc = "/tmp/tmp-reth-disc.txt"
)

var outputFlag string
var continueFlag bool
var useBinPathFlag bool
var validateFlag bool
var genesisDelayFlag uint64
var latestForkFlag bool
var useRethForValidation bool
var secondaryBuilderPort uint64

var rootCmd = &cobra.Command{
	Use:   "playground",
	Short: "",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runIt()
	},
}

var downloadArtifactsCmd = &cobra.Command{
	Use:   "download-artifacts",
	Short: "Download the artifacts",
	Long:  `Download the artifacts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		bins, err := artifacts.DownloadArtifacts()
		if err != nil {
			return err
		}

		if validateFlag {
			for _, path := range bins {
				// make sure you can run the binary
				// In this case, both reth and lighthouse have the --version flag
				cmd := exec.Command(path, "--version")
				if err := cmd.Run(); err != nil {
					return fmt.Errorf("error running %s: %v", path, err)
				}
			}
		}
		return err
	},
}

var numBlocksValidate uint64
var validatePayloads bool

var watchCmd = &cobra.Command{
	Use:  "watch-payloads",
	Long: `Watch the payload attribute events`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Test that blocks are being produced
		log := mevRCommon.LogSetup(false, "info")
		clt := beaconclient.NewProdBeaconInstance(log, "http://localhost:3500", "http://localhost:3500")

		// Subscribe to head events right away even if the connection has not been established yet
		// That is handled internally in the function already.
		// Otherwise, if we connect only when the first head slot happens we might miss some initial slots.
		ch := make(chan beaconclient.PayloadAttributesEvent)
		go clt.SubscribeToPayloadAttributesEvents(ch)

		{
			// If the chain has not started yet, wait for it to start.
			// Otherwise, the subscription will not return any data.
			bClient := beaconclient.NewMultiBeaconClient(log, []beaconclient.IBeaconInstance{
				clt,
			})

			isReady := func() bool {
				sync, err := bClient.BestSyncStatus()
				if err != nil {
					return false
				}
				return sync.HeadSlot >= 1
			}

			if !isReady() {
				syncTimeoutCh := time.After(30 * time.Second)
				for {
					if isReady() {
						break
					}
					select {
					case <-syncTimeoutCh:
						return fmt.Errorf("beacon client failed to start")
					default:
						time.Sleep(1 * time.Second)
					}
				}
			}
		}

		log.Infof("Chain is alive. Subscribing to head events")

		var lastSlot uint64
		for {
			select {
			case head := <-ch:
				log.Infof("Slot: %d Parent block number: %d", head.Data.ProposalSlot, head.Data.ParentBlockNumber)

				if validatePayloads {
					// If we are being notified of a new slot, validate that the slots are contiguous
					// Note that lighthouse might send multiple updates for the same slot.
					if lastSlot != 0 && lastSlot != head.Data.ProposalSlot && lastSlot+1 != head.Data.ProposalSlot {
						return fmt.Errorf("slot mismatch, expected %d, got %d", lastSlot+1, head.Data.ProposalSlot)
					}
					// if the network did not miss any initial slots, lighthouse will send payload attribute updates
					// of the form: (slot = slot, parent block number = slot - 2), (slot, slot - 1).
					// The -2 is in case we want to handle reorgs in the chain.
					// We need to validate that at least the difference between the parent block number and the slot is 2.
					if head.Data.ProposalSlot-head.Data.ParentBlockNumber > 2 {
						return fmt.Errorf("parent block too big %d", head.Data.ParentBlockNumber)
					}

					if lastSlot != head.Data.ProposalSlot {
						numBlocksValidate--
						if numBlocksValidate == 0 {
							return nil
						}
					}
				}

				lastSlot = head.Data.ProposalSlot
			case <-time.After(20 * time.Second):
				return fmt.Errorf("timeout waiting for block")
			}
		}
	},
}

// minimumGenesisDelay is the minimum delay for the genesis time. This is required
// because lighthouse takes some time to start and we need to make sure it is ready
// otherwise, some blocks are missed.
var minimumGenesisDelay uint64 = 10

func main() {
	rootCmd.Flags().StringVar(&outputFlag, "output", "", "")
	rootCmd.Flags().BoolVar(&continueFlag, "continue", false, "")
	rootCmd.Flags().BoolVar(&useBinPathFlag, "use-bin-path", false, "")
	rootCmd.Flags().Uint64Var(&genesisDelayFlag, "genesis-delay", minimumGenesisDelay, "")
	rootCmd.Flags().BoolVar(&latestForkFlag, "electra", false, "")
	rootCmd.Flags().BoolVar(&useRethForValidation, "use-reth-for-validation", false, "enable flashbots_validateBuilderSubmissionV* on reth and use them for validation")
	rootCmd.Flags().Uint64Var(&secondaryBuilderPort, "secondary", 1234, "port to use for the secondary builder")

	downloadArtifactsCmd.Flags().BoolVar(&validateFlag, "validate", false, "")
	watchCmd.Flags().Uint64Var(&numBlocksValidate, "validate-num-blocks", 5, "")
	watchCmd.Flags().BoolVar(&validatePayloads, "validate-payloads", false, "")

	rootCmd.AddCommand(downloadArtifactsCmd)
	rootCmd.AddCommand(watchCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runIt() error {
	if genesisDelayFlag < minimumGenesisDelay {
		return fmt.Errorf("genesis delay must be at least %d", minimumGenesisDelay)
	}

	if outputFlag == "" {
		// Use the $HOMEDIR/devnet as the default output
		homeDir, err := getHomeDir()
		if err != nil {
			return err
		}
		outputFlag = filepath.Join(homeDir, "devnet")
	}

	fmt.Printf("Output directory: %s\n", outputFlag)
	out := &output{dst: outputFlag}

	exists := out.Exists("data_reth")
	if exists {
		if continueFlag {
			fmt.Println("Artifacts already exist, continuing...")
		} else {
			fmt.Println("Artifacts already exist, resetting them...")

			// Remove the current artifacts and create new ones
			if err := out.Remove(""); err != nil {
				return err
			}
			if err := setupArtifacts(); err != nil {
				return err
			}
		}
	} else {
		// artifacts do not exist yet, create them
		if err := setupArtifacts(); err != nil {
			return err
		}
	}

	svcManager := newServiceManager(out)
	if err := setupServices(svcManager, out); err != nil {
		// close all services if there was an error
		svcManager.StopAndWait()
		return err
	}

	dockerRunner := NewDockerRunner(out, svcManager)
	if err := dockerRunner.Run(); err != nil {
		return err
	}

	go watchProposerPayloads()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	select {
	case <-sig:
		fmt.Println("Stopping...")
	case <-svcManager.NotifyErrCh():
	}

	dockerRunner.Stop()
	svcManager.StopAndWait()

	return nil
}

func setupArtifacts() error {
	out := &output{dst: outputFlag}

	// enable the latest fork in config.yaml or not
	var latestForkEpoch string
	if latestForkFlag {
		latestForkEpoch = "0"
	} else {
		latestForkEpoch = "18446744073709551615"
	}
	clConfigContentStr := strings.Replace(string(clConfigContent), "{{.LatestForkEpoch}}", latestForkEpoch, 1)

	// load the config.yaml file
	clConfig, err := params.UnmarshalConfig([]byte(clConfigContentStr), nil)
	if err != nil {
		return err
	}
	if err := params.SetActive(clConfig); err != nil {
		return err
	}

	genesisTime := uint64(time.Now().Add(time.Duration(genesisDelayFlag) * time.Second).Unix())
	config := params.BeaconConfig()

	gen := interop.GethTestnetGenesis(genesisTime, config)
	// HACK: fix this in prysm?
	gen.Config.DepositContractAddress = gethcommon.HexToAddress(config.DepositContractAddress)

	// add pre-funded accounts
	prefundedBalance, _ := new(big.Int).SetString("10000000000000000000000", 16)

	for _, privStr := range prefundedAccounts {
		priv, err := getPrivKey(privStr)
		if err != nil {
			return err
		}
		addr := ecrypto.PubkeyToAddress(priv.PublicKey)
		gen.Alloc[addr] = types.Account{
			Balance: prefundedBalance,
			Nonce:   1,
		}
	}

	// Apply Optimism pre-state
	{
		var state struct {
			L1StateDump string `json:"l1StateDump"`
		}
		if err := json.Unmarshal(opState, &state); err != nil {
			log.Fatal(err)
		}

		decoded, err := base64.StdEncoding.DecodeString(state.L1StateDump)
		if err != nil {
			log.Fatal(err)
		}

		// Create gzip reader from the base64 decoded data
		gr, err := gzip.NewReader(bytes.NewReader(decoded))
		if err != nil {
			log.Fatal(err)
		}
		defer gr.Close()

		// Read and decode the contents
		contents, err := io.ReadAll(gr)
		if err != nil {
			log.Fatal(err)
		}

		var alloc types.GenesisAlloc
		if err := json.Unmarshal(contents, &alloc); err != nil {
			log.Fatal(err)
		}

		for addr, account := range alloc {
			fmt.Printf("Address: %s, Balance: %s\n", addr.Hex(), account.Balance.String())
			gen.Alloc[addr] = account
		}
	}

	block := gen.ToBlock()
	header, _ := json.MarshalIndent(block.Header(), "", "  ")
	log.Printf("Genesis block hash: %s json: %s", block.Hash(), header)

	var v int
	if latestForkFlag {
		v = version.Electra
	} else {
		v = version.Deneb
	}

	priv, pub, err := interop.DeterministicallyGenerateKeys(0, 100)
	if err != nil {
		return err
	}

	depositData, roots, err := interop.DepositDataFromKeysWithExecCreds(priv, pub, 100)
	if err != nil {
		return err
	}

	opts := make([]interop.PremineGenesisOpt, 0)
	opts = append(opts, interop.WithDepositData(depositData, roots))

	state, err := interop.NewPreminedGenesis(context.Background(), genesisTime, 0, 100, v, block, opts...)
	if err != nil {
		return err
	}

	err = out.WriteBatch(map[string]interface{}{
		"testnet/config.yaml":                 func() ([]byte, error) { return convert(config) },
		"testnet/genesis.ssz":                 state,
		"genesis.json":                        gen,
		"jwtsecret":                           defaultJWTToken,
		"testnet/boot_enr.yaml":               "[]",
		"testnet/deploy_block.txt":            "0",
		"testnet/deposit_contract_block.txt":  "0",
		"testnet/genesis_validators_root.txt": hex.EncodeToString(state.GenesisValidatorsRoot()),
		"data_validator/":                     &lighthouseKeystore{privKeys: priv},
	})
	if err != nil {
		return err
	}

	{
		opTimestamp := genesisTime + 2

		// override l2 genesis, make the timestamp start 2 seconds after the L1 genesis
		newOpGenesis, err := overrideJSON(opGenesis, map[string]interface{}{
			"timestamp": hexutil.Uint64(opTimestamp).String(),
		})
		if err != nil {
			return err
		}

		// the hash of the genesis has changed beause of the timestamp so we need to account for that
		var opGenesisObj core.Genesis
		if err := json.Unmarshal(newOpGenesis, &opGenesisObj); err != nil {
			panic(err)
		}

		opGenesisHash := opGenesisObj.ToBlock().Hash()

		// override rollup.json with the real values for the L1 chain and the correct timestamp
		newOpRollup, err := overrideJSON(opRollupConfig, map[string]interface{}{
			"genesis": map[string]interface{}{
				"l2_time": opTimestamp, // this one not in hex
				"l1": map[string]interface{}{
					"hash":   block.Hash().String(),
					"number": 0,
				},
				"l2": map[string]interface{}{
					"hash":   opGenesisHash.String(),
					"number": 0,
				},
			},
			"chain_op_config": map[string]interface{}{ // TODO: Read this from somewhere (genesis??)
				"eip1559Elasticity":        6,
				"eip1559Denominator":       50,
				"eip1559DenominatorCanyon": 250,
			},
		})
		if err != nil {
			return err
		}

		if err := out.WriteFile("l2-genesis.json", newOpGenesis); err != nil {
			return err
		}
		if err := out.WriteFile("rollup.json", newOpRollup); err != nil {
			return err
		}
	}

	return nil
}

func overrideJSON(jsonData []byte, overrides map[string]interface{}) ([]byte, error) {
	// Parse original JSON into a map
	var original map[string]interface{}
	if err := json.Unmarshal(jsonData, &original); err != nil {
		return nil, fmt.Errorf("failed to unmarshal original JSON: %w", err)
	}

	// Recursively merge the overrides into the original
	mergeMap(original, overrides)

	// Marshal back to JSON
	result, err := json.Marshal(original)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal modified JSON: %w", err)
	}

	return result, nil
}

// mergeMap recursively merges src into dst
func mergeMap(dst, src map[string]interface{}) {
	for key, srcVal := range src {
		if dstVal, exists := dst[key]; exists {
			// If both values are maps, merge them recursively
			if dstMap, ok := dstVal.(map[string]interface{}); ok {
				if srcMap, ok := srcVal.(map[string]interface{}); ok {
					mergeMap(dstMap, srcMap)
					continue
				}
			}
		}
		// For all other cases, override the value
		dst[key] = srcVal
	}
}

func getPrivKey(privStr string) (*ecdsa.PrivateKey, error) {
	privBuf, err := hex.DecodeString(strings.TrimPrefix(privStr, "0x"))
	if err != nil {
		return nil, err
	}

	priv, err := ecrypto.ToECDSA(privBuf)
	if err != nil {
		return nil, err
	}
	return priv, nil
}

func setupServices(svcManager *serviceManager, out *output) error {
	/*
		var (
			rethBin, lighthouseBin string
		)

		if useBinPathFlag {
			fmt.Println("Using binaries from the PATH")

			rethBin = "reth"
			lighthouseBin = "lighthouse"
		} else {
			binArtifacts, err := artifacts.DownloadArtifacts()
			if err != nil {
				return err
			}

			rethBin = binArtifacts["reth"]
			lighthouseBin = binArtifacts["lighthouse"]
		}
	*/

	// log the prefunded accounts
	fmt.Printf("\nPrefunded accounts:\n==================\n")
	for indx, acc := range prefundedAccounts {
		priv, _ := getPrivKey(acc)
		fmt.Printf("(%d) %s (%s)\n", indx, acc, ecrypto.PubkeyToAddress(priv.PublicKey).Hex())
	}
	fmt.Println("")
	if err := os.WriteFile(defaultRethDiscoveryPrivKeyLoc, []byte(defaultRethDiscoveryPrivKey), 0644); err != nil {
		return err
	}

	svcManager.AddService(&RethEL{})
	svcManager.AddService(&LighthouseBeaconNode{})
	svcManager.AddService(&LighthouseValidator{})
	svcManager.AddService(&OpNode{})
	svcManager.AddService(&OpGeth{})
	svcManager.AddService(&OpBatcher{})

	// svcManager.AddNativeService(&MevBoostRelay{})
	// svcManager.AddNativeService(&ClProxy{})

	// start all the services
	svcManager.Start(true)

	/*
		svcManager.GenerateDockerCompose("docker-compose.yaml", map[string]string{
			"playground": "true",
		})

		// print services info
		fmt.Printf("Services started:\n==================\n")
		for _, ss := range svcManager.services {
			sort.Slice(ss.ports, func(i, j int) bool {
				return ss.ports[i].name < ss.ports[j].name
			})

			ports := []string{}
			for _, p := range ss.ports {
				ports = append(ports, fmt.Sprintf("%s: %d", p.name, p.port))
			}
			fmt.Printf("- %s (%s)\n", ss.name, strings.Join(ports, ", "))
		}
		fmt.Printf("\n")

		fmt.Printf("All services started, press Ctrl+C to stop\n")
	*/

	return nil
}

// Add this new function to generate docker-compose.yaml
func (s *serviceManager) GenerateDockerCompose(outputPath string, labels map[string]string) ([]byte, error) {
	compose := map[string]interface{}{
		"version":  "3.8",
		"services": map[string]interface{}{},
		// Add networks configuration
		"networks": map[string]interface{}{
			"ethereum": map[string]interface{}{
				"name": "ethereum",
			},
		},
	}

	services := compose["services"].(map[string]interface{})

	for _, svc := range s.services {
		if svc.srvMng != nil { // Only include services that were created with NewService
			services[svc.name] = svc.ToDockerComposeService(labels)
		}
	}

	yamlData, err := yaml.Marshal(compose)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal docker-compose: %w", err)
	}

	return yamlData, nil
}

func (s *service) ToDockerComposeService(labels map[string]string) map[string]interface{} {
	service := map[string]interface{}{
		"image":   fmt.Sprintf("%s:%s", s.imageReal, s.tag),
		"command": s.args,
		// Add volume mount for the output directory
		"volumes": []string{
			fmt.Sprintf("./:/output"),
		},
		// Add the ethereum network
		"networks": []string{"ethereum"},
		"labels":   labels,
	}

	if s.entrypoint != "" {
		service["entrypoint"] = s.entrypoint
	}

	if len(s.ports) > 0 {
		ports := []string{}
		for _, p := range s.ports {
			ports = append(ports, fmt.Sprintf("%d:%d", p.port, p.port))
		}
		service["ports"] = ports
	}

	return service
}

type OpBatcher struct{}

func (o *OpBatcher) Artifacts() []artifacts.Release {
	return []artifacts.Release{}
}

func (o *OpBatcher) Run(svcManager *serviceManager) {
	svcManager.
		NewService("op-batcher").
		WithImage("op-batcher").
		WithImageReal("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-batcher").
		WithTag("v1.11.1").
		WithArgs(
			"op-batcher",
			"--l1-eth-rpc", "http://reth:8545",
			"--l2-eth-rpc", "http://op-geth:8547",
			"--rollup-rpc", "http://op-node:8549",
			"--max-channel-duration=2",
			"--sub-safety-margin=4",
			"--poll-interval=1s",
			"--num-confirmations=1",
			"--private-key=0x2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6",
		).
		Build()
}

type OpNode struct {
}

func (o *OpNode) Artifacts() []artifacts.Release {
	return []artifacts.Release{}
}

func (o *OpNode) Run(svcManager *serviceManager) {
	svcManager.
		NewService("op-node").
		WithImage("op-node").
		WithImageReal("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-node").
		WithTag("v1.11.0").
		WithArgs(
			"op-node",
			"--l1", "http://reth:8545",
			"--l1.beacon", "http://beacon_node:3500",
			"--l1.epoch-poll-interval", "12s",
			"--l1.http-poll-interval", "6s",
			"--l2", "http://op-geth:8552",
			"--l2.jwt-secret", "{{.Dir}}/jwt-secret.txt",
			"--sequencer.enabled",
			"--sequencer.l1-confs", "0",
			"--verifier.l1-confs", "0",
			"--p2p.sequencer.key", "8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba",
			"--rollup.config", "{{.Dir}}/rollup.json",
			"--rpc.addr", "0.0.0.0",
			"--rpc.port", "8549",
			"--p2p.listen.ip", "0.0.0.0",
			"--p2p.listen.tcp", "9003",
			"--p2p.listen.udp", "9003",
			"--p2p.scoring.peers", "light",
			"--p2p.ban.peers", "true",
			// "--p2p.priv.path", "{{.Dir}}/p2p-node-key.txt",
			"--metrics.enabled",
			"--metrics.addr", "0.0.0.0",
			"--metrics.port", "7300",
			"--pprof.enabled",
			"--rpc.enable-admin",
			"--safedb.path", "{{.Dir}}/db",
		).
		WithPort("rpc", 8549).
		WithPort("p2p", 9003).
		WithPort("metrics", 7300).
		Build()
}

type OpGeth struct {
}

func (o *OpGeth) Artifacts() []artifacts.Release {
	return []artifacts.Release{}
}

func (o *OpGeth) Run(svcManager *serviceManager) {
	svcManager.
		NewService("op-geth").
		WithImage("geth").
		WithImageReal("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-geth").
		WithTag("v1.101500.0").
		WithEntrypoint("/bin/sh").
		WithArgs(
			"-c",
			"geth init --datadir {{.Dir}}/op-reth --state.scheme hash {{.Dir}}/l2-genesis.json && "+
				"exec geth "+
				"--datadir {{.Dir}}/op-reth "+
				"--verbosity 3 "+
				"--http "+
				"--http.corsdomain \"*\" "+
				"--http.vhosts \"*\" "+
				"--http.addr 0.0.0.0 "+
				"--http.port 8547 "+
				"--http.api web3,debug,eth,txpool,net,engine,miner "+
				"--ws "+
				"--ws.addr 0.0.0.0 "+
				"--ws.port 8548 "+
				"--ws.origins \"*\" "+
				"--ws.api debug,eth,txpool,net,engine,miner "+
				"--syncmode full "+
				"--nodiscover "+
				"--maxpeers 0 "+
				"--rpc.allow-unprotected-txs "+
				"--authrpc.addr 0.0.0.0 "+
				"--authrpc.port 8552 "+
				"--authrpc.vhosts \"*\" "+
				"--authrpc.jwtsecret {{.Dir}}/jwt-secret.txt "+
				"--gcmode archive "+
				"--state.scheme hash "+
				"--metrics "+
				"--metrics.addr 0.0.0.0 "+
				"--metrics.port 6061",
		).
		WithPort("http", 8547).
		WithPort("ws", 8548).
		WithPort("authrpc", 8552).
		WithPort("metrics", 6061).
		Build()
}

type RethEL struct {
}

func (r *RethEL) Artifacts() []artifacts.Release {
	return []artifacts.Release{
		{
			Name:    "reth",
			Org:     "paradigmxyz",
			Version: "v1.2.0",
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
		},
	}
}

func (r *RethEL) Run(svcManager *serviceManager) {
	// start the reth el client
	svcManager.
		NewService("reth").
		WithImage("reth").
		WithImageReal("ghcr.io/paradigmxyz/reth").
		WithTag("v1.2.0").
		WithArgs(
			"node",
			"--chain", "{{.Dir}}/genesis.json",
			"--datadir", "{{.Dir}}/data_reth",
			"--color", "never",
			"--ipcpath", "{{.Dir}}/reth.ipc",
			// p2p config. Use a default discovery key and disable public discovery and connections
			"--p2p-secret-key", defaultRethDiscoveryPrivKeyLoc,
			"--addr", "127.0.0.1",
			"--port", "30303",
			// "--disable-discovery",
			// http config
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.api", "admin,eth,net,web3",
			"--http.port", "8545",
			"--authrpc.port", "8551",
			"--authrpc.addr", "0.0.0.0",
			"--authrpc.jwtsecret", "{{.Dir}}/jwtsecret",
			// For reth version 1.2.0 the "legacy" engine was removed, so we now require these arguments:
			"--engine.persistence-threshold", "0", "--engine.memory-block-buffer-target", "0",
			"-vvvv",
		).
		If(useRethForValidation, func(s *service) *service {
			return s.WithReplacementArgs("--http.api", "admin,eth,web3,net,rpc,flashbots")
		}).
		WithPort("rpc", 30303).
		WithPort("http", 8545).
		WithPort("authrpc", 8551).
		Build()
}

type LighthouseBeaconNode struct {
}

func (l *LighthouseBeaconNode) Artifacts() []artifacts.Release {
	return []artifacts.Release{
		{
			Name:    "lighthouse",
			Org:     "sigp",
			Version: "v7.0.0-beta.0",
			Arch: func(goos, goarch string) string {
				if goos == "linux" {
					return "x86_64-unknown-linux-gnu"
				} else if goos == "darwin" && goarch == "arm64" { // Apple M1
					return "x86_64-apple-darwin"
				} else if goos == "darwin" && goarch == "amd64" {
					return "x86_64-apple-darwin"
				}
				return ""
			},
		},
	}
}

func (l *LighthouseBeaconNode) Run(svcManager *serviceManager) {
	/*
		// TODO: Figure out how to do this
			lightHouseVersion := func() string {
				cmd := exec.Command(lighthouseBin, "--version")
				out, err := cmd.Output()
				if err != nil {
					return "unknown"
				}
				// find the line of the form:
				// Lighthouse v5.2.1-9e12c21
				for _, line := range strings.Split(string(out), "\n") {
					if strings.HasPrefix(line, "Lighthouse ") {
						v := strings.TrimSpace(strings.TrimPrefix(line, "Lighthouse "))
						if !strings.HasPrefix(v, "v") {
							v = "v" + v
						}
						// Go semver considers - as a pre-release, so we need to remove it
						v = strings.Split(v, "-")[0]
						return semver.Canonical(v)
					}
				}
				return "unknown"
			}()

			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				cmd := exec.Command("file", lighthouseBin)
				out, _ := cmd.Output()
				if strings.Contains(string(out), "x86_64") {
					fmt.Println("WARNING: ", lighthouseBin, "is an x86_64 binary, using a self-compiled verison with `--use-bin-path` is recommended.")
				}
			}
	*/

	lightHouseVersion := "v5.3"

	// start the beacon node
	svcManager.
		NewService("beacon_node").
		WithImage("lighthouse").
		WithImageReal("sigp/lighthouse").
		WithTag("v7.0.0-beta.0").
		WithArgs(
			"lighthouse",
			"bn",
			"--datadir", "{{.Dir}}/data_beacon_node",
			"--testnet-dir", "{{.Dir}}/testnet",
			"--enable-private-discovery",
			"--disable-peer-scoring",
			"--staking",
			"--enr-address", "127.0.0.1",
			"--enr-udp-port", "9000",
			"--enr-tcp-port", "9000",
			"--enr-quic-port", "9100",
			"--port", "9000",
			"--quic-port", "9100",
			"--http",
			"--http-port", "3500",
			"--http-address", "0.0.0.0",
			"--http-allow-origin", "*",
			"--disable-packet-filter",
			"--target-peers", "0",
			"--execution-endpoint", "http://reth:8551",
			"--execution-jwt", "{{.Dir}}/jwtsecret",
			"--builder", "http://localhost:5555",
			"--builder-fallback-epochs-since-finalization", "0",
			"--builder-fallback-disable-checks",
			"--always-prepare-payload",
			"--prepare-payload-lookahead", "8000",
		).
		If(
			semver.Compare(lightHouseVersion, "v5.3") < 0,
			func(s *service) *service {
				// For versions <= v5.2.1, we want to run with --http-allow-sync-stalled
				// However this flag is not available in newer versions
				return s.WithArgs("--http-allow-sync-stalled")
			},
		).
		If(
			semver.Compare(lightHouseVersion, "v5.3") >= 0,
			func(s *service) *service {
				// For versions >= v5.3.0, ----suggested-fee-recipient is apparently now required for non-validator nodes as well
				return s.WithArgs("--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990")
			},
		).
		WithPort("http", 3500).
		Build()
}

type LighthouseValidator struct {
}

func (l *LighthouseValidator) Artifacts() []artifacts.Release {
	return []artifacts.Release{
		{
			Name:    "lighthouse",
			Org:     "sigp",
			Version: "v7.0.0-beta.0",
			Arch: func(goos, goarch string) string {
				if goos == "linux" {
					return "x86_64-unknown-linux-gnu"
				} else if goos == "darwin" && goarch == "arm64" { // Apple M1
					return "x86_64-apple-darwin"
				} else if goos == "darwin" && goarch == "amd64" {
					return "x86_64-apple-darwin"
				}
				return ""
			},
		},
	}
}

func (l *LighthouseValidator) Run(svcManager *serviceManager) {
	// start validator client
	svcManager.
		NewService("validator").
		WithImage("lighthouse").
		WithImageReal("sigp/lighthouse").
		WithTag("v7.0.0-beta.0").
		WithArgs(
			"lighthouse",
			"vc",
			"--datadir", "{{.Dir}}/data_validator",
			"--testnet-dir", "{{.Dir}}/testnet",
			"--init-slashing-protection",
			"--beacon-nodes", "http://beacon_node:3500",
			"--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990",
			"--builder-proposals",
			"--prefer-builder-proposals",
		).Build()
}

type ClProxy struct {
}

func (c *ClProxy) Service() *service {
	return &service{
		name: "cl-proxy",
		ports: []*port{
			{name: "jsonrpc", port: 5656},
		},
	}
}

func (c *ClProxy) Run(out *output, ctx context.Context) error {
	// Start the cl proxy
	cfg := clproxy.DefaultConfig()
	cfg.Primary = "http://localhost:8551"

	if secondaryBuilderPort != 0 {
		cfg.Secondary = fmt.Sprintf("http://localhost:%d", secondaryBuilderPort)
	}

	var err error
	if cfg.LogOutput, err = out.LogOutput("cl-proxy"); err != nil {
		return err
	}
	clproxy, err := clproxy.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create cl proxy: %w", err)
	}
	return clproxy.Run()
}

type MevBoostRelay struct {
}

func (m *MevBoostRelay) Service() *service {
	return &service{
		name: "mev-boost-relay",
		ports: []*port{
			{name: "http", port: 5555},
		},
	}
}

func (m *MevBoostRelay) Run(out *output, ctx context.Context) error {
	cfg := mevboostrelay.DefaultConfig()
	var err error
	if cfg.LogOutput, err = out.LogOutput("mev-boost-relay"); err != nil {
		return err
	}
	cfg.UseRethForValidation = useRethForValidation
	relay, err := mevboostrelay.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create relay: %w", err)
	}

	return relay.Start()
}

type output struct {
	dst string
}

func (o *output) Exists(path string) bool {
	_, err := os.Stat(filepath.Join(o.dst))
	return err == nil
}

func (o *output) Remove(path string) error {
	return os.RemoveAll(filepath.Join(o.dst, path))
}

func (o *output) CopyFile(src string, dst string) error {
	// Open the source file
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	// Create the destination directory if it doesn't exist
	dstPath := filepath.Join(o.dst, dst)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Create the destination file
	destFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	// Copy the contents
	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	// Copy file permissions from source to destination
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to get source file info: %w", err)
	}

	if err := os.Chmod(dstPath, sourceInfo.Mode()); err != nil {
		return fmt.Errorf("failed to set destination file permissions: %w", err)
	}

	return nil
}

func (o *output) WriteBatch(data map[string]interface{}) error {
	for dst, data := range data {
		if err := o.WriteFile(dst, data); err != nil {
			return err
		}
	}
	return nil
}

func (o *output) LogOutput(name string) (*os.File, error) {
	path := filepath.Join(o.dst, "logs", name+".log")

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	logOutput, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return logOutput, nil
}

func (o *output) WriteFile(dst string, data interface{}) error {
	dst = filepath.Join(o.dst, dst)

	var dataRaw []byte
	var err error

	if raw, ok := data.([]byte); ok {
		dataRaw = raw
	} else if raw, ok := data.(string); ok {
		dataRaw = []byte(raw)
	} else if ssz, ok := data.(sszObject); ok {
		if dataRaw, err = ssz.MarshalSSZ(); err != nil {
			return err
		}
	} else if encObj, ok := data.(encObject); ok {
		// create a new output for this sub-object and delegate the full encoding to it
		if err = encObj.Encode(&output{dst: dst}); err != nil {
			return err
		}
		return nil
	} else if encFn, ok := data.(func() ([]byte, error)); ok {
		if dataRaw, err = encFn(); err != nil {
			return err
		}
	} else {
		// figure out how to decode the object given the file extension
		ext := filepath.Ext(dst)
		if ext == ".json" {
			if dataRaw, err = json.MarshalIndent(data, "", "\t"); err != nil {
				return err
			}
		} else if ext == ".yaml" {
			if dataRaw, err = yaml.Marshal(data); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("unsupported file extension: %s", ext)
		}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, dataRaw, 0644); err != nil {
		return err
	}
	return nil
}

var secret = "secret"

type lighthouseKeystore struct {
	privKeys []common.SecretKey
}

func (l *lighthouseKeystore) Encode(o *output) error {
	for _, key := range l.privKeys {
		encryptor := keystorev4.New()
		cryptoFields, err := encryptor.Encrypt(key.Marshal(), secret)
		if err != nil {
			return err
		}

		id, _ := uuid.GenerateUUID()

		pubKeyHex := "0x" + hex.EncodeToString(key.PublicKey().Marshal())
		item := map[string]interface{}{
			"crypto":      cryptoFields,
			"uuid":        id,
			"pubkey":      pubKeyHex[2:], // without 0x in the json file
			"version":     4,
			"description": "",
		}
		valJSON, err := json.MarshalIndent(item, "", "\t")
		if err != nil {
			return err
		}

		if err := o.WriteBatch(map[string]interface{}{
			"validators/" + pubKeyHex + "/voting-keystore.json": valJSON,
			"secrets/" + pubKeyHex:                              secret,
		}); err != nil {
			return err
		}
	}

	return nil
}

type encObject interface {
	Encode(o *output) error
}

type sszObject interface {
	MarshalSSZ() ([]byte, error)
}

type serviceManager struct {
	// list of services to start
	services []*service

	// list of available artifacts to download
	artifacts []artifacts.Release

	out     *output
	handles []*handle

	stopping atomic.Bool

	wg sync.WaitGroup

	// channel for the handles to nofify when they are shutting down
	closeCh chan struct{}
}

func newServiceManager(out *output) *serviceManager {
	return &serviceManager{out: out, handles: []*handle{}, stopping: atomic.Bool{}, wg: sync.WaitGroup{}, closeCh: make(chan struct{}, 5), artifacts: []artifacts.Release{}}
}

func (s *serviceManager) emitError() {
	select {
	case s.closeCh <- struct{}{}:
	default:
	}
}

func (s *serviceManager) Build(ss *service) {
	s.services = append(s.services, ss)
}

type Service interface {
	Run(svcManager *serviceManager)
	Artifacts() []artifacts.Release
}

type NativeService interface {
	Run(out *output, ctx context.Context) error
	Service() *service
}

func (s *serviceManager) AddService(srv Service) {
	// add the artifacts to the list of available artifacts
	s.artifacts = append(s.artifacts, srv.Artifacts()...)

	srv.Run(s)
}

func (s *serviceManager) AddNativeService(srv NativeService) {
	s.services = append(s.services, srv.Service())

	go func() {
		if err := srv.Run(s.out, context.Background()); err != nil {
			s.emitError()
		}
	}()
}

func (s *serviceManager) downloadArtifact(ss *service) error {
	if ss.srvMng == nil {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Define the path for our custom home directory
	customHomeDir := filepath.Join(homeDir, ".playground")

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(customHomeDir, 0755); err != nil {
		return err
	}

	for _, a := range s.artifacts {
		if a.Name == ss.image {
			a.Version = ss.tag
			binPath, err := artifacts.DownloadRelease(customHomeDir, a)
			if err != nil {
				panic(err)
			}
			ss.imagePath = binPath
			return nil
		}
	}
	return fmt.Errorf("artifact not found: %s", ss.image)
}

func (s *serviceManager) Start(dryRun bool) {
	// first, try to download all the artifacts
	/*
		for _, ss := range s.services {
			if err := s.downloadArtifact(ss); err != nil {
				panic(err)
			}
		}
	*/

	if dryRun {
		return
	}

	// now, run the services
	for _, ss := range s.services {
		s.runService(ss)
	}
}

func (s *serviceManager) runService(ss *service) {
	if ss.srvMng == nil {
		// this one was not created with Build so it is not a binary service, but a native one
		return
	}

	fmt.Println("Running", ss.imagePath, ss.args)
	cmd := exec.Command(ss.imagePath, ss.args...)

	logOutput, err := s.out.LogOutput(ss.name)
	if err != nil {
		// this should not happen, log it
		fmt.Println("Error creating log output for", ss.name)
		logOutput = os.Stdout
	}

	// first thing to output is the command itself
	fmt.Fprint(logOutput, strings.Join(ss.args, " ")+"\n\n")

	cmd.Stdout = logOutput
	cmd.Stderr = logOutput

	s.wg.Add(1)
	go func() {
		if err := cmd.Run(); err != nil {
			if !s.stopping.Load() {
				fmt.Printf("Error running %s: %v\n", ss.name, err)
			}
		}
		s.wg.Done()
		s.emitError()
	}()

	s.handles = append(s.handles, &handle{
		Process: cmd,
		Service: ss,
	})
}

type handle struct {
	Process *exec.Cmd
	Service *service
}

func (s *serviceManager) NotifyErrCh() <-chan struct{} {
	return s.closeCh
}

func (s *serviceManager) StopAndWait() {
	s.stopping.Store(true)

	for _, h := range s.handles {
		if h.Process != nil {
			fmt.Printf("Stopping %s\n", h.Service.name)
			h.Process.Process.Kill()
		}
	}
	s.wg.Wait()
}

type port struct {
	name string
	port int
}

type service struct {
	name string
	args []string

	ports  []*port
	srvMng *serviceManager

	// release specific configuration
	// we call this image here but it can also represent a release binary
	image      string
	imagePath  string
	tag        string
	imageReal  string
	entrypoint string
}

func (s *serviceManager) NewService(name string) *service {
	return &service{name: name, args: []string{}, srvMng: s}
}

func (s *service) WithImageReal(image string) *service {
	s.imageReal = image
	return s
}

func (s *service) WithImage(image string) *service {
	s.image = image
	return s
}

func (s *service) WithEntrypoint(entrypoint string) *service {
	s.entrypoint = entrypoint
	return s
}

func (s *service) WithTag(tag string) *service {
	s.tag = tag
	return s
}

func (s *service) WithPort(name string, portNumber int) *service {
	s.ports = append(s.ports, &port{name: name, port: portNumber})
	return s
}

func (s *service) WithArgs(args ...string) *service {
	// use template substitution to load constants
	tmplVars := s.tmplVars()
	for i, arg := range args {
		args[i] = applyTemplate(arg, tmplVars)
	}

	s.args = append(s.args, args...)
	return s
}

func (s *service) tmplVars() map[string]interface{} {
	tmplVars := map[string]interface{}{
		"Dir": s.srvMng.out.dst,
	}
	return tmplVars
}

// WithReplacementArgs finds the first occurrence of the first argument in the current arguments,
// and replaces it and len(args) - 1 more arguments with the new arguments.
//
// For example:
//
// s.WithArgs("a", "b", "c").WithReplacementArgs("b", "d") will result in ["a", "b", "d"]
func (s *service) WithReplacementArgs(args ...string) *service {
	if len(args) == 0 {
		return s
	}
	// use template substitution to load constants
	tmplVars := s.tmplVars()
	for i, arg := range args {
		args[i] = applyTemplate(arg, tmplVars)
	}

	if i := slices.Index(s.args, args[0]); i != -1 {
		s.args = slices.Replace(s.args, i, i+len(args), args...)
	} else {
		s.args = append(s.args, args...)
	}
	return s
}

func (s *service) If(cond bool, fn func(*service) *service) *service {
	if cond {
		return fn(s)
	}
	return s
}

func (s *service) Build() {
	s.srvMng.Build(s)
}

func applyTemplate(templateStr string, input interface{}) string {
	tpl, err := template.New("").Parse(templateStr)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to parse template, err: %s", err))
	}

	var out strings.Builder
	if err := tpl.Execute(&out, input); err != nil {
		panic(fmt.Sprintf("BUG: failed to execute template, err: %s", err))
	}
	return out.String()
}

func convert(config *params.BeaconChainConfig) ([]byte, error) {
	val := reflect.ValueOf(config).Elem()

	vals := []string{}
	for i := 0; i < val.NumField(); i++ {
		// only encode the public fields with tag 'yaml'
		tag := val.Type().Field(i).Tag.Get("yaml")
		if tag == "" {
			continue
		}

		// decode the type of the value
		typ := val.Field(i).Type()

		var resTyp string
		if isByteArray(typ) || isByteSlice(typ) {
			resTyp = "0x" + hex.EncodeToString(val.Field(i).Bytes())
		} else {
			// basic types
			switch typ.Kind() {
			case reflect.String:
				resTyp = val.Field(i).String()
			case reflect.Uint8, reflect.Uint64:
				resTyp = fmt.Sprintf("%d", val.Field(i).Uint())
			case reflect.Int:
				resTyp = fmt.Sprintf("%d", val.Field(i).Int())
			default:
				panic(fmt.Sprintf("BUG: unsupported type, tag '%s', err: '%s'", tag, val.Field(i).Kind()))
			}
		}

		vals = append(vals, fmt.Sprintf("%s: %s", tag, resTyp))
	}

	return []byte(strings.Join(vals, "\n")), nil
}

func isByteArray(t reflect.Type) bool {
	return t.Kind() == reflect.Array && t.Elem().Kind() == reflect.Uint8
}

func isByteSlice(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}

var prefundedAccounts = []string{
	"0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
	"0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d",
	"0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a",
	"0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6",
	"0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a",
	"0x8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba",
	"0x92db14e403b83dfe3df233f83dfa3a0d7096f21ca9b0d6d6b8d88b2b4ec1564e",
	"0x4bbbf85ce3377467afe5d46f804f221813b2bb87f24d81f60f1fcdbf7cbf4356",
	"0xdbda1821b80551c9d65939329250298aa3472ba22feea921c0cf5d620ea67b97",
	"0x2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6",
}

func getHomeDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("error getting user home directory: %w", err)
	}

	// Define the path for our custom home directory
	customHomeDir := filepath.Join(homeDir, ".playground")

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(customHomeDir, 0755); err != nil {
		return "", fmt.Errorf("error creating output directory: %v", err)
	}

	return customHomeDir, nil
}

func watchProposerPayloads() {
	// Wait for at least 10 seconds for Mev-boost to start
	timerC := time.After(10 * time.Second)
LOOP:
	for {
		select {
		case <-timerC:
			break
		case <-time.After(2 * time.Second):
			if _, err := getProposerPayloadDelivered(); err == nil {
				break LOOP
			}
		}
	}

	// This is not the most efficient solution since we are querying the endpoint for the full list of payloads
	// every 2 seconds. It should be fine for the kind of workloads expected to run.

	lastSlot := uint64(0)

	for {
		time.Sleep(2 * time.Second)

		vals, err := getProposerPayloadDelivered()
		if err != nil {
			fmt.Println("Error getting proposer payloads:", err)
			continue
		}

		for _, val := range vals {
			if val.Slot <= lastSlot {
				continue
			}

			fmt.Printf("Block Proposed: Slot: %d, Builder: %s, Block: %d\n", val.Slot, val.BuilderPubkey, val.BlockNumber)
			lastSlot = val.Slot
		}
	}
}

func getProposerPayloadDelivered() ([]*mevRCommon.BidTraceV2JSON, error) {
	resp, err := http.Get("http://localhost:5555/relay/v1/data/bidtraces/proposer_payload_delivered")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var payloadDeliveredList []*mevRCommon.BidTraceV2JSON
	if err := json.Unmarshal(data, &payloadDeliveredList); err != nil {
		return nil, err
	}
	return payloadDeliveredList, nil
}

type DockerRunner struct {
	out        *output
	svcManager *serviceManager
	composeCmd *exec.Cmd
	ctx        context.Context
	cancel     context.CancelFunc
	client     *client.Client
}

func NewDockerRunner(out *output, svcManager *serviceManager) *DockerRunner {
	ctx, cancel := context.WithCancel(context.Background())

	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}

	return &DockerRunner{
		out:        out,
		svcManager: svcManager,
		ctx:        ctx,
		cancel:     cancel,
		client:     client,
	}
}

func (d *DockerRunner) Stop() {
	fmt.Println("Stopping all containers")

	// try to stop all the containers from the container list for playground
	containers, err := d.client.ContainerList(d.ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", "playground=true")),
	})
	if err != nil {
		fmt.Println("Error getting container list:", err)
		return
	}

	fmt.Printf("Found %d containers to stop\n", len(containers))

	var wg sync.WaitGroup
	wg.Add(len(containers))

	for _, cont := range containers {
		fmt.Println("Stopping container:", cont.ID)

		go func(contID string) {
			defer wg.Done()
			if err := d.client.ContainerRemove(context.Background(), contID, container.RemoveOptions{
				RemoveVolumes: true,
				RemoveLinks:   false,
				Force:         true,
			}); err != nil {
				fmt.Println("Error removing container:", err)
			}
		}(cont.ID)
	}

	wg.Wait()
}

func (d *DockerRunner) Run() error {
	yamlData, err := d.svcManager.GenerateDockerCompose("docker-compose.yaml", map[string]string{
		"playground": "true",
	})
	if err != nil {
		return err
	}

	if err := d.out.WriteFile("docker-compose.yaml", yamlData); err != nil {
		return err
	}

	d.composeCmd = exec.Command("docker-compose", "-f", "./output/docker-compose.yaml", "up", "-d")
	// d.composeCmd.Stdout = os.Stdout
	// d.composeCmd.Stderr = os.Stderr

	go func() {
		fmt.Println("Starting event listener")

		// Ok, track all the events that happen for the playground=true contianers.
		eventCh, errCh := d.client.Events(context.Background(), events.ListOptions{
			Filters: filters.NewArgs(filters.Arg("label", "playground=true")),
		})

		for {
			select {
			case event := <-eventCh:
				fmt.Println("--- event ---")
				name := event.Actor.Attributes["com.docker.compose.service"]
				fmt.Println(event.Action, event.Actor.ID, name)

				if event.Action == "start" {
					// track the container logs
					go func() {
						fmt.Println("Starting log listener for", name)

						log_output, err := d.out.LogOutput(name)
						if err != nil {
							fmt.Println("Error getting log output:", err)
							return
						}

						logs, err := d.client.ContainerLogs(context.Background(), event.Actor.ID, container.LogsOptions{
							ShowStdout: true,
							ShowStderr: true,
							Follow:     true,
						})
						if err != nil {
							fmt.Println("Error getting container logs:", err)
							return
						}

						if _, err := stdcopy.StdCopy(log_output, log_output, logs); err != nil {
							fmt.Println("Error copying logs:", err)
							return
						}
						fmt.Println("DONE")
					}()
				}
			case err := <-errCh:
				fmt.Println("--- err ---")
				fmt.Println(err)
			case <-d.ctx.Done():
				return
			}
		}
	}()

	return d.composeCmd.Run()
}
