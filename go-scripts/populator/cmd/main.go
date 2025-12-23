package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/canopy-network/k8s-node-tester/go-scripts/shared"
	"golang.org/x/sync/semaphore"
	"gopkg.in/yaml.v3"
)

var (
	path          = flag.String("path", "../config.yml", "Path to the configuration file")
	profileConfig = flag.String("profile", "default", "Profile to use from the configuration file")
	accounts      = flag.String("accounts", "", "path to the accounts file")
)

const (
	baseFee = uint64(10_000) // Base fee for transactions
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
	log.Debug("starting populator")
	// load the accounts and config
	profile, accounts, err := LoadConfigs(*path, *profileConfig, *accounts)
	if err != nil {
		log.Error("failed to load configs", "error", err)
		os.Exit(1)
	}
	// set the client urls
	SetCanopyClient(profile.General.RpcURL, profile.General.AdminRpcURL)
	// setup the block notifier
	notifier := NotifyNewBlock(log, profile, timeout, blockCheckInterval, retries)
	// fan-out: listen for new blocks to broadcast
	b := NewBroadcaster(notifier, 2)
	// start the send tx handlers
	wg := sync.WaitGroup{}
	wg.Go(func() {
		HandleTxSends(log, b.Channels()[0], profile, accounts)
	})
	wg.Go(func() {
		HandleTxs(log, b.Channels()[1], profile, accounts)
	})
	wg.Wait()
	log.Info("finished running populator")
}

// HandleTxSends handles the sending of bulk `send` transactions per block
func HandleTxSends(log *slog.Logger, notifier <-chan uint64, profile *Profile, accounts []shared.Account) {
	for height := range notifier {
		if profile.Send.Count == 0 {
			continue
		}
		send := func() (string, error) { return sendTx(profile.Send, accounts[0], accounts[1], profile.General) }
		success, errors := RunConcurrentTxs(context.Background(),
			profile.Send.Count, profile.Send.Concurrency, send, log)
		if errors > 0 {
			log.Warn("errors sending txs",
				slog.Int("errors", errors),
				slog.Int("success", success),
				slog.Uint64("height", height),
			)
			continue
		}
		log.Info("success sending txs",
			slog.Int("success", success),
			slog.Uint64("count", uint64(profile.Send.Count)),
			slog.Uint64("height", height),
		)
	}
}

// HandleTxs handles the sending of most transactions per defined block
func HandleTxs(log *slog.Logger, notifier <-chan uint64, profile *Profile, accounts []shared.Account) {
	for height := range notifier {
		// gather all the transactions for the current height
		txs := GatherAtHeight(profile, height)
		for _, tx := range txs {
			log.Info("sending transaction",
				slog.String("type", string(tx.Kind())), slog.Uint64("height", height))
			// send the transaction
			hash, err := sendTx(tx, accounts[tx.Sender()], accounts[tx.Receiver()], profile.General)
			if err != nil {
				log.Error("failed to send transaction",
					slog.String("type", string(tx.Kind())),
					slog.Uint64("height", height), slog.String("error", err.Error()))
				continue
			}
			log.Info("successfully sent transaction", slog.String("type",
				string(tx.Kind())), slog.Uint64("height", height), slog.String("hash", hash))
		}
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
		return nil, nil, fmt.Errorf("parse accounts: %s: %w", path, err)
	}
	accounts := make([]shared.Account, 0, len(accountsMap.Accounts))
	for _, account := range accountsMap.Accounts {
		accounts = append(accounts, account)
	}
	// sort the accounts lexicographically for deterministic order
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].Address < accounts[j].Address
	})
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
	// validate the profile configuration
	if err := pf.Validate(); err != nil {
		return nil, nil, fmt.Errorf("validate profile %s: %w", profile, err)
	}
	// validate there's the minimun number of accounts enforced by the config
	min := max(2, pf.General.Accounts)
	if len(accounts) < min {
		return nil, nil, fmt.Errorf("not enough accounts, min: %d, actual: %d",
			min, len(accounts))
	}
	return &pf, accounts, nil
}

