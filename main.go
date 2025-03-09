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
	"net"
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
var withOverrides []string

var rootCmd = &cobra.Command{
	Use:   "playground",
	Short: "",
	Long:  ``,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runIt()
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
	rootCmd.Flags().StringArrayVar(&withOverrides, "override", []string{}, "override a service's config")

	watchCmd.Flags().Uint64Var(&numBlocksValidate, "validate-num-blocks", 5, "")
	watchCmd.Flags().BoolVar(&validatePayloads, "validate-payloads", false, "")

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

	if err := saveDotGraph(svcManager, out); err != nil {
		fmt.Println("Error saving dot graph:", err)
	}

	// generate the overrides map --override mev-boost-relay=./mev-boost-relay
	overrides := make(map[string]string)
	for _, override := range withOverrides {
		parts := strings.Split(override, "=")
		if len(parts) != 2 {
			return fmt.Errorf("invalid override: %s", override)
		}
		overrides[parts[0]] = parts[1]
	}
	dockerRunner := NewDockerRunner(out, svcManager, overrides)
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

	svcManager.AddService("el", &RethEL{})
	svcManager.AddService("beacon", &LighthouseBeaconNode{
		ExecutionNode: "el",
		MevBoostNode:  "mev-boost",
	})
	svcManager.AddService("validator", &LighthouseValidator{
		BeaconNode: "beacon",
	})
	svcManager.AddService("op-node", &OpNode{
		L1Node:   "el",
		L1Beacon: "beacon",
		L2Node:   "op-geth",
	})
	svcManager.AddService("op-geth", &OpGeth{})
	svcManager.AddService("op-batcher", &OpBatcher{
		L1Node:     "el",
		L2Node:     "op-geth",
		RollupNode: "op-node",
	})
	svcManager.AddService("mev-boost", &MevBoostRelay{
		BeaconClient:     "beacon",
		ValidationServer: "el",
	})

	// svcManager.AddService(&ClProxy{})

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

func Connect(service, port string) string {
	return fmt.Sprintf(`{{Service "%s" "%s"}}`, service, port)
}

type OpBatcher struct {
	L1Node     string
	L2Node     string
	RollupNode string
}

func (o *OpBatcher) Run(service *service) {
	service.
		WithImage("op-batcher").
		WithImageReal("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-batcher").
		WithTag("v1.11.1").
		WithEntrypoint("op-batcher").
		WithArgs(
			"--l1-eth-rpc", Connect(o.L1Node, "http"),
			"--l2-eth-rpc", Connect(o.L1Node, "http"),
			"--rollup-rpc", Connect(o.RollupNode, "http"),
			"--max-channel-duration=2",
			"--sub-safety-margin=4",
			"--poll-interval=1s",
			"--num-confirmations=1",
			"--private-key=0x2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6",
		).
		Build()
}

// NodeRef is a connection reference from one service to another
type NodeRef struct {
	Service   string
	PortLabel string
}

type OpNode struct {
	L1Node   string
	L1Beacon string
	L2Node   string
}

func (o *OpNode) Run(service *service) {
	service.
		WithImage("op-node").
		WithImageReal("us-docker.pkg.dev/oplabs-tools-artifacts/images/op-node").
		WithTag("v1.11.0").
		WithEntrypoint("op-node").
		WithArgs(
			"--l1", Connect(o.L1Node, "http"),
			"--l1.beacon", Connect(o.L1Beacon, "http"),
			"--l1.epoch-poll-interval", "12s",
			"--l1.http-poll-interval", "6s",
			"--l2", Connect(o.L2Node, "authrpc"),
			"--l2.jwt-secret", "{{.Dir}}/jwt-secret.txt",
			"--sequencer.enabled",
			"--sequencer.l1-confs", "0",
			"--verifier.l1-confs", "0",
			"--p2p.sequencer.key", "8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba",
			"--rollup.config", "{{.Dir}}/rollup.json",
			"--rpc.addr", "0.0.0.0",
			"--rpc.port", `{{Port "http" 8549}}`,
			"--p2p.listen.ip", "0.0.0.0",
			"--p2p.listen.tcp", `{{Port "p2p" 9003}}`,
			"--p2p.listen.udp", `{{Port "p2p" 9003}}`,
			"--p2p.scoring.peers", "light",
			"--p2p.ban.peers", "true",
			"--metrics.enabled",
			"--metrics.addr", "0.0.0.0",
			"--metrics.port", `{{Port "metrics" 7300}}`,
			"--pprof.enabled",
			"--rpc.enable-admin",
			"--safedb.path", "{{.Dir}}/db",
		).
		Build()
}

type OpGeth struct {
}

func (o *OpGeth) Run(service *service) {
	service.
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
				"--http.port "+`{{Port "http" 8545}} `+
				"--http.api web3,debug,eth,txpool,net,engine,miner "+
				"--ws "+
				"--ws.addr 0.0.0.0 "+
				"--ws.port "+`{{Port "ws" 8546}} `+
				"--ws.origins \"*\" "+
				"--ws.api debug,eth,txpool,net,engine,miner "+
				"--syncmode full "+
				"--nodiscover "+
				"--maxpeers 0 "+
				"--rpc.allow-unprotected-txs "+
				"--authrpc.addr 0.0.0.0 "+
				"--authrpc.port "+`{{Port "authrpc" 8551}} `+
				"--authrpc.vhosts \"*\" "+
				"--authrpc.jwtsecret {{.Dir}}/jwt-secret.txt "+
				"--gcmode archive "+
				"--state.scheme hash "+
				"--metrics "+
				"--metrics.addr 0.0.0.0 "+
				"--metrics.port "+`{{Port "metrics" 6061}}`,
		).
		Build()
}

