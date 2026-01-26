package playground

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
	"io"
	"log"
	"log/slog"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/crypto/bls"
	"github.com/OffchainLabs/prysm/v7/runtime/interop"
	"github.com/OffchainLabs/prysm/v7/runtime/version"
	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	ecrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/flashbots/builder-playground/utils"
	"github.com/flashbots/builder-playground/utils/keys"
	"github.com/otiai10/copy"
	"gopkg.in/yaml.v2"
)

var (
	defaultL1BlockTimeSeconds = uint64(12)
	defaultOpBlockTimeSeconds = uint64(2)
)

// minimumGenesisDelay is the minimum delay for the genesis time. This is required
// because lighthouse takes some time to start and we need to make sure it is ready
// otherwise, some blocks are missed.
var MinimumGenesisDelay uint64 = 10

//go:embed utils/rollup-isthmus.json
var opRollupConfigIsthmus []byte

//go:embed utils/rollup-jovian.json
var opRollupConfigJovian []byte

//go:embed utils/genesis-isthmus.json
var opGenesisIsthmus []byte

//go:embed utils/genesis-jovian.json
var opGenesisJovian []byte

//go:embed utils/state-isthmus.json
var opStateIsthmus []byte

//go:embed utils/state-jovian.json
var opStateJovian []byte

//go:embed config.yaml.tmpl
var clConfigContent []byte

// l2ForkConfig holds the selected L2 fork configuration files
type l2ForkConfig struct {
	genesis      []byte  // L2 genesis JSON
	rollupConfig []byte  // L2 rollup config JSON
	state        []byte  // L1 state allocations for OP contracts
	forkBlock    *uint64 // block number to activate jovian (nil = at genesis or disabled)
}

// selectL2Fork selects the appropriate L2 fork configuration based on applyLatestL2Fork:
//   - nil: Use isthmus (default, no jovian)
//   - 0: Use jovian at genesis
//   - > 0: Use isthmus at genesis, activate jovian at block N
func selectL2Fork(applyLatestL2Fork *uint64) *l2ForkConfig {
	if applyLatestL2Fork == nil {
		// Default: isthmus only (no jovian)
		return &l2ForkConfig{
			genesis:      opGenesisIsthmus,
			rollupConfig: opRollupConfigIsthmus,
			state:        opStateIsthmus,
			forkBlock:    nil,
		}
	}
	if *applyLatestL2Fork == 0 {
		// Jovian at genesis
		return &l2ForkConfig{
			genesis:      opGenesisJovian,
			rollupConfig: opRollupConfigJovian,
			state:        opStateJovian,
			forkBlock:    nil,
		}
	}
	// Isthmus at genesis, jovian at block N
	return &l2ForkConfig{
		genesis:      opGenesisIsthmus,
		rollupConfig: opRollupConfigIsthmus,
		state:        opStateIsthmus,
		forkBlock:    applyLatestL2Fork,
	}
}

type ArtifactsBuilder struct {
	// Shared options
	prefundedAccounts []string

	// L1 options
	applyLatestL1Fork    bool
	genesisDelay         uint64
	l1BlockTimeInSeconds uint64

	// Op-stack options
	l2Enabled            bool
	applyLatestL2Fork    *uint64
	opBlockTimeInSeconds uint64
	predeploysFile       string
}

func NewArtifactsBuilder() *ArtifactsBuilder {
	return &ArtifactsBuilder{
		applyLatestL1Fork:    false,
		genesisDelay:         MinimumGenesisDelay,
		l1BlockTimeInSeconds: defaultL1BlockTimeSeconds,
		opBlockTimeInSeconds: defaultOpBlockTimeSeconds,
	}
}

func (b *ArtifactsBuilder) ApplyLatestL1Fork(applyLatestL1Fork bool) *ArtifactsBuilder {
	b.applyLatestL1Fork = applyLatestL1Fork
	return b
}

func (b *ArtifactsBuilder) ApplyLatestL2Fork(applyLatestL2Fork *uint64) *ArtifactsBuilder {
	b.applyLatestL2Fork = applyLatestL2Fork
	return b
}

func (b *ArtifactsBuilder) GenesisDelay(genesisDelaySeconds uint64) *ArtifactsBuilder {
	b.genesisDelay = genesisDelaySeconds
	return b
}

func (b *ArtifactsBuilder) L1BlockTime(blockTimeSeconds uint64) *ArtifactsBuilder {
	b.l1BlockTimeInSeconds = blockTimeSeconds
	return b
}

func (b *ArtifactsBuilder) WithL2() *ArtifactsBuilder {
	b.l2Enabled = true
	return b
}

