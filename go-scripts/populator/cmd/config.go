package main

type TxType string

const (
	TxStake       TxType = "stake"
	TxEditStake   TxType = "editStake"
	TxPause       TxType = "pause"
	TxUnstake     TxType = "unstake"
	TxChangeParam TxType = "changeParam"
)

// Profile is a configuration for a single profile
type Profile struct {
	General      General      `yaml:"general"`
	Send         SendTx       `yaml:"send"`         // handled separately
	Transactions Transactions `yaml:"transactions"` // height-driven ones
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
	BaseURL  string `yaml:"baseURL"`
	BasePort int    `yaml:"basePort"`
	Accounts int    `yaml:"accounts"`
	Fee      int64  `yaml:"fee"`
	Chains   []int  `yaml:"chains"`
}

// Common fields

type Height struct {
	Height int `yaml:"height"`
}

type Account struct {
	Account int `yaml:"account"`
}

type Amount struct {
	Amount int `yaml:"amount"`
}

type Committees struct {
	Committees []int `yaml:"committees"`
}

// SendTx Tx is handled separately
type SendTx struct {
	Chains []int `yaml:"chains"`
	Count  int   `yaml:"count"`
	Amount int64 `yaml:"amount"`
}

// Transaction types

type StakeTx struct {
	Height     `yaml:",inline"`
	Account    `yaml:",inline"`
	Amount     `yaml:",inline"`
	Committees `yaml:",inline"`
	Delegate   bool `yaml:"delegate"`
}

type EditStakeTx struct {
	Height     `yaml:",inline"`
	Account    `yaml:",inline"`
	Amount     `yaml:",inline"`
	Committees `yaml:",inline"`
}

type PauseTx struct {
	Height  `yaml:",inline"`
	Account `yaml:",inline"`
}

type UnstakeTx struct {
	Height  `yaml:",inline"`
	Account `yaml:",inline"`
}

type ChangeParamTx struct {
	Account    `yaml:",inline"`
	Height     `yaml:",inline"`
	ParamSpace string `yaml:"paramSpace"`
	ParamKey   string `yaml:"paramKey"`
	ParamValue any    `yaml:"paramValue"`
	StartBlock int    `yaml:"startBlock"`
	EndBlock   int    `yaml:"endBlock"`
}

// Tx is a type that represents a transaction
type Tx interface {
	Kind() TxType
}

// DueAt is a type that represents a transaction that is due at a specific height
type DueAt interface {
	Tx
	Due(h int) bool
}

// Kind implementations
func (StakeTx) Kind() TxType       { return TxStake }
func (EditStakeTx) Kind() TxType   { return TxEditStake }
func (PauseTx) Kind() TxType       { return TxPause }
func (UnstakeTx) Kind() TxType     { return TxUnstake }
func (ChangeParamTx) Kind() TxType { return TxChangeParam }

// Due returns true if the height is due
func (s Height) Due(h int) bool { return s.Height == h }

// Due implementations
func (t StakeTx) Due(h int) bool     { return t.Height.Due(h) }
func (t EditStakeTx) Due(h int) bool { return t.Height.Due(h) }
func (t PauseTx) Due(h int) bool     { return t.Height.Due(h) }
func (t UnstakeTx) Due(h int) bool   { return t.Height.Due(h) }
