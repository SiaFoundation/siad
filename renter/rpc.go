package renter

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

var (
	// ErrPaymentRequired is returned when a payment method is required but not
	// provided.
	ErrPaymentRequired = errors.New("payment method is required")
)

// AccountBalance returns the current balance of an ephemeral account.
func (s *Session) AccountBalance(accountID types.PublicKey, payment PaymentMethod) (types.Currency, error) {
	if payment == nil {
		return types.ZeroCurrency, ErrPaymentRequired
	}

	stream, err := s.session.DialStream()
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to open new stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(time.Second * 30))

	id, settings, err := s.currentSettings()
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to load host settings: %w", err)
	}

	err = rpc.WriteRequest(stream, rhp.RPCAccountBalanceID, &id)
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to write account balance request: %w", err)
	}

	if err := s.pay(stream, payment, settings.RPCAccountBalanceCost); err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to pay for account balance: %w", err)
	}

	req := &rhp.RPCAccountBalanceRequest{
		AccountID: accountID,
	}
	if err = rpc.WriteResponse(stream, req); err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to write account balance request: %w", err)
	}

	var resp rhp.RPCAccountBalanceResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to read account balance response: %w", err)
	}

	return resp.Balance, nil
}

// FundAccount funds an ephemeral account with the given amount. The ephemeral
// account's balance can be used as the payment method for other RPC calls.
func (s *Session) FundAccount(accountID types.PublicKey, amount types.Currency, payment PaymentMethod) (types.Currency, error) {
	if payment == nil {
		return types.ZeroCurrency, ErrPaymentRequired
	} else if _, ok := payment.(*payByContract); !ok {
		return types.ZeroCurrency, errors.New("ephemeral accounts must be funded by a contract")
	}

	stream, err := s.session.DialStream()
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to open new stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(time.Second * 30))

	id, settings, err := s.currentSettings()
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to load host settings: %w", err)
	}

	err = rpc.WriteRequest(stream, rhp.RPCFundAccountID, &id)
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to write account balance request: %w", err)
	}

	if err := s.pay(stream, payment, settings.RPCFundAccountCost.Add(amount)); err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to pay for account balance: %w", err)
	}

	err = rpc.WriteResponse(stream, &rhp.RPCFundAccountRequest{
		AccountID: accountID,
	})
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to write account balance request: %w", err)
	}

	var resp rhp.RPCFundAccountResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to read account balance response: %w", err)
	}

	return resp.Balance, nil
}

// LatestRevision returns the latest revision of a contract.
func (s *Session) LatestRevision(contractID types.ElementID, payment PaymentMethod) (rhp.Contract, error) {
	if payment == nil {
		return rhp.Contract{}, ErrPaymentRequired
	}

	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.Contract{}, fmt.Errorf("failed to open new stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(time.Second * 30))

	id, settings, err := s.currentSettings()
	if err != nil {
		return rhp.Contract{}, fmt.Errorf("failed to load host settings: %w", err)
	}

	if err := rpc.WriteRequest(stream, rhp.RPCLatestRevisionID, &id); err != nil {
		return rhp.Contract{}, fmt.Errorf("failed to write latest revision request: %w", err)
	}

	if err := s.pay(stream, payment, settings.RPCLatestRevisionCost); err != nil {
		return rhp.Contract{}, fmt.Errorf("failed to pay for latest revision: %w", err)
	}

	req := &rhp.RPCLatestRevisionRequest{
		ContractID: contractID,
	}
	if err := rpc.WriteResponse(stream, req); err != nil {
		return rhp.Contract{}, fmt.Errorf("failed to write latest revision request: %w", err)
	}

	var resp rhp.RPCLatestRevisionResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.Contract{}, fmt.Errorf("failed to read latest revision response: %w", err)
	}
	return resp.Revision, nil
}

// RegisterSettings returns the current settings from the host and registers
// them for use in other RPC.
func (s *Session) RegisterSettings(payment PaymentMethod) (settings rhp.HostSettings, _ error) {
	if payment == nil {
		return rhp.HostSettings{}, ErrPaymentRequired
	}

	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(time.Second * 30))

	if err := rpc.WriteRequest(stream, rhp.RPCSettingsID, nil); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to write settings request: %w", err)
	}

	var resp rhp.RPCSettingsResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to read settings response: %w", err)
	}

	if err := json.Unmarshal(resp.Settings, &settings); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to decode settings: %w", err)
	}

	if err := s.pay(stream, payment, settings.RPCHostSettingsCost); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to pay for settings: %w", err)
	}

	var registerResp rhp.RPCSettingsRegisteredResponse
	if err = rpc.ReadResponse(stream, &registerResp); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to read tracking response: %w", err)
	}

	s.settings = settings
	s.settingsID = registerResp.ID
	return
}

// ScanSettings returns the current settings for the host.
func (s *Session) ScanSettings() (settings rhp.HostSettings, _ error) {
	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(time.Second * 30))

	if err := rpc.WriteRequest(stream, rhp.RPCSettingsID, nil); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to write settings request: %w", err)
	}

	var resp rhp.RPCSettingsResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to read settings response: %w", err)
	}

	if err := json.Unmarshal(resp.Settings, &settings); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to decode settings: %w", err)
	}
	return
}