type RethEL struct {
}

func (r *RethEL) Run(svc *service) {
	// start the reth el client
	svc.
		WithImage("reth").
		WithImageReal("ghcr.io/paradigmxyz/reth").
		WithTag("v1.2.0").
		WithEntrypoint("/usr/local/bin/reth").
		WithArgs(
			"node",
			"--chain", "{{.Dir}}/genesis.json",
			"--datadir", "{{.Dir}}/data_reth",
			"--color", "never",
			"--ipcpath", "{{.Dir}}/reth.ipc",
			// p2p config. Use a default discovery key and disable public discovery and connections
			"--p2p-secret-key", defaultRethDiscoveryPrivKeyLoc,
			"--addr", "127.0.0.1",
			"--port", `{{Port "rpc" 30303}}`,
			// "--disable-discovery",
			// http config
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.api", "admin,eth,net,web3",
			"--http.port", `{{Port "http" 8545}}`,
			"--authrpc.port", `{{Port "authrpc" 8551}}`,
			"--authrpc.addr", "0.0.0.0",
			"--authrpc.jwtsecret", "{{.Dir}}/jwtsecret",
			// For reth version 1.2.0 the "legacy" engine was removed, so we now require these arguments:
			"--engine.persistence-threshold", "0", "--engine.memory-block-buffer-target", "0",
			"-vvvv",
		).
		If(useRethForValidation, func(s *service) *service {
			return s.WithReplacementArgs("--http.api", "admin,eth,web3,net,rpc,flashbots")
		}).
		Build()
}

type LighthouseBeaconNode struct {
	ExecutionNode string
	MevBoostNode  string
}

func (l *LighthouseBeaconNode) Run(svc *service) {
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
	svc.
		WithImage("lighthouse").
		WithImageReal("sigp/lighthouse").
		WithTag("v7.0.0-beta.0").
		WithEntrypoint("lighthouse").
		WithArgs(
			"bn",
			"--datadir", "{{.Dir}}/data_beacon_node",
			"--testnet-dir", "{{.Dir}}/testnet",
			"--enable-private-discovery",
			"--disable-peer-scoring",
			"--staking",
			"--enr-address", "127.0.0.1",
			"--enr-udp-port", `{{Port "p2p" 9000}}`,
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
			"--execution-jwt", "{{.Dir}}/jwtsecret",
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
		)

	if l.MevBoostNode != "" {
		svc.WithArgs(
			"--builder", Connect(l.MevBoostNode, "http"),
			"--builder-fallback-epochs-since-finalization", "0",
			"--builder-fallback-disable-checks",
		)
	}

	svc.Build()
}

