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
	stream.SetDeadline(time.Now().Add(30 * time.Second))

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
	stream.SetDeadline(time.Now().Add(30 * time.Second))

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

	req := &rhp.RPCFundAccountRequest{
		AccountID: accountID,
	}
	if err := rpc.WriteResponse(stream, req); err != nil {
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
	stream.SetDeadline(time.Now().Add(30 * time.Second))

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
	stream.SetDeadline(time.Now().Add(30 * time.Second))

	if err := rpc.WriteRequest(stream, rhp.RPCSettingsID, nil); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to write settings request: %w", err)
	}

	var resp rhp.RPCSettingsResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to read settings response: %w", err)
	} else if err := json.Unmarshal(resp.Settings, &settings); err != nil {
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
func (s *Session) ScanSettings() (rhp.HostSettings, error) {
	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(30 * time.Second))

	if err := rpc.WriteRequest(stream, rhp.RPCSettingsID, nil); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to write settings request: %w", err)
	}

	var resp rhp.RPCSettingsResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to read settings response: %w", err)
	}

	var settings rhp.HostSettings
	if err := json.Unmarshal(resp.Settings, &settings); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to decode settings: %w", err)
	}
	return settings, nil
}

// FormContract negotiates a new contract with the host using the specified
// funds and duration.
func (s *Session) FormContract(renterKey types.PrivateKey, hostCollateral, renterAllowance types.Currency, settings rhp.HostSettings, endHeight uint64) (rhp.Contract, []types.Transaction, error) {
	renterAddr := s.wallet.Address()
	renterPub := renterKey.PublicKey()
	hostAddr := settings.Address

	vc := s.cm.TipContext()
	startHeight := vc.Index.Height
	if endHeight < startHeight {
		return rhp.Contract{}, nil, errors.New("end height must be greater than start height")
	}

	hostPayout := hostCollateral.Add(settings.ContractFee)
	fc := types.FileContract{
		Filesize:           0,
		WindowStart:        endHeight,
		WindowEnd:          endHeight + settings.WindowSize,
		ValidRenterOutput:  types.SiacoinOutput{Value: renterAllowance, Address: renterAddr},
		ValidHostOutput:    types.SiacoinOutput{Value: hostPayout, Address: hostAddr},
		MissedRenterOutput: types.SiacoinOutput{Value: renterAllowance, Address: renterAddr},
		MissedHostOutput:   types.SiacoinOutput{Value: hostPayout, Address: hostAddr},
		RenterPublicKey:    renterPub,
		HostPublicKey:      s.hostKey,
	}

	if err := rhp.ValidateContractFormation(fc, startHeight, settings); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("contract formation invalid: %w", err)
	}

	sigHash := vc.ContractSigHash(fc)
	fc.RenterSignature = renterKey.SignHash(sigHash)

	txn := types.Transaction{
		FileContracts: []types.FileContract{fc},
	}
	txn.MinerFee = s.tpool.RecommendedFee().Mul64(vc.TransactionWeight(txn))
	// fund the formation transaction with the renter allowance, contract fee,
	// siafund tax, and miner fee.
	renterFundAmount := renterAllowance.Add(settings.ContractFee).Add(vc.FileContractTax(fc)).Add(txn.MinerFee)
	toSign, cleanup, err := s.wallet.FundTransaction(&txn, renterFundAmount, nil)
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to fund transaction: %w", err)
	}
	defer cleanup()

	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(2 * time.Minute))

	req := &rhp.RPCContractRequest{
		Transactions: []types.Transaction{txn},
	}
	if err := rpc.WriteRequest(stream, rhp.RPCFormContractID, req); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write form contract request: %w", err)
	}

	var resp rhp.RPCFormContractHostAdditions
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host additions: %w", err)
	} else if !s.hostKey.VerifyHash(sigHash, resp.ContractSignature) {
		return rhp.Contract{}, nil, errors.New("host signature is invalid")
	}

	// add the host's additions to the transaction.
	txn.SiacoinInputs = append(txn.SiacoinInputs, resp.Inputs...)
	txn.SiacoinOutputs = append(txn.SiacoinOutputs, resp.Outputs...)
	fc.HostSignature = resp.ContractSignature
	txn.FileContracts[0] = fc

	// sign the transaction and send the signatures to the host.
	if err := s.wallet.SignTransaction(vc, &txn, toSign); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to sign transaction: %w", err)
	}
	renterSigs := rhp.RPCContractSignatures{
		SiacoinInputSignatures: make([][]types.InputSignature, len(txn.SiacoinInputs)),
	}
	for i := range txn.SiacoinInputs {
		renterSigs.SiacoinInputSignatures[i] = append(renterSigs.SiacoinInputSignatures[i], txn.SiacoinInputs[i].Signatures...)
	}

	if err := rpc.WriteResponse(stream, &renterSigs); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write renter signatures: %w", err)
	}

	// read the host's signatures and add them to the transaction.
	var hostSigs rhp.RPCContractSignatures
	if err := rpc.ReadResponse(stream, &hostSigs); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host signatures: %w", err)
	}
	for i := range hostSigs.SiacoinInputSignatures {
		txn.SiacoinInputs[i].Signatures = append(txn.SiacoinInputs[i].Signatures, hostSigs.SiacoinInputSignatures[i]...)
	}

	return rhp.Contract{
		ID:       txn.FileContractID(0),
		Revision: fc,
	}, append(resp.Parents, txn), nil
}

