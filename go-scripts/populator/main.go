package main

import (
	"context"
	"encoding/json"
	"errors"
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
	baseFee = uint64(10_000) // base fee for transactions
	// TODO: should this be configurable?
	retries            = 5                      // number of retries for failed requests
	timeout            = 5 * time.Second        // timeout for each request
	blockCheckInterval = 500 * time.Millisecond // interval to check for new blocks
)

func main() {
	// parse flags
	flag.Parse()
	// create default logger
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		// Remove timestamps
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
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
	notifier := BlockNotifier(log, profile.General, timeout, blockCheckInterval, retries)
	// fan-out: listen for new blocks to broadcast
	b := NewBroadcaster(notifier, 2)
	// start the tx handlers
	wg := sync.WaitGroup{}
	wg.Go(func() {
		HandleSendTxs(log, b.Channels()[0], profile, accounts)
	})
	wg.Go(func() {
		HandleTxs(log, b.Channels()[1], profile, accounts)
	})
	wg.Wait()
	log.Info("finished running populator")
}

// HandleSendTxs handles the sending of bulk `send` transactions per block
func HandleSendTxs(log *slog.Logger, notifier <-chan HeightCh, profile *Profile, accounts []shared.Account) {
	if profile.Send.Count() == 0 {
		return
	}
	lastBlockTime := time.Now()
	for height := range notifier {
		start := time.Now()
		// execute the transactions
		success, errors, _ := executeSendTxs(profile, accounts, height.Height, log)
		duration := time.Since(start)
		// get block
		block, err := cnpyClient.BlockByHeight(0)
		if err != nil {
			log.Error("error getting block", slog.Uint64("height", height.Height),
				slog.String("error", err.Error()))
			continue
		}
		// calculate block duration
		blockTime := time.UnixMicro(int64(block.BlockHeader.Time))
		lastBlockDuration := blockTime.Sub(lastBlockTime)
		lastBlockTime = blockTime
		// log data
		log.Info("finished sending SEND txs",
			slog.Int("success", success),
			slog.Int("failure", errors),
			slog.Uint64("count", uint64(profile.Send.Count())),
			slog.Uint64("height", height.Height),
			slog.String("duration", duration.String()),
			slog.Uint64("last_block_txs", block.BlockHeader.NumTxs),
			slog.String("last_block_duration", lastBlockDuration.String()),
		)
	}
}

