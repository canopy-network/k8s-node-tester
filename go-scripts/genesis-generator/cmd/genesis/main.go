package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/canopy-network/canopy/fsm"
	"github.com/canopy-network/canopy/lib"
	"github.com/canopy-network/canopy/lib/crypto"
	"github.com/launchdarkly/go-jsonstream/v3/jwriter"
	"gopkg.in/yaml.v3"
)

var nickNames = make(chan string, 1000)

const (
	validatorNick = "validator"
	delegatorNick = "delegator"
	accountNick   = "account"
	fullNodeNick  = "fullnode"
)

// GeneralConfig holds general configuration
type GeneralConfig struct {
	Concurrency      int64  `yaml:"concurrency"`
	Password         string `yaml:"password"`
	Buffer           int    `yaml:"buffer"`
	NetAddressSuffix string `yaml:"netAddressSuffix"`
	JsonBeautify     bool   `yaml:"jsonBeautify"`
}

// NodesConfig holds the total node count
type NodesConfig struct {
	Count int `yaml:"count"`
}

// ValidatorsConfig holds validator-specific configuration
type ValidatorsConfig struct {
	Count        int    `yaml:"count"`
	StakedAmount uint64 `yaml:"stakedAmount"`
	Amount       uint64 `yaml:"amount"`
}

// FullNodesConfig holds full node-specific configuration
type FullNodesConfig struct {
	Count  int    `yaml:"count"`
	Amount uint64 `yaml:"amount"`
}

// AccountsConfig holds account-specific configuration
type AccountsConfig struct {
	Count  int    `yaml:"count"`
	Amount uint64 `yaml:"amount"`
}

// DelegatorsConfig holds delegator-specific configuration
type DelegatorsConfig struct {
	Count        int    `yaml:"count"`
	StakedAmount uint64 `yaml:"stakedAmount"`
	Amount       uint64 `yaml:"amount"`
}

// CommitteeAssignment defines cross-chain committee participation
type CommitteeAssignment struct {
	ID             int `yaml:"id"`
	ValidatorCount int `yaml:"validatorCount"`
	DelegatorCount int `yaml:"delegatorCount"`
}

// ChainConfig represents a single chain's configuration
type ChainConfig struct {
	ID         int                   `yaml:"id"`
	RootChain  int                   `yaml:"rootChain"`
	Validators ValidatorsConfig      `yaml:"validators"`
	FullNodes  FullNodesConfig       `yaml:"fullNodes"`
	Accounts   AccountsConfig        `yaml:"accounts"`
	Delegators DelegatorsConfig      `yaml:"delegators"`
	Committees []CommitteeAssignment `yaml:"committees"`
}

// AppConfig represents the configuration structure
type AppConfig struct {
	General GeneralConfig           `yaml:"general"`
	Nodes   NodesConfig             `yaml:"nodes"`
	Chains  map[string]*ChainConfig `yaml:"chains"`
}

// NodeIdentity represents a node's identity for ids.json
type NodeIdentity struct {
	ID              int      `json:"id"`
	ChainID         int      `json:"chainId"`
	RootChainID     int      `json:"rootChainId"`
	RootChainNode   *int     `json:"rootChainNode,omitempty"` // nil for delegators (they're not physical nodes)
	Address         string   `json:"address"`
	PublicKey       string   `json:"publicKey"`
	PrivateKey      string   `json:"privateKey"`
	NodeType        string   `json:"nodeType"`
	Committees      []uint64 `json:"-"` // Not exported to JSON, used internally
	PrivateKeyBytes []byte   `json:"-"` // Not exported to JSON, used for keystore
	StakedAmount    uint64   `json:"-"` // Not exported to JSON, used for genesis
	Amount          uint64   `json:"-"` // Not exported to JSON, used for genesis
	IsDelegate      bool     `json:"-"` // Not exported to JSON, used for genesis
	NetAddress      string   `json:"-"` // Not exported to JSON, used for genesis
}

// IdsFile represents the structure of ids.json
type IdsFile struct {
	Keys map[string]NodeIdentity `json:"keys"`
}

const configFile = "../../configs.yaml"

func loadConfigs() (map[string]*AppConfig, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file '%s': %w", configFile, err)
	}

	configs := make(map[string]*AppConfig)
	if err := yaml.Unmarshal(data, &configs); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return configs, nil
}

func getConfig(name string) (*AppConfig, error) {
	configs, err := loadConfigs()
	if err != nil {
		return nil, err
	}

	config, exists := configs[strings.ToLower(name)]
	if !exists {
		availableConfigs := make([]string, 0, len(configs))
		for k := range configs {
			availableConfigs = append(availableConfigs, k)
		}
		return nil, fmt.Errorf("unknown config '%s'. Available configs: %s", name, strings.Join(availableConfigs, ", "))
	}
	return config, nil
}