// GatherAtHeight returns all scheduled transactions due at height
// SendPlan is excluded (handled separately).
func GatherAtHeight(p *Profile, height uint64) []Tx {
	var out []Tx
	out = append(out, filterDue(p.Transactions.Stake, height)...)
	out = append(out, filterDue(p.Transactions.EditStake, height)...)
	out = append(out, filterDue(p.Transactions.Pause, height)...)
	out = append(out, filterDue(p.Transactions.Unstake, height)...)
	out = append(out, filterDue(p.Transactions.ChangeParam, height)...)
	out = append(out, filterDue(p.Transactions.DaoTransfer, height)...)
	out = append(out, filterDue(p.Transactions.Subsidy, height)...)
	return out
}

// filterDue is a helper that filters a slice of DueAt items by height
func filterDue[T DueAt](items []T, height uint64) []Tx {
	var out []Tx
	for _, v := range items {
		if v.Due(height) {
			out = append(out, v)
		}
	}
	return out
}

// NotifyNewBlock notifies the caller each time a new block is created
func NotifyNewBlock(log *slog.Logger, profile *Profile, timeout time.Duration,
	checkInterval time.Duration, maxRetries int) <-chan uint64 {
	type HeightResp struct {
		Height int `json:"height"`
	}
	heightCh := make(chan uint64)

	go func() {
		defer close(heightCh)
		// to avoid sending data to genesis blocks, avoid sending txs to genesis blocks
		lastHeight := uint64(1)
		retries := 0
		initialized := !profile.General.WaitForNewBlock
		// helper function to handle errors
		handleErr := func(msg string, err error) (shouldContinue bool) {
			retries++
			log.Error(msg,
				slog.String("err", err.Error()),
				slog.Int("retry", retries),
				slog.Int("maxRetries", maxRetries),
			)
			if retries >= maxRetries {
				return false
			}
			return true
		}
		// handleHeight decides what to emit and whether to stop
		counter := uint64(0)
		handleHeight := func(height uint64) (stop bool, h uint64) {
			// emit actual chain height until it exceeds MaxHeight
			max := profile.General.MaxHeight
			if !profile.General.Incremental {
				if height <= max {
					return false, height
				}
				return true, height
			}
			// incremental mode: height becomes a 0 based counter, incremented by 1 per block
			// emit the counter value
			counter++
			if counter <= max {
				return false, counter
			}
			return true, counter
		}
		// check for new heights on each tick
		for range time.Tick(checkInterval) {
			resp, err := cnpyClient.Height()
			if err != nil {
				if !handleErr("wait block: get height:", err) {
					return
				}
				continue
			}
			// reset retries on success
			retries = 0
			// ignore genesis or non-increasing heights
			if resp.Height == 0 || resp.Height <= lastHeight {
				continue
			}
			lastHeight = resp.Height
			// wait for the next block on the very first iteration so is always notified on a "new block"
			if !initialized {
				initialized = true
				continue
			}
			// handle the new height
			stop, height := handleHeight(resp.Height)
			if stop {
				return
			}
			heightCh <- height
		}
	}()
	// exit
	return heightCh
}

// RunConcurrentTxs runs concurrent tx for a total of count.
// The do function should perform the work for a single idempotent job.
func RunConcurrentTxs(ctx context.Context, count, concurrency uint,
	do func() (string, error), log *slog.Logger) (int, int) {
	if concurrency == 0 {
		concurrency = 1
	}

	sem := semaphore.NewWeighted(int64(concurrency))
	var wg sync.WaitGroup
	var successes atomic.Int32
	var errors atomic.Int32

	for range count {
		if err := sem.Acquire(ctx, 1); err != nil {
			// typically only fails if ctx is canceled
			if errors.Add(1) == 1 {
				log.Error("semaphore acquire failed", slog.String("error", err.Error()))
			}
			break
		}
		wg.Add(1)
		go func() {
			defer sem.Release(1)
			defer wg.Done()

			if _, err := do(); err != nil {
				// log the first error only (others are likely same cause)
				if errors.Add(1) == 1 {
					log.Error("error sending tx", slog.String("error", err.Error()))
				}
				return
			}
			successes.Add(1)
		}()
	}

	wg.Wait()
	return int(successes.Load()), int(errors.Load())
}

// sendTx is an util to build and send a transaction
func sendTx(tx Tx, from, to shared.Account, config General) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := BuildTxRequest(from, to, config)
	if err != nil {
		return "", fmt.Errorf("build tx request: %w", err)
	}
	hash, err := tx.Do(ctx, req, config.AdminRpcURL)
	if err != nil {
		return "", fmt.Errorf("send transaction: %w", err)
	}
	return hash, nil
}
