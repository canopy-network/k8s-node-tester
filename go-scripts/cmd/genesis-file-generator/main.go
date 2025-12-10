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
	Concurrency int64  `yaml:"concurrency"`
	Password    string `yaml:"password"`
}

// NodesConfig holds the total node count
type NodesConfig struct {
	Count int `yaml:"count"`
}

// ValidatorsConfig holds validator-specific configuration
type ValidatorsConfig struct {
	Count        int      `yaml:"count"`
	StakedAmount uint64   `yaml:"stakedAmount"`
	Amount       uint64   `yaml:"amount"`
	Committees   []uint64 `yaml:"committees"`
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
	Count        int      `yaml:"count"`
	StakedAmount uint64   `yaml:"stakedAmount"`
	Amount       uint64   `yaml:"amount"`
	Committees   []uint64 `yaml:"committees"`
}

// ChainConfig represents a single chain's configuration
type ChainConfig struct {
	ID         int              `yaml:"id"`
	RootChain  int              `yaml:"rootChain"`
	Validators ValidatorsConfig `yaml:"validators"`
	FullNodes  FullNodesConfig  `yaml:"fullNodes"`
	Accounts   AccountsConfig   `yaml:"accounts"`
	Delegators DelegatorsConfig `yaml:"delegators"`
}

// AppConfig represents the configuration structure
type AppConfig struct {
	General GeneralConfig           `yaml:"general"`
	Nodes   NodesConfig             `yaml:"nodes"`
	Chains  map[string]*ChainConfig `yaml:"chains"`
}

// NodeIdentity represents a node's identity for ids.json
type NodeIdentity struct {
	Idx             int    `json:"idx"`
	ChainID         int    `json:"chainId"`
	RootChainID     int    `json:"rootChainId"`
	Address         string `json:"address"`
	PublicKey       string `json:"publicKey"`
	PrivateKey      string `json:"privateKey"`
	NodeType        string `json:"nodeType"`
	PrivateKeyBytes []byte `json:"-"` // Not exported to JSON, used for keystore
}

