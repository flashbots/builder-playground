package main

import (
	"context"
	"crypto/ecdsa"
	_ "embed"
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

	"github.com/flashbots/mev-boost-relay/beaconclient"
	mevRCommon "github.com/flashbots/mev-boost-relay/common"

	gethcommon "github.com/ethereum/go-ethereum/common"
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
var dotFlag bool

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
	rootCmd.Flags().BoolVar(&dotFlag, "dot", false, "generate a dot file")

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

	go watchProposerPayloads()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	select {
	case <-sig:
		fmt.Println("Stopping...")
	case <-svcManager.NotifyErrCh():
	}

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

	return nil
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

	// Start the cl proxy
	{
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

		go func() {
			if err := clproxy.Run(); err != nil {
				svcManager.emitError()
			}
		}()
	}

	/*
		rethVersion := func() string {
			cmd := exec.Command(rethBin, "--version")
			out, err := cmd.Output()
			if err != nil {
				return "unknown"
			}
			// find the line of the form:
			// reth Version: x.y.z
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "reth Version: ") {
					v := strings.TrimSpace(strings.TrimPrefix(line, "reth Version: "))
					if !strings.HasPrefix(v, "v") {
						v = "v" + v
					}
					return semver.Canonical(v)
				}
			}
			return "unknown"
		}()
	*/

	services := []Service{
		&LighthouseBeaconNode{},
		&LighthouseValidator{},
		&RethNode{},
		&MevBoostRelay{},
	}

	/*
		// start the reth el client
		fmt.Println("Starting reth version " + rethVersion)
		svcManager.
			NewService("reth").
			WithArgs(
				rethBin,
				"node",
				"--chain", "{{.Dir}}/genesis.json",
				"--datadir", "{{.Dir}}/data_reth",
				"--color", "never",
				"--ipcpath", "{{.Dir}}/reth.ipc",
				// p2p config. Use a default discovery key and disable public discovery and connections
				"--p2p-secret-key", defaultRethDiscoveryPrivKeyLoc,
				"--addr", "127.0.0.1",
				"--port", "{{Port \"p2p\" 30303}}",
				// "--disable-discovery",
				// http config
				"--http",
				"--http.api", "admin,eth,net,web3",
				"--http.port", "{{Port \"http\" 8545}}",
				"--authrpc.port", "{{Port \"authrpc\" 8551}}",
				"--authrpc.jwtsecret", "{{.Dir}}/jwtsecret",
				// For reth version 1.2.0 the "legacy" engine was removed, so we now require these arguments:
				"--engine.persistence-threshold", "0", "--engine.memory-block-buffer-target", "0",
				"-vvvv",
			).
			If(useRethForValidation, func(s *service) *service {
				return s.WithReplacementArgs("--http.api", "admin,eth,web3,net,rpc,flashbots")
			}).
			Run()

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

		// start the beacon node
		fmt.Println("Starting lighthouse version " + lightHouseVersion)
		svcManager.
			NewService("beacon_node").
			WithArgs(
				lighthouseBin,
				"bn",
				"--datadir", "{{.Dir}}/data_beacon_node",
				"--testnet-dir", "{{.Dir}}/testnet",
				"--enable-private-discovery",
				"--disable-peer-scoring",
				"--staking",
				"--enr-address", "127.0.0.1",
				"--enr-udp-port", "{{Port \"p2p\" 9000}}",
				"--enr-tcp-port", "{{Port \"p2p\" 9000}}",
				"--enr-quic-port", "{{Port \"quic\" 9001}}",
				"--port", "{{Port \"p2p\" 9000}}",
				"--quic-port", "{{Port \"quic\" 9100}}",
				"--http",
				"--http-port", "{{Port \"http\" 3500}}",
				"--http-allow-origin", "*",
				"--disable-packet-filter",
				"--target-peers", "0",
				"--execution-endpoint", "http://{{Connect \"reth\" \"authrpc\"}}",
				"--execution-jwt", "{{.Dir}}/jwtsecret",
				"--builder", "http://{{Connect \"mev-boost-relay\" \"http\"}}",
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
			Run()

		// start validator client
		svcManager.
			NewService("validator").
			WithArgs(
				lighthouseBin,
				"vc",
				"--datadir", "{{.Dir}}/data_validator",
				"--testnet-dir", "{{.Dir}}/testnet",
				"--init-slashing-protection",
				"--beacon-nodes", "http://{{Connect \"beacon_node\" \"http\"}}",
				"--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990",
				"--builder-proposals",
				"--prefer-builder-proposals",
			).Run()
	*/

	/*
		{
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

			go func() {
				if err := relay.Start(); err != nil {
					svcManager.emitError()
				}
			}()
		}
	*/

	/*
		services := []*service{}
		for _, ss := range svcManager.handles {
			services = append(services, ss.Service)
		}
		services = append(services, &service{
			name: "mev-boost-relay",
			ports: []*port{
				{name: "http", port: 5555},
			},
		}, &service{
			name: "cl-proxy",
			ports: []*port{
				{name: "jsonrpc", port: 5656},
			},
		})
	*/

	/*
		// add them for now to be able to resolve dependencies
		for _, x := range services {
			svcManager.handles = append(svcManager.handles, &handle{
				Service: x,
			})
		}
	*/

	svcManager.Setup(services)

	if dotFlag {
		svcManager.PrintDot()
		return nil
	} else {
		svcManager.Start()
	}

	/*
		// print services info
		fmt.Printf("Services started:\n==================\n")
		for _, ss := range services {
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
	*/

	fmt.Printf("All services started, press Ctrl+C to stop\n")
	return nil
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
	out     *output
	handles []*handle

	stopping atomic.Bool

	wg sync.WaitGroup

	// map of reserved ports, these ports might be reserved
	// but not used yet by any service
	reserved_ports map[int]bool

	// channel for the handles to nofify when they are shutting down
	closeCh chan struct{}

	dag *Dag
}

