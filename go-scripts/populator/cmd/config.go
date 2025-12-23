package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/canopy-network/canopy/cmd/rpc"
)

type TxType string

const (
	TxSend        TxType = "send"
	TxStake       TxType = "stake"
	TxEditStake   TxType = "editStake"
	TxPause       TxType = "pause"
	TxUnstake     TxType = "unstake"
	TxChangeParam TxType = "changeParam"
)

var (
	// default http client for making requests
	httpClient = &http.Client{}
	cnpyClient *rpc.Client
)

// SetCanopyClient sets the canopy global client for making requests
func SetCanopyClient(rpcURL, adminRPCURL string) {
	cnpyClient = rpc.NewClient(rpcURL, adminRPCURL)
}

// Profile is a configuration for a single profile
type Profile struct {
	General      General      `yaml:"general"`
	Send         SendTx       `yaml:"send"`         // handled separately
	Transactions Transactions `yaml:"transactions"` // height-driven ones
}

// Validate validates the profile configuration
func (p *Profile) Validate() error {
	var errs error
	required := func(field string) error { return fmt.Errorf("%s is required", field) }
	if p.General.RpcURL == "" {
		errs = errors.Join(errs, required("baseURL"))
	}
	if p.General.AdminRpcURL == "" {
		errs = errors.Join(errs, required("adminURL"))
	}
	if len(p.General.Chains) == 0 {
		errs = errors.Join(errs, required("chains"))
	}
	return errs
}

// Transactions is the config part that defines all the transactions to make
type Transactions struct {
	Stake       []StakeTx       `yaml:"stake"`
	EditStake   []EditStakeTx   `yaml:"editStake"`
	Pause       []PauseTx       `yaml:"pause"`
	Unstake     []UnstakeTx     `yaml:"unstake"`
	ChangeParam []ChangeParamTx `yaml:"changeParam"`
}

// General populator configuration
type General struct {
	RpcURL          string `yaml:"rpcURL"`
	AdminRpcURL     string `yaml:"adminRpcURL"`
	Incremental     bool   `yaml:"incremental"`
	BasePort        int    `yaml:"basePort"`
	Accounts        int    `yaml:"accounts"`
	Fee             uint64 `yaml:"fee"`
	Chains          []int  `yaml:"chains"`
	MaxHeight       uint64 `yaml:"maxHeight"`
	WaitForNewBlock bool   `yaml:"waitForNewBlock"`
}

// Common fields

type height struct {
	Height uint64 `yaml:"height"`
}

type account struct {
	From int `yaml:"from"`
	To   int `yaml:"to"`
}

type amount struct {
	Amount uint64 `yaml:"amount"`
}

type committees struct {
	Committees []int `yaml:"committees"`
}

func (c committees) String() string {
	strSlice := make([]string, len(c.Committees))
	for i, committee := range c.Committees {
		strSlice[i] = fmt.Sprintf("%d", committee)
	}
	return strings.Join(strSlice, ",")
}

// SendTx Tx is handled separately
type SendTx struct {
	amount      `yaml:",inline"`
	Chains      []int `yaml:"chains"`
	Count       uint  `yaml:"count"`
	Concurrency uint  `yaml:"concurrency"`
}

// Transaction types

// StakeTx represents a transaction to stake a validator/delegator
type StakeTx struct {
	height          `yaml:",inline"`
	account         `yaml:",inline"`
	amount          `yaml:",inline"`
	committees      `yaml:",inline"`
	Delegate        bool   `yaml:"delegate"`
	EarlyWithdrawal bool   `yaml:"earlyWithdrawal"`
	NetAddr         string `yaml:"netAddress"`
}

// EditStakeTx represents a transaction to edit a validator/delegator's stake
type EditStakeTx struct {
	StakeTx `yaml:",inline"`
}

// PauseTx represents a transaction to pause a validator
type PauseTx struct {
	height  `yaml:",inline"`
	account `yaml:",inline"`
}

// UnstakeTx represents a transaction to unstake a validator/delegator
type UnstakeTx struct {
	height  `yaml:",inline"`
	account `yaml:",inline"`
}

// ChangeParam represents a transaction to change a parameter
type ChangeParamTx struct {
	account    `yaml:",inline"`
	height     `yaml:",inline"`
	ParamSpace string `yaml:"paramSpace"`
	ParamKey   string `yaml:"paramKey"`
	ParamValue string `yaml:"paramValue"`
	StartBlock uint64 `yaml:"startBlock"`
	EndBlock   uint64 `yaml:"endBlock"`
}