func (b *ArtifactsBuilder) OpBlockTime(blockTimeSeconds uint64) *ArtifactsBuilder {
	b.opBlockTimeInSeconds = blockTimeSeconds
	return b
}

func (b *ArtifactsBuilder) PrefundedAccounts(accounts []string) *ArtifactsBuilder {
	b.prefundedAccounts = accounts
	return b
}

func (b *ArtifactsBuilder) PredeployFile(filePath string) *ArtifactsBuilder {
	b.predeploysFile = filePath
	return b
}

func (b *ArtifactsBuilder) loadPredeploys() (types.GenesisAlloc, error) {
	if b.predeploysFile == "" {
		return types.GenesisAlloc{}, nil
	}
	data, err := os.ReadFile(b.predeploysFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read predeploy file: %w", err)
	}
	var alloc types.GenesisAlloc
	if err := json.Unmarshal(data, &alloc); err != nil {
		return nil, fmt.Errorf("failed to parse predeploy JSON: %w", err)
	}
	return alloc, nil
}

var staticPrefundedAccounts = []string{
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

func (b *ArtifactsBuilder) getPrefundedAccounts() []string {
	if len(b.prefundedAccounts) > 0 {
		return append(append([]string{}, staticPrefundedAccounts...), b.prefundedAccounts...)
	}

	return staticPrefundedAccounts
}

func (b *ArtifactsBuilder) Build(out *output) error {
	defer utils.StartTimer("artifacts.builder")()

	if b.genesisDelay < MinimumGenesisDelay {
		log.Printf("genesis delay must be at least %d seconds, using %d", MinimumGenesisDelay, MinimumGenesisDelay)
		b.genesisDelay = MinimumGenesisDelay
	}

	// enable the latest fork in config.yaml or not
	var latestForkEpoch string
	if b.applyLatestL1Fork {
		latestForkEpoch = "0"
	} else {
		latestForkEpoch = "18446744073709551615"
	}
	clConfigContentStr := strings.Replace(string(clConfigContent), "{{.LatestForkEpoch}}", latestForkEpoch, 1)
	clConfigContentStr = strings.Replace(clConfigContentStr, "{{.SecondsPerSlot}}", strconv.FormatInt(int64(b.l1BlockTimeInSeconds), 10), 1)

	// load the config.yaml file
	clConfig, err := params.UnmarshalConfig([]byte(clConfigContentStr), nil)
	if err != nil {
		return err
	}
	if err := params.SetActive(clConfig); err != nil {
		return err
	}

	genesisTime := time.Now().Add(time.Duration(b.genesisDelay) * time.Second)
	config := params.BeaconConfig()

	gen := interop.GethTestnetGenesis(genesisTime, config)
	// HACK: fix this in prysm?
	gen.Config.DepositContractAddress = gethcommon.HexToAddress(config.DepositContractAddress)

	// add pre-funded accounts
	if err := appendPrefundedAccountsToAlloc(&gen.Alloc, b.getPrefundedAccounts()); err != nil {
		return err
	}

	// Apply Optimism pre-state
	var l2Fork *l2ForkConfig
	if b.l2Enabled {
		l2Fork = selectL2Fork(b.applyLatestL2Fork)
		opAllocs, err := readOptimismL1Allocs(l2Fork.state)
		if err != nil {
			return err
		}
		maps.Copy(gen.Alloc, opAllocs)
	}

	block := gen.ToBlock()
	slog.Info("Genesis block created", "hash", block.Hash())

	var v int
	if b.applyLatestL1Fork {
		v = version.Fulu
	} else {
		v = version.Electra
	}

	slog.Debug("Generating keys...")
	keys, err := keys.GetPregeneratedBLSKeys()
	if err != nil {
		return err
	}

	var (
		priv []bls.SecretKey
		pub  []bls.PublicKey
	)
	for _, key := range keys {
		priv = append(priv, key.Priv)
		pub = append(pub, key.Pub)
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

	slog.Debug("Writing artifacts...")
	err = out.WriteBatch(map[string]interface{}{
		"testnet/config.yaml":                 params.ConfigToYaml(config),
		"testnet/genesis.ssz":                 state,
		"genesis.json":                        gen,
		"jwtsecret":                           defaultJWTToken,
		"testnet/boot_enr.yaml":               "[]",
		"testnet/deploy_block.txt":            "0",
		"testnet/deposit_contract_block.txt":  "0",
		"testnet/genesis_validators_root.txt": hex.EncodeToString(state.GenesisValidatorsRoot()),
		"data_validator/":                     &lighthouseKeystore{privKeys: keys},
	})
	if err != nil {
		return err
	}
	slog.Debug("Done writing artifacts.")

	if b.l2Enabled {
		// We have to start slightly ahead of L1 genesis time
		opTimestamp := uint64(genesisTime.Unix()) + 2

		// Calculate fork time if activating jovian at a specific block
		var forkTime *uint64
		if l2Fork.forkBlock != nil {
			forkTime = new(uint64)
			*forkTime = opTimestamp + b.opBlockTimeInSeconds*(*l2Fork.forkBlock)
		}

		// Unmarshal the genesis to get the existing alloc (which contains predeploys)
		var originalGenesis core.Genesis
		if err := json.Unmarshal(l2Fork.genesis, &originalGenesis); err != nil {
			return fmt.Errorf("failed to unmarshal original opGenesis: %w", err)
		}

		// Update the allocs to include the same prefunded accounts as the L1 genesis,
		// while preserving the existing predeploys from the template
		allocs := originalGenesis.Alloc

		// Add custom predeploys, if any
		predeploys, err := b.loadPredeploys()
		if err != nil {
			return err
		}
		if err := appendPredeploysToAlloc(&allocs, predeploys); err != nil {
			return err
		}

		if err := appendPrefundedAccountsToAlloc(&allocs, b.getPrefundedAccounts()); err != nil {
			return err
		}

		// override l2 genesis, make the timestamp start 2 seconds after the L1 genesis
		input := map[string]interface{}{
			"timestamp": hexutil.Uint64(opTimestamp).String(),
			"alloc":     allocs,
		}
		if forkTime != nil {
			input["config"] = map[string]interface{}{
				"jovianTime": *forkTime,
			}
		}

		newOpGenesis, err := overrideJSON(l2Fork.genesis, input)
		if err != nil {
			return err
		}

		// the hash of the genesis has changed because of the timestamp so we need to account for that
		opGenesisBlock, err := toOpBlock(newOpGenesis)
		if err != nil {
			return fmt.Errorf("failed to convert opGenesis to block: %w", err)
		}

		opGenesisHash := opGenesisBlock.Hash()

		// override rollup.json with the real values for the L1 chain and the correct timestamp
		rollupInput := map[string]interface{}{
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
			"block_time": b.opBlockTimeInSeconds,
			"chain_op_config": map[string]interface{}{ // TODO: Read this from somewhere (genesis??)
				"eip1559Elasticity":        6,
				"eip1559Denominator":       50,
				"eip1559DenominatorCanyon": 250,
			},
		}
		if forkTime != nil {
			rollupInput["jovian_time"] = *forkTime
		}

		newOpRollup, err := overrideJSON(l2Fork.rollupConfig, rollupInput)
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

type OpGenesisTmplInput struct {
	Timestamp  uint64
	LatestFork *uint64
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
	result, err := json.MarshalIndent(original, "", " ")
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

func ConnectRaw(service, port, protocol, user string) string {
	return fmt.Sprintf(`{{Service "%s" "%s" "%s" "%s"}}`, service, port, protocol, user)
}

func ConnectEnode(service, id string) string {
	// Note, this assumes that all the enode ports are registered with the 'rpc' label.
	return ConnectRaw(service, "rpc", "enode", id)
}

func Connect(service, port string) string {
	return ConnectRaw(service, port, "http", "")
}

func ConnectWs(service, port string) string {
	return ConnectRaw(service, port, "ws", "")
}

type output struct {
	dst string

	homeDir string
	lock    sync.Mutex

	enodeAddrSeq *big.Int
}

func NewOutput(dst string) (*output, error) {
	homeDir, err := GetHomeDir()
	if err != nil {
		return nil, err
	}
	if dst == "" {
		// Use the $HOMEDIR/devnet as the default output
		dst = filepath.Join(homeDir, "devnet")
	}

	out := &output{dst: dst, homeDir: homeDir}

	// check if the output directory exists
	if out.Exists("") {
		if err := out.Remove(""); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func (o *output) Dst() string {
	return o.dst
}

func (o *output) Read(path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(o.dst, path))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (o *output) AbsoluteDstPath() (string, error) {
	return filepath.Abs(o.dst)
}

func (o *output) Exists(path string) bool {
	_, err := os.Stat(filepath.Join(o.dst))
	return err == nil
}

func (o *output) Remove(path string) error {
	return os.RemoveAll(filepath.Join(o.dst, path))
}

// CreateDir creates a new dir in the output folder and returns the
// absolute file path
func (o *output) CreateDir(path string) (string, error) {
	absPath, err := filepath.Abs(filepath.Join(o.dst, path))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absPath, 0o755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}
	return absPath, nil
}

func (o *output) CopyFile(src, dst string) error {
	// Open the source file
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	// Create the destination directory if it doesn't exist
	dstPath := filepath.Join(o.dst, dst)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
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

func (o *output) WriteDir(src string) error {
	return copy.Copy(src, o.dst)
}

func (o *output) LogOutput(name string) (*os.File, error) {
	// lock this because some services might be trying to access this in parallel
	o.lock.Lock()
	defer o.lock.Unlock()

	path := filepath.Join(o.dst, "logs", name+".log")

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, dataRaw, 0o644); err != nil {
		return err
	}
	return nil
}

type lighthouseKeystore struct {
	privKeys []*keys.Key
}

func (l *lighthouseKeystore) Encode(o *output) error {
	for _, key := range l.privKeys {
		pubKeyHex := "0x" + hex.EncodeToString(key.Pub.Marshal())

		if err := o.WriteBatch(map[string]interface{}{
			"validators/" + pubKeyHex + "/voting-keystore.json": key.Keystore,
			"secrets/" + pubKeyHex:                              keys.DefaultSecret,
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

func GetHomeDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("error getting user home directory: %w", err)
	}

	// if legacy .playground dir is present, remove it
	if err := os.RemoveAll(filepath.Join(homeDir, ".playground")); err != nil {
		return "", err
	}

	stateHomeDir := os.Getenv("XDG_STATE_HOME")
	if stateHomeDir == "" {
		stateHomeDir = filepath.Join(homeDir, ".local", "state")
	}

	// Define the path for our custom home directory
	customHomeDir := filepath.Join(stateHomeDir, "builder-playground")

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(customHomeDir, 0o755); err != nil {
		return "", fmt.Errorf("error creating output directory: %v", err)
	}

	return customHomeDir, nil
}

type EnodeAddr struct {
	PrivKey  *ecdsa.PrivateKey
	Artifact string
}

func (e *EnodeAddr) PrivKeyHex() string {
	return hex.EncodeToString(gethcommon.LeftPadBytes(e.PrivKey.D.Bytes(), 32))
}

func (e *EnodeAddr) NodeID() string {
	nodeid := fmt.Sprintf("%x", ecrypto.FromECDSAPub(&e.PrivKey.PublicKey)[1:])
	return nodeid
}

func (o *output) GetEnodeAddr() *EnodeAddr {
	// TODO: This is a bit enshrined here
	if o.enodeAddrSeq == nil {
		o.enodeAddrSeq = big.NewInt(0)
	}

	// always start with 1 since 0 is not a valid private key for an enode address
	o.enodeAddrSeq.Add(o.enodeAddrSeq, big.NewInt(1))
	privKeyBytes := gethcommon.LeftPadBytes(o.enodeAddrSeq.Bytes(), 32)

	privKey, err := ecrypto.ToECDSA(privKeyBytes)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to convert private key to ECDSA: %v", err))
	}

	privKeyBytesHex := hex.EncodeToString(privKeyBytes)

	// write the key to an artifact file
	fileName := fmt.Sprintf("enode-key-%d.txt", o.enodeAddrSeq.Int64())
	if err := o.WriteFile(fileName, privKeyBytesHex); err != nil {
		panic(fmt.Sprintf("BUG: failed to write enode key to artifact file: %v", err))
	}

	return &EnodeAddr{PrivKey: privKey, Artifact: fileName}
}

func readOptimismL1Allocs(opStateData []byte) (types.GenesisAlloc, error) {
	var state struct {
		L1StateDump string `json:"l1StateDump"`
	}
	if err := json.Unmarshal(opStateData, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal opState: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(state.L1StateDump)
	if err != nil {
		return nil, fmt.Errorf("failed to decode opState: %w", err)
	}

	// Create gzip reader from the base64 decoded data
	gr, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	// Read and decode the contents
	contents, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("failed to read opState: %w", err)
	}

	var alloc types.GenesisAlloc
	if err := json.Unmarshal(contents, &alloc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal opState: %w", err)
	}

	return alloc, nil
}

var prefundedBalance, _ = new(big.Int).SetString("10000000000000000000000", 16)

func appendPrefundedAccountsToAlloc(allocs *types.GenesisAlloc, privKeys []string) error {
	for _, privStr := range privKeys {
		priv, err := getPrivKey(privStr)
		if err != nil {
			return err
		}
		addr := ecrypto.PubkeyToAddress(priv.PublicKey)
		(*allocs)[addr] = types.Account{
			Balance: prefundedBalance,
			Nonce:   1,
		}
	}
	return nil
}

func appendPredeploysToAlloc(allocs *types.GenesisAlloc, predeploys types.GenesisAlloc) error {
	for addr, account := range predeploys {
		if _, exists := (*allocs)[addr]; exists {
			return fmt.Errorf("custom predeploy address %s conflicts with existing alloc entry in template genesis", addr.Hex())
		}
		(*allocs)[addr] = account
	}
	return nil
}
