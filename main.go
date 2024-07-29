package main

import (
	"context"
	"crypto/ecdsa"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"math/big"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flashbots/mev-boost-relay/beaconclient"
	mevRCommon "github.com/flashbots/mev-boost-relay/common"

	"github.com/ethereum/go-ethereum/core/types"
	ecrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/ferranbt/builder-playground/artifacts"
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

//go:embed config.yaml
var clConfigContent []byte

var defaultJWTToken = "04592280e1778419b7aa954d43871cb2cfb2ebda754fb735e8adeb293a88f9bf"

var outputFlag string
var resetFlag bool
var useBinPathFlag bool
var validateFlag bool

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

var validateCmd = &cobra.Command{
	Use:  "validate",
	Long: `Validate the playground`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Test that blocks are being produced
		log := mevRCommon.LogSetup(false, "info")
		clt := beaconclient.NewProdBeaconInstance(log, "http://localhost:3500", "http://localhost:3500")

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
				return sync.HeadSlot > 1
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

		ch := make(chan beaconclient.HeadEventData)
		go clt.SubscribeToHeadEvents(ch)

		var lastSlot uint64
		for i := uint64(0); i < numBlocksValidate; i++ {
			select {
			case head := <-ch:
				if lastSlot != 0 && lastSlot+1 != head.Slot {
					return fmt.Errorf("slot mismatch, expected %d, got %d", lastSlot+1, head.Slot)
				}

				log.Infof("Slot: %d Block: %s", head.Slot, head.Block)
				lastSlot = head.Slot
			case <-time.After(20 * time.Second):
				return fmt.Errorf("timeout waiting for block")
			}
		}

		return nil
	},
}

func main() {
	rootCmd.Flags().StringVar(&outputFlag, "output", "local-testnet", "")
	rootCmd.Flags().BoolVar(&resetFlag, "reset", false, "")
	rootCmd.Flags().BoolVar(&useBinPathFlag, "use-bin-path", false, "")
	downloadArtifactsCmd.Flags().BoolVar(&validateFlag, "validate", false, "")
	validateCmd.Flags().Uint64Var(&numBlocksValidate, "num-blocks", 5, "")

	rootCmd.AddCommand(downloadArtifactsCmd)
	rootCmd.AddCommand(validateCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runIt() error {
	out := &output{dst: outputFlag}

	exists := out.Exists("data_reth")
	if exists && resetFlag || !exists {
		if resetFlag {
			if err := out.Remove(""); err != nil {
				return err
			}
		}
		if err := setupArtifacts(); err != nil {
			return err
		}
	} else {
		fmt.Println("Artifacts already exist, skipping setup")
	}

	svcManager := newServiceManager(out)
	if err := setupServices(svcManager, out); err != nil {
		// close all services if there was an error
		svcManager.StopAndWait()
		return err
	}

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

	// load the config.yaml file
	clConfig, err := params.UnmarshalConfig(clConfigContent, nil)
	if err != nil {
		return err
	}
	if err := params.SetActive(clConfig); err != nil {
		return err
	}

	genesisTime := uint64(time.Now().Unix())
	config := params.BeaconConfig()

	gen := interop.GethTestnetGenesis(genesisTime, config)

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

	v, err := version.FromString("deneb") // TODO: Derive from config.toml
	if err != nil {
		return err
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

	// log the prefunded accounts
	fmt.Printf("\nPrefunded accounts:\n==================\n")
	for indx, acc := range prefundedAccounts {
		priv, _ := getPrivKey(acc)
		fmt.Printf("(%d) %s (%s)\n", indx, acc, ecrypto.PubkeyToAddress(priv.PublicKey).Hex())
	}
	fmt.Println("")

	// start the reth el client
	svcManager.
		NewService("reth").
		WithArgs(
			rethBin,
			"node",
			"--chain", "{{.Dir}}/genesis.json",
			"--datadir", "{{.Dir}}/data_reth",
			"--http",
			"--http.port", "8545",
			"--authrpc.port", "8551",
			"--authrpc.jwtsecret", "{{.Dir}}/jwtsecret",
		).
		Run()

	// start the beacon node
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
			"--http-allow-sync-stalled",
			"--enr-address", "127.0.0.1",
			"--enr-udp-port", "9000",
			"--enr-tcp-port", "9000",
			"--enr-quic-port", "9100",
			"--port", "9000",
			"--quic-port", "9100",
			"--http-port", "3500",
			"--disable-packet-filter",
			"--target-peers", "0",
			"--execution-endpoint", "http://localhost:8551",
			"--execution-jwt", "{{.Dir}}/jwtsecret",
			"--builder", "http://localhost:5555",
			"--builder-fallback-epochs-since-finalization", "0",
			"--builder-fallback-disable-checks",
			"--always-prepare-payload",
			"--prepare-payload-lookahead", "8000",
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
			"--beacon-nodes", "http://localhost:3500",
			"--suggested-fee-recipient", "0x690B9A9E9aa1C9dB991C7721a92d351Db4FaC990",
			"--builder-proposals",
		).Run()

	{
		cfg := mevboostrelay.DefaultConfig()
		var err error
		if cfg.LogOutput, err = out.LogOutput("mev-boost-relay"); err != nil {
			return err
		}
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
	handles []*exec.Cmd

	stopping atomic.Bool

	wg sync.WaitGroup

	// channel for the handles to nofify when they are shutting down
	closeCh chan struct{}
}

func newServiceManager(out *output) *serviceManager {
	return &serviceManager{out: out, handles: []*exec.Cmd{}, stopping: atomic.Bool{}, wg: sync.WaitGroup{}, closeCh: make(chan struct{}, 5)}
}

func (s *serviceManager) emitError() {
	select {
	case s.closeCh <- struct{}{}:
	default:
	}
}

func (s *serviceManager) Run(ss *service) {
	cmd := exec.Command(ss.args[0], ss.args[1:]...)

	logOutput, err := s.out.LogOutput(ss.name)
	if err != nil {
		// this should not happen, log it
		fmt.Println("Error creating log output for", ss.name)
		logOutput = os.Stdout
	}

	// first thing to output is the command itself
	fmt.Fprint(logOutput, strings.Join(ss.args, " "))

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

	s.handles = append(s.handles, cmd)
}

func (s *serviceManager) NotifyErrCh() <-chan struct{} {
	return s.closeCh
}

func (s *serviceManager) StopAndWait() {
	s.stopping.Store(true)

	for _, h := range s.handles {
		if h.Process != nil {
			fmt.Printf("Stopping %s\n", h.Path)
			h.Process.Kill()
		}
	}
	s.wg.Wait()
}

type service struct {
	name string
	args []string

	srvMng *serviceManager
}

func (s *serviceManager) NewService(name string) *service {
	return &service{name: name, args: []string{}, srvMng: s}
}

func (s *service) WithArgs(args ...string) *service {
	tmplVars := map[string]interface{}{
		"Dir": s.srvMng.out.dst,
	}

	// use template substitution to load constants
	for i, arg := range args {
		args[i] = applyTemplate(arg, tmplVars)
	}

	s.args = append(s.args, args...)
	return s
}

func (s *service) Run() {
	s.srvMng.Run(s)
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
