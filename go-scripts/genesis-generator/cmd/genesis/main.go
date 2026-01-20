package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
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
	ID int `yaml:"id"`
	// RepeatedIdentityValidatorCount: existing validators that participate in this committee AND appear in BOTH chains' genesis
	// These reuse validators from the chain's validator pool and create expanded entries in ids.json (one per chain)
	RepeatedIdentityValidatorCount int `yaml:"repeatedIdentityValidatorCount"`
	// RepeatedIdentityDelegatorCount: existing delegators that participate in this committee AND appear in BOTH chains' genesis
	RepeatedIdentityDelegatorCount int `yaml:"repeatedIdentityDelegatorCount"`
	// ValidatorCount: NEW validators staked ONLY for the target committee
	// Genesis validators: appear in ROOT chain's genesis with committees: [target_committee]
	// Accounts/Keystore: appear in TARGET chain
	// In ids.json they have chainId = target committee ID
	// These are additional nodes that count towards nodes.count
	ValidatorCount int `yaml:"validatorCount"`
	// DelegatorCount: NEW delegators staked ONLY for the target committee
	// Genesis validators: appear in ROOT chain's genesis with committees: [target_committee]
	// Accounts/Keystore: appear in TARGET chain
	// In ids.json they would have chainId = target committee ID (if included)
	DelegatorCount int `yaml:"delegatorCount"`
}

// ChainConfig represents a single chain's configuration
type ChainConfig struct {
	ID                         int                   `yaml:"id"`
	RootChain                  int                   `yaml:"rootChain"`
	Validators                 ValidatorsConfig      `yaml:"validators"`
	FullNodes                  FullNodesConfig       `yaml:"fullNodes"`
	Accounts                   AccountsConfig        `yaml:"accounts"`
	Delegators                 DelegatorsConfig      `yaml:"delegators"`
	Committees                 []CommitteeAssignment `yaml:"committees"`
	GossipThreshold            uint                  `yaml:"gossipThreshold"`                      // Optional: gossip threshold (default: 0)
	SleepUntil                 int                   `yaml:"sleepUntil,omitempty"`                 // Optional: epoch timestamp for sleepUntil
	MaxCommitteeSize           int                   `yaml:"maxCommitteeSize,omitempty"`           // Optional: max committee size (default: 100)
	MinimumPeersToStart        int                   `yaml:"minimumPeersToStart,omitempty"`        // Optional: minimum peers to start (default: 0)
	MaxInbound                 int                   `yaml:"maxInbound,omitempty"`                 // Optional: max inbound connections (default: 100)
	MaxOutbound                int                   `yaml:"maxOutbound,omitempty"`                // Optional: max outbound connections (default: 100)
	InMemory                   bool                  `yaml:"inMemory,omitempty"`                   // Optional: in-memory mode (default: false)
	LazyMempoolCheckFrequencyS int                   `yaml:"lazyMempoolCheckFrequencyS,omitempty"` // Optional: frequency of lazy mempool check in seconds (default: 1)
	DropPercentage             int                   `yaml:"dropPercentage,omitempty"`             // Optional: percentage of transactions to drop (default: 0)
	MaxTransactionCount        uint32                `yaml:"maxTransactionCount,omitempty"`        // Optional: max transactions count (default: 1000)
}

// AppConfig represents the configuration structure
type AppConfig struct {
	General GeneralConfig           `yaml:"general"`
	Nodes   NodesConfig             `yaml:"nodes"`
	Chains  map[string]*ChainConfig `yaml:"chains"`
}

// NodeIdentity represents a node's identity for ids.json
type NodeIdentity struct {
	ID            int      `json:"id"`
	ChainID       int      `json:"chainId"`
	RootChainID   int      `json:"rootChainId"`
	RootChainNode *int     `json:"rootChainNode,omitempty"` // nil for delegators (they're not physical nodes)
	PeerNode      *int     `json:"peerNode,omitempty"`      // nil for delegators (they're not physical nodes)
	Address       string   `json:"address"`
	PublicKey     string   `json:"publicKey"`
	PrivateKey    string   `json:"privateKey"`
	NodeType      string   `json:"nodeType"`
	Committees    []uint64 `json:"-"` // Not exported to JSON, used internally
	// ExpandingCommittees tracks which committees this validator should create expanded entries for
	// (appears in other chain's genesis). Other committees are just staked but don't expand.
	ExpandingCommittees map[uint64]bool `json:"-"` // Not exported to JSON, used internally
	PrivateKeyBytes     []byte          `json:"-"` // Not exported to JSON, used for keystore
	StakedAmount        uint64          `json:"-"` // Not exported to JSON, used for genesis
	Amount              uint64          `json:"-"` // Not exported to JSON, used for genesis
	IsDelegate          bool            `json:"-"` // Not exported to JSON, used for genesis
	NetAddress          string          `json:"-"` // Not exported to JSON, used for genesis
	// GenesisChainID is which chain's genesis this validator appears in (may differ from ChainID for committee-only validators)
	GenesisChainID int `json:"-"` // Not exported to JSON, used for genesis placement
}

// MainAccount represents a main account identity for ids.json
type MainAccount struct {
	Address         string `json:"address" yaml:"address"`
	PublicKey       string `json:"publicKey" yaml:"publicKey"`
	PrivateKey      string `json:"privateKey" yaml:"privateKey"`
	Password        string `json:"password" yaml:"-"` // Set from config, not from accounts.yml
	PrivateKeyBytes []byte `json:"-" yaml:"-"`        // Not exported to JSON, used for keystore
}

// MainAccountsFile represents the structure of accounts.yml
type MainAccountsFile struct {
	Accounts map[string]*MainAccount `yaml:"accounts"`
}

// IdsFile represents the structure of ids.json
type IdsFile struct {
	MainAccounts map[string]*MainAccount `json:"main-accounts,omitempty"`
	Keys         map[string]NodeIdentity `json:"keys"`
}

var configFile = "configs.yml"
var accountsFile = "accounts.yml"