func listAvailableConfigs() []string {
	configs, err := loadConfigs()
	if err != nil {
		return []string{}
	}
	availableConfigs := make([]string, 0, len(configs))
	for k := range configs {
		availableConfigs = append(availableConfigs, k)
	}
	return availableConfigs
}

// validateConfig checks that the sum of all validators, delegators, and full nodes equals nodes.count
// Multi-committee validators (not delegators) count once per committee they participate in
func validateConfig(cfg *AppConfig) error {
	totalNodes := 0
	for chainName, chainCfg := range cfg.Chains {
		// Base count: validators + full nodes (delegators don't count as physical nodes)
		baseNodes := chainCfg.Validators.Count + chainCfg.FullNodes.Count

		// Count additional entries from cross-chain committee assignments
		// Each validator assigned to another chain's committee creates an additional ids.json entry
		crossChainExpansions := 0
		for _, ca := range chainCfg.Committees {
			crossChainExpansions += ca.ValidatorCount
		}

		chainNodes := baseNodes + crossChainExpansions
		totalNodes += chainNodes

		if crossChainExpansions > 0 {
			fmt.Printf("  Chain %s: %d validators + %d full nodes + %d cross-chain expansions = %d entries (+ %d delegators)\n",
				chainName, chainCfg.Validators.Count, chainCfg.FullNodes.Count, crossChainExpansions, chainNodes, chainCfg.Delegators.Count)
		} else {
			fmt.Printf("  Chain %s: %d validators + %d full nodes = %d entries (+ %d delegators)\n",
				chainName, chainCfg.Validators.Count, chainCfg.FullNodes.Count, chainNodes, chainCfg.Delegators.Count)
		}
	}

	if totalNodes != cfg.Nodes.Count {
		return fmt.Errorf("node count mismatch: total entries including cross-chain expansions (%d) does not equal nodes.count (%d)",
			totalNodes, cfg.Nodes.Count)
	}

	fmt.Printf("  Total entries: %d (matches nodes.count: %d) ✓\n", totalNodes, cfg.Nodes.Count)
	return nil
}

// validateCommitteeAssignments checks that committee assignments don't exceed available validators/delegators
// and that committee IDs reference valid chain IDs
func validateCommitteeAssignments(cfg *AppConfig) error {
	// Build a set of valid chain IDs
	validChainIDs := make(map[int]string) // map from chain ID to chain name
	for chainName, chainCfg := range cfg.Chains {
		validChainIDs[chainCfg.ID] = chainName
	}

	// Validate that at least one root chain has validators (delegators don't count as physical nodes)
	rootChainValidatorCount := 0
	for _, chainCfg := range cfg.Chains {
		if chainCfg.ID == chainCfg.RootChain {
			// This is a root chain - only count validators
			rootChainValidatorCount += chainCfg.Validators.Count
		}
	}
	if rootChainValidatorCount == 0 {
		return fmt.Errorf("no validators found on any root chain; at least one root chain must have validators for rootChainNode assignment")
	}
	fmt.Printf("  Root chain validators: %d ✓\n", rootChainValidatorCount)

	for chainName, chainCfg := range cfg.Chains {
		for _, ca := range chainCfg.Committees {
			// Validate committee ID exists as a chain ID
			if _, exists := validChainIDs[ca.ID]; !exists {
				return fmt.Errorf("chain %s: committee ID %d does not match any chain ID (available chain IDs: %v)",
					chainName, ca.ID, getChainIDs(cfg))
			}

			if ca.ValidatorCount > chainCfg.Validators.Count {
				return fmt.Errorf("chain %s: committee %d validatorCount (%d) exceeds total validators (%d)",
					chainName, ca.ID, ca.ValidatorCount, chainCfg.Validators.Count)
			}
			if ca.DelegatorCount > chainCfg.Delegators.Count {
				return fmt.Errorf("chain %s: committee %d delegatorCount (%d) exceeds total delegators (%d)",
					chainName, ca.ID, ca.DelegatorCount, chainCfg.Delegators.Count)
			}
			fmt.Printf("  Chain %s: committee %d assignment - %d validators, %d delegators ✓\n",
				chainName, ca.ID, ca.ValidatorCount, ca.DelegatorCount)
		}
	}
	return nil
}

// getChainIDs returns a slice of all chain IDs in the config
func getChainIDs(cfg *AppConfig) []int {
	ids := make([]int, 0, len(cfg.Chains))
	for _, chainCfg := range cfg.Chains {
		ids = append(ids, chainCfg.ID)
	}
	sort.Ints(ids)
	return ids
}

