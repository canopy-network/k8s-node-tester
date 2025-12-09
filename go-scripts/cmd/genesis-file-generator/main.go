package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
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

var (
	defaultConfig = &lib.Config{
		MainConfig: lib.MainConfig{
			LogLevel: "debug",
			ChainId:  1,
			RootChain: []lib.RootChain{
				{
					ChainId: 1,
					Url:     "http://node-1:50002",
				},
			},
			RunVDF: true,
		},
		RPCConfig: lib.RPCConfig{
			WalletPort:   "50000",
			ExplorerPort: "50001",
			RPCPort:      "50002",
			AdminPort:    "50003",
			RPCUrl:       "http://localhost:50002",
			AdminRPCUrl:  "http://localhost:50003",
			TimeoutS:     3,
		},
		StoreConfig: lib.StoreConfig{
			DataDirPath: "/root/.canopy",
			DBName:      "canopy",
			InMemory:    false,
		},
		P2PConfig: lib.P2PConfig{
			NetworkID:       1,
			ListenAddress:   "0.0.0.0:9001",
			ExternalAddress: "node-1",
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
	nickNames = make(chan string, 1000)
)

const (
	validatorNick = "validator"
	delegatorNick = "delegator"
	accountNick   = "account"
)

// AppConfig represents the configuration structure
type AppConfig struct {
	Delegators            int    `yaml:"delegators"`
	Validators            int    `yaml:"validators"`
	Accounts              int    `yaml:"accounts"`
	Password              string `yaml:"password"`
	MultiNode             bool   `yaml:"multi_node"`
	Concurrency           int64  `yaml:"concurrency"`
	Buffer                int64  `yaml:"buffer"`
	CustomRootChainURL    string `yaml:"root_chain_url"`
	CustomExternalAddress string `yaml:"external_address"`
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

func logData() {
	var accounts, validators, delegators int32

	go func() {
		for nickname := range nickNames {
			switch nickname {
			case accountNick:
				atomic.AddInt32(&accounts, 1)
			case validatorNick:
				atomic.AddInt32(&validators, 1)
			case delegatorNick:
				atomic.AddInt32(&delegators, 1)
			default:
				fmt.Println("Unknown data type received:", nickname)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(2 * time.Second)

		for range ticker.C {
			fmt.Printf("Accounts: %d, Validators: %d, Delegators: %d\n",
				atomic.LoadInt32(&accounts),
				atomic.LoadInt32(&validators),
				atomic.LoadInt32(&delegators),
			)
		}
	}()
}

type IndividualFile struct {
	ValidatorKey string
	Config       *lib.Config
	Keystore     *crypto.Keystore
	Nick         string
}

type IndividualFiles struct {
	Files []*IndividualFile
}

func mustCreateKey() crypto.PrivateKeyI {
	pk, err := crypto.NewBLS12381PrivateKey()
	if err != nil {
		panic(err)
	}

	return pk
}

func mustUpdateKeystore(privateKey []byte, nickName, password string, keystore *crypto.Keystore) {
	_, err := keystore.ImportRaw(privateKey, password, crypto.ImportRawOpts{
		Nickname: nickName,
	})
	if err != nil {
		panic(err)
	}
}

// addAccounts concurrently creates keys and accounts
func addAccounts(accounts int, wg *sync.WaitGroup, semaphoreChan chan struct{}, accountChan chan *fsm.Account) {
	for i := range accounts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			semaphoreChan <- struct{}{}
			defer func() { <-semaphoreChan }()

			addrStr := fmt.Sprintf("%020x", i)

			// fmt.Printf("Creating key for: %s \n", nick)

			accountChan <- &fsm.Account{
				Address: []byte(addrStr),
				Amount:  1000000,
			}
			nickNames <- accountNick
		}(i)
	}
}

// addValidators concurrently creates validators and optional config
func addValidators(validators int, isDelegate, multiNode bool, nickPrefix, password, customRootChainURL, customExternalAddress string,
	files *IndividualFiles, gsync *sync.Mutex, wg *sync.WaitGroup, semaphoreChan chan struct{},
	accountChan chan *fsm.Account, validatorChan chan *fsm.Validator) {

	for i := range validators {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			semaphoreChan <- struct{}{}
			defer func() { <-semaphoreChan }()

			stakedAmount := 0
			if multiNode || (i == 0 && !isDelegate) {
				stakedAmount = 1000000000
			}
			nick := fmt.Sprintf("%s-%d", nickPrefix, i)
			pk := mustCreateKey()
			// fmt.Printf("Creating key for: %s \n", nick)

			var configCopy *lib.Config
			keystore := &crypto.Keystore{
				AddressMap:  make(map[string]*crypto.EncryptedPrivateKey, 1),
				NicknameMap: make(map[string]string, 1),
			}
			if (multiNode || i == 0) && !isDelegate {
				config := *defaultConfig
				if customRootChainURL != "" {
					config.RootChain[0].Url = customRootChainURL
				} else {
					config.RootChain[0].Url = fmt.Sprintf("http://%s:50002", nick)
				}
				if customExternalAddress != "" {
					config.ExternalAddress = customExternalAddress
				} else {
					config.ExternalAddress = nick
				}
				configCopy = &config
				mustUpdateKeystore(pk.Bytes(), nick, password, keystore)
			}

			validatorChan <- &fsm.Validator{
				Address:      pk.PublicKey().Address().Bytes(),
				PublicKey:    pk.PublicKey().Bytes(),
				Committees:   []uint64{1},
				NetAddress:   fmt.Sprintf("tcp://%s", nick),
				StakedAmount: uint64(stakedAmount),
				Output:       pk.PublicKey().Address().Bytes(),
				Delegate:     isDelegate,
			}

			accountChan <- &fsm.Account{
				Address: pk.PublicKey().Address().Bytes(),
				Amount:  1000000,
			}

			if configCopy != nil {
				gsync.Lock()
				files.Files = append(files.Files, &IndividualFile{
					Config:       configCopy,
					ValidatorKey: pk.String(),
					Nick:         nick,
					Keystore:     keystore,
				})
				gsync.Unlock()
			}
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

func accountsWriter(accountLen int, wg *sync.WaitGroup, accountChan chan *fsm.Account) {
	defer wg.Done()

	accountsFile, err := os.Create(".config/accounts.json")
	if err != nil {
		panic(err)
	}
	defer accountsFile.Close()

	writer := jwriter.NewStreamingWriter(accountsFile, 1024)

	fmt.Println("Starting to write accounts!")

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

func genesisWriter(multiNode bool, validatorLen int, wg, accountsWG *sync.WaitGroup, validatorChan chan *fsm.Validator) {
	defer wg.Done()

	genesisFile, err := os.Create(".config/genesis.json")
	if err != nil {
		panic(err)
	}
	defer genesisFile.Close()

	writer := jwriter.NewStreamingWriter(genesisFile, 1024)

	obj := writer.Object()
	obj.Name("time").String("2024-12-14 20:10:52")

	fmt.Println("Starting to write validators!")

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
	fmt.Println("Accounts arrived to main thread!")
	rawAccounts, err := os.ReadFile(".config/accounts.json")
	if err != nil {
		panic(err)
	}
	obj.Name("accounts").Raw(rawAccounts)

	nonSignWindow := 10
	maxNonSign := 4
	if !multiNode {
		maxNonSign = math.MaxInt64
		nonSignWindow = math.MaxInt64
	}

	remainingFields := map[string]interface{}{
		"params": &fsm.Params{
			Consensus: &fsm.ConsensusParams{
				BlockSize:       1000000,
				ProtocolVersion: "1/0",
				RootChainId:     1,
				Retired:         0,
			},
			Validator: &fsm.ValidatorParams{
				UnstakingBlocks:                    2,
				MaxPauseBlocks:                     4380,
				DoubleSignSlashPercentage:          10,
				NonSignSlashPercentage:             1,
				MaxNonSign:                         uint64(maxNonSign),
				NonSignWindow:                      uint64(nonSignWindow),
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
	fmt.Println("Deleting old files!")

	mustSetDirectory(".config")
	mustDeleteInDirectory(".config")

	fmt.Println("Creating new files!")

	if cfg.MultiNode && (cfg.CustomRootChainURL != "" || cfg.CustomExternalAddress != "") {
		panic("Custom root chain url and/or external address can just be used on not multi node")
	}

	acountsLen := cfg.Delegators + cfg.Validators + cfg.Accounts
	validatorsLen := cfg.Delegators + cfg.Validators

	accountChan := make(chan *fsm.Account, cfg.Buffer)
	validatorChan := make(chan *fsm.Validator, cfg.Buffer)

	logData()

	var genesisWG, accountsWG sync.WaitGroup
	genesisWG.Add(1)
	accountsWG.Add(1)
	go genesisWriter(cfg.MultiNode, validatorsLen, &genesisWG, &accountsWG, validatorChan)
	go accountsWriter(acountsLen, &accountsWG, accountChan)

	files := &IndividualFiles{}
	semaphoreChan := make(chan struct{}, cfg.Concurrency)
	var gsync sync.Mutex
	var wg sync.WaitGroup
	addValidators(cfg.Validators, false, cfg.MultiNode, "validator", cfg.Password, cfg.CustomRootChainURL, cfg.CustomExternalAddress, files, &gsync, &wg, semaphoreChan, accountChan, validatorChan)
	addValidators(cfg.Delegators, true, cfg.MultiNode, "delegator", cfg.Password, cfg.CustomRootChainURL, cfg.CustomExternalAddress, files, &gsync, &wg, semaphoreChan, accountChan, validatorChan)
	addAccounts(cfg.Accounts, &wg, semaphoreChan, accountChan)

	wg.Wait()
	genesisWG.Wait()

	for _, file := range files.Files {
		nodePath := fmt.Sprintf(".config/%s", file.Nick)

		mustSetDirectory(nodePath)

		input, err := os.ReadFile(".config/genesis.json")
		if err != nil {
			panic(err)
		}

		err = os.WriteFile(fmt.Sprintf("%s/genesis.json", nodePath), input, 0644)
		if err != nil {
			panic(err)
		}

		mustSaveAsJSON(fmt.Sprintf("%s/keystore.json", nodePath), file.Keystore)
		mustSaveAsJSON(fmt.Sprintf("%s/config.json", nodePath), file.Config)
		mustSaveAsJSON(fmt.Sprintf("%s/validator_key.json", nodePath), file.ValidatorKey)
	}
}