type LighthouseValidator struct {
	BeaconNode string
}

func (l *LighthouseValidator) Run(service *service) {
	// start validator client
	service.
		WithImage("lighthouse").
		WithImageReal("sigp/lighthouse").
		WithTag("v7.0.0-beta.0").
		WithEntrypoint("lighthouse").
		WithArgs(
			"vc",
			"--datadir", "{{.Dir}}/data_validator",
			"--testnet-dir", "{{.Dir}}/testnet",
			"--init-slashing-protection",
			"--beacon-nodes", Connect(l.BeaconNode, "http"),
			"--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990",
			"--builder-proposals",
			"--prefer-builder-proposals",
		).Build()
}

type ClProxy struct {
	PrimaryBuilder   string
	SecondaryBuilder string
}

func (c *ClProxy) Run(service *service) {
	service.
		WithImage("cl-proxy").
		WithImageReal("ghcr.io/flashbots/playground/utils").
		WithTag("latest").
		WithEntrypoint("cl-proxy").
		WithArgs(
			"--primary-builder", Connect(c.PrimaryBuilder, "authrpc"),
			"--secondary-builder", Connect(c.SecondaryBuilder, "authrpc"),
			"--port", `{{Port "authrpc" 5656}}`,
		).Build()
}

/*
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
*/

type MevBoostRelay struct {
	BeaconClient     string
	ValidationServer string
}

func (m *MevBoostRelay) Run(service *service) {
	srv := service.
		WithImage("mev-boost-relay").
		WithImageReal("ghcr.io/flashbots/playground/utils").
		WithTag("latest").
		WithEntrypoint("mev-boost-relay").
		WithArgs(
			"--api-listen-addr", "0.0.0.0",
			"--api-listen-port", `{{Port "http" 5555}}`,
			"--beacon-client-addr", Connect(m.BeaconClient, "http"),
		)

	if m.ValidationServer != "" {
		srv.WithArgs("--validation-server-addr", Connect(m.ValidationServer, "http"))
	}
	srv.Build()
}

/*
func (m *MevBoostRelay) Run(out *output, ctx context.Context) error {
	cfg := mevboostrelay.DefaultConfig()
	var err error
	if cfg.LogOutput, err = out.LogOutput("mev-boost-relay"); err != nil {
		return err
	}
	if useRethForValidation {
		cfg.ValidationServerAddr = "http://localhost:8545"
	}
	relay, err := mevboostrelay.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create relay: %w", err)
	}

	return relay.Start()
}
*/

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

	out     *output
	handles []*handle

	stopping atomic.Bool

	wg sync.WaitGroup

	// channel for the handles to nofify when they are shutting down
	closeCh chan struct{}
}

func newServiceManager(out *output) *serviceManager {
	return &serviceManager{out: out, handles: []*handle{}, stopping: atomic.Bool{}, wg: sync.WaitGroup{}, closeCh: make(chan struct{}, 5)}
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
	Run(service *service)
}

func (s *serviceManager) AddService(name string, srv Service) {
	service := s.NewService(name)
	srv.Run(service)
}

/*
func (s *serviceManager) AddNativeService(srv NativeService) {
	s.services = append(s.services, srv.Service())

	go func() {
		if err := srv.Run(s.out, context.Background()); err != nil {
			s.emitError()
		}
	}()
}
*/