// HandleTxs handles the sending of most transactions per defined block
func HandleTxs(log *slog.Logger, notifier <-chan HeightCh, profile *Profile, accounts []shared.Account) {
	var height uint64
	for heightInfo := range notifier {
		// set which type of height to use
		if profile.General.Incremental {
			height = heightInfo.Counter
		} else {
			height = heightInfo.Height
		}
		// gather all the transactions for the current height
		txs := GatherAtHeight(profile, height)
		for _, tx := range txs {
			txLog := log.With(slog.String("type", string(tx.Kind())),
				slog.Uint64("height", height), slog.Bool("batched", tx.IsBatch()))
			txLog.Info("sending transaction")
			// send the transaction
			if tx.IsBatch() {
				success, errors, err := doExecuteBulkTxs(tx, profile, accounts, heightInfo.Height)
				batchLog := txLog.With(slog.Int("success", success), slog.Int("errors", errors))
				if err != nil {
					batchLog.Error("failed to send transaction", slog.String("error", err.Error()))
					continue
				} else {
					batchLog.Info("successfully sent transaction")
				}
			} else {
				hashes, err := sendTx(tx, accounts[tx.Sender()], accounts[tx.Receiver()],
					profile.General, heightInfo.Height, tx.IsBatch(), 0)
				if err != nil {
					txLog.Error("failed to send transaction", slog.String("error", err.Error()))
					continue
				}
				txLog.Info("successfully sent transaction", slog.String("hash", hashes[0]))
			}
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
	out = append(out, filterDue(p.Transactions.CreateOrder, height)...)
	out = append(out, filterDue(p.Transactions.EditOrder, height)...)
	out = append(out, filterDue(p.Transactions.DeleteOrder, height)...)
	out = append(out, filterDue(p.Transactions.LockOrder, height)...)
	out = append(out, filterDue(p.Transactions.CloseOrder, height)...)
	out = append(out, filterDue(p.Transactions.StartPoll, height)...)
	out = append(out, filterDue(p.Transactions.DexLimitOrder, height)...)
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

// RunConcurrentTxs runs concurrent tx for a total of count.
// The do function should perform the work for a single idempotent job.
func RunConcurrentTxs(ctx context.Context, count, concurrency uint,
	do func() (string, error), log *slog.Logger) (int, int, error) {
	if concurrency == 0 {
		concurrency = 1
	}
	// create a semaphore to limit concurrency
	sem := semaphore.NewWeighted(int64(concurrency))
	var wg sync.WaitGroup
	var successes atomic.Int32
	var errors atomic.Int32
	// run the tx N times
	var err error
	for range count {
		if err := sem.Acquire(ctx, 1); err != nil {
			// typically only fails if ctx is canceled
			if errors.Add(1) == 1 {
				log.Error("semaphore acquire failed", slog.String("error", err.Error()))
			}
			break
		}
		wg.Add(1)
		// only save the last error
		go func() {
			defer sem.Release(1)
			defer wg.Done()

			if _, txErr := do(); txErr != nil {
				err = txErr
				errors.Add(1)
				return
			}
			successes.Add(1)
		}()
	}
	// wait for all txs to complete
	wg.Wait()
	return int(successes.Load()), int(errors.Load()), err
}

// executeSendTxs runs the send transactions for a given height
func executeSendTxs(config *Profile, accounts []shared.Account, height uint64,
	log *slog.Logger) (success, errors int, errs error) {
	if config.Send.IsBatch() {
		return doExecuteBulkTxs(&config.Send, config, accounts, height)
	}
	send := func() (string, error) {
		hashes, err := sendTx(&config.Send,
			accounts[0], accounts[1], config.General, uint64(height), false, 0)
		if err != nil {
			return "", err
		}
		return hashes[0], nil
	}
	return RunConcurrentTxs(context.Background(),
		config.Send.Count(), config.Send.Concurrency, send, log)
}

// doExecuteBulkTxs sends bulk transactions in parallel batches
func doExecuteBulkTxs(tx Tx, config *Profile, accounts []shared.Account,
	height uint64) (success, errs int, err error) {
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var errorCount atomic.Int32

	bulkTx, ok := tx.(BulkTx)
	if !ok {
		return 0, 0, errors.New("tx does not support bulk transactions")
	}

	total := bulkTx.Count()
	batchSize := bulkTx.BatchSize()
	// calculate number of batches needed
	numBatches := (total + batchSize - 1) / batchSize
	for i := range numBatches {
		// calculate how many to send in this batch
		toSend := batchSize
		remaining := total - (i * batchSize)
		if toSend > remaining {
			toSend = remaining
		}
		// set the count for this batch
		wg.Add(1)
		go func(batchNum, batchSize uint) {
			defer wg.Done()
			hashes, txErr := sendTx(bulkTx, accounts[0], accounts[1], config.General,
				uint64(height), true, toSend)
			if txErr != nil {
				err = txErr
				errorCount.Add(int32(int(batchSize) - len(hashes)))
				successCount.Add(int32(len(hashes)))
				return
			}
			successCount.Add(int32(len(hashes)))
		}(i, toSend)
	}
	wg.Wait()
	return int(successCount.Load()), int(errorCount.Load()), err
}

// sendTx is an util to build and send a transaction
func sendTx(tx Tx, from, to shared.Account, config General, height uint64,
	bulk bool, count uint) (hashes []string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := BuildTxRequest(from, to, config, height, count)
	if err != nil {
		return nil, fmt.Errorf("build tx request: %w", err)
	}
	if bulk {
		bulkTx, ok := tx.(BulkTx)
		if !ok {
			return nil, fmt.Errorf("tx [%T] does not implement BulkTx", tx)
		}
		hashes, err = bulkTx.DoBulk(ctx, req, config.AdminRpcURL)
	} else {
		hash, doErr := tx.Do(ctx, req, config.AdminRpcURL)
		hashes, err = []string{hash}, doErr
	}
	if err != nil {
		return nil, fmt.Errorf("send transaction: %w", err)
	}
	return hashes, nil
}