func logData() {
	var accounts, validators, delegators, fullNodes int32

	go func() {
		for nickname := range nickNames {
			switch nickname {
			case accountNick:
				atomic.AddInt32(&accounts, 1)
			case validatorNick:
				atomic.AddInt32(&validators, 1)
			case delegatorNick:
				atomic.AddInt32(&delegators, 1)
			case fullNodeNick:
				atomic.AddInt32(&fullNodes, 1)
			default:
				fmt.Println("Unknown data type received:", nickname)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(2 * time.Second)

		for range ticker.C {
			fmt.Printf("Accounts: %d, Validators: %d, Delegators: %d, FullNodes: %d\n",
				atomic.LoadInt32(&accounts),
				atomic.LoadInt32(&validators),
				atomic.LoadInt32(&delegators),
				atomic.LoadInt32(&fullNodes),
			)
		}
	}()
}

func mustCreateKey() crypto.PrivateKeyI {
	pk, err := crypto.NewBLS12381PrivateKey()
	if err != nil {
		panic(err)
	}

	return pk
}

// addAccounts concurrently creates keys and accounts
func addAccounts(count int, amount uint64, wg *sync.WaitGroup, semaphoreChan chan struct{}, accountChan chan *fsm.Account) {
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			semaphoreChan <- struct{}{}
			defer func() { <-semaphoreChan }()

			addrStr := fmt.Sprintf("%020x", i)

			accountChan <- &fsm.Account{
				Address: []byte(addrStr),
				Amount:  amount,
			}
			nickNames <- accountNick
		}(i)
	}
}

// addFullNodes concurrently creates full nodes (not staked, but with identities)
func addFullNodes(count int, amount uint64, startIdx int, chainID int, rootChainID int,
	identities *[]NodeIdentity, gsync *sync.Mutex, wg *sync.WaitGroup, semaphoreChan chan struct{},
	accountChan chan *fsm.Account) {

	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			semaphoreChan <- struct{}{}
			defer func() { <-semaphoreChan }()

			pk := mustCreateKey()

			accountChan <- &fsm.Account{
				Address: pk.PublicKey().Address().Bytes(),
				Amount:  amount,
			}

			identity := NodeIdentity{
				ID:              startIdx + i,
				ChainID:         chainID,
				RootChainID:     rootChainID,
				Address:         hex.EncodeToString(pk.PublicKey().Address().Bytes()),
				PublicKey:       hex.EncodeToString(pk.PublicKey().Bytes()),
				PrivateKey:      hex.EncodeToString(pk.Bytes()),
				NodeType:        "fullnode",
				PrivateKeyBytes: pk.Bytes(),
			}

			gsync.Lock()
			*identities = append(*identities, identity)
			gsync.Unlock()

			nickNames <- fullNodeNick
		}(i)
	}
}

// addValidators concurrently creates validators and delegators
// committeeAssignments maps validator index to additional committees they participate in
func addValidators(count int, isDelegate bool, startIdx int, stakedAmount uint64, amount uint64,
	chainID int, rootChainID int, committeeAssignments map[int][]uint64, netAddressSuffix string,
	identities *[]NodeIdentity, gsync *sync.Mutex, wg *sync.WaitGroup, semaphoreChan chan struct{},
	accountChan chan *fsm.Account) {

	nodeType := "validator"
	if isDelegate {
		nodeType = "delegator"
	}

	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			semaphoreChan <- struct{}{}
			defer func() { <-semaphoreChan }()

			pk := mustCreateKey()

			// Base committee is the chain's own ID
			committees := []uint64{uint64(chainID)}

			// Add additional committee assignments if any
			if additionalCommittees, ok := committeeAssignments[i]; ok {
				committees = append(committees, additionalCommittees...)
			}

			netAddress := fmt.Sprintf("tcp://node-%d%s", startIdx+i, netAddressSuffix)

			accountChan <- &fsm.Account{
				Address: pk.PublicKey().Address().Bytes(),
				Amount:  amount,
			}

			identity := NodeIdentity{
				ID:              startIdx + i,
				ChainID:         chainID,
				RootChainID:     rootChainID,
				Address:         hex.EncodeToString(pk.PublicKey().Address().Bytes()),
				PublicKey:       hex.EncodeToString(pk.PublicKey().Bytes()),
				PrivateKey:      hex.EncodeToString(pk.Bytes()),
				NodeType:        nodeType,
				Committees:      committees,
				PrivateKeyBytes: pk.Bytes(),
				StakedAmount:    stakedAmount,
				Amount:          amount,
				IsDelegate:      isDelegate,
				NetAddress:      netAddress,
			}

			gsync.Lock()
			*identities = append(*identities, identity)
			gsync.Unlock()

			if isDelegate {
				nickNames <- delegatorNick
			} else {
				nickNames <- validatorNick
			}
		}(i)
	}
}

func mustSetDirectory(dir string) {
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
}

func mustDeleteInDirectory(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}

	for _, entry := range entries {
		err := os.RemoveAll(filepath.Join(dir, entry.Name()))
		if err != nil {
			panic(err)
		}
	}
}

func mustSaveAsJSON(filename string, data any) {
	file, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	err = encoder.Encode(data)
	if err != nil {
		panic(err)
	}
}

