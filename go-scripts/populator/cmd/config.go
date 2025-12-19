package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/canopy-network/canopy/lib"
	"github.com/canopy-network/canopy/lib/crypto"
	"github.com/canopy-network/k8s-node-tester/go-scripts/shared"
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
	BaseRpcURL      string `yaml:"baseRpcURL"`
	BaseAdminRpcURL string `yaml:"baseAdminRpcURL"`
	Incremental     bool   `yaml:"incremental"`
	BasePort        int    `yaml:"basePort"`
	Accounts        int    `yaml:"accounts"`
	Fee             uint64 `yaml:"fee"`
	Chains          []int  `yaml:"chains"`
	MaxHeight       int    `yaml:"maxHeight"`
	WaitForNewBlock bool   `yaml:"waitForNewBlock"`
}

// Common fields

type height struct {
	Height int `yaml:"height"`
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
	Count       int   `yaml:"count"`
	Concurrency int   `yaml:"concurrency"`
}

// Transaction types

// StakeTx represents a transaction to stake a validator/delegator
type StakeTx struct {
	height          `yaml:",inline"`
	account         `yaml:",inline"`
	amount          `yaml:",inline"`
	committees      `yaml:",inline"`
	Delegate        bool `yaml:"delegate"`
	EarlyWithdrawal bool `yaml:"earlyWithdrawal"`
}

// EditStakeTx represents a transaction to edit a validator/delegator's stake
type EditStakeTx struct {
	height     `yaml:",inline"`
	account    `yaml:",inline"`
	amount     `yaml:",inline"`
	committees `yaml:",inline"`
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
	ParamValue any    `yaml:"paramValue"`
	StartBlock int    `yaml:"startBlock"`
	EndBlock   int    `yaml:"endBlock"`
}

// Tx is the interface to represent a transaction
type Tx interface {
	// Kind returns the type of the transaction that is being represented
	Kind() TxType
	// Route returns the full URL for the transaction
	Route(baseURL string) string
	// Do executes the transaction
	Do(ctx context.Context, request *TxRequest, baseURL string) (string, error)
	Sender() int   // Idx of the account to use to send
	Receiver() int // Idx of the account to receive
}

// DueAt is the interface to represent a transaction that is due at a specific height
type DueAt interface {
	Tx
	Due(h int) bool
}

// Kind implementations
func (SendTx) Kind() TxType        { return TxSend }
func (StakeTx) Kind() TxType       { return TxStake }
func (EditStakeTx) Kind() TxType   { return TxEditStake }
func (PauseTx) Kind() TxType       { return TxPause }
func (UnstakeTx) Kind() TxType     { return TxUnstake }
func (ChangeParamTx) Kind() TxType { return TxChangeParam }

// Due returns true if the height is due
func (s height) Due(h int) bool { return s.Height == h }

// Due implementations
func (tx StakeTx) Due(h int) bool     { return tx.height.Due(h) }
func (tx EditStakeTx) Due(h int) bool { return tx.height.Due(h) }
func (tx PauseTx) Due(h int) bool     { return tx.height.Due(h) }
func (tx UnstakeTx) Due(h int) bool   { return tx.height.Due(h) }

// Routes implementations
func (tx SendTx) Route(baseURL string) string        { return baseURL + "/v1/admin/tx-send" }
func (tx StakeTx) Route(baseURL string) string       { return baseURL + "/v1/admin/tx-stake" }
func (tx EditStakeTx) Route(baseURL string) string   { return baseURL + "/v1/admin/tx-edit-stake" }
func (tx PauseTx) Route(baseURL string) string       { return baseURL + "/v1/admin/tx-pause" }
func (tx UnstakeTx) Route(baseURL string) string     { return baseURL + "/v1/admin/tx-unstake" }
func (tx ChangeParamTx) Route(baseURL string) string { return baseURL + "/v1/admin/tx-change-param" }

// Sender implementation
func (tx SendTx) Sender() int        { return 0 } // does not have a fixed sender
func (tx StakeTx) Sender() int       { return tx.From }
func (tx EditStakeTx) Sender() int   { return tx.From }
func (tx PauseTx) Sender() int       { return tx.From }
func (tx UnstakeTx) Sender() int     { return tx.From }
func (tx ChangeParamTx) Sender() int { return tx.From }

// Receiver implementation
func (tx SendTx) Receiver() int        { return 0 } // does not have a fixed receiver
func (tx StakeTx) Receiver() int       { return tx.To }
func (tx EditStakeTx) Receiver() int   { return tx.To }
func (tx PauseTx) Receiver() int       { return tx.To }
func (tx UnstakeTx) Receiver() int     { return tx.To }
func (tx ChangeParamTx) Receiver() int { return tx.To }