// RenewContract finalizes and renews an existing contract with the host adding
// additional funds and duration.
func (s *Session) RenewContract(renterKey types.PrivateKey, contract types.FileContractRevision, additionalCollateral, additionalRenterFunds types.Currency, endHeight uint64) (rhp.Contract, []types.Transaction, error) {
	// contract renewal should use the registered settings ID.
	settingsID, settings, err := s.currentSettings()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to use settings: %w", err)
	}
	hostAddr := settings.Address
	vc := s.cm.TipContext()
	startHeight := vc.Index.Height
	if endHeight < startHeight {
		return rhp.Contract{}, nil, errors.New("end height must be greater than start height")
	}
	renterAddr := s.wallet.Address()

	// calculate the "base" storage cost to the renter and risked collateral for
	// the host for the data already in the contract. If the contract height did
	// not increase, base costs are zero.
	var baseStorageCost, baseCollateral types.Currency
	if contractEnd := endHeight + settings.WindowSize; contractEnd > contract.Revision.WindowEnd {
		extension := contractEnd - contract.Revision.WindowEnd
		baseStorageCost = settings.StoragePrice.Mul64(contract.Revision.Filesize).Mul64(extension)
		baseCollateral = settings.Collateral.Mul64(contract.Revision.Filesize).Mul64(extension)
	}

	// calculate the total collateral the host is expected to add to the
	// contract.
	totalCollateral := baseCollateral.Add(additionalCollateral)
	if totalCollateral.Cmp(settings.MaxCollateral) > 0 {
		return rhp.Contract{}, nil, errors.New("collateral too large")
	}

	// create the renewed contract
	//
	// The host valid output includes the contract fee, base storage revenue,
	// base collateral, and additional collateral. In the event of failure
	// the base collateral and base storage cost should be burned.
	validHostPayout := settings.ContractFee.Add(baseStorageCost).Add(totalCollateral)
	missedHostPayout := settings.ContractFee.Add(additionalCollateral)
	renewal := types.FileContract{
		Filesize:           contract.Revision.Filesize,
		FileMerkleRoot:     contract.Revision.FileMerkleRoot,
		WindowStart:        endHeight,
		WindowEnd:          endHeight + settings.WindowSize,
		ValidRenterOutput:  types.SiacoinOutput{Address: renterAddr, Value: additionalRenterFunds},
		ValidHostOutput:    types.SiacoinOutput{Address: hostAddr, Value: validHostPayout},
		MissedRenterOutput: types.SiacoinOutput{Address: renterAddr, Value: additionalRenterFunds},
		MissedHostOutput:   types.SiacoinOutput{Address: hostAddr, Value: missedHostPayout},
		RenterPublicKey:    contract.Revision.RenterPublicKey,
		HostPublicKey:      contract.Revision.HostPublicKey,
	}
	renewalSigHash := vc.ContractSigHash(renewal)
	renewal.RenterSignature = renterKey.SignHash(renewalSigHash)
	// create a finalization revision that locks the contract to future
	// revisions.
	finalization := contract.Revision
	finalization.RevisionNumber = types.MaxRevisionNumber
	finalizationSigHash := vc.ContractSigHash(finalization)
	finalization.RenterSignature = renterKey.SignHash(finalizationSigHash)

	// create the renewal transaction. The transaction must include a resolution
	// with a finalization revision and a new contract with a matching file size
	// and merkle root. Both contracts must be signed by the renter, but input
	// signatures should not be included yet.
	renewalTxn := types.Transaction{
		FileContractResolutions: []types.FileContractResolution{{
			Parent:       contract.Parent,
			Finalization: finalization,
		}},
		FileContracts: []types.FileContract{renewal},
	}
	// TODO: better fee calculation
	renewalTxn.MinerFee = s.tpool.RecommendedFee().Mul64(vc.TransactionWeight(renewalTxn))
	// Fund the renewal transaction. The renter is responsible for the renter
	// funds, contract fee, base storage cost, siafund tax, and miner fee.
	renterFundAmount := additionalRenterFunds.Add(settings.ContractFee).Add(baseStorageCost).Add(vc.FileContractTax(renewal)).Add(renewalTxn.MinerFee)
	toSign, cleanup, err := s.wallet.FundTransaction(&renewalTxn, renterFundAmount, nil)
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to fund renewal transaction: %w", err)
	}
	defer cleanup()

	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(2 * time.Minute))

	req := &rhp.RPCContractRequest{
		Transactions: []types.Transaction{renewalTxn},
	}
	if err := rpc.WriteRequest(stream, rhp.RPCRenewContractID, &settingsID); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write RPC id: %w", err)
	} else if err := rpc.WriteObject(stream, req); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write request: %w", err)
	}

	var resp rhp.RPCRenewContractHostAdditions
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host additions: %w", err)
	} else if !s.hostKey.VerifyHash(finalizationSigHash, resp.FinalizationSignature) {
		return rhp.Contract{}, nil, errors.New("host finalization signature is invalid")
	} else if !s.hostKey.VerifyHash(renewalSigHash, resp.RenewalSignature) {
		return rhp.Contract{}, nil, errors.New("host renewal signature is invalid")
	}

	// add the host's contract signatures to the transaction.
	renewal.HostSignature = resp.RenewalSignature
	finalization.HostSignature = resp.FinalizationSignature
	renewalTxn.FileContracts[0] = renewal
	renewalTxn.FileContractResolutions[0].Finalization = finalization
	// add the host's inputs to the transaction.
	renewalTxn.SiacoinInputs = append(renewalTxn.SiacoinInputs, resp.Inputs...)
	renewalTxn.SiacoinOutputs = append(renewalTxn.SiacoinOutputs, resp.Outputs...)

	// sign the transaction and send the signatures to the host.
	if err := s.wallet.SignTransaction(vc, &renewalTxn, toSign); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to sign renewal transaction: %w", err)
	}
	renterSigs := rhp.RPCContractSignatures{
		SiacoinInputSignatures: make([][]types.InputSignature, len(renewalTxn.SiacoinInputs)),
	}
	for i := range renewalTxn.SiacoinInputs {
		renterSigs.SiacoinInputSignatures[i] = append(renterSigs.SiacoinInputSignatures[i], renewalTxn.SiacoinInputs[i].Signatures...)
	}
	if err := rpc.WriteResponse(stream, &renterSigs); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write renter signatures: %w", err)
	}

	// add the host signatures to the transaction.
	var hostSigs rhp.RPCContractSignatures
	if err := rpc.ReadResponse(stream, &hostSigs); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host signatures: %w", err)
	}
	for i := range hostSigs.SiacoinInputSignatures {
		renewalTxn.SiacoinInputs[i].Signatures = append(renewalTxn.SiacoinInputs[i].Signatures, hostSigs.SiacoinInputSignatures[i]...)
	}

	return rhp.Contract{
		ID:       renewalTxn.FileContractID(0),
		Revision: renewal,
	}, append(resp.Parents, renewalTxn), nil
}