func accountsWriter(chainDir string, accountLen int, wg *sync.WaitGroup, accountChan chan *fsm.Account) {
	defer wg.Done()

	accountsFile, err := os.Create(filepath.Join(chainDir, "accounts.json"))
	if err != nil {
		panic(err)
	}
	defer accountsFile.Close()

	writer := jwriter.NewStreamingWriter(accountsFile, 1024)

	arr := writer.Array()
	for range accountLen {
		account := <-accountChan
		accountObj := writer.Object()
		accountObj.Name("address").String(hex.EncodeToString(account.Address))
		accountObj.Name("amount").Int(int(account.Amount))
		accountObj.End()
	}
	arr.End()

	if err := writer.Flush(); err != nil {
		panic(err)
	}
}

// writeGenesisFromIdentities writes genesis.json for a specific chain using identities
// For validators from other chains (cross-chain), only include this chain's committee
func writeGenesisFromIdentities(chainDir string, chainID int, rootChainID int, validators []NodeIdentity, accountsPath string) {
	genesisFile, err := os.Create(filepath.Join(chainDir, "genesis.json"))
	if err != nil {
		panic(err)
	}
	defer genesisFile.Close()

	writer := jwriter.NewStreamingWriter(genesisFile, 1024)

	obj := writer.Object()
	obj.Name("time").String("2024-12-14 20:10:52")

	obj.Name("validators")
	arr := writer.Array()
	for _, v := range validators {
		// Determine which committees to include in this genesis
		var committeesForGenesis []uint64
		if v.ChainID == chainID {
			// Native validator: include all their committees
			committeesForGenesis = v.Committees
		} else {
			// Cross-chain validator: only include this chain's committee
			committeesForGenesis = []uint64{uint64(chainID)}
		}

		addressBytes, _ := hex.DecodeString(v.Address)

		validatorObj := writer.Object()
		validatorObj.Name("address").String(v.Address)
		validatorObj.Name("publicKey").String(v.PublicKey)
		validatorObj.Name("committees")
		cArr := writer.Array()
		for _, committee := range committeesForGenesis {
			writer.Int(int(committee))
		}
		cArr.End()
		// Delegators don't have netAddress (they're not physical servers)
		if !v.IsDelegate {
			validatorObj.Name("netAddress").String(v.NetAddress)
		}
		validatorObj.Name("stakedAmount").Int(int(v.StakedAmount))
		validatorObj.Name("output").String(hex.EncodeToString(addressBytes))
		validatorObj.Name("delegate").Bool(v.IsDelegate)
		validatorObj.End()
	}
	arr.End()

	rawAccounts, err := os.ReadFile(accountsPath)
	if err != nil {
		panic(err)
	}
	obj.Name("accounts").Raw(rawAccounts)

	remainingFields := map[string]interface{}{
		"params": &fsm.Params{
			Consensus: &fsm.ConsensusParams{
				BlockSize:       1000000,
				ProtocolVersion: "1/0",
				RootChainId:     uint64(rootChainID),
				Retired:         0,
			},
			Validator: &fsm.ValidatorParams{
				UnstakingBlocks:                    2,
				MaxPauseBlocks:                     4380,
				DoubleSignSlashPercentage:          10,
				NonSignSlashPercentage:             1,
				MaxNonSign:                         4,
				NonSignWindow:                      10,
				MaxCommittees:                      15,
				MaxCommitteeSize:                   100,
				EarlyWithdrawalPenalty:             20,
				DelegateUnstakingBlocks:            2,
				MinimumOrderSize:                   1000,
				StakePercentForSubsidizedCommittee: 33,
				MaxSlashPerCommittee:               15,
				DelegateRewardPercentage:           10,
				BuyDeadlineBlocks:                  15,
				LockOrderFeeMultiplier:             2,
			},
			Fee: &fsm.FeeParams{
				SendFee:            10000,
				StakeFee:           10000,
				EditStakeFee:       10000,
				UnstakeFee:         10000,
				PauseFee:           10000,
				UnpauseFee:         10000,
				ChangeParameterFee: 10000,
				DaoTransferFee:     10000,
				SubsidyFee:         10000,
				CreateOrderFee:     10000,
				EditOrderFee:       10000,
				DeleteOrderFee:     10000,
			},
			Governance: &fsm.GovernanceParams{
				DaoRewardPercentage: 10,
			},
		},
	}

	for key, value := range remainingFields {
		obj.Name(key)
		data, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		writer.Raw(json.RawMessage(data))
	}

	obj.End()

	if err := writer.Flush(); err != nil {
		panic(err)
	}
}

