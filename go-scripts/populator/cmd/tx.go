package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/canopy-network/canopy/cmd/rpc"
	"github.com/canopy-network/canopy/fsm"
	"github.com/canopy-network/canopy/lib"
	"github.com/canopy-network/canopy/lib/crypto"
	"github.com/canopy-network/k8s-node-tester/go-scripts/shared"
)

const (
	TxSend        TxType = "send"
	TxStake       TxType = "stake"
	TxEditStake   TxType = "editStake"
	TxPause       TxType = "pause"
	TxUnstake     TxType = "unstake"
	TxChangeParam TxType = "changeParam"
	TxDaoTransfer TxType = "daoTransfer"
	TxSubsidy     TxType = "subsidy"
	TxCreateOrder TxType = "createOrder"
	TxEditOrder   TxType = "editOrder"
	TxDeleteOrder TxType = "deleteOrder"
	TxLockOrder   TxType = "lockOrder"
	TxCloseOrder  TxType = "closeOrder"
	TxStartPoll   TxType = "startPoll"

	subsidyRoute = "/v1/admin/tx-subsidy"
)

var (
	ErrAlreadyStaked        = errors.New("validator already staked")
	ErrNotStaked            = errors.New("validator not staked")
	ErrInsufficientStake    = errors.New("insufficient stake")
	ErrNotValidator         = errors.New("not a validator")
	ErrInvalidJSON          = errors.New("invalid JSON")
	ErrInvalidPollEndHeight = errors.New("invalid poll end height")
)

// TxType is the type of transaction
type TxType string

// Tx is the interface to represent a transaction
type Tx interface {
	// Kind returns the type of the transaction that is being represented
	Kind() TxType
	// Do executes the transaction
	Do(ctx context.Context, request *TxRequest, baseURL string) (string, error)
	// Validate makes sure the transaction is valid under its own rules
	Validate(ctx context.Context, req *TxRequest) error
	Sender() int   // Idx of the account to use to send
	Receiver() int // Idx of the account to receive
}

// DueAt is the interface to represent a transaction that is due at a specific height
type DueAt interface {
	Tx
	Due(h uint64) bool
}

// Kind implementations
func (SendTx) Kind() TxType        { return TxSend }
func (StakeTx) Kind() TxType       { return TxStake }
func (EditStakeTx) Kind() TxType   { return TxEditStake }
func (PauseTx) Kind() TxType       { return TxPause }
func (UnstakeTx) Kind() TxType     { return TxUnstake }
func (ChangeParamTx) Kind() TxType { return TxChangeParam }
func (DaoTransferTx) Kind() TxType { return TxDaoTransfer }
func (SubsidyTx) Kind() TxType     { return TxSubsidy }
func (CreateOrderTx) Kind() TxType { return TxCreateOrder }
func (EditOrderTx) Kind() TxType   { return TxEditOrder }
func (DeleteOrderTx) Kind() TxType { return TxDeleteOrder }
func (LockOrderTx) Kind() TxType   { return TxLockOrder }
func (CloseOrderTx) Kind() TxType  { return TxCloseOrder }
func (StartPollTx) Kind() TxType   { return TxStartPoll }

// Due returns true if the height is due
func (s height) Due(h uint64) bool { return s.Height == h }

// Due implementations
func (tx StakeTx) Due(h uint64) bool       { return tx.height.Due(h) }
func (tx EditStakeTx) Due(h uint64) bool   { return tx.height.Due(h) }
func (tx PauseTx) Due(h uint64) bool       { return tx.height.Due(h) }
func (tx UnstakeTx) Due(h uint64) bool     { return tx.height.Due(h) }
func (tx DaoTransferTx) Due(h uint64) bool { return tx.height.Due(h) }
func (tx SubsidyTx) Due(h uint64) bool     { return tx.height.Due(h) }
func (tx CreateOrderTx) Due(h uint64) bool { return tx.height.Due(h) }
func (tx EditOrderTx) Due(h uint64) bool   { return tx.height.Due(h) }
func (tx DeleteOrderTx) Due(h uint64) bool { return tx.height.Due(h) }
func (tx LockOrderTx) Due(h uint64) bool   { return tx.height.Due(h) }
func (tx CloseOrderTx) Due(h uint64) bool  { return tx.height.Due(h) }
func (tx StartPollTx) Due(h uint64) bool   { return tx.height.Due(h) }

// Sender implementation
func (tx SendTx) Sender() int        { return -1 } // does not have a fixed sender
func (tx StakeTx) Sender() int       { return tx.From }
func (tx EditStakeTx) Sender() int   { return tx.From }
func (tx PauseTx) Sender() int       { return tx.From }
func (tx UnstakeTx) Sender() int     { return tx.From }
func (tx ChangeParamTx) Sender() int { return tx.From }
func (tx DaoTransferTx) Sender() int { return tx.From }
func (tx SubsidyTx) Sender() int     { return tx.From }
func (tx CreateOrderTx) Sender() int { return tx.From }
func (tx EditOrderTx) Sender() int   { return tx.From }
func (tx DeleteOrderTx) Sender() int { return tx.From }
func (tx LockOrderTx) Sender() int   { return tx.From }
func (tx CloseOrderTx) Sender() int  { return tx.From }
func (tx StartPollTx) Sender() int   { return tx.From }

