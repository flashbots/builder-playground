package mevboostrelay

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/flashbots/go-boost-utils/bls"
	"github.com/flashbots/mev-boost-relay/beaconclient"
	"github.com/flashbots/mev-boost-relay/common"
	"github.com/flashbots/mev-boost-relay/database"
	"github.com/flashbots/mev-boost-relay/datastore"
	"github.com/flashbots/mev-boost-relay/services/api"
	"github.com/flashbots/mev-boost-relay/services/housekeeper"
	"github.com/sirupsen/logrus"
)

var defaultSecretKey = "5eae315483f028b5cdd5d1090ff0c7618b18737ea9bf3c35047189db22835c48"

type Config struct {
	ApiListenAddr    string
	ApiListenPort    uint64
	ApiSecretKey     string
	BeaconClientAddr string
	LogOutput        io.Writer
}

func DefaultConfig() *Config {
	return &Config{
		ApiListenAddr:    "127.0.0.1",
		ApiListenPort:    5555,
		ApiSecretKey:     defaultSecretKey,
		BeaconClientAddr: "http://localhost:3500",
		LogOutput:        os.Stdout,
	}
}

type MevBoostRelay struct {
	log            *logrus.Entry
	apiSrv         *api.RelayAPI
	housekeeperSrv *housekeeper.Housekeeper
}

func New(config *Config) (*MevBoostRelay, error) {
	log := common.LogSetup(false, "info")
	log.Logger.SetOutput(config.LogOutput)

	// connect to the beacon client
	bClient := beaconclient.NewMultiBeaconClient(log, []beaconclient.IBeaconInstance{
		beaconclient.NewProdBeaconInstance(log, config.BeaconClientAddr, config.BeaconClientAddr),
	})

	// wait until the beacon client is ready, otherwise, the api and housekeeper services
	// will fail at startup
	syncTimeoutCh := time.After(10 * time.Second)
	for {
		if _, err := bClient.BestSyncStatus(); err == nil {
			break
		}
		select {
		case <-syncTimeoutCh:
			return nil, fmt.Errorf("beacon client failed to start")
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
	log.Info("Beacon client synced")

	// get the spec and genesis info to compute the eth network details
	spec, err := getSpec(config.BeaconClientAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get spec: %w", err)
	}
	info, err := bClient.GetGenesis()
	if err != nil {
		return nil, fmt.Errorf("failed to get genesis: %w", err)
	}
	ethNetworkDetails, err := generateEthNetworkDetails(spec, info)
	if err != nil {
		return nil, fmt.Errorf("failed to generate eth network details: %w", err)
	}

	// start redis in-memory
	redis, err := startInMemoryRedisDatastore()
	if err != nil {
		return nil, fmt.Errorf("failed to start in-memory redis: %w", err)
	}

	// create the mockDB
	pqDB := newInmemoryDB()

	// datastore
	ds, err := datastore.NewDatastore(redis, nil, pqDB)
	if err != nil {
		log.WithError(err).Fatalf("Failed setting up prod datastore")
	}

	// Refresh the initial set of validators from the beacon node. This adds the validators
	// as known validators in the chain. (not registered yet).
	ds.RefreshKnownValidatorsWithoutChecks(log, bClient, 0)

	// start housekeeping service
	housekeeperOpts := &housekeeper.HousekeeperOpts{
		Log:          log.WithField("service", "housekeeper"),
		Redis:        redis,
		DB:           pqDB,
		BeaconClient: bClient,
	}

	housekeeperSrv := housekeeper.NewHousekeeper(housekeeperOpts)

	// start a mock block validation service that always
	// returns the blocks as valids.
	apiBlockSimURL, err := startMockBlockValidationServiceServer()
	if err != nil {
		return nil, fmt.Errorf("failed to start mock block validation service: %w", err)
	}
	log.Info("Started mock block validation service, addr: ", apiBlockSimURL)

	// decode the secret key
	envSkBytes, err := hex.DecodeString(strings.TrimPrefix(config.ApiSecretKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("incorrect secret key provided")
	}
	secretKey, err := bls.SecretKeyFromBytes(envSkBytes[:])
	if err != nil {
		return nil, fmt.Errorf("incorrect builder API secret key provided")
	}

	apiOpts := api.RelayAPIOpts{
		Log:             log.WithField("service", "api"),
		ListenAddr:      fmt.Sprintf("%s:%d", config.ApiListenAddr, config.ApiListenPort),
		BeaconClient:    bClient,
		Datastore:       ds,
		Redis:           redis,
		DB:              pqDB,
		SecretKey:       secretKey,
		EthNetDetails:   *ethNetworkDetails,
		BlockSimURL:     apiBlockSimURL,
		ProposerAPI:     true,
		BlockBuilderAPI: true,
		DataAPI:         true,
	}
	apiSrv, err := api.NewRelayAPI(apiOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create service")
	}

	return &MevBoostRelay{
		log:            log,
		apiSrv:         apiSrv,
		housekeeperSrv: housekeeperSrv,
	}, nil
}

func (m *MevBoostRelay) Start() error {
	errChan := make(chan error, 2)

	m.log.Info("Starting housekeeper service...")
	go func() {
		err := m.housekeeperSrv.Start()
		m.log.WithError(err).Error("Housekeeper service stopped")
		errChan <- err
	}()

	m.log.Info("Starting API service...")
	go func() {
		err := m.apiSrv.StartServer()
		m.log.WithError(err).Error("API service stopped")
		errChan <- err
	}()

	go func() {
		// We only require to do this at startup once, because otherwise we will
		// just keep with the normal workflow of the mev-boost-relay.
		<-m.apiSrv.ValidatorUpdateCh()

		m.log.Info("Forcing validator registration at startup")

		m.housekeeperSrv.UpdateProposerDutiesWithoutChecks(0)
		m.apiSrv.UpdateProposerDutiesWithoutChecks(0)
	}()

	err := <-errChan
	return err
}

func generateEthNetworkDetails(spec *Spec, info *beaconclient.GetGenesisResponse) (*common.EthNetworkDetails, error) {
	envs := map[string]string{
		"GENESIS_FORK_VERSION":    info.Data.GenesisForkVersion,
		"GENESIS_VALIDATORS_ROOT": info.Data.GenesisValidatorsRoot,
		"BELLATRIX_FORK_VERSION":  spec.BellatrixForkVersion,
		"CAPELLA_FORK_VERSION":    spec.CapellaForkVersion,
		"DENEB_FORK_VERSION":      spec.DenebForkVersion,
	}
	for k, v := range envs {
		if err := os.Setenv(k, v); err != nil {
			return nil, fmt.Errorf("failed to set env var %s: %w", k, err)
		}
	}

	netDetails, err := common.NewEthNetworkDetails("custom")
	if err != nil {
		return nil, err
	}
	return netDetails, nil
}

func startInMemoryRedisDatastore() (*datastore.RedisCache, error) {
	redisTestServer, err := miniredis.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to start miniredis: %w", err)
	}
	redisService, err := datastore.NewRedisCache("", redisTestServer.Addr(), "")
	if err != nil {
		return nil, fmt.Errorf("failed to create redis cache: %w", err)
	}
	return redisService, nil
}

var emptyResponse = `{
	"jsonrpc": "2.0",
	"id": 1,
	"result": null
}`

func startMockBlockValidationServiceServer() (string, error) {
	// Generate a random port number between 10000 and 65535 (how likely is this?)
	rand.Seed(time.Now().UnixNano())
	port := rand.Intn(55536) + 10000

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return "", err
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, emptyResponse)
	})

	go func() {
		if err := http.Serve(listener, nil); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	addr := fmt.Sprintf("http://localhost:%d", port)
	return addr, nil
}