func loadConfigs() (map[string]*AppConfig, error) {
	configFile = filepath.Join(*configPath, configFile)
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

func loadMainAccounts() (map[string]*MainAccount, error) {
	accountsFilePath := filepath.Join(*configPath, accountsFile)
	data, err := os.ReadFile(accountsFilePath)
	if err != nil {
		// Return empty map if file doesn't exist (main accounts are optional)
		if os.IsNotExist(err) {
			return make(map[string]*MainAccount), nil
		}
		return nil, fmt.Errorf("failed to read accounts file '%s': %w", accountsFilePath, err)
	}

	var accountsData MainAccountsFile
	if err := yaml.Unmarshal(data, &accountsData); err != nil {
		return nil, fmt.Errorf("failed to parse accounts file: %w", err)
	}

	// Decode private key bytes for each account
	for name, account := range accountsData.Accounts {
		privateKeyBytes, err := hex.DecodeString(account.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode private key for account '%s': %w", name, err)
		}
		account.PrivateKeyBytes = privateKeyBytes
	}

	return accountsData.Accounts, nil
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
		// RepeatedIdentityValidatorCount: creates expanded entries (same identity in multiple chains)
		// ValidatorCount: creates NEW validators staked only for the target committee
		repeatedIdentityExpansions := 0
		committeeOnlyValidators := 0
		for _, ca := range chainCfg.Committees {
			repeatedIdentityExpansions += ca.RepeatedIdentityValidatorCount
			committeeOnlyValidators += ca.ValidatorCount
		}

		chainNodes := baseNodes + repeatedIdentityExpansions + committeeOnlyValidators
		totalNodes += chainNodes

		if repeatedIdentityExpansions > 0 || committeeOnlyValidators > 0 {
			fmt.Printf("  Chain %s: %d validators + %d full nodes + %d repeatedIdentity expansions + %d committee-only validators = %d entries (+ %d delegators)\n",
				chainName, chainCfg.Validators.Count, chainCfg.FullNodes.Count, repeatedIdentityExpansions, committeeOnlyValidators, chainNodes, chainCfg.Delegators.Count)
		} else {
			fmt.Printf("  Chain %s: %d validators + %d full nodes = %d entries (+ %d delegators)\n",
				chainName, chainCfg.Validators.Count, chainCfg.FullNodes.Count, chainNodes, chainCfg.Delegators.Count)
		}
	}

	if totalNodes != cfg.Nodes.Count {
		return fmt.Errorf("node count mismatch: total entries (%d) does not equal nodes.count (%d)",
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

			// RepeatedIdentity counts must not exceed available validators/delegators (they reuse existing ones)
			// ValidatorCount/DelegatorCount create NEW entities, so no limit check needed
			if ca.RepeatedIdentityValidatorCount > chainCfg.Validators.Count {
				return fmt.Errorf("chain %s: committee %d repeatedIdentityValidatorCount (%d) exceeds total validators (%d)",
					chainName, ca.ID, ca.RepeatedIdentityValidatorCount, chainCfg.Validators.Count)
			}
			if ca.RepeatedIdentityDelegatorCount > chainCfg.Delegators.Count {
				return fmt.Errorf("chain %s: committee %d repeatedIdentityDelegatorCount (%d) exceeds total delegators (%d)",
					chainName, ca.ID, ca.RepeatedIdentityDelegatorCount, chainCfg.Delegators.Count)
			}
			fmt.Printf("  Chain %s: committee %d assignment - %d repeatedIdentity validators + %d committee-only validators, %d repeatedIdentity delegators + %d committee-only delegators ✓\n",
				chainName, ca.ID, ca.RepeatedIdentityValidatorCount, ca.ValidatorCount, ca.RepeatedIdentityDelegatorCount, ca.DelegatorCount)
		}
	}

	// Validate that for each nested chain, its root chain has at least one validator in the nested chain's committee
	for chainName, chainCfg := range cfg.Chains {
		// Skip root chains (they are their own root)
		if chainCfg.ID == chainCfg.RootChain {
			continue
		}

		// This is a nested chain - find its root chain
		var rootChainCfg *ChainConfig
		for _, c := range cfg.Chains {
			if c.ID == chainCfg.RootChain {
				rootChainCfg = c
				break
			}
		}

		if rootChainCfg == nil {
			return fmt.Errorf("chain %s: rootChain %d does not exist", chainName, chainCfg.RootChain)
		}

		// Check if there's any committee assignment for this nested chain
		// At least one of validatorCount + repeatedIdentityValidatorCount must be > 0 for peerNode assignment
		repeatedIdentityValidatorCount := 0
		committeeOnlyValidatorCount := 0
		for _, ca := range rootChainCfg.Committees {
			if ca.ID == chainCfg.ID {
				repeatedIdentityValidatorCount = ca.RepeatedIdentityValidatorCount
				committeeOnlyValidatorCount = ca.ValidatorCount
				break
			}
		}

		totalValidatorsForCommittee := repeatedIdentityValidatorCount + committeeOnlyValidatorCount
		if totalValidatorsForCommittee == 0 {
			return fmt.Errorf("nested chain %s (ID %d): root chain must have at least one validator assigned to committee %d "+
				"(either via repeatedIdentityValidatorCount or validatorCount) for peerNode assignment",
				chainName, chainCfg.ID, chainCfg.ID)
		}
		fmt.Printf("  Nested chain %s: root chain has %d validators in committee %d (%d repeatedIdentity + %d committee-only) ✓\n",
			chainName, totalValidatorsForCommittee, chainCfg.ID, repeatedIdentityValidatorCount, committeeOnlyValidatorCount)
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
	netAddressSuffix string, identities *[]NodeIdentity, gsync *sync.Mutex, wg *sync.WaitGroup, semaphoreChan chan struct{},
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

			netAddress := fmt.Sprintf("tcp://node-%d%s", startIdx+i, netAddressSuffix)

			identity := NodeIdentity{
				ID:              startIdx + i,
				ChainID:         chainID,
				RootChainID:     rootChainID,
				Address:         hex.EncodeToString(pk.PublicKey().Address().Bytes()),
				PublicKey:       hex.EncodeToString(pk.PublicKey().Bytes()),
				PrivateKey:      hex.EncodeToString(pk.Bytes()),
				NodeType:        "fullnode",
				NetAddress:      netAddress,
				PrivateKeyBytes: pk.Bytes(),
				GenesisChainID:  chainID,
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
// expandingCommittees maps validator index to committees that should create expanded entries (repeated identity)
func addValidators(count int, isDelegate bool, startIdx int, stakedAmount uint64, amount uint64,
	chainID int, rootChainID int, committeeAssignments map[int][]uint64, expandingCommittees map[int]map[uint64]bool,
	netAddressSuffix string, identities *[]NodeIdentity, gsync *sync.Mutex, wg *sync.WaitGroup,
	semaphoreChan chan struct{}, accountChan chan *fsm.Account) {

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

			// Calculate ID: validators use positive IDs (startIdx + i), delegators use negative IDs (startIdx - i)
			var nodeID int
			if isDelegate {
				nodeID = startIdx - i // Delegators count down: -1, -2, -3, ...
			} else {
				nodeID = startIdx + i // Validators count up: 1, 2, 3, ...
			}

			netAddress := fmt.Sprintf("tcp://node-%d%s", nodeID, netAddressSuffix)

			accountChan <- &fsm.Account{
				Address: pk.PublicKey().Address().Bytes(),
				Amount:  amount,
			}

			// Copy the expanding committees for this validator
			var identityExpandingCommittees map[uint64]bool
			if ec, ok := expandingCommittees[i]; ok {
				identityExpandingCommittees = make(map[uint64]bool)
				for k, v := range ec {
					identityExpandingCommittees[k] = v
				}
			}

			identity := NodeIdentity{
				ID:                  nodeID,
				ChainID:             chainID,
				RootChainID:         rootChainID,
				Address:             hex.EncodeToString(pk.PublicKey().Address().Bytes()),
				PublicKey:           hex.EncodeToString(pk.PublicKey().Bytes()),
				PrivateKey:          hex.EncodeToString(pk.Bytes()),
				NodeType:            nodeType,
				Committees:          committees,
				ExpandingCommittees: identityExpandingCommittees,
				PrivateKeyBytes:     pk.Bytes(),
				StakedAmount:        stakedAmount,
				Amount:              amount,
				IsDelegate:          isDelegate,
				NetAddress:          netAddress,
				GenesisChainID:      chainID,
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

// addCommitteeOnlyValidator creates a validator staked ONLY for a specific committee
// Genesis validators: appear in ROOT chain's genesis with committees: [target_committee]
// Accounts/Keystore: appear in TARGET chain (not root chain)
// In ids.json, they have chainId = target committee (the committee they're staked for)
func addCommitteeOnlyValidator(nodeID int, stakedAmount uint64, amount uint64,
	chainID int, rootChainID int, targetCommittee uint64, netAddressSuffix string,
	identities *[]NodeIdentity, gsync *sync.Mutex, wg *sync.WaitGroup,
	semaphoreChan chan struct{}, accountChan chan *fsm.Account) {

	wg.Go(func() {
		semaphoreChan <- struct{}{}
		defer func() { <-semaphoreChan }()

		pk := mustCreateKey()

		// Committee is ONLY the target committee (not the chain's own committee)
		committees := []uint64{targetCommittee}

		netAddress := fmt.Sprintf("tcp://node-%d%s", nodeID, netAddressSuffix)

		accountChan <- &fsm.Account{
			Address: pk.PublicKey().Address().Bytes(),
			Amount:  amount,
		}

		identity := NodeIdentity{
			ID:                  nodeID,
			ChainID:             int(targetCommittee), // ids.json and accounts/keystore use target committee
			RootChainID:         rootChainID,
			Address:             hex.EncodeToString(pk.PublicKey().Address().Bytes()),
			PublicKey:           hex.EncodeToString(pk.PublicKey().Bytes()),
			PrivateKey:          hex.EncodeToString(pk.Bytes()),
			NodeType:            "validator",
			Committees:          committees,
			ExpandingCommittees: nil, // No expanding
			PrivateKeyBytes:     pk.Bytes(),
			StakedAmount:        stakedAmount,
			Amount:              amount,
			IsDelegate:          false,
			NetAddress:          netAddress,
			GenesisChainID:      chainID, // Genesis validators in ROOT chain
		}

		gsync.Lock()
		*identities = append(*identities, identity)
		gsync.Unlock()

		nickNames <- validatorNick
	})
}

// addCommitteeOnlyDelegator creates a delegator staked ONLY for a specific committee
// Genesis validators: appear in ROOT chain's genesis with committees: [target_committee]
// Accounts/Keystore: appear in TARGET chain (not root chain)
// In ids.json (if included), they would have chainId = target committee
func addCommitteeOnlyDelegator(nodeID int, stakedAmount uint64, amount uint64,
	chainID int, rootChainID int, targetCommittee uint64, netAddressSuffix string,
	identities *[]NodeIdentity, gsync *sync.Mutex, wg *sync.WaitGroup,
	semaphoreChan chan struct{}, accountChan chan *fsm.Account) {

	wg.Go(func() {
		semaphoreChan <- struct{}{}
		defer func() { <-semaphoreChan }()

		pk := mustCreateKey()

		// Committee is ONLY the target committee (not the chain's own committee)
		committees := []uint64{targetCommittee}

		netAddress := fmt.Sprintf("tcp://node-%d%s", nodeID, netAddressSuffix)

		accountChan <- &fsm.Account{
			Address: pk.PublicKey().Address().Bytes(),
			Amount:  amount,
		}

		identity := NodeIdentity{
			ID:                  nodeID,
			ChainID:             int(targetCommittee), // ids.json and accounts/keystore use target committee
			RootChainID:         rootChainID,
			Address:             hex.EncodeToString(pk.PublicKey().Address().Bytes()),
			PublicKey:           hex.EncodeToString(pk.PublicKey().Bytes()),
			PrivateKey:          hex.EncodeToString(pk.Bytes()),
			NodeType:            "delegator",
			Committees:          committees,
			ExpandingCommittees: nil, // No expanding
			PrivateKeyBytes:     pk.Bytes(),
			StakedAmount:        stakedAmount,
			Amount:              amount,
			IsDelegate:          true,
			NetAddress:          netAddress,
			GenesisChainID:      chainID, // Genesis validators in ROOT chain
		}

		gsync.Lock()
		*identities = append(*identities, identity)
		gsync.Unlock()

		nickNames <- delegatorNick
	})
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

// writeGenesisFromIdentities writes genesis.json for a specific chain using identities
// For validators from other chains (cross-chain), only include this chain's committee
func writeGenesisFromIdentities(chainDir string, chainID int, rootChainID int, validators []NodeIdentity, accountsPath string, maxCommitteeSize int) {
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
		// There are three cases:
		// 1. Native validator (first committee == chainID): include all committees
		// 2. Committee-only validator (GenesisChainID == chainID but ChainID != chainID, no expanding): include original committees [target_committee]
		// 3. RepeatedIdentity expanded entry (expanded to this chain): only include this chain's committee
		var committeesForGenesis []uint64
		isNativeValidator := len(v.Committees) > 0 && int(v.Committees[0]) == chainID
		// Committee-only: GenesisChainID is root chain, but ChainID is target committee
		genesisChainID := v.GenesisChainID
		if genesisChainID == 0 {
			genesisChainID = v.ChainID
		}
		isCommitteeOnlyValidator := genesisChainID == chainID && v.ChainID != chainID && v.ExpandingCommittees == nil

		if isNativeValidator {
			// Native validator: include all their committees
			committeesForGenesis = v.Committees
		} else if isCommitteeOnlyValidator {
			// Committee-only validator: include their target committee only
			committeesForGenesis = v.Committees
		} else {
			// RepeatedIdentity expanded entry or cross-chain: only include this chain's committee
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
				MaxCommitteeSize:                   uint64(maxCommitteeSize),
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

func createTemplateConfig(
	chainID int,
	rootChainID int,
	sleepUntilEpoch int,
	minimumPeersToStart int,
	maxInbound int,
	maxOutbound int,
	inMemory bool,
	gossipThreshold uint,
	dialPeers []string,
	maxTransactionCount uint32,
	dropPercentage int,
	lazyMempoolCheckFrequencyS int) *lib.Config {
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
		// Nested chain: single entry with just the root chain
		rootChain = []lib.RootChain{
			{
				ChainId: uint64(rootChainID),
				Url:     "ROOT_NODE_ID",
			},
		}
	}

	// Convert sleepUntil epoch to uint64
	sleepUntil := uint64(sleepUntilEpoch)

	// Set ProposeVoteTimeoutMS based on chain type
	proposeVoteTimeoutMS := 4000 // Root chain default
	if chainID != rootChainID {
		proposeVoteTimeoutMS = 3000 // Nested chain
	}

	if maxInbound == 0 {
		maxInbound = 21
	}
	if maxOutbound == 0 {
		maxOutbound = 7
	}

	if maxTransactionCount == 0 {
		maxTransactionCount = 5000
	}

	if dropPercentage == 0 {
		dropPercentage = 35
	}

	if lazyMempoolCheckFrequencyS == 0 {
		lazyMempoolCheckFrequencyS = 1
	}

	return &lib.Config{
		MainConfig: lib.MainConfig{
			LogLevel:   "debug",
			ChainId:    uint64(chainID),
			RootChain:  rootChain,
			RunVDF:     false,
			SleepUntil: sleepUntil,
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
			InMemory:    inMemory,
		},
		P2PConfig: lib.P2PConfig{
			NetworkID:           1,
			ListenAddress:       fmt.Sprintf("0.0.0.0:%d", 9000+chainID),
			ExternalAddress:     "NODE_ID",
			MaxInbound:          maxInbound,
			MaxOutbound:         maxOutbound,
			TrustedPeerIDs:      nil,
			DialPeers:           dialPeers,
			BannedPeerIDs:       nil,
			BannedIPs:           nil,
			MinimumPeersToStart: minimumPeersToStart,
			GossipThreshold:     gossipThreshold,
		},
		ConsensusConfig: lib.ConsensusConfig{
			NewHeightTimeoutMs:      4500,
			ElectionTimeoutMS:       1500,
			ElectionVoteTimeoutMS:   1500,
			ProposeTimeoutMS:        2500,
			ProposeVoteTimeoutMS:    proposeVoteTimeoutMS,
			PrecommitTimeoutMS:      2000,
			PrecommitVoteTimeoutMS:  2000,
			CommitTimeoutMS:         2000,
			RoundInterruptTimeoutMS: 2000,
		},
		MempoolConfig: lib.MempoolConfig{
			MaxTotalBytes:              1000000,
			MaxTransactionCount:        maxTransactionCount,
			IndividualMaxTxSize:        4000,
			DropPercentage:             dropPercentage,
			LazyMempoolCheckFrequencyS: lazyMempoolCheckFrequencyS,
		},
		MetricsConfig: lib.MetricsConfig{
			MetricsEnabled:    true,
			PrometheusAddress: "0.0.0.0:9090",
		},
	}
}

// generateChainIdentities generates all identities for a chain (validators, delegators, fullnodes)
// Returns the identities and accounts for this chain
// startIdx is for validators/fullnodes (positive IDs), delegatorStartIdx is for delegators (negative IDs)
func generateChainIdentities(chainName string, chainCfg *ChainConfig, startIdx int, delegatorStartIdx int, buffer int, netAddressSuffix string,
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

	// Build committee assignments for regular validators (RepeatedIdentity)
	// Track which committees are "expanding" (repeated identity - will appear in other chain's genesis)
	validatorCommitteeAssignments := make(map[int][]uint64)
	validatorExpandingCommittees := make(map[int]map[uint64]bool)
	for _, ca := range chainCfg.Committees {
		// Assign RepeatedIdentityValidatorCount validators (these will expand to other chain's genesis)
		for i := 0; i < ca.RepeatedIdentityValidatorCount && i < chainCfg.Validators.Count; i++ {
			validatorCommitteeAssignments[i] = append(validatorCommitteeAssignments[i], uint64(ca.ID))
			if validatorExpandingCommittees[i] == nil {
				validatorExpandingCommittees[i] = make(map[uint64]bool)
			}
			validatorExpandingCommittees[i][uint64(ca.ID)] = true
		}
	}

	// Build committee assignments for regular delegators (RepeatedIdentity)
	delegatorCommitteeAssignments := make(map[int][]uint64)
	delegatorExpandingCommittees := make(map[int]map[uint64]bool)
	for _, ca := range chainCfg.Committees {
		// Assign RepeatedIdentityDelegatorCount delegators (these will expand to other chain's genesis)
		for i := 0; i < ca.RepeatedIdentityDelegatorCount && i < chainCfg.Delegators.Count; i++ {
			delegatorCommitteeAssignments[i] = append(delegatorCommitteeAssignments[i], uint64(ca.ID))
			if delegatorExpandingCommittees[i] == nil {
				delegatorExpandingCommittees[i] = make(map[uint64]bool)
			}
			delegatorExpandingCommittees[i][uint64(ca.ID)] = true
		}
	}

	// Calculate how many committee-only validators/delegators to create
	totalCommitteeOnlyValidators := 0
	totalCommitteeOnlyDelegators := 0
	for _, ca := range chainCfg.Committees {
		totalCommitteeOnlyValidators += ca.ValidatorCount
		totalCommitteeOnlyDelegators += ca.DelegatorCount
	}

	// Assign unique idx within this chain
	// Validators get positive IDs starting from startIdx
	validatorStartIdx := startIdx
	// Committee-only validators get positive IDs right after regular validators
	committeeOnlyValidatorStartIdx := validatorStartIdx + chainCfg.Validators.Count
	// Full nodes get positive IDs right after committee-only validators
	fullNodeStartIdx := committeeOnlyValidatorStartIdx + totalCommitteeOnlyValidators
	// Delegators get negative IDs (passed in from caller)

	// Create regular validators (staked for their own chain's committee + any repeatedIdentity assignments)
	addValidators(chainCfg.Validators.Count, false, validatorStartIdx, chainCfg.Validators.StakedAmount, chainCfg.Validators.Amount,
		chainCfg.ID, chainCfg.RootChain, validatorCommitteeAssignments, validatorExpandingCommittees,
		netAddressSuffix, &chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)

	// Create committee-only validators (staked ONLY for target committee in the root chain)
	// These validators appear in the ROOT chain's genesis with committees: [target_committee]
	committeeOnlyValidatorIdx := committeeOnlyValidatorStartIdx
	for _, ca := range chainCfg.Committees {
		for i := 0; i < ca.ValidatorCount; i++ {
			addCommitteeOnlyValidator(committeeOnlyValidatorIdx+i, chainCfg.Validators.StakedAmount, chainCfg.Validators.Amount,
				chainCfg.ID, chainCfg.RootChain, uint64(ca.ID), netAddressSuffix,
				&chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)
		}
		committeeOnlyValidatorIdx += ca.ValidatorCount
	}

	// Create regular delegators
	addValidators(chainCfg.Delegators.Count, true, delegatorStartIdx, chainCfg.Delegators.StakedAmount, chainCfg.Delegators.Amount,
		chainCfg.ID, chainCfg.RootChain, delegatorCommitteeAssignments, delegatorExpandingCommittees,
		netAddressSuffix, &chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)

	// Create committee-only delegators (staked ONLY for target committee in the root chain)
	committeeOnlyDelegatorIdx := delegatorStartIdx - chainCfg.Delegators.Count // Continue negative IDs after regular delegators
	for _, ca := range chainCfg.Committees {
		for i := 0; i < ca.DelegatorCount; i++ {
			addCommitteeOnlyDelegator(committeeOnlyDelegatorIdx-i, chainCfg.Delegators.StakedAmount, chainCfg.Delegators.Amount,
				chainCfg.ID, chainCfg.RootChain, uint64(ca.ID), netAddressSuffix,
				&chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)
		}
		committeeOnlyDelegatorIdx -= ca.DelegatorCount
	}

	addFullNodes(chainCfg.FullNodes.Count, chainCfg.FullNodes.Amount, fullNodeStartIdx, chainCfg.ID, chainCfg.RootChain,
		netAddressSuffix, &chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)
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
// expandedValidators contains validators/delegators with correct IDs for this chain (including cross-chain)
func writeChainFiles(chainName string, chainCfg *ChainConfig, chainIdentities []NodeIdentity,
	genesisValidators []NodeIdentity, keystoreValidators []NodeIdentity, dialPeers []string,
	accounts []*fsm.Account, mainAccounts map[string]*MainAccount, password string, jsonBeautify bool, outputBaseDir string) {

	chainDir := filepath.Join(outputBaseDir, chainName)
	mustSetDirectory(chainDir)

	// Build a set of native account addresses for deduplication
	nativeAddresses := make(map[string]bool)
	for _, account := range accounts {
		nativeAddresses[hex.EncodeToString(account.Address)] = true
	}

	// Find validators/delegators that need accounts in this chain (from keystoreValidators)
	// (those whose addresses are not already in native accounts)
	var crossChainAccounts []NodeIdentity
	for _, v := range keystoreValidators {
		if !nativeAddresses[v.Address] {
			crossChainAccounts = append(crossChainAccounts, v)
			nativeAddresses[v.Address] = true // Prevent duplicates
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
	// Write main accounts (same identities across all chains, uses chain's account amount)
	for _, mainAccount := range mainAccounts {
		mainAccountObj := writer.Object()
		mainAccountObj.Name("address").String(mainAccount.Address)
		mainAccountObj.Name("amount").Int(int(chainCfg.Accounts.Amount))
		mainAccountObj.End()
	}
	arr.End()
	if err := writer.Flush(); err != nil {
		panic(err)
	}
	accountsFile.Close()

	// Write genesis.json (uses genesisValidators for validators section)
	maxCommitteeSize := chainCfg.MaxCommitteeSize
	if maxCommitteeSize == 0 {
		maxCommitteeSize = 100 // Default value
	}
	writeGenesisFromIdentities(chainDir, chainCfg.ID, chainCfg.RootChain, genesisValidators, accountsPath, maxCommitteeSize)

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
	templateConfig := createTemplateConfig(
		chainCfg.ID,
		chainCfg.RootChain,
		chainCfg.SleepUntil,
		chainCfg.MinimumPeersToStart,
		chainCfg.MaxInbound,
		chainCfg.MaxOutbound,
		chainCfg.InMemory,
		chainCfg.GossipThreshold,
		dialPeers,
		chainCfg.MaxTransactionCount,
		chainCfg.DropPercentage,
		chainCfg.LazyMempoolCheckFrequencyS,
	)
	mustSaveAsJSON(filepath.Join(chainDir, "config.json"), templateConfig)

	// Create keystore.json for this chain
	// Include all validators/delegators whose accounts are in this chain (keystoreValidators)
	// Plus all native full nodes
	keystoreIdentities := make([]NodeIdentity, 0)

	// Add all validators/delegators for this chain's keystore
	keystoreIdentities = append(keystoreIdentities, keystoreValidators...)

	// Add native full nodes
	for _, identity := range chainIdentities {
		if identity.NodeType == "fullnode" {
			keystoreIdentities = append(keystoreIdentities, identity)
		}
	}

	keystore := &crypto.Keystore{
		AddressMap:  make(map[string]*crypto.EncryptedPrivateKey, len(keystoreIdentities)+len(mainAccounts)),
		NicknameMap: make(map[string]string, len(keystoreIdentities)+len(mainAccounts)),
	}
	for _, identity := range keystoreIdentities {
		var nickname string
		if identity.IsDelegate {
			// Delegators use "delegator-{abs(id)}" - IDs are unique negative numbers
			nickname = fmt.Sprintf("delegator-%d", -identity.ID)
		} else {
			nickname = fmt.Sprintf("node-%d", identity.ID)
		}
		_, err := keystore.ImportRaw(identity.PrivateKeyBytes, password, crypto.ImportRawOpts{
			Nickname: nickname,
		})
		if err != nil {
			panic(err)
		}
	}
	// Add main accounts to keystore
	for name, mainAccount := range mainAccounts {
		_, err = keystore.ImportRaw(mainAccount.PrivateKeyBytes, password, crypto.ImportRawOpts{
			Nickname: name,
		})
		if err != nil {
			panic(err)
		}
	}
	mustSaveAsJSON(filepath.Join(chainDir, "keystore.json"), keystore)

	fmt.Printf("Written files for chain %s\n", chainName)
}

var (
	configPath = flag.String("path", "../../", "path to the folder containing the config files")
	configName = flag.String("config", "default", "name of the config to use")
	outputDir  = flag.String("output", "../../artifacts", "path to the folder where the output files will be saved")
)

func init() {
	// Customize the usage output
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  genesis -config <name>\n\n")
		fmt.Fprintf(os.Stderr, "Available configs: %s\n", strings.Join(listAvailableConfigs(), ", "))
		fmt.Fprintf(os.Stderr, "Example:\n  genesis -config max\n")
		flag.PrintDefaults()
	}
}

func main() {
	flag.Parse()

	cfg, err := getConfig(*configName)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Using config: %s\n", *configName)

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
	outputBaseDir := filepath.Join(*outputDir, *configName)

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

	// Pre-calculate starting indices for each chain (only validators and full nodes get positive IDs)
	chainStartIndices := make(map[string]int)
	currentIdx := 1
	for _, chainName := range chainNames {
		chainCfg := cfg.Chains[chainName]
		chainStartIndices[chainName] = currentIdx
		// Calculate total nodes for this chain (delegators use negative IDs, so not counted here)
		// Include regular validators + committee-only validators + full nodes
		committeeOnlyValidators := 0
		for _, ca := range chainCfg.Committees {
			committeeOnlyValidators += ca.ValidatorCount
		}
		currentIdx += chainCfg.Validators.Count + committeeOnlyValidators + chainCfg.FullNodes.Count
	}

	// Pre-calculate delegator starting indices (negative IDs)
	chainDelegatorStartIndices := make(map[string]int)
	currentDelegatorIdx := -1
	for _, chainName := range chainNames {
		chainCfg := cfg.Chains[chainName]
		chainDelegatorStartIndices[chainName] = currentDelegatorIdx
		// Delegators get negative IDs counting down (-1, -2, -3, ...)
		// Include regular delegators + committee-only delegators
		committeeOnlyDelegators := 0
		for _, ca := range chainCfg.Committees {
			committeeOnlyDelegators += ca.DelegatorCount
		}
		currentDelegatorIdx -= chainCfg.Delegators.Count + committeeOnlyDelegators
	}

	// Load main accounts from accounts.yml (same identities across all chains)
	fmt.Println("Loading main accounts...")
	mainAccounts, err := loadMainAccounts()
	if err != nil {
		fmt.Printf("Error loading main accounts: %v\n", err)
		os.Exit(1)
	}
	if len(mainAccounts) > 0 {
		fmt.Printf("Loaded %d main accounts\n", len(mainAccounts))
		// Set password from config for each main account
		for _, account := range mainAccounts {
			account.Password = cfg.General.Password
		}
	}

	// Phase 1: Generate all identities for all chains
	fmt.Println("Phase 1: Generating identities...")
	chainIdentitiesMap := make(map[string][]NodeIdentity)
	chainAccountsMap := make(map[string][]*fsm.Account)
	chainDialPeers := make(map[int][]string)
	var allIdentities []NodeIdentity

	for _, chainName := range chainNames {
		identities, accounts := generateChainIdentities(
			chainName,
			cfg.Chains[chainName],
			chainStartIndices[chainName],
			chainDelegatorStartIndices[chainName],
			cfg.General.Buffer,
			cfg.General.NetAddressSuffix,
			semaphoreChan,
		)
		chainIdentitiesMap[chainName] = identities
		chainAccountsMap[chainName] = accounts
		allIdentities = append(allIdentities, identities...)
	}

	// Build a map of chain ID to root chain ID
	chainToRootChain := make(map[int]int)
	for _, chainCfg := range cfg.Chains {
		chainToRootChain[chainCfg.ID] = chainCfg.RootChain
	}

	// Sort all identities by ID
	sort.Slice(allIdentities, func(i, j int) bool {
		return allIdentities[i].ID < allIdentities[j].ID
	})

	// Expand multi-committee validators into multiple entries
	// This is needed before Phase 2 so genesis.json and keystore use correct IDs
	type expandedEntry struct {
		identity     NodeIdentity
		originalID   int    // Original ID before expansion
		originalAddr string // Original address to match multi-committee entries
		isRootChain  bool   // Whether this entry is for a root chain
	}

	var expandedEntries []expandedEntry

	// Calculate nextExpandedID based only on validators and full nodes (not delegators)
	baseNodeCount := 0
	for _, identity := range allIdentities {
		if !identity.IsDelegate {
			baseNodeCount++
		}
	}
	nextExpandedID := baseNodeCount + 1

	// Calculate nextExpandedDelegatorID - find the lowest (most negative) delegator ID
	// and continue from there to avoid collisions
	nextExpandedDelegatorID := 0
	for _, identity := range allIdentities {
		if identity.IsDelegate && identity.ID < nextExpandedDelegatorID {
			nextExpandedDelegatorID = identity.ID
		}
	}
	nextExpandedDelegatorID-- // Start one below the lowest existing delegator ID

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
			// Multi-committee validator/delegator
			// First entry (native chain) always appears
			// Additional entries only appear for committees that are in ExpandingCommittees
			for i, committee := range identity.Committees {
				if i == 0 {
					// First entry (native chain) keeps original ID
					expandedEntries = append(expandedEntries, expandedEntry{
						identity:     identity,
						originalID:   identity.ID,
						originalAddr: identity.Address,
						isRootChain:  isRootChain,
					})
				} else {
					// For additional committees, only expand if it's in ExpandingCommittees
					if identity.ExpandingCommittees == nil || !identity.ExpandingCommittees[committee] {
						// This committee is not expanding - skip expansion
						// The validator still has this committee in their committees list
						// but won't appear in the other chain's genesis
						continue
					}

					// This is an expanding committee - create a new expanded entry
					expandedIdentity := identity
					if identity.IsDelegate {
						// Delegators get unique negative IDs (counting down from lowest base delegator ID)
						expandedIdentity.ID = nextExpandedDelegatorID
						nextExpandedDelegatorID--
					} else {
						expandedIdentity.ID = nextExpandedID
						nextExpandedID++
					}

					// Update chainId to match the committee (for ids.json)
					expandedIdentity.ChainID = int(committee)
					// Update GenesisChainID to match the committee (expanded entries go to target chain's genesis)
					expandedIdentity.GenesisChainID = int(committee)
					// Update netAddress to use the correct ID for this expanded entry
					expandedIdentity.NetAddress = fmt.Sprintf("tcp://node-%d%s", expandedIdentity.ID, cfg.General.NetAddressSuffix)

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
	}

	// Build two maps:
	// 1. chainGenesisValidators: validators for genesis validators section (uses GenesisChainID)
	// 2. chainKeystoreValidators: validators for accounts and keystore (uses ChainID)
	chainGenesisValidators := make(map[int][]NodeIdentity)
	chainKeystoreValidators := make(map[int][]NodeIdentity)
	for _, entry := range expandedEntries {
		if entry.identity.NodeType == "validator" || entry.identity.NodeType == "delegator" {
			// GenesisChainID for genesis validators section
			genesisChainID := entry.identity.GenesisChainID
			if genesisChainID == 0 {
				genesisChainID = entry.identity.ChainID
			}
			chainGenesisValidators[genesisChainID] = append(
				chainGenesisValidators[genesisChainID],
				entry.identity,
			)

			// ChainID for accounts and keystore
			chainKeystoreValidators[entry.identity.ChainID] = append(
				chainKeystoreValidators[entry.identity.ChainID],
				entry.identity,
			)
		}

		// Build dial peers list for each chain
		// Include all validators and full nodes (exclude delegators)
		if !entry.identity.IsDelegate {
			// Format: publicKey@netAddress
			// Example: 90703...@tcp://node-1.p2p
			peer := fmt.Sprintf("%s@%s", entry.identity.PublicKey, entry.identity.NetAddress)
			chainDialPeers[entry.identity.ChainID] = append(chainDialPeers[entry.identity.ChainID], peer)
		}
	}

	// Phase 2: Write files for all chains
	fmt.Println("Phase 2: Writing chain files...")
	for _, chainName := range chainNames {
		chainID := cfg.Chains[chainName].ID
		writeChainFiles(
			chainName,
			cfg.Chains[chainName],
			chainIdentitiesMap[chainName],
			chainGenesisValidators[chainID],
			chainKeystoreValidators[chainID],
			chainDialPeers[chainID],
			chainAccountsMap[chainName],
			mainAccounts,
			cfg.General.Password,
			cfg.General.JsonBeautify,
			outputBaseDir,
		)
	}

	// Phase 3: Generate ids.json
	fmt.Println("Phase 3: Writing ids.json...")

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

	// For peerNode: Build a map of nested chain ID -> list of validator IDs that have root chain identity
	// These are validators from the root chain that also participate in this nested chain (repeatedIdentity)
	nestedChainPeerNodes := make(map[int][]int) // chainID -> []nodeID
	// Also build a map of committee-only validators per chain (validators from root chain staked only for that committee)
	committeeOnlyPeerNodes := make(map[int][]int) // chainID -> []nodeID
	for _, entry := range expandedEntries {
		if entry.identity.NodeType != "validator" || entry.identity.IsDelegate {
			continue
		}
		// Check if this is a nested chain entry AND the validator has a root chain identity (repeatedIdentity)
		if !entry.isRootChain {
			if _, hasRootIdentity := addressToRootChainID[entry.originalAddr]; hasRootIdentity {
				// This validator has root chain identity and participates in this nested chain
				nestedChainPeerNodes[entry.identity.ChainID] = append(
					nestedChainPeerNodes[entry.identity.ChainID],
					entry.identity.ID,
				)
			}
		}
		// Check if this is a committee-only validator (GenesisChainID != ChainID)
		// These are validators from root chain staked only for a specific committee
		genesisChainID := entry.identity.GenesisChainID
		if genesisChainID == 0 {
			genesisChainID = entry.identity.ChainID
		}
		if genesisChainID != entry.identity.ChainID && entry.identity.ExpandingCommittees == nil {
			// Committee-only validator: from root chain, staked for target committee
			committeeOnlyPeerNodes[entry.identity.ChainID] = append(
				committeeOnlyPeerNodes[entry.identity.ChainID],
				entry.identity.ID,
			)
		}
	}

	// Count existing assignments to each root chain node
	// (root chain validators count themselves, multi-committee nested validators count their root chain entry)
	// Delegators are skipped as they don't get rootChainNode assignments
	rootChainNodeAssignments := make(map[int]int)
	for _, id := range rootChainNodeIDs {
		rootChainNodeAssignments[id] = 0
	}

	// Count existing assignments to each peer node (per nested chain)
	peerNodeAssignments := make(map[int]int) // nodeID -> count
	for _, peerIDs := range nestedChainPeerNodes {
		for _, id := range peerIDs {
			peerNodeAssignments[id] = 0
		}
	}
	// Also track committee-only validators for peerNode
	for _, peerIDs := range committeeOnlyPeerNodes {
		for _, id := range peerIDs {
			peerNodeAssignments[id] = 0
		}
	}
	// Also track root chain validators for peerNode (used by root chain full nodes)
	for _, id := range rootChainNodeIDs {
		peerNodeAssignments[id] = 0
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

	// Count peerNode assignments for validators that reference themselves
	for _, entry := range expandedEntries {
		if entry.identity.IsDelegate || entry.identity.NodeType != "validator" {
			continue
		}
		if entry.isRootChain {
			// Root chain validators reference themselves for peerNode
			peerNodeAssignments[entry.identity.ID]++
		} else if _, hasRootIdentity := addressToRootChainID[entry.originalAddr]; hasRootIdentity {
			// Nested chain validators with root chain identity (repeatedIdentity) reference themselves for peerNode
			peerNodeAssignments[entry.identity.ID]++
		} else {
			// Check if this is a committee-only validator (from root chain, staked for this committee)
			genesisChainID := entry.identity.GenesisChainID
			if genesisChainID == 0 {
				genesisChainID = entry.identity.ChainID
			}
			if genesisChainID != entry.identity.ChainID && entry.identity.ExpandingCommittees == nil {
				// Committee-only validator: references itself for peerNode
				peerNodeAssignments[entry.identity.ID]++
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

	// Helper function to find the root chain validator with fewest peerNode assignments
	findLeastAssignedRootChainPeerNode := func() int {
		minAssignments := -1
		selectedNode := rootChainNodeIDs[0]
		for _, id := range rootChainNodeIDs {
			if minAssignments == -1 || peerNodeAssignments[id] < minAssignments {
				minAssignments = peerNodeAssignments[id]
				selectedNode = id
			}
		}
		return selectedNode
	}

	// Helper function to find the peer node with fewest assignments for a given nested chain
	// Priority: repeatedIdentity validators > committee-only validators
	// Note: Validation ensures at least one of these exists for each nested chain
	findLeastAssignedPeerNode := func(chainID int) int {
		// First try repeatedIdentity validators
		peerIDs := nestedChainPeerNodes[chainID]
		// If no repeatedIdentity validators, use committee-only validators
		if len(peerIDs) == 0 {
			peerIDs = committeeOnlyPeerNodes[chainID]
		}
		// Validation ensures peerIDs is never empty for nested chains
		minAssignments := -1
		selectedNode := peerIDs[0]
		for _, id := range peerIDs {
			if minAssignments == -1 || peerNodeAssignments[id] < minAssignments {
				minAssignments = peerNodeAssignments[id]
				selectedNode = id
			}
		}
		return selectedNode
	}

	// Second pass: Assign rootChainNode and peerNode to each entry
	idsFile := IdsFile{
		Keys: make(map[string]NodeIdentity),
	}

	for _, entry := range expandedEntries {
		identity := entry.identity

		// Skip delegators - they don't appear in ids.json
		if identity.IsDelegate {
			continue
		}

		// Assign rootChainNode
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

		// Assign peerNode (for validators and full nodes)
		// Check if this is a committee-only validator (from root chain, staked for target committee)
		genesisChainID := identity.GenesisChainID
		if genesisChainID == 0 {
			genesisChainID = identity.ChainID
		}
		isCommitteeOnlyValidator := genesisChainID != identity.ChainID && identity.ExpandingCommittees == nil

		switch identity.NodeType {
		case "validator":
			if entry.isRootChain {
				// Root chain validator: peerNode is itself
				identity.PeerNode = &identity.ID
			} else if _, hasRootIdentity := addressToRootChainID[entry.originalAddr]; hasRootIdentity {
				// Nested chain validator with same identity on root chain (repeatedIdentity): peerNode is itself
				identity.PeerNode = &identity.ID
			} else if isCommitteeOnlyValidator {
				// Committee-only validator (from root chain, staked for this committee): peerNode is itself
				identity.PeerNode = &identity.ID
			} else {
				// Nested chain validator without root chain identity: assign to least-used peer node
				// Priority: repeatedIdentity > committee-only > root chain validators
				leastUsed := findLeastAssignedPeerNode(identity.ChainID)
				identity.PeerNode = &leastUsed
				peerNodeAssignments[leastUsed]++
			}
		case "fullnode":
			if entry.isRootChain {
				// Root chain full node: peerNode is assigned to a root chain validator (distributed evenly)
				leastUsed := findLeastAssignedRootChainPeerNode()
				identity.PeerNode = &leastUsed
				peerNodeAssignments[leastUsed]++
			} else {
				// Nested chain full node: assign to least-used peer node
				// Falls back to root chain validators if no repeatedIdentity validators exist
				leastUsed := findLeastAssignedPeerNode(identity.ChainID)
				identity.PeerNode = &leastUsed
				peerNodeAssignments[leastUsed]++
			}
		}

		key := fmt.Sprintf("node-%d", identity.ID)
		idsFile.Keys[key] = identity
	}

	// Add main accounts to ids.json
	if len(mainAccounts) > 0 {
		idsFile.MainAccounts = mainAccounts
	}

	mustSaveAsJSON(filepath.Join(outputBaseDir, "ids.json"), idsFile)

	fmt.Println("Done!")
	fmt.Printf("Total base nodes: %d\n", len(allIdentities))
	fmt.Printf("Total ids.json entries (including multi-committee expansions): %d\n", len(idsFile.Keys))
}