// Receiver implementation
func (tx SendTx) Receiver() int        { return -1 } // does not have a fixed receiver
func (tx StakeTx) Receiver() int       { return tx.To }
func (tx EditStakeTx) Receiver() int   { return tx.To }
func (tx PauseTx) Receiver() int       { return tx.To }
func (tx UnstakeTx) Receiver() int     { return tx.To }
func (tx ChangeParamTx) Receiver() int { return tx.To }
func (tx DaoTransferTx) Receiver() int { return tx.To }
func (tx SubsidyTx) Receiver() int     { return tx.To }
func (tx CreateOrderTx) Receiver() int { return tx.To }
func (tx EditOrderTx) Receiver() int   { return tx.To }
func (tx DeleteOrderTx) Receiver() int { return tx.To }
func (tx LockOrderTx) Receiver() int   { return tx.To }
func (tx CloseOrderTx) Receiver() int  { return tx.To }
func (tx StartPollTx) Receiver() int   { return tx.To }

// Validate implementation
func (tx SendTx) Validate(ctx context.Context, req *TxRequest) error        { return nil }
func (tx ChangeParamTx) Validate(ctx context.Context, req *TxRequest) error { return nil }
func (tx DaoTransferTx) Validate(ctx context.Context, req *TxRequest) error { return nil }
func (tx SubsidyTx) Validate(ctx context.Context, req *TxRequest) error     { return nil }
func (tx CreateOrderTx) Validate(ctx context.Context, req *TxRequest) error { return nil }
func (tx EditOrderTx) Validate(ctx context.Context, req *TxRequest) error   { return nil }
func (tx DeleteOrderTx) Validate(ctx context.Context, req *TxRequest) error { return nil }
func (tx LockOrderTx) Validate(ctx context.Context, req *TxRequest) error   { return nil }
func (tx CloseOrderTx) Validate(ctx context.Context, req *TxRequest) error  { return nil }

// Validate ensures that the sender is not already staked
func (tx StakeTx) Validate(ctx context.Context, req *TxRequest) error {
	staked, _, err := isStaked(req.From.String())
	if err != nil {
		return err
	}
	if staked {
		return ErrAlreadyStaked
	}
	return nil
}

// Validate ensures that the sender is already staked and the new stake is higher than the
// current stake
func (tx EditStakeTx) Validate(ctx context.Context, req *TxRequest) error {
	// validate that is staked
	staked, _, err := isStaked(req.From.String())
	if err != nil {
		return err
	}
	if !staked {
		return ErrNotStaked
	}
	// confirm new stake is higher than the current stake
	val, err := cnpyClient.Validator(0, req.From.String())
	if err != nil {
		return err
	}
	if tx.Amount <= val.StakedAmount {
		return fmt.Errorf("%w [current: %d]", ErrInsufficientStake, val.StakedAmount)
	}
	return nil
}

// Validate ensures that the sender is staked
func (tx UnstakeTx) Validate(ctx context.Context, req *TxRequest) error {
	staked, _, err := isStaked(req.From.String())
	if err != nil {
		return err
	}
	if !staked {
		return ErrNotStaked
	}

	return nil
}

// Validate ensures that the sender is stake and not a delegator
func (tx PauseTx) Validate(ctx context.Context, req *TxRequest) error {
	staked, delegator, err := isStaked(req.From.String())
	if err != nil {
		return err
	}
	if !staked {
		return ErrNotStaked
	}
	if delegator {
		return ErrNotValidator
	}
	return nil
}

// Validate ensures that the poll has the valid JSON structure
func (tx StartPollTx) Validate(ctx context.Context, req *TxRequest) error {
	var poll fsm.StartPoll
	if err := json.Unmarshal([]byte(tx.PollJSON), &poll); err != nil {
		return err
	}
	if poll.EndHeight == 0 {
		return ErrInvalidPollEndHeight
	}
	return nil
}

// Do sends a send transaction
func (tx SendTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxSend(from, req.To.String(), tx.Amount, req.Password, true, req.Fee)
	return *hash, err
}

// Do sends a stake transaction
func (tx StakeTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	if err := tx.Validate(ctx, req); err != nil {
		return "", fmt.Errorf("stake: [%s] %w", req.From, err)
	}
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxStake(from,
		tx.NetAddr,
		tx.Amount,
		tx.committees.String(),
		req.To.String(),
		from,
		tx.Delegate,
		tx.EarlyWithdrawal,
		req.Password,
		true,
		req.Fee)
	if err != nil {
		return "", fmt.Errorf("stake: [%s] %w", req.From, err)
	}
	return *hash, nil
}