func (s *serviceManager) Start(dryRun bool) {
	// first, try to download all the artifacts
	// figure out if all the port dependencies are met from the service description
	servicesMap := make(map[string]*service)
	for _, ss := range s.services {
		servicesMap[ss.name] = ss
	}

	for _, ss := range s.services {
		for _, nodeRef := range ss.nodeRefs {
			targetService, ok := servicesMap[nodeRef.Service]
			if !ok {
				panic(fmt.Sprintf("service %s depends on service %s, but it is not defined", ss.name, nodeRef.Service))
			}

			found := false
			for _, targetPort := range targetService.ports {
				if targetPort.name == nodeRef.PortLabel {
					found = true
					break
				}
			}
			if !found {
				fmt.Println(targetService.ports)
				panic(fmt.Sprintf("service %s depends on service %s, but it does not expose port %s", ss.name, nodeRef.Service, nodeRef.PortLabel))
			}
		}
	}

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

	// this is populated by the service manager
	hostPort int
}

type service struct {
	name string
	args []string

	ports    []*port
	nodeRefs []*NodeRef

	srvMng *serviceManager

	override string

	// release specific configuration
	// we call this image here but it can also represent a release binary
	image      string
	imagePath  string
	tag        string
	imageReal  string
	entrypoint string
}

func (s *service) GetPort(name string) *port {
	for _, p := range s.ports {
		if p.name == name {
			return p
		}
	}
	panic(fmt.Sprintf("BUG: port %s not found for service %s", name, s.name))
}

func (s *serviceManager) NewService(name string) *service {
	return &service{name: name, args: []string{}, srvMng: s, ports: []*port{}, nodeRefs: []*NodeRef{}}
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
	// add the port if not already present with the same name.
	// if preset with the same name, they must have same port number
	for _, p := range s.ports {
		if p.name == name {
			if p.port != portNumber {
				panic(fmt.Sprintf("port %s already defined with different port number", name))
			}
			return s
		}
	}
	s.ports = append(s.ports, &port{name: name, port: portNumber})
	return s
}