func createTemplateConfig(chainID int, rootChainID int) *lib.Config {
	var rootChain []lib.RootChain

	if chainID == rootChainID {
		// Root chain: single entry with ROOT_NODE_ID
		rootChain = []lib.RootChain{
			{
				ChainId: uint64(chainID),
				Url:     "ROOT_NODE_ID",
			},
		}
	} else {
		// Nested chain: two entries - own chain with NODE_ID, root chain with ROOT_NODE_ID
		rootChain = []lib.RootChain{
			{
				ChainId: uint64(chainID),
				Url:     "NODE_ID",
			},
			{
				ChainId: uint64(rootChainID),
				Url:     "ROOT_NODE_ID",
			},
		}
	}

	return &lib.Config{
		MainConfig: lib.MainConfig{
			LogLevel:  "debug",
			ChainId:   uint64(chainID),
			RootChain: rootChain,
			RunVDF:    false,
		},
		RPCConfig: lib.RPCConfig{
			WalletPort:   "50000",
			ExplorerPort: "50001",
			RPCPort:      "50002",
			AdminPort:    "50003",
			RPCUrl:       "http://0.0.0.0:50002",
			AdminRPCUrl:  "http://0.0.0.0:50003",
			TimeoutS:     3,
		},
		StoreConfig: lib.StoreConfig{
			DataDirPath: "/root/.canopy",
			DBName:      "canopy",
			InMemory:    false,
		},
		P2PConfig: lib.P2PConfig{
			NetworkID:       1,
			ListenAddress:   fmt.Sprintf("0.0.0.0:%d", 9000+chainID),
			ExternalAddress: "NODE_ID",
			MaxInbound:      21,
			MaxOutbound:     7,
			TrustedPeerIDs:  nil,
			DialPeers:       []string{"DIAL_PEER"},
			BannedPeerIDs:   nil,
			BannedIPs:       nil,
		},
		ConsensusConfig: lib.ConsensusConfig{
			ElectionTimeoutMS:       2000,
			ElectionVoteTimeoutMS:   3000,
			ProposeTimeoutMS:        3000,
			ProposeVoteTimeoutMS:    2000,
			PrecommitTimeoutMS:      2000,
			PrecommitVoteTimeoutMS:  2000,
			CommitTimeoutMS:         6000,
			RoundInterruptTimeoutMS: 2000,
		},
		MempoolConfig: lib.MempoolConfig{
			MaxTotalBytes:       1000000,
			MaxTransactionCount: 5000,
			IndividualMaxTxSize: 4000,
			DropPercentage:      35,
		},
		MetricsConfig: lib.MetricsConfig{
			MetricsEnabled:    true,
			PrometheusAddress: "0.0.0.0:9090",
		},
	}
}

// generateChainIdentities generates all identities for a chain (validators, delegators, fullnodes)
// Returns the identities and accounts for this chain
func generateChainIdentities(chainName string, chainCfg *ChainConfig, startIdx int, buffer int, netAddressSuffix string,
	semaphoreChan chan struct{}) ([]NodeIdentity, []*fsm.Account) {

	fmt.Printf("Generating identities for chain: %s (ID: %d, RootChain: %d)\n", chainName, chainCfg.ID, chainCfg.RootChain)

	chainIdentities := make([]NodeIdentity, 0, chainCfg.Validators.Count+chainCfg.Delegators.Count+chainCfg.FullNodes.Count)
	var chainSync sync.Mutex
	var wg sync.WaitGroup

	accountChan := make(chan *fsm.Account, buffer)
	accounts := make([]*fsm.Account, 0, chainCfg.Delegators.Count+chainCfg.Validators.Count+chainCfg.FullNodes.Count+chainCfg.Accounts.Count)
	var accountSync sync.Mutex

	// Collect accounts from channel
	go func() {
		for acc := range accountChan {
			accountSync.Lock()
			accounts = append(accounts, acc)
			accountSync.Unlock()
		}
	}()

	// Build committee assignments for validators
	validatorCommitteeAssignments := make(map[int][]uint64)
	for _, ca := range chainCfg.Committees {
		for i := 0; i < ca.ValidatorCount && i < chainCfg.Validators.Count; i++ {
			validatorCommitteeAssignments[i] = append(validatorCommitteeAssignments[i], uint64(ca.ID))
		}
	}

	// Build committee assignments for delegators
	delegatorCommitteeAssignments := make(map[int][]uint64)
	for _, ca := range chainCfg.Committees {
		for i := 0; i < ca.DelegatorCount && i < chainCfg.Delegators.Count; i++ {
			delegatorCommitteeAssignments[i] = append(delegatorCommitteeAssignments[i], uint64(ca.ID))
		}
	}

	// Assign unique idx within this chain
	validatorStartIdx := startIdx
	delegatorStartIdx := validatorStartIdx + chainCfg.Validators.Count
	fullNodeStartIdx := delegatorStartIdx + chainCfg.Delegators.Count

	addValidators(chainCfg.Validators.Count, false, validatorStartIdx, chainCfg.Validators.StakedAmount, chainCfg.Validators.Amount,
		chainCfg.ID, chainCfg.RootChain, validatorCommitteeAssignments, netAddressSuffix,
		&chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)
	addValidators(chainCfg.Delegators.Count, true, delegatorStartIdx, chainCfg.Delegators.StakedAmount, chainCfg.Delegators.Amount,
		chainCfg.ID, chainCfg.RootChain, delegatorCommitteeAssignments, netAddressSuffix,
		&chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)
	addFullNodes(chainCfg.FullNodes.Count, chainCfg.FullNodes.Amount, fullNodeStartIdx, chainCfg.ID, chainCfg.RootChain,
		&chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)
	addAccounts(chainCfg.Accounts.Count, chainCfg.Accounts.Amount, &wg, semaphoreChan, accountChan)

	wg.Wait()
	close(accountChan)

	// Sort chain identities by ID
	sort.Slice(chainIdentities, func(i, j int) bool {
		return chainIdentities[i].ID < chainIdentities[j].ID
	})

	fmt.Printf("Chain %s: %d validators, %d delegators, %d full nodes, %d accounts\n",
		chainName, chainCfg.Validators.Count, chainCfg.Delegators.Count, chainCfg.FullNodes.Count, chainCfg.Accounts.Count)

	return chainIdentities, accounts
}