// inmemoryDB is an extension of the MockDB that stores the validator registry entries in memory.
type inmemoryDB struct {
	*database.MockDB

	validatorRegistryEntriesLock sync.Mutex
	validatorRegistryEntries     map[string]*database.ValidatorRegistrationEntry

	deliveredPayloadsLock sync.Mutex
	deliveredPayloads     []*database.DeliveredPayloadEntry
}

func newInmemoryDB() *inmemoryDB {
	return &inmemoryDB{
		MockDB:                   &database.MockDB{},
		validatorRegistryEntries: make(map[string]*database.ValidatorRegistrationEntry),
		deliveredPayloads:        make([]*database.DeliveredPayloadEntry, 0),
	}
}

// -- endpoints for the validator registry ---

func (i *inmemoryDB) NumRegisteredValidators() (count uint64, err error) {
	return uint64(len(i.validatorRegistryEntries)), nil
}

func (i *inmemoryDB) SaveValidatorRegistration(entry database.ValidatorRegistrationEntry) error {
	i.validatorRegistryEntriesLock.Lock()
	defer i.validatorRegistryEntriesLock.Unlock()

	i.validatorRegistryEntries[entry.Pubkey] = &entry
	return nil
}

func (i *inmemoryDB) GetLatestValidatorRegistrations(timestampOnly bool) ([]*database.ValidatorRegistrationEntry, error) {
	i.validatorRegistryEntriesLock.Lock()
	defer i.validatorRegistryEntriesLock.Unlock()

	entries := make([]*database.ValidatorRegistrationEntry, 0, len(i.validatorRegistryEntries))
	for _, entry := range i.validatorRegistryEntries {
		entries = append(entries, entry)
	}
	return entries, nil
}

func (i *inmemoryDB) GetValidatorRegistration(pubkey string) (*database.ValidatorRegistrationEntry, error) {
	i.validatorRegistryEntriesLock.Lock()
	defer i.validatorRegistryEntriesLock.Unlock()

	entry, found := i.validatorRegistryEntries[pubkey]
	if !found {
		return nil, fmt.Errorf("validator registration not found")
	}
	return entry, nil
}