func (s *service) WithArgs(args ...string) *service {
	// use template substitution to load constants
	tmplVars := s.tmplVars()
	for i, arg := range args {
		var port []port
		var nodeRef []NodeRef
		args[i], port, nodeRef = applyTemplate(arg, tmplVars)
		for _, p := range port {
			s.WithPort(p.name, p.port)
		}
		for _, n := range nodeRef {
			s.nodeRefs = append(s.nodeRefs, &n)
		}
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
		// skip refs since we do not do them yet on replacement args
		args[i], _, _ = applyTemplate(arg, tmplVars)
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

func applyTemplate(templateStr string, input interface{}) (string, []port, []NodeRef) {
	var portRef []port
	var nodeRef []NodeRef
	// ther can be multiple port and nodere because in the case of op-geth we pass a whole string as nested command args

	funcs := template.FuncMap{
		"Service": func(name string, portLabel string) string {
			if name == "" {
				panic("BUG: service name cannot be empty")
			}
			if portLabel == "" {
				panic("BUG: port label cannot be empty")
			}

			// for the first pass of service we do not do anything, keep it as it is for the followup pass
			// here we only keep the references to the services to be checked if they are valid and an be resolved
			// later on for the runtime we will do the resolve stage.
			// TODO: this will get easier when we move away from templates and use interface and structs.
			nodeRef = append(nodeRef, NodeRef{Service: name, PortLabel: portLabel})
			return fmt.Sprintf(`{{Service "%s" "%s"}}`, name, portLabel)
		},
		"Port": func(name string, defaultPort int) string {
			portRef = append(portRef, port{name: name, port: defaultPort})
			return fmt.Sprintf(`{{Port "%s" %d}}`, name, defaultPort)
		},
	}

	tpl, err := template.New("").Funcs(funcs).Parse(templateStr)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to parse template, err: %s", err))
	}

	var out strings.Builder
	if err := tpl.Execute(&out, input); err != nil {
		panic(fmt.Sprintf("BUG: failed to execute template, err: %s", err))
	}
	res := out.String()

	// escape quotes
	res = strings.ReplaceAll(res, `&#34;`, `"`)

	return res, portRef, nodeRef
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
	out           *output
	svcManager    *serviceManager
	composeCmd    *exec.Cmd
	ctx           context.Context
	cancel        context.CancelFunc
	client        *client.Client
	reservedPorts map[int]bool
	overrides     map[string]string
	handles       []*exec.Cmd
}

func NewDockerRunner(out *output, svcManager *serviceManager, overrides map[string]string) *DockerRunner {
	ctx, cancel := context.WithCancel(context.Background())

	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}

	return &DockerRunner{
		out:           out,
		svcManager:    svcManager,
		ctx:           ctx,
		cancel:        cancel,
		client:        client,
		reservedPorts: map[int]bool{},
		overrides:     overrides,
		handles:       []*exec.Cmd{},
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

	// stop all the handles
	for _, handle := range d.handles {
		handle.Process.Kill()
	}
}

func (d *DockerRunner) reservePort(startPort int) int {
	for i := startPort; i < startPort+1000; i++ {
		if _, ok := d.reservedPorts[i]; ok {
			continue
		}
		// make a net.Listen on the port to see if it is aavailable
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", i))
		if err != nil {
			continue
		}
		listener.Close()
		d.reservedPorts[i] = true
		return i
	}
	panic("BUG: could not reserve a port")
}

func (d *DockerRunner) getService(name string) *service {
	for _, svc := range d.svcManager.services {
		if svc.name == name {
			return svc
		}
	}
	return nil
}

func (d *DockerRunner) applyTemplate(s *service) []string {
	funcs := template.FuncMap{
		"Service": func(name string, portLabel string) string {
			// find the service and the port that it resolves for that label
			svc := d.getService(name)
			if svc == nil {
				panic(fmt.Sprintf("BUG: service %s not found", name))
			}
			port := svc.GetPort(portLabel)
			if port == nil {
				panic(fmt.Sprintf("BUG: port label %s not found for service %s", portLabel, name))
			}

			if s.override == "" {
				// service is running inside docker
				if svc.override == "" {
					// use the DNS discovery of docker compose to connect to the service and the docker port
					return fmt.Sprintf("http://%s:%d", svc.name, port.port)
				}
				// the service is going to be running with the host port in the host machine
				// use host.docker.internal to connect to it.
				return fmt.Sprintf("http://host.docker.internal:%d", port.hostPort)
			} else {
				// either if the target service is running inside docker or outside, it is exposed in localhost
				// with the host port
				return fmt.Sprintf("http://localhost:%d", port.hostPort)
			}
		},
		"Port": func(name string, defaultPort int) int {
			if s.override == "" {
				// running inside docker, return the port
				return defaultPort
			}
			// return the host port
			return s.GetPort(name).hostPort
		},
	}

	var argsResult []string
	for _, arg := range s.args {
		tpl, err := template.New("").Funcs(funcs).Parse(arg)
		if err != nil {
			panic(fmt.Sprintf("BUG: failed to parse template, err: %s, arg: %s", err, arg))
		}

		var out strings.Builder
		if err := tpl.Execute(&out, nil); err != nil {
			panic(fmt.Sprintf("BUG: failed to execute template, err: %s, arg: %s", err, arg))
		}
		argsResult = append(argsResult, out.String())
	}

	return argsResult
}

func (d *DockerRunner) ToDockerComposeService(s *service) map[string]interface{} {
	// apply the template again on the arguments to figure out the connections
	// at this point all of them are valid, we just have to resolve them again. We assume for now
	// everyone is going to be on docker at the same network.
	args := d.applyTemplate(s)

	service := map[string]interface{}{
		"image":   fmt.Sprintf("%s:%s", s.imageReal, s.tag),
		"command": args,
		// Add volume mount for the output directory
		"volumes": []string{
			fmt.Sprintf("./:/output"),
		},
		// Add the ethereum network
		"networks": []string{"ethereum"},
		"labels":   map[string]string{"playground": "true"},
	}

	if s.entrypoint != "" {
		service["entrypoint"] = s.entrypoint
	}

	if len(s.ports) > 0 {
		ports := []string{}
		for _, p := range s.ports {
			ports = append(ports, fmt.Sprintf("%d:%d", p.hostPort, p.port))
		}
		service["ports"] = ports
	}

	return service
}

func (d *DockerRunner) GenerateDockerCompose() ([]byte, error) {
	// First, figure out if the overrides are valid, they might reference a service that does not exist.
	for name, val := range d.overrides {
		svc := d.getService(name)
		if svc == nil {
			return nil, fmt.Errorf("service %s from override not found", name)
		}
		svc.override = val
	}

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

	// for each of the ports, reserve a port on the host machine
	for _, svc := range d.svcManager.services {
		for _, port := range svc.ports {
			port.hostPort = d.reservePort(port.port)
		}
	}

	for _, svc := range d.svcManager.services {
		if svc.srvMng != nil { // Only include services that were created with NewService
			// resolve the template again for the variables because things Connect need to be resolved now.
			if svc.override != "" {
				// skip services that are going to be launched with an override
				continue
			}
			services[svc.name] = d.ToDockerComposeService(svc)
		}
	}

	yamlData, err := yaml.Marshal(compose)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal docker-compose: %w", err)
	}

	return yamlData, nil
}