// writeChainFiles writes genesis.json, config.json, and keystore.json for a chain
func writeChainFiles(chainName string, chainCfg *ChainConfig, chainIdentities []NodeIdentity, allIdentities []NodeIdentity,
	accounts []*fsm.Account, password string, jsonBeautify bool, outputBaseDir string) {

	chainDir := filepath.Join(outputBaseDir, chainName)
	mustSetDirectory(chainDir)

	// Find all validators/delegators that should be in this chain's genesis
	// (those whose committees include this chain's ID)
	var validatorsForGenesis []NodeIdentity
	for _, identity := range allIdentities {
		if identity.NodeType == "validator" || identity.NodeType == "delegator" {
			for _, c := range identity.Committees {
				if int(c) == chainCfg.ID {
					validatorsForGenesis = append(validatorsForGenesis, identity)
					break
				}
			}
		}
	}

	// Build a set of native account addresses for deduplication
	nativeAddresses := make(map[string]bool)
	for _, account := range accounts {
		nativeAddresses[hex.EncodeToString(account.Address)] = true
	}

	// Find cross-chain validators/delegators that need accounts in this chain
	// (validators/delegators from other chains that participate in this chain's committee)
	var crossChainAccounts []NodeIdentity
	for _, v := range validatorsForGenesis {
		if v.ChainID != chainCfg.ID {
			// This is a cross-chain validator/delegator - needs an account
			if !nativeAddresses[v.Address] {
				crossChainAccounts = append(crossChainAccounts, v)
				nativeAddresses[v.Address] = true // Prevent duplicates
			}
		}
	}

	// Write accounts.json first (needed for genesis)
	accountsPath := filepath.Join(chainDir, "accounts.json")
	accountsFile, err := os.Create(accountsPath)
	if err != nil {
		panic(err)
	}

	writer := jwriter.NewStreamingWriter(accountsFile, 1024)
	arr := writer.Array()
	// Write native accounts
	for _, account := range accounts {
		accountObj := writer.Object()
		accountObj.Name("address").String(hex.EncodeToString(account.Address))
		accountObj.Name("amount").Int(int(account.Amount))
		accountObj.End()
	}
	// Write accounts for cross-chain validators/delegators
	for _, v := range crossChainAccounts {
		accountObj := writer.Object()
		accountObj.Name("address").String(v.Address)
		accountObj.Name("amount").Int(int(v.Amount))
		accountObj.End()
	}
	arr.End()
	if err := writer.Flush(); err != nil {
		panic(err)
	}
	accountsFile.Close()

	// Write genesis.json
	writeGenesisFromIdentities(chainDir, chainCfg.ID, chainCfg.RootChain, validatorsForGenesis, accountsPath)

	// Beautify genesis.json if configured
	if jsonBeautify {
		genesisPath := filepath.Join(chainDir, "genesis.json")
		rawData, err := os.ReadFile(genesisPath)
		if err != nil {
			panic(err)
		}
		var parsed interface{}
		if err := json.Unmarshal(rawData, &parsed); err != nil {
			panic(err)
		}
		beautified, err := json.MarshalIndent(parsed, "", "  ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(genesisPath, beautified, 0644); err != nil {
			panic(err)
		}
	}

	// Delete accounts.json as it was only needed for genesis.json
	if err := os.Remove(accountsPath); err != nil {
		panic(err)
	}

	// Write config.json for this chain
	templateConfig := createTemplateConfig(chainCfg.ID, chainCfg.RootChain)
	mustSaveAsJSON(filepath.Join(chainDir, "config.json"), templateConfig)

	// Create keystore.json for this chain
	// Include all validators/delegators that participate in this chain (including cross-chain)
	// Plus all native full nodes
	keystoreIdentities := make([]NodeIdentity, 0)

	// Add all validators/delegators for this chain's genesis (includes cross-chain)
	keystoreIdentities = append(keystoreIdentities, validatorsForGenesis...)

	// Add native full nodes
	for _, identity := range chainIdentities {
		if identity.NodeType == "fullnode" {
			keystoreIdentities = append(keystoreIdentities, identity)
		}
	}

	keystore := &crypto.Keystore{
		AddressMap:  make(map[string]*crypto.EncryptedPrivateKey, len(keystoreIdentities)),
		NicknameMap: make(map[string]string, len(keystoreIdentities)),
	}
	for _, identity := range keystoreIdentities {
		nickname := fmt.Sprintf("node-%d", identity.ID)
		_, err := keystore.ImportRaw(identity.PrivateKeyBytes, password, crypto.ImportRawOpts{
			Nickname: nickname,
		})
		if err != nil {
			panic(err)
		}
	}
	mustSaveAsJSON(filepath.Join(chainDir, "keystore.json"), keystore)

	fmt.Printf("Written files for chain %s\n", chainName)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <config-name>")
		fmt.Printf("Available configs: %s\n", strings.Join(listAvailableConfigs(), ", "))
		fmt.Println("Example: go run main.go max")
		os.Exit(1)
	}

	configName := os.Args[1]
	cfg, err := getConfig(configName)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Using config: %s\n", configName)

	// Validate node count
	fmt.Println("Validating configuration...")
	if err := validateConfig(cfg); err != nil {
		fmt.Printf("Configuration error: %v\n", err)
		os.Exit(1)
	}

	// Validate committee assignments
	fmt.Println("Validating committee assignments...")
	if err := validateCommitteeAssignments(cfg); err != nil {
		fmt.Printf("Committee assignment error: %v\n", err)
		os.Exit(1)
	}

	// Set up output directory (relative to genesis-generator directory)
	outputBaseDir := filepath.Join("../../artifacts", configName, "chains")

	fmt.Println("Deleting old files!")

	mustSetDirectory(outputBaseDir)
	mustDeleteInDirectory(outputBaseDir)

	fmt.Println("Creating new files!")

	logData()

	semaphoreChan := make(chan struct{}, cfg.General.Concurrency)

	// Sort chain names for consistent idx assignment
	chainNames := make([]string, 0, len(cfg.Chains))
	for name := range cfg.Chains {
		chainNames = append(chainNames, name)
	}
	sort.Strings(chainNames)

	// Pre-calculate starting indices for each chain
	chainStartIndices := make(map[string]int)
	currentIdx := 1
	for _, chainName := range chainNames {
		chainCfg := cfg.Chains[chainName]
		chainStartIndices[chainName] = currentIdx
		// Calculate total nodes for this chain
		currentIdx += chainCfg.Validators.Count + chainCfg.Delegators.Count + chainCfg.FullNodes.Count
	}

	// Phase 1: Generate all identities for all chains
	fmt.Println("Phase 1: Generating identities...")
	chainIdentitiesMap := make(map[string][]NodeIdentity)
	chainAccountsMap := make(map[string][]*fsm.Account)
	var allIdentities []NodeIdentity

	for _, chainName := range chainNames {
		identities, accounts := generateChainIdentities(
			chainName,
			cfg.Chains[chainName],
			chainStartIndices[chainName],
			cfg.General.Buffer,
			cfg.General.NetAddressSuffix,
			semaphoreChan,
		)
		chainIdentitiesMap[chainName] = identities
		chainAccountsMap[chainName] = accounts
		allIdentities = append(allIdentities, identities...)
	}

	// Sort all identities by ID
	sort.Slice(allIdentities, func(i, j int) bool {
		return allIdentities[i].ID < allIdentities[j].ID
	})

	// Phase 2: Write files for all chains
	fmt.Println("Phase 2: Writing chain files...")
	for _, chainName := range chainNames {
		writeChainFiles(
			chainName,
			cfg.Chains[chainName],
			chainIdentitiesMap[chainName],
			allIdentities,
			chainAccountsMap[chainName],
			cfg.General.Password,
			cfg.General.JsonBeautify,
			outputBaseDir,
		)
	}

	// Phase 3: Generate ids.json with multi-committee validators having multiple entries
	fmt.Println("Phase 3: Writing ids.json...")

	// Build a map of chain ID to root chain ID
	chainToRootChain := make(map[int]int)
	for _, chainCfg := range cfg.Chains {
		chainToRootChain[chainCfg.ID] = chainCfg.RootChain
	}

	// First pass: Expand multi-committee validators into multiple entries
	// and track root chain nodes and multi-committee mappings
	type expandedEntry struct {
		identity     NodeIdentity
		originalID   int    // Original ID before expansion
		originalAddr string // Original address to match multi-committee entries
		isRootChain  bool   // Whether this entry is for a root chain
	}

	var expandedEntries []expandedEntry
	nextExpandedID := len(allIdentities) + 1

	for _, identity := range allIdentities {
		rootChainID := chainToRootChain[identity.ChainID]
		isRootChain := identity.ChainID == rootChainID

		if identity.NodeType == "fullnode" {
			// Full nodes only appear once
			expandedEntries = append(expandedEntries, expandedEntry{
				identity:     identity,
				originalID:   identity.ID,
				originalAddr: identity.Address,
				isRootChain:  isRootChain,
			})
		} else if len(identity.Committees) == 1 {
			// Single committee validator/delegator - appears once
			expandedEntries = append(expandedEntries, expandedEntry{
				identity:     identity,
				originalID:   identity.ID,
				originalAddr: identity.Address,
				isRootChain:  isRootChain,
			})
		} else {
			// Multi-committee validator/delegator - appears once per committee
			for i, committee := range identity.Committees {
				expandedIdentity := identity
				if i == 0 {
					// First entry keeps original ID
					expandedIdentity.ID = identity.ID
				} else {
					// Additional entries get new IDs
					expandedIdentity.ID = nextExpandedID
					nextExpandedID++
				}
				// Update chainId to match the committee
				expandedIdentity.ChainID = int(committee)

				entryRootChainID := chainToRootChain[int(committee)]
				entryIsRootChain := int(committee) == entryRootChainID

				expandedEntries = append(expandedEntries, expandedEntry{
					identity:     expandedIdentity,
					originalID:   identity.ID,
					originalAddr: identity.Address,
					isRootChain:  entryIsRootChain,
				})
			}
		}
	}

	// Collect root chain node IDs for distribution (only validators, not delegators or fullnodes)
	var rootChainNodeIDs []int
	for _, entry := range expandedEntries {
		if entry.isRootChain && entry.identity.NodeType == "validator" {
			rootChainNodeIDs = append(rootChainNodeIDs, entry.identity.ID)
		}
	}

	// Build a map from address to root chain entry ID (for multi-committee validators)
	addressToRootChainID := make(map[string]int)
	for _, entry := range expandedEntries {
		if entry.isRootChain {
			addressToRootChainID[entry.identity.Address] = entry.identity.ID
		}
	}

	// Count existing assignments to each root chain node
	// (root chain validators count themselves, multi-committee nested validators count their root chain entry)
	// Delegators are skipped as they don't get rootChainNode assignments
	rootChainNodeAssignments := make(map[int]int)
	for _, id := range rootChainNodeIDs {
		rootChainNodeAssignments[id] = 0
	}

	// First, count assignments from root chain validators (they reference themselves)
	// and from multi-committee nested chain validators (they reference their root chain entry)
	for _, entry := range expandedEntries {
		// Skip delegators - they don't get rootChainNode
		if entry.identity.IsDelegate {
			continue
		}
		if entry.isRootChain && entry.identity.NodeType == "validator" {
			// Root chain validator references itself
			rootChainNodeAssignments[entry.identity.ID]++
		} else if rootID, exists := addressToRootChainID[entry.originalAddr]; exists {
			// Multi-committee nested chain validator references its root chain entry
			if entry.identity.NodeType == "validator" {
				rootChainNodeAssignments[rootID]++
			}
		}
	}

	// Helper function to find the root chain node with fewest assignments
	findLeastAssignedRootNode := func() int {
		minAssignments := -1
		selectedNode := rootChainNodeIDs[0]
		for _, id := range rootChainNodeIDs {
			if minAssignments == -1 || rootChainNodeAssignments[id] < minAssignments {
				minAssignments = rootChainNodeAssignments[id]
				selectedNode = id
			}
		}
		return selectedNode
	}

	// Second pass: Assign rootChainNode to each entry
	idsFile := IdsFile{
		Keys: make(map[string]NodeIdentity),
	}

	for _, entry := range expandedEntries {
		identity := entry.identity

		// Delegators don't get rootChainNode (they're not physical servers)
		if identity.IsDelegate {
			// Leave RootChainNode as nil for delegators
			key := fmt.Sprintf("node-%d", identity.ID)
			idsFile.Keys[key] = identity
			continue
		}

		if entry.isRootChain {
			// Root chain node: rootChainNode is itself
			identity.RootChainNode = &identity.ID
		} else if rootID, exists := addressToRootChainID[entry.originalAddr]; exists {
			// Nested chain node with same identity on root chain: use the root chain entry's ID
			identity.RootChainNode = &rootID
		} else {
			// Nested chain node without same identity: assign to least-used root chain node
			// Note: rootChainNodeIDs is guaranteed to be non-empty due to config validation
			leastUsed := findLeastAssignedRootNode()
			identity.RootChainNode = &leastUsed
			rootChainNodeAssignments[leastUsed]++
		}

		key := fmt.Sprintf("node-%d", identity.ID)
		idsFile.Keys[key] = identity
	}

	mustSaveAsJSON(filepath.Join(outputBaseDir, "ids.json"), idsFile)

	fmt.Println("Done!")
	fmt.Printf("Total base nodes: %d\n", len(allIdentities))
	fmt.Printf("Total ids.json entries (including multi-committee expansions): %d\n", len(idsFile.Keys))
}