func (i *inmemoryDB) GetValidatorRegistrationsForPubkeys(pubkeys []string) ([]*database.ValidatorRegistrationEntry, error) {
	i.validatorRegistryEntriesLock.Lock()
	defer i.validatorRegistryEntriesLock.Unlock()

	entries := make([]*database.ValidatorRegistrationEntry, 0, len(pubkeys))
	for _, pubkey := range pubkeys {
		entry, found := i.validatorRegistryEntries[pubkey]
		if found {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// -- endpoints for the delivered payloads ---

func (i *inmemoryDB) SaveDeliveredPayload(bidTrace *common.BidTraceV2WithBlobFields, signedBlindedBeaconBlock *common.VersionedSignedBlindedBeaconBlock, signedAt time.Time, publishMs uint64) error {
	i.deliveredPayloadsLock.Lock()
	defer i.deliveredPayloadsLock.Unlock()

	_signedBlindedBeaconBlock, err := json.Marshal(signedBlindedBeaconBlock)
	if err != nil {
		return err
	}

	deliveredPayloadEntry := database.DeliveredPayloadEntry{
		SignedAt:                 database.NewNullTime(signedAt),
		SignedBlindedBeaconBlock: database.NewNullString(string(_signedBlindedBeaconBlock)),

		Slot:  bidTrace.Slot,
		Epoch: bidTrace.Slot / common.SlotsPerEpoch,

		BuilderPubkey:        bidTrace.BuilderPubkey.String(),
		ProposerPubkey:       bidTrace.ProposerPubkey.String(),
		ProposerFeeRecipient: bidTrace.ProposerFeeRecipient.String(),

		ParentHash:  bidTrace.ParentHash.String(),
		BlockHash:   bidTrace.BlockHash.String(),
		BlockNumber: bidTrace.BlockNumber,

		GasUsed:  bidTrace.GasUsed,
		GasLimit: bidTrace.GasLimit,

		NumTx: bidTrace.NumTx,
		Value: bidTrace.Value.ToBig().String(),

		NumBlobs:      bidTrace.NumBlobs,
		BlobGasUsed:   bidTrace.BlobGasUsed,
		ExcessBlobGas: bidTrace.ExcessBlobGas,

		PublishMs: publishMs,
	}

	i.deliveredPayloads = append(i.deliveredPayloads, &deliveredPayloadEntry)
	return nil
}

func (i *inmemoryDB) GetNumDeliveredPayloads() (uint64, error) {
	i.deliveredPayloadsLock.Lock()
	defer i.deliveredPayloadsLock.Unlock()

	return uint64(len(i.deliveredPayloads)), nil
}

func (i *inmemoryDB) GetRecentDeliveredPayloads(filters database.GetPayloadsFilters) ([]*database.DeliveredPayloadEntry, error) {
	i.deliveredPayloadsLock.Lock()
	defer i.deliveredPayloadsLock.Unlock()

	entries := []*database.DeliveredPayloadEntry{}
	for _, entry := range i.deliveredPayloads {
		filtered := filterPayload(entry, filters)
		if !filtered {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func filterPayload(entry *database.DeliveredPayloadEntry, filter database.GetPayloadsFilters) bool {
	if filter.BlockNumber != 0 {
		if entry.BlockNumber != uint64(filter.BlockNumber) {
			return true
		}
	}

	if filter.BuilderPubkey != "" {
		if entry.BuilderPubkey != filter.BuilderPubkey {
			return true
		}
	}

	return false
}

type Spec struct {
	SecondsPerSlot                  uint64 `json:"SECONDS_PER_SLOT,string"`            //nolint:tagliatelle
	DepositContractAddress          string `json:"DEPOSIT_CONTRACT_ADDRESS"`           //nolint:tagliatelle
	DepositNetworkID                string `json:"DEPOSIT_NETWORK_ID"`                 //nolint:tagliatelle
	DomainAggregateAndProof         string `json:"DOMAIN_AGGREGATE_AND_PROOF"`         //nolint:tagliatelle
	InactivityPenaltyQuotient       string `json:"INACTIVITY_PENALTY_QUOTIENT"`        //nolint:tagliatelle
	InactivityPenaltyQuotientAltair string `json:"INACTIVITY_PENALTY_QUOTIENT_ALTAIR"` //nolint:tagliatelle
	BellatrixForkVersion            string `json:"BELLATRIX_FORK_VERSION"`             //nolint:tagliatelle
	CapellaForkVersion              string `json:"CAPELLA_FORK_VERSION"`               //nolint:tagliatelle
	DenebForkVersion                string `json:"DENEB_FORK_VERSION"`                 //nolint:tagliatelle
}

func getSpec(beaconURL string) (*Spec, error) {
	uri := fmt.Sprintf("%s/eth/v1/config/spec", beaconURL)

	resp, err := http.Get(uri)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var spec struct {
		Data *Spec `json:"data"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	return spec.Data, nil
}