func (d *DockerRunner) runOnHost(ss *service) {
	// we have to apply the template to the args like we do in docker-compose services
	args := d.applyTemplate(ss)

	fmt.Println("Running", ss.override, args)
	cmd := exec.Command(ss.override, args...)

	logOutput, err := d.out.LogOutput(ss.name)
	if err != nil {
		// this should not happen, log it
		fmt.Println("Error creating log output for", ss.name)
		logOutput = os.Stdout
	}

	// first thing to output is the command itself
	fmt.Fprint(logOutput, strings.Join(ss.args, " ")+"\n\n")

	cmd.Stdout = logOutput
	cmd.Stderr = logOutput

	go func() {
		if err := cmd.Run(); err != nil {
			panic(err)
		}
	}()

	d.handles = append(d.handles, cmd)
}

func (d *DockerRunner) Run() error {
	yamlData, err := d.GenerateDockerCompose()
	if err != nil {
		return err
	}

	if err := d.out.WriteFile("docker-compose.yaml", yamlData); err != nil {
		return err
	}

	d.composeCmd = exec.Command("docker-compose", "-f", "./output/docker-compose.yaml", "up", "-d")

	// in parallel start the services that need to be overriten and ran on host
	go func() {
		for _, svc := range d.svcManager.services {
			if svc.override != "" {
				d.runOnHost(svc)
			}
		}
	}()

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

func (s *serviceManager) GenerateDotGraph() string {
	var b strings.Builder
	b.WriteString("digraph G {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=record];\n\n")

	// Create a map of services for easy lookup
	servicesMap := make(map[string]*service)
	for _, ss := range s.services {
		servicesMap[ss.name] = ss
	}

	// Add nodes (services) with their ports as labels
	for _, ss := range s.services {
		var ports []string
		for _, p := range ss.ports {
			ports = append(ports, fmt.Sprintf("%s:%d", p.name, p.port))
		}
		portLabel := ""
		if len(ports) > 0 {
			portLabel = "|{" + strings.Join(ports, "|") + "}"
		}
		// Replace hyphens with underscores for DOT compatibility
		nodeName := strings.ReplaceAll(ss.name, "-", "_")
		b.WriteString(fmt.Sprintf("  %s [label=\"%s%s\"];\n", nodeName, ss.name, portLabel))
	}

	b.WriteString("\n")

	// Add edges (connections between services)
	for _, ss := range s.services {
		sourceNode := strings.ReplaceAll(ss.name, "-", "_")
		for _, ref := range ss.nodeRefs {
			targetNode := strings.ReplaceAll(ref.Service, "-", "_")
			b.WriteString(fmt.Sprintf("  %s -> %s [label=\"%s\"];\n",
				sourceNode,
				targetNode,
				ref.PortLabel,
			))
		}
	}

	b.WriteString("}\n")
	return b.String()
}

func saveDotGraph(svcManager *serviceManager, out *output) error {
	dotGraph := svcManager.GenerateDotGraph()
	return out.WriteFile("services.dot", dotGraph)
}
