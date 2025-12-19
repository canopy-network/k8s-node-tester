package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/canopy-network/k8s-node-tester/go-scripts/shared"
	"gopkg.in/yaml.v3"
)

var (
	path          = flag.String("path", "../config.yml", "Path to the configuration file")
	profileConfig = flag.String("profile", "default", "Profile to use from the configuration file")
	accounts      = flag.String("accounts", "", "path to the accounts file")
)

const (
	baseFee        = 10_000             // Base fee for transactions
	queryHeightURL = "/v1/query/height" // URL for querying the height of the blockchain
	// TODO: should this be configurable?
	retries            = 5                      // Number of retries for failed requests
	timeout            = 5 * time.Second        // Timeout for each request
	blockCheckInterval = 500 * time.Millisecond // Interval to check for new blocks
)

// go run *.go --accounts ../../genesis-generator/artifacts/default/ids.json
func main() {
	// parse flags
	flag.Parse()
	// create default logger
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// load the accounts and config
	profile, _, err := LoadConfigs(*path, *profileConfig, *accounts)
	if err != nil {
		log.Error("failed to load configs", "error", err)
		os.Exit(1)
	}
	// setup the block notifier
	notifier := NotifyNewBlock(log, profile.General.BaseRpcURL, timeout, blockCheckInterval, retries)

	for height := range notifier {
		log.Info("new block", "height", height)
	}
}

// LoadConfigs loads the configuration and accounts from the given paths
func LoadConfigs(configPath, profile string, accountsPath string) (*Profile, []shared.Account, error) {
	// retrieve the accounts
	path := filepath.Clean(accountsPath)
	rawAccounts, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("load accounts %s: %w", path, err)
	}
	var accountsMap struct {
		Accounts map[string]shared.Account `json:"main-accounts"`
	}
	if err := json.Unmarshal(rawAccounts, &accountsMap); err != nil {
		return nil, nil, fmt.Errorf("parse accounts %s: %w", path, err)
	}
	accounts := make([]shared.Account, 0, len(accountsMap.Accounts))
	for _, account := range accountsMap.Accounts {
		accounts = append(accounts, account)
	}
	// retrieve the populator config
	path = filepath.Clean(configPath)
	rawConfig, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("load config %s: %w", path, err)
	}
	var profiles map[string]Profile
	if err := yaml.Unmarshal(rawConfig, &profiles); err != nil {
		return nil, nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	pf, ok := profiles[profile]
	if !ok {
		return nil, nil, fmt.Errorf("profile %s not found", profile)
	}
	// validate there's the minimun number of accounts enforced by the config
	if len(accounts) < pf.General.Accounts {
		return nil, nil, fmt.Errorf("not enough accounts, min: %d, actual: %d",
			pf.General.Accounts, len(accounts))
	}
	return &pf, accounts, nil
}

// ExecuteScheduledAtHeight runs scheduled txs for the height
func ExecuteScheduledAtHeight(ctx context.Context, profile *Profile, height int) error {
	due := GatherAtHeight(profile, height)
	for _, tx := range due {
		switch v := tx.(type) {
		case StakeTx:
			// doStake(ctx, v)
		case EditStakeTx:
			// doEditStake(ctx, v)
		case PauseTx:
			// doPause(ctx, v)
		case UnstakeTx:
			// doUnstake(ctx, v)
		case ChangeParamTx:
			// doChangeParam(ctx, v)
		default:
			return fmt.Errorf("unhandled tx: %T", v)
		}
	}
	return nil
}

// GatherAtHeight returns all scheduled transactions due at height
// SendPlan is excluded (handled separately).
func GatherAtHeight(p *Profile, height int) []Tx {
	var out []Tx
	out = append(out, filterDue(p.Transactions.Stake, height)...)
	out = append(out, filterDue(p.Transactions.EditStake, height)...)
	out = append(out, filterDue(p.Transactions.Pause, height)...)
	out = append(out, filterDue(p.Transactions.Unstake, height)...)
	out = append(out, filterDue(p.Transactions.ChangeParam, height)...)
	return out
}

// filterDue is a helper that filters a slice of DueAt items by height
func filterDue[T DueAt](items []T, height int) []Tx {
	var out []Tx
	for _, v := range items {
		if v.Due(height) {
			out = append(out, v)
		}
	}
	return out
}

// NotifyNewBlock notifies the caller each time a new block is created
func NotifyNewBlock(log *slog.Logger, baseURL string, timeout time.Duration,
	checkInterval time.Duration, maxRetries int) <-chan int {
	// to avoid sending data to genesis blocks, avoid sending txs to genesis blocks
	lastHeight := 1
	type HeightResp struct {
		Height int `json:"height"`
	}
	heightCh := make(chan int)
	go func() {
		retries := 0
		// helper function to handle errors
		handleErr := func(msg string, err error) (shouldContinue bool) {
			retries++
			log.Error(msg,
				slog.String("err", err.Error()),
				slog.Int("retry", retries),
				slog.Int("maxRetries", maxRetries),
			)
			if retries >= maxRetries {
				close(heightCh)
				return false
			}
			return true
		}
		// check for new heights on each tick
		for range time.Tick(checkInterval) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			// get height
			got, err := post(ctx, baseURL+queryHeightURL, nil)
			if err != nil {
				if !handleErr("wait block: failed to unmarshal height", err) {
					return
				}
				continue
			}
			// unmarshal response
			resp := &HeightResp{}
			err = json.Unmarshal(got, resp)
			if err != nil {
				if !handleErr("wait block: failed to unmarshal height", err) {
					return
				}
				continue
			}
			// reset retries on success
			retries = 0
			// check for new heights
			if resp.Height == 0 || resp.Height <= lastHeight {
				continue
			}
			lastHeight = resp.Height
			heightCh <- resp.Height
		}
	}()
	// exit
	return heightCh
}
