package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/canopy-network/k8s-node-tester/go-scripts/shared"
	"gopkg.in/yaml.v3"
)

var (
	path          = flag.String("path", "../config.yml", "Path to the configuration file")
	profileConfig = flag.String("profile", "default", "Profile to use from the configuration file")
	accounts      = flag.String("accounts", "", "path to the accounts file")
)

const (
	baseFee = 10_000
)

// go run *.go --accounts ../../genesis-generator/artifacts/default/ids.json
func main() {
	// parse flags
	flag.Parse()
	// create default logger
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// load the accounts and config
	_, _, err := LoadConfigs(*path, *profileConfig, *accounts)
	if err != nil {
		log.Error("failed to load configs", "error", err)
		os.Exit(1)
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