const configFile = "configs.yaml"

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
func validateConfig(cfg *AppConfig) error {
	totalNodes := 0
	for chainName, chainCfg := range cfg.Chains {
		chainNodes := chainCfg.Validators.Count + chainCfg.Delegators.Count + chainCfg.FullNodes.Count
		totalNodes += chainNodes
		fmt.Printf("  Chain %s: %d validators + %d delegators + %d full nodes = %d nodes\n",
			chainName, chainCfg.Validators.Count, chainCfg.Delegators.Count, chainCfg.FullNodes.Count, chainNodes)
	}

	if totalNodes != cfg.Nodes.Count {
		return fmt.Errorf("node count mismatch: sum of all validators, delegators, and full nodes (%d) does not equal nodes.count (%d)",
			totalNodes, cfg.Nodes.Count)
	}

	fmt.Printf("  Total nodes: %d (matches nodes.count: %d) ✓\n", totalNodes, cfg.Nodes.Count)
	return nil
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
				Idx:             startIdx + i,
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
func addValidators(count int, isDelegate bool, startIdx int, stakedAmount uint64, amount uint64,
	chainID int, rootChainID int, committees []uint64,
	identities *[]NodeIdentity, gsync *sync.Mutex, wg *sync.WaitGroup, semaphoreChan chan struct{},
	accountChan chan *fsm.Account, validatorChan chan *fsm.Validator) {

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

			validatorChan <- &fsm.Validator{
				Address:      pk.PublicKey().Address().Bytes(),
				PublicKey:    pk.PublicKey().Bytes(),
				Committees:   committees,
				NetAddress:   fmt.Sprintf("tcp://node-%d", startIdx+i),
				StakedAmount: stakedAmount,
				Output:       pk.PublicKey().Address().Bytes(),
				Delegate:     isDelegate,
			}

			accountChan <- &fsm.Account{
				Address: pk.PublicKey().Address().Bytes(),
				Amount:  amount,
			}

			identity := NodeIdentity{
				Idx:             startIdx + i,
				ChainID:         chainID,
				RootChainID:     rootChainID,
				Address:         hex.EncodeToString(pk.PublicKey().Address().Bytes()),
				PublicKey:       hex.EncodeToString(pk.PublicKey().Bytes()),
				PrivateKey:      hex.EncodeToString(pk.Bytes()),
				NodeType:        nodeType,
				PrivateKeyBytes: pk.Bytes(),
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

func genesisWriter(chainDir string, rootChainID int, validatorLen int, wg, accountsWG *sync.WaitGroup, validatorChan chan *fsm.Validator) {
	defer wg.Done()

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
	for range validatorLen {
		validator := <-validatorChan
		validatorObj := writer.Object()
		validatorObj.Name("address").String(hex.EncodeToString(validator.Address))
		validatorObj.Name("publicKey").String(hex.EncodeToString(validator.PublicKey))
		validatorObj.Name("committees")
		cArr := writer.Array()
		for _, committee := range validator.Committees {
			writer.Int(int(committee))
		}
		cArr.End()
		validatorObj.Name("netAddress").String(validator.NetAddress)
		validatorObj.Name("stakedAmount").Int(int(validator.StakedAmount))
		validatorObj.Name("output").String(hex.EncodeToString(validator.Output))
		validatorObj.Name("delegate").Bool(validator.Delegate)
		validatorObj.End()
	}
	arr.End()

	accountsWG.Wait()
	rawAccounts, err := os.ReadFile(filepath.Join(chainDir, "accounts.json"))
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
				Url:     "http://node-{{ROOT_NODE_ID}}:50002",
			},
		}
	} else {
		// Nested chain: two entries - own chain with NODE_ID, root chain with ROOT_NODE_ID
		rootChain = []lib.RootChain{
			{
				ChainId: uint64(chainID),
				Url:     "http://node-{{NODE_ID}}:50002",
			},
			{
				ChainId: uint64(rootChainID),
				Url:     "http://node-{{ROOT_NODE_ID}}:50002",
			},
		}
	}

	return &lib.Config{
		MainConfig: lib.MainConfig{
			LogLevel:  "debug",
			ChainId:   uint64(chainID),
			RootChain: rootChain,
			RunVDF:    true,
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
			ExternalAddress: "node-{{NODE_ID}}",
			MaxInbound:      21,
			MaxOutbound:     7,
			TrustedPeerIDs:  nil,
			DialPeers:       []string{},
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

func processChain(chainName string, chainCfg *ChainConfig, startIdx int, password string,
	semaphoreChan chan struct{}, allIdentities *[]NodeIdentity, globalSync *sync.Mutex, chainWG *sync.WaitGroup) {
	defer chainWG.Done()

	chainDir := filepath.Join(".config", chainName)
	mustSetDirectory(chainDir)

	fmt.Printf("Processing chain: %s (ID: %d, RootChain: %d)\n", chainName, chainCfg.ID, chainCfg.RootChain)

	accountsLen := chainCfg.Delegators.Count + chainCfg.Validators.Count + chainCfg.FullNodes.Count + chainCfg.Accounts.Count
	validatorsLen := chainCfg.Delegators.Count + chainCfg.Validators.Count

	accountChan := make(chan *fsm.Account, 1000)
	validatorChan := make(chan *fsm.Validator, 1000)

	var genesisWG, accountsWG sync.WaitGroup
	genesisWG.Add(1)
	accountsWG.Add(1)
	go genesisWriter(chainDir, chainCfg.RootChain, validatorsLen, &genesisWG, &accountsWG, validatorChan)
	go accountsWriter(chainDir, accountsLen, &accountsWG, accountChan)

	chainIdentities := make([]NodeIdentity, 0, chainCfg.Validators.Count+chainCfg.Delegators.Count+chainCfg.FullNodes.Count)
	var chainSync sync.Mutex
	var wg sync.WaitGroup

	// Assign unique idx within this chain
	validatorStartIdx := startIdx
	delegatorStartIdx := validatorStartIdx + chainCfg.Validators.Count
	fullNodeStartIdx := delegatorStartIdx + chainCfg.Delegators.Count

	addValidators(chainCfg.Validators.Count, false, validatorStartIdx, chainCfg.Validators.StakedAmount, chainCfg.Validators.Amount,
		chainCfg.ID, chainCfg.RootChain, chainCfg.Validators.Committees,
		&chainIdentities, &chainSync, &wg, semaphoreChan, accountChan, validatorChan)
	addValidators(chainCfg.Delegators.Count, true, delegatorStartIdx, chainCfg.Delegators.StakedAmount, chainCfg.Delegators.Amount,
		chainCfg.ID, chainCfg.RootChain, chainCfg.Delegators.Committees,
		&chainIdentities, &chainSync, &wg, semaphoreChan, accountChan, validatorChan)
	addFullNodes(chainCfg.FullNodes.Count, chainCfg.FullNodes.Amount, fullNodeStartIdx, chainCfg.ID, chainCfg.RootChain,
		&chainIdentities, &chainSync, &wg, semaphoreChan, accountChan)
	addAccounts(chainCfg.Accounts.Count, chainCfg.Accounts.Amount, &wg, semaphoreChan, accountChan)

	wg.Wait()
	genesisWG.Wait()

	// Delete accounts.json as it was only needed for genesis.json
	if err := os.Remove(filepath.Join(chainDir, "accounts.json")); err != nil {
		panic(err)
	}

	// Sort chain identities by idx
	sort.Slice(chainIdentities, func(i, j int) bool {
		return chainIdentities[i].Idx < chainIdentities[j].Idx
	})

	// Write config.json for this chain
	templateConfig := createTemplateConfig(chainCfg.ID, chainCfg.RootChain)
	mustSaveAsJSON(filepath.Join(chainDir, "config.json"), templateConfig)

	// Create keystore.json for this chain
	keystore := &crypto.Keystore{
		AddressMap:  make(map[string]*crypto.EncryptedPrivateKey, len(chainIdentities)),
		NicknameMap: make(map[string]string, len(chainIdentities)),
	}
	for _, identity := range chainIdentities {
		nickname := fmt.Sprintf("node-%d", identity.Idx)
		_, err := keystore.ImportRaw(identity.PrivateKeyBytes, password, crypto.ImportRawOpts{
			Nickname: nickname,
		})
		if err != nil {
			panic(err)
		}
	}
	mustSaveAsJSON(filepath.Join(chainDir, "keystore.json"), keystore)

	// Add chain identities to global identities
	globalSync.Lock()
	*allIdentities = append(*allIdentities, chainIdentities...)
	globalSync.Unlock()

	fmt.Printf("Chain %s: %d validators, %d delegators, %d full nodes, %d accounts\n",
		chainName, chainCfg.Validators.Count, chainCfg.Delegators.Count, chainCfg.FullNodes.Count, chainCfg.Accounts.Count)
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

	fmt.Println("Deleting old files!")

	mustSetDirectory(".config")
	mustDeleteInDirectory(".config")

	fmt.Println("Creating new files!")

	logData()

	semaphoreChan := make(chan struct{}, cfg.General.Concurrency)
	allIdentities := make([]NodeIdentity, 0)
	var globalSync sync.Mutex

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

	// Process all chains concurrently
	var chainWG sync.WaitGroup
	for _, chainName := range chainNames {
		chainWG.Add(1)
		go processChain(chainName, cfg.Chains[chainName], chainStartIndices[chainName], cfg.General.Password, semaphoreChan, &allIdentities, &globalSync, &chainWG)
	}
	chainWG.Wait()

	// Sort all identities by idx for consistent output
	sort.Slice(allIdentities, func(i, j int) bool {
		return allIdentities[i].Idx < allIdentities[j].Idx
	})

	// Write global ids.json with ALL nodes from ALL chains
	fmt.Println("Writing global ids.json...")
	mustSaveAsJSON(".config/ids.json", allIdentities)

	fmt.Println("Done!")
	fmt.Printf("Total nodes across all chains: %d\n", len(allIdentities))
}