func newServiceManager(out *output) *serviceManager {
	return &serviceManager{out: out, handles: []*handle{}, stopping: atomic.Bool{}, reserved_ports: map[int]bool{}, wg: sync.WaitGroup{}, closeCh: make(chan struct{}, 5)}
}

func (s *serviceManager) emitError() {
	select {
	case s.closeCh <- struct{}{}:
	default:
	}
}

func (s *serviceManager) reserveAvailablePort(start_port int) (int, error) {
	for port := start_port; port < 65535; port++ {
		if _, ok := s.reserved_ports[port]; ok {
			continue
		}
		fmt.Println("Checking port --> ", port)
		fmt.Println(net.Dial("tcp", fmt.Sprintf("localhost:%d", port)))

		addr := fmt.Sprintf("localhost:%d", port)
		if _, err := net.Dial("tcp", addr); err != nil {
			s.reserved_ports[port] = true
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports found")
}

type NativeService interface {
	Run(out *output) error
}

type Service interface {
	Generate(svcManager *serviceManager) *service
}

type MevBoostRelay struct {
	Port int
}

func (m *MevBoostRelay) Generate(svcManager *serviceManager) *service {
	service := svcManager.
		NewService("mev-boost-relay")

	m.Port = svcManager.reservePortFor("mev-boost-relay", "http", 5555)
	service.WithPort("http", m.Port)

	return service
}

func (m *MevBoostRelay) Run(out *output) error {
	fmt.Println("Running mev-boost-relay on port --> ", m.Port)

	cfg := mevboostrelay.DefaultConfig()
	cfg.ApiListenPort = uint64(m.Port)
	var err error
	if cfg.LogOutput, err = out.LogOutput("mev-boost-relay"); err != nil {
		return err
	}
	cfg.UseRethForValidation = useRethForValidation
	relay, err := mevboostrelay.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create relay: %w", err)
	}
	if err := relay.Start(); err != nil {
		return err
	}
	return nil
}

type RethNode struct {
}

func (r *RethNode) Generate(svcManager *serviceManager) *service {
	return svcManager.
		NewService("reth").
		WithArgs(
			"/Users/ferranbt/.playground/reth-v1.2.0",
			"node",
			"--chain", "{{.Dir}}/genesis.json",
			"--datadir", "{{.Dir}}/data_reth",
			"--color", "never",
			"--ipcpath", "{{.Dir}}/reth.ipc",
			// p2p config. Use a default discovery key and disable public discovery and connections
			"--p2p-secret-key", defaultRethDiscoveryPrivKeyLoc,
			"--addr", "127.0.0.1",
			"--port", "{{Port \"p2p\" 30303}}",
			// "--disable-discovery",
			// http config
			"--http",
			"--http.api", "admin,eth,net,web3",
			"--http.port", "{{Port \"http\" 8545}}",
			"--authrpc.port", "{{Port \"authrpc\" 8551}}",
			"--authrpc.jwtsecret", "{{.Dir}}/jwtsecret",
			"--disable-discovery",
			// For reth version 1.2.0 the "legacy" engine was removed, so we now require these arguments:
			"--engine.persistence-threshold", "0", "--engine.memory-block-buffer-target", "0",
			"-vvvv",
		).
		If(useRethForValidation, func(s *service) *service {
			return s.WithReplacementArgs("--http.api", "admin,eth,web3,net,rpc,flashbots")
		})
}

type LighthouseBeaconNode struct {
}

func (l *LighthouseBeaconNode) Generate(svcManager *serviceManager) *service {
	return svcManager.
		NewService("beacon_node").
		WithArgs(
			"/Users/ferranbt/.playground/lighthouse-v5.3.0",
			"bn",
			"--datadir", "{{.Dir}}/data_beacon_node",
			"--testnet-dir", "{{.Dir}}/testnet",
			"--enable-private-discovery",
			"--disable-peer-scoring",
			"--staking",
			"--enr-address", "127.0.0.1",
			"--enr-udp-port", "{{Port \"p2p\" 9000}}",
			"--enr-tcp-port", "{{Port \"p2p\" 9000}}",
			"--enr-quic-port", "{{Port \"quic\" 9001}}",
			"--port", "{{Port \"p2p\" 9000}}",
			"--quic-port", "{{Port \"quic\" 9100}}",
			"--http",
			"--http-port", "{{Port \"http\" 3500}}",
			"--http-allow-origin", "*",
			"--disable-packet-filter",
			"--target-peers", "0",
			"--execution-endpoint", "http://{{Connect \"reth\" \"authrpc\"}}",
			"--execution-jwt", "{{.Dir}}/jwtsecret",
			"--builder", "http://{{Connect \"mev-boost-relay\" \"http\"}}",
			"--builder-fallback-epochs-since-finalization", "0",
			"--builder-fallback-disable-checks",
			"--always-prepare-payload",
			"--prepare-payload-lookahead", "8000",
			"--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990",
		)
}

type LighthouseValidator struct {
}

func (l *LighthouseValidator) Generate(svcManager *serviceManager) *service {
	return svcManager.
		NewService("validator").
		WithArgs(
			"/Users/ferranbt/.playground/lighthouse-v5.3.0",
			"vc",
			"--datadir", "{{.Dir}}/data_validator",
			"--testnet-dir", "{{.Dir}}/testnet",
			"--init-slashing-protection",
			"--beacon-nodes", "http://{{Connect \"beacon_node\" \"http\"}}",
			"--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990",
			"--builder-proposals",
			"--prefer-builder-proposals",
		)
}

type serviceSpec struct {
	Connections []*Connection
	Ports       map[string]int
}

func findConnectStatement(args []string) (*serviceSpec, error) {
	spec := &serviceSpec{
		Connections: []*Connection{},
		Ports:       map[string]int{},
	}

	funcs := template.FuncMap{
		"Port": func(name string, num int) int {
			// We can write down the port in spec so taht it can be resolved but we do not have
			// to write yet the block number
			spec.Ports[name] = 0
			return 0
		},
		"Connect": func(name string, port string) string {
			spec.Connections = append(spec.Connections, &Connection{
				Target: name,
				Port:   port,
			})
			return ""
		},
	}

	new_args := []string{}
	for _, arg := range args {
		tpl, err := template.New("").Funcs(funcs).Parse(arg)
		if err != nil {
			return nil, fmt.Errorf("BUG: failed to parse template, err: %s", err)
		}

		var out strings.Builder
		if err := tpl.Execute(&out, nil); err != nil {
			return nil, fmt.Errorf("BUG: failed to execute template, err: %s", err)
		}
		new_args = append(new_args, out.String())
	}

	return spec, nil
}

func (s *serviceManager) PrintDot() {
	// create a dap with the handles that you have and the dependencies
	dag := &Dag{}
	for _, h := range s.handles {
		dag.AddVertex(h.Service.name)
	}
	for _, handle := range s.handles {
		serviceName := handle.Service.name
		for _, conn := range handle.Service.connections {
			dag.AddEdge(Edge{
				Src: conn.Target,
				Dst: serviceName,
			})
		}
	}

	dag.PrintDot(os.Stdout)
}

func (s *serviceManager) Setup(services []Service) {
	// for each of the service, create a handle with the info returned with Generate
	for _, svcType := range services {
		svc := svcType.Generate(s) // TODO: it should return the spec not register it itself
		s.Run(svc, svcType)
	}

	s.ValidateGraph()

	// create a dap with the handles that you have and the dependencies
	dag := &Dag{}
	for _, h := range s.handles {
		dag.AddVertex(h.Service.name)
	}
	for _, handle := range s.handles {
		serviceName := handle.Service.name
		for _, conn := range handle.Service.connections {
			dag.AddEdge(Edge{
				Src: conn.Target,
				Dst: serviceName,
			})
		}
	}
	s.dag = dag
}

func (s *serviceManager) Start() {
	// Now, we resolve the tree topologically to run the services in the correct order
	fmt.Println(s.dag)

	s.dag.TopologicalTraverse(func(v Vertex) {
		fmt.Println("--> ", v)

		// find the handle for this service
		for _, h := range s.handles {
			if h.Service.name == v {
				s.Run2(h.Service)
			}
		}
	})
}

func (s *serviceManager) findHandle(name string) (*handle, bool) {
	for _, h := range s.handles {
		if h.Service.name == name {
			return h, true
		}
	}
	return nil, false
}

func (s *serviceManager) ValidateGraph() {
	// at this point, the handles alreadt have all the ports that they cover
	// and the depednencies, we have to ensure now that those dependeices are correct

	// now we do a safe check to ensure that all the services and connectiosn exist
	indx := 0
	for _, handle := range s.handles {
		service := handle.Service

		for _, conn := range service.connections {
			target, ok := s.findHandle(conn.Target)
			if !ok {
				panic(fmt.Sprintf("service %s not found", conn.Target))
			}

			targetService := target.Service
			if _, ok := targetService.ports[conn.Port]; !ok {
				panic(fmt.Sprintf("port %s not found for service %s", conn.Port, conn.Target))
			}
		}
		indx++
	}
}

func (s *serviceManager) reservePortFor(name string, portName string, initialPort int) int {
	available_port, err := s.reserveAvailablePort(initialPort)
	if err != nil {
		panic(err)
	}

	return available_port
}

type Connection struct {
	Target string
	Port   string
}

func (s *serviceManager) Run(ss *service, impl Service) {
	if ss.ports == nil {
		ss.ports = map[string]*port{}
	}
	if ss.connections == nil {
		ss.connections = []*Connection{}
	}
	ss.serviceImpl = impl

	// populate the ports and connections nfor the service now
	spec, err := findConnectStatement(ss.args)
	if err != nil {
		panic(err)
	}

	// aggregate the extra data generate from the Spec
	ss.connections = append(ss.connections, spec.Connections...)

	for name, portNumber := range spec.Ports {
		ss.ports[name] = &port{name: name, port: portNumber}
	}

	s.handles = append(s.handles, &handle{
		Process: nil,
		Service: ss,
	})
}

func (s *serviceManager) resolveArgs(ss *service) ([]string, map[string]int, error) {
	tmplVars := map[string]interface{}{
		"Dir": s.out.dst,
	}

	reservedPorts := map[string]int{}

	funcs := template.FuncMap{
		"Port": func(name string, num int) int {
			if portNum, ok := reservedPorts[name]; ok {
				return portNum
			}
			availablePort, err := s.reserveAvailablePort(num)
			if err != nil {
				panic(err)
			}
			reservedPorts[name] = availablePort
			return availablePort
		},
		"Connect": func(name string, port string) string {
			fmt.Println("Connect --> ", name, port)
			// It assumes that you are calling the services in roder to the port has already
			// being populated
			for _, h := range s.handles {
				if h.Service.name == name {
					num, ok := h.Service.ports[port]
					if !ok {
						panic(fmt.Sprintf("port %s not found for service %s", port, name))
					}
					return fmt.Sprintf("localhost:%d", num.port)
				}
			}
			panic(fmt.Sprintf("service %s not found", name))
		},
	}

	new_args := []string{}
	for _, arg := range ss.args {
		tpl, err := template.New("").Funcs(funcs).Parse(arg)
		if err != nil {
			return nil, nil, fmt.Errorf("BUG: failed to parse template, err: %s", err)
		}

		var out strings.Builder
		if err := tpl.Execute(&out, tmplVars); err != nil {
			return nil, nil, fmt.Errorf("BUG: failed to execute template, err: %s", err)
		}
		new_args = append(new_args, out.String())
	}

	return new_args, reservedPorts, nil
}

func (s *serviceManager) Run2(ss *service) {
	// use template substitution to load constants
	fmt.Println(ss)

	args, reservedPorts, err := s.resolveArgs(ss)
	if err != nil {
		panic(err)
	}

	// now that the ports are allocated, update the service
	for name, portNumber := range reservedPorts {
		ss.ports[name].port = portNumber
	}

	fmt.Println("-- new args --")
	fmt.Println(args)

	if native, ok := ss.serviceImpl.(NativeService); ok {
		// it is native type, just let it go, it should be call internal type instead
		go func() {
			if err := native.Run(s.out); err != nil {
				panic(err)
			}
		}()
		return
	}

	cmd := exec.Command(args[0], args[1:]...)

	logOutput, err := s.out.LogOutput(ss.name)
	if err != nil {
		// this should not happen, log it
		fmt.Println("Error creating log output for", ss.name)
		logOutput = os.Stdout
	}

	// first thing to output is the command itself
	fmt.Fprint(logOutput, strings.Join(args, " ")+"\n\n")

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

	// redo, adding handles twice here
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

	serviceImpl Service

	ports  map[string]*port
	srvMng *serviceManager

	// this is the target port when using the generator
	// we also store this to do another check for service and port
	connections []*Connection
}

func (s *serviceManager) NewService(name string) *service {
	return &service{name: name, args: []string{}, srvMng: s}
}

func (s *service) WithPort(name string, portNumber int) *service {
	if s.ports == nil {
		s.ports = map[string]*port{}
	}
	s.ports[name] = &port{name: name, port: portNumber}
	return s
}

func (s *service) WithArgs(args ...string) *service {
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

// Dag is a directly acyclic graph
type Dag struct {
	once   sync.Once
	vertex set

	inbound  set
	outbound set
}

// Hashable is the interface implemented by vertex objects
// that have a hash representation
type Hashable interface {
	Hash() interface{}
}

// Vertex is a vertex in the graph
type Vertex interface{}

// Edge is an edge between two vertex of the graph
type Edge struct {
	Src Vertex
	Dst Vertex
}

func (d *Dag) init() {
	d.once.Do(func() {
		d.vertex = set{}
		d.inbound = set{}
		d.outbound = set{}
	})
}

func (d *Dag) GetInbound(v Vertex) (res []Vertex) {
	vals, ok := d.inbound[v]
	if !ok {
		return
	}
	for k := range vals.(set) {
		res = append(res, k)
	}
	return
}

func (d *Dag) GetOutbound(v Vertex) (res []Vertex) {
	vals, ok := d.outbound[v]
	if !ok {
		return
	}
	for k := range vals.(set) {
		res = append(res, k)
	}
	return
}

// AddVertex adds a new vertex on the DAG
func (d *Dag) AddVertex(v Vertex) {
	d.init()
	d.vertex.add(v)
}

// AddEdge adds a new edge on the DAG
func (d *Dag) AddEdge(e Edge) {
	d.init()

	if s, ok := d.inbound[e.Dst]; ok && s.(set).include(e.Src) {
		return
	}

	s, ok := d.inbound[e.Dst]
	if !ok {
		s = set{}
		d.inbound[e.Dst] = s
	}
	s.(set).add(e.Src)

	s, ok = d.outbound[e.Src]
	if !ok {
		s = set{}
		d.outbound[e.Src] = s
	}
	s.(set).add(e.Dst)
}

func (d *Dag) FindComponents() [][]Vertex {

	// find components without any inbound
	leafVertex := []Vertex{}
	for v := range d.vertex {
		if _, ok := d.inbound[v]; !ok {
			leafVertex = append(leafVertex, v)
		}
	}

	result := [][]Vertex{}

	// follow each leaf vertex upwards to find the component
	for _, leaf := range leafVertex {
		component := []Vertex{}

		queue := []Vertex{leaf}
		for len(queue) != 0 {
			var item Vertex
			item, queue = queue[0], queue[1:]

			component = append(component, item)
			if outbound, ok := d.outbound[item]; ok {
				for v := range outbound.(set) {
					queue = append(queue, v)
				}
			}
		}
		result = append(result, component)
	}
	return result
}

type set map[interface{}]interface{}

func (s set) add(v Vertex) {
	k := v
	if h, ok := v.(Hashable); ok {
		k = h.Hash()
	}
	if _, ok := s[k]; !ok {
		s[k] = struct{}{}
	}
}

func (s set) include(v Vertex) bool {
	_, ok := s[v]
	return ok
}

// PrintDot generates a DOT file representation of the DAG
func (d *Dag) PrintDot(w io.Writer) error {
	// Write DOT file header
	if _, err := fmt.Fprintln(w, "digraph DAG {"); err != nil {
		return err
	}

	// Write all vertices
	for v := range d.vertex {
		// Convert vertex to string for label
		label := fmt.Sprintf("%v", v)
		// Escape quotes in label if present
		label = strings.ReplaceAll(label, `"`, `\"`)
		if _, err := fmt.Fprintf(w, "    \"%v\" [label=\"%s\"];\n", v, label); err != nil {
			return err
		}
	}

	// Write all edges
	for dst, srcSet := range d.inbound {
		for src := range srcSet.(set) {
			if _, err := fmt.Fprintf(w, "    \"%v\" -> \"%v\";\n", src, dst); err != nil {
				return err
			}
		}
	}

	// Write DOT file footer
	if _, err := fmt.Fprintln(w, "}"); err != nil {
		return err
	}

	return nil
}

// TopologicalTraverse traverses the DAG in topological order, ensuring all parents
// are visited before visiting a vertex. The provided callback function is called
// for each vertex in the correct order.
func (d *Dag) TopologicalTraverse(callback func(v Vertex)) {
	// Track visited vertices
	visited := make(map[Vertex]bool)

	// Track number of unvisited parents for each vertex
	unvisitedParents := make(map[Vertex]int)

	// Initialize unvisitedParents count
	for v := range d.vertex {
		if inbound, ok := d.inbound[v]; ok {
			unvisitedParents[v] = len(inbound.(set))
		} else {
			unvisitedParents[v] = 0
		}
	}

	// Start with vertices that have no parents (roots)
	queue := []Vertex{}
	for v := range d.vertex {
		if unvisitedParents[v] == 0 && !visited[v] {
			queue = append(queue, v)
		}
	}

	// Process vertices in order
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]

		if visited[v] {
			continue
		}

		// Visit the vertex
		visited[v] = true
		callback(v)

		// Update unvisited parent count for all children and add ready children to queue
		if outbound, ok := d.outbound[v]; ok {
			for child := range outbound.(set) {
				unvisitedParents[child]--
				if unvisitedParents[child] == 0 && !visited[child] {
					queue = append(queue, child)
				}
			}
		}
	}
}