// Do sends an edit stake transaction
func (tx EditStakeTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	if err := tx.Validate(ctx, req); err != nil {
		return "", fmt.Errorf("edit stake: [%s] %w", req.From, err)
	}
	// send transaction
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxEditStake(from,
		tx.NetAddr,
		tx.Amount,
		tx.committees.String(),
		req.To.String(),
		from,
		tx.Delegate,
		tx.EarlyWithdrawal,
		req.Password,
		true,
		req.Fee)
	if err != nil {
		return "", fmt.Errorf("edit stake: [%s] %w", req.From, err)
	}
	return *hash, nil
}

// Do sends a pause transaction
func (tx PauseTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	if err := tx.Validate(ctx, req); err != nil {
		return "", fmt.Errorf("pause: [%s] %w", req.From, err)
	}
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxPause(from, from, req.Password, true, req.Fee)
	return *hash, err
}

// Do sends an unstake transaction
func (tx UnstakeTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	if err := tx.Validate(ctx, req); err != nil {
		return "", fmt.Errorf("unstake: [%s] %w", req.From, err)
	}
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxUnstake(from, from, req.Password, true, req.Fee)
	return *hash, err
}

// Do sends a change parameter transaction
func (tx ChangeParamTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxChangeParam(
		from,
		tx.ParamSpace,
		tx.ParamKey,
		tx.ParamValue,
		tx.StartBlock,
		tx.EndBlock,
		req.Password,
		true,
		req.Fee)
	return *hash, err
}

// Do sends a DAO transfer transaction
func (tx DaoTransferTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxDaoTransfer(
		from,
		tx.Amount,
		tx.StartBlock,
		tx.EndBlock,
		req.Password,
		true,
		req.Fee)
	return *hash, err
}

// Do sends a subsidy transaction
func (tx SubsidyTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	return postTx(ctx, baseURL+subsidyRoute, txRequest{
		Address:    req.From.String(),
		Amount:     tx.Amount,
		Committees: tx.committees.String(),
		Password:   req.Password,
		Fee:        req.Fee,
		OpCode:     lib.HexBytes(tx.OpCode),
	})
}

// CreateOrderTx sends a create order transaction
func (tx CreateOrderTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxCreateOrder(
		from,
		tx.SellAmount,
		tx.ReceiveAmount,
		tx.ChainId,
		req.To.String(),
		req.Password,
		lib.HexBytes(tx.Data),
		true,
		req.Fee)
	return *hash, err
}

// EditOrderTx sends an edit order transaction
func (tx EditOrderTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxEditOrder(
		from,
		tx.SellAmount,
		tx.ReceiveAmount,
		tx.OrderId,
		tx.ChainId,
		req.To.String(),
		req.Password,
		true,
		req.Fee)
	return *hash, err
}

// DeleteOrderTx sends a delete order transaction
func (tx DeleteOrderTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxDeleteOrder(
		from,
		tx.OrderId,
		tx.ChainId,
		req.To.String(),
		true,
		req.Fee)
	return *hash, err
}

// LockOrderTx sends a lock order transaction
func (tx LockOrderTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxLockOrder(
		from,
		req.To.String(),
		tx.OrderId,
		req.Password,
		true,
		req.Fee)
	return *hash, err
}

// CloseOrderTx sends a close order transaction
func (tx CloseOrderTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxCloseOrder(
		from,
		tx.OrderId,
		req.Password,
		true,
		req.Fee)
	return *hash, err
}

// StartPollTx sends a start poll transaction
func (tx StartPollTx) Do(ctx context.Context, req *TxRequest, baseURL string) (string, error) {
	if err := tx.Validate(ctx, req); err != nil {
		return "", err
	}
	from := rpc.AddrOrNickname{Address: req.From.String()}
	hash, _, err := cnpyClient.TxStartPoll(
		from,
		json.RawMessage(tx.PollJSON),
		req.Password,
		true,
		req.Fee)
	return *hash, err
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

// postTx sends a transaction to the node, used for transactions that are not implemented by the
// client
func postTx(ctx context.Context, url string, obj txRequest) (string, error) {
	// marshal the tx
	bz, e := json.Marshal(obj)
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

// post sends a POST request to the node
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

// network utils

func isStaked(address string) (staked, delegator bool, err error) {
	if address == "" {
		return false, false, errors.New("address is empty")
	}
	validator, err := cnpyClient.Validator(0, address)
	if err != nil {
		// client error handling is broken, need to handle errors by looking at the error message string
		if strings.Contains(err.Error(), "validator does not exist") {
			return false, false, nil
		}
		return false, false, err
	}
	return validator.UnstakingHeight == 0, validator.Delegate, nil
}