// Do sends a send transaction
func (tx SendTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	return postTx(ctx, tx.Route(baseURL), txRequest{
		Amount:   tx.Amount,
		Address:  req.From.String(),
		Output:   req.To.String(),
		Password: req.Password,
		Fee:      req.Fee,
		Submit:   true,
	})
}

// Do sends a stake transaction
func (tx StakeTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	return postTx(ctx, tx.Route(baseURL), txRequest{
		Amount:          tx.Amount,
		Address:         req.From.String(),
		Output:          req.To.String(),
		Password:        req.Password,
		Fee:             req.Fee,
		Delegate:        tx.Delegate,
		Committees:      tx.committees.String(),
		EarlyWithdrawal: tx.EarlyWithdrawal,
		Submit:          true,
	})
}

// Do sends an edit stake transaction
func (tx EditStakeTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	return postTx(ctx, tx.Route(baseURL), txRequest{})
}

// Do sends a pause transaction
func (tx PauseTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	return postTx(ctx, tx.Route(baseURL), txRequest{})
}

// Do sends an unstake transaction
func (tx UnstakeTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	return postTx(ctx, tx.Route(baseURL), txRequest{})
}

// Do sends a change parameter transaction
func (tx ChangeParamTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	return postTx(ctx, tx.Route(baseURL), txRequest{})
}

// BuildTxRequest constructs a TxRequest with the required fields
func BuildTxRequest(from, to shared.Account, config General) (*TxRequest, error) {
	fromAddr, err := crypto.NewAddressFromString(from.Address)
	if err != nil {
		return nil, fmt.Errorf("create FROM address: %w", err)
	}
	toAddr, err := crypto.NewAddressFromString(to.Address)
	if err != nil {
		return nil, fmt.Errorf("create TO address: %w", err)
	}
	fee := baseFee
	if config.Fee != 0 {
		fee = config.Fee
	}
	req := TxRequest{
		Fee:      fee,
		Password: from.Password,
		From:     fromAddr,
		To:       toAddr,
	}
	return &req, nil
}

func postTx(ctx context.Context, url string, obj any) (string, error) {
	// marshal the tx
	bz, e := json.MarshalIndent(obj, "", "  ")
	if e != nil {
		return "", fmt.Errorf("post tx: marshalling: %w", e)
	}
	// send the tx
	hash, e := post(ctx, url, bz)
	if e != nil {
		return "", fmt.Errorf("post tx: posting: %w", e)
	}
	return strings.Trim(string(hash), "\""), nil
}

func post(ctx context.Context, url string, bz []byte) ([]byte, error) {
	// generate the request
	request, e := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bz))
	if e != nil {
		return nil, fmt.Errorf("post: request %s:%s", url, e.Error())
	}
	// execute the request
	resp, e := httpClient.Do(request)
	if e != nil {
		return nil, fmt.Errorf("post: do %s:%s", url, e.Error())
	}
	defer resp.Body.Close()
	// check the status code
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("post: non 200 status code (%s): %d", url, resp.StatusCode)
	}
	// read the request bytes
	respBz, e := io.ReadAll(resp.Body)
	if e != nil {
		return nil, fmt.Errorf("post: reading response %s:%s", url, e.Error())
	}
	// return
	return respBz, nil
}

// TxRequest is the public struct for the arguments for a transaction request
type TxRequest struct {
	From     crypto.AddressI // Address of the sender
	To       crypto.AddressI // Address of the recipient
	Password string          // Password for the sender's account
	Fee      uint64          // Fee for the transaction
}

// txRequest represents a full transaction request
type txRequest struct {
	Amount          uint64          `json:"amount"`
	PubKey          string          `json:"pubKey"`
	NetAddress      string          `json:"netAddress"`
	Output          string          `json:"output"`
	OpCode          lib.HexBytes    `json:"opCode"`
	Data            lib.HexBytes    `json:"data"`
	Fee             uint64          `json:"fee"`
	Delegate        bool            `json:"delegate"`
	EarlyWithdrawal bool            `json:"earlyWithdrawal"`
	Submit          bool            `json:"submit"`
	ReceiveAmount   uint64          `json:"receiveAmount"`
	ReceiveAddress  lib.HexBytes    `json:"receiveAddress"`
	Percent         uint64          `json:"percent"`
	OrderId         string          `json:"orderId"`
	Memo            string          `json:"memo"`
	PollJSON        json.RawMessage `json:"pollJSON"`
	PollApprove     bool            `json:"pollApprove"`
	Signer          lib.HexBytes    `json:"signer"`
	SignerNickname  string          `json:"signerNickname"`
	Address         string          `json:"address"`
	Nickname        string          `json:"nickname"`
	Password        string          `json:"password"`

	ParamSpace string `json:"paramSpace"`
	ParamKey   string `json:"paramKey"`
	ParamValue string `json:"paramValue"`
	StartBlock uint64 `json:"startBlock"`
	EndBlock   uint64 `json:"endBlock"`

	Committees string `json:"committees"`
}
