package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/canopy-network/canopy/cmd/rpc"
)

var (
	// default http/canopy client for making requests
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
	if _, err := url.Parse(p.General.RpcURL); err != nil {
		errs = errors.Join(errs, fmt.Errorf("rpcURL: %w", err))
	}
	if _, err := url.Parse(p.General.AdminRpcURL); err != nil {
		errs = errors.Join(errs, fmt.Errorf("adminRpcURL: %w", err))
	}
	if p.General.ChainId == 0 {
		errs = errors.Join(errs, required("chain"))
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
	DaoTransfer []DaoTransferTx `yaml:"daoTransfer"`
	Subsidy     []SubsidyTx     `yaml:"subsidy"`
	CreateOrder []CreateOrderTx `yaml:"createOrder"`
	EditOrder   []EditOrderTx   `yaml:"editOrder"`
	DeleteOrder []DeleteOrderTx `yaml:"deleteOrder"`
	LockOrder   []LockOrderTx   `yaml:"lockOrder"`
	CloseOrder  []CloseOrderTx  `yaml:"closeOrder"`
	StartPoll   []StartPollTx   `yaml:"startPoll"`
}

// General populator configuration
type General struct {
	RpcURL                string `yaml:"rpcURL"`
	AdminRpcURL           string `yaml:"adminRpcURL"`
	Incremental           bool   `yaml:"incremental"`
	BasePort              int    `yaml:"basePort"`
	Accounts              int    `yaml:"accounts"`
	Fee                   uint64 `yaml:"fee"`
	ChainId               int    `yaml:"chainId"`
	NetworkId             int    `yaml:"networkId"`
	MaxHeight             uint64 `yaml:"maxHeight"`
	WaitForNewBlock       bool   `yaml:"waitForNewBlock"`
	NotifyNewBlockDelayMs uint   `yaml:"notifyNewBlockDelay"` // milliseconds
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

type delimitedBlock struct {
	StartBlock uint64 `yaml:"startBlock"`
	EndBlock   uint64 `yaml:"endBlock"`
}

// Transaction types

// SendTx Tx is handled separately
type SendTx struct {
	amount        `yaml:",inline"`
	Count         uint `yaml:"count"`
	Concurrency   uint `yaml:"concurrency"`
	UsePrivateKey bool `yaml:"usePrivateKey"`
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
	account        `yaml:",inline"`
	height         `yaml:",inline"`
	ParamSpace     string `yaml:"paramSpace"`
	ParamKey       string `yaml:"paramKey"`
	ParamValue     string `yaml:"paramValue"`
	delimitedBlock `yaml:",inline"`
}

// DaoTransferTx represents a transaction to transfer DAO tokens
type DaoTransferTx struct {
	account        `yaml:",inline"`
	amount         `yaml:",inline"`
	height         `yaml:",inline"`
	delimitedBlock `yaml:",inline"`
}

// SubsidyTx represents a transaction to subsidy a nested chain
type SubsidyTx struct {
	account    `yaml:",inline"`
	amount     `yaml:",inline"`
	height     `yaml:",inline"`
	committees `yaml:",inline"`
	OpCode     string `yaml:"opCode"`
}

// order is the set of fields that are used to work with order-related transactions
type order struct {
	OrderId       string `yaml:"orderId"`
	SellAmount    uint64 `yaml:"sellAmount"`
	ReceiveAmount uint64 `yaml:"receiveAmount"`
	ChainId       uint64 `yaml:"chainID"`
	committees    `yaml:",inline"`
}

// CreateOrderTx represents a transaction to create an order
type CreateOrderTx struct {
	account `yaml:",inline"`
	order   `yaml:",inline"`
	height  `yaml:",inline"`
	Data    string `yaml:"data"`
}

// EditOrderTx represents a transaction to edit an order
type EditOrderTx struct {
	order   `yaml:",inline"`
	account `yaml:",inline"`
	height  `yaml:",inline"`
}

// DeleteOrderTx represents a transaction to delete an order
type DeleteOrderTx struct {
	order   `yaml:",inline"`
	account `yaml:",inline"`
	height  `yaml:",inline"`
}

// LockOrderTx represents a transaction to lock an order
type LockOrderTx struct {
	order   `yaml:",inline"`
	account `yaml:",inline"`
	height  `yaml:",inline"`
}

// CloseOrderTx represents a transaction to close an order
type CloseOrderTx struct {
	order   `yaml:",inline"`
	account `yaml:",inline"`
	height  `yaml:",inline"`
}

// StartPollTx represents a transaction to start a poll
type StartPollTx struct {
	height   `yaml:",inline"`
	account  `yaml:",inline"`
	PollJSON string `yaml:"pollJSON"`
}
