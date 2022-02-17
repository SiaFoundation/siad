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
func (s *Session) FormContract(renterKey types.PrivateKey, renterFunds, hostCollateral types.Currency, endHeight uint64, settings rhp.HostSettings) (rhp.Contract, types.Transaction, error) {
	renterAddr := s.wallet.Address()
	hostAddr := settings.Address

	vc := s.cm.TipContext()
	startHeight := vc.Index.Height
	if endHeight < startHeight {
		return rhp.Contract{}, types.Transaction{}, errors.New("end height must be greater than start height")
	}

	hostPayout := hostCollateral.Add(settings.ContractFee)
	fc := types.FileContract{
		Filesize:           0,
		WindowStart:        endHeight,
		WindowEnd:          endHeight + settings.WindowSize,
		ValidRenterOutput:  types.SiacoinOutput{Value: renterFunds, Address: renterAddr},
		ValidHostOutput:    types.SiacoinOutput{Value: hostPayout, Address: hostAddr},
		MissedRenterOutput: types.SiacoinOutput{Value: renterFunds, Address: renterAddr},
		MissedHostOutput:   types.SiacoinOutput{Value: hostPayout, Address: hostAddr},
		RenterPublicKey:    renterKey.PublicKey(),
		HostPublicKey:      s.hostKey,
	}

	if err := rhp.ValidateContractFormation(fc, startHeight, settings); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("contract formation invalid: %w", err)
	}

	sigHash := vc.ContractSigHash(fc)
	fc.RenterSignature = renterKey.SignHash(sigHash)

	txn := types.Transaction{
		FileContracts: []types.FileContract{fc},
	}
	txn.MinerFee = s.tpool.RecommendedFee().Mul64(vc.TransactionWeight(txn))
	// fund the formation transaction with the renter allowance, contract fee,
	// siafund tax, and miner fee.
	totalCost := renterFunds.Add(settings.ContractFee).Add(vc.FileContractTax(fc)).Add(txn.MinerFee)
	toSign, cleanup, err := s.wallet.FundTransaction(&txn, totalCost, nil)
	if err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to fund transaction: %w", err)
	}
	defer cleanup()

	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(2 * time.Minute))

	req := &rhp.RPCFormContractRequest{
		Inputs:   txn.SiacoinInputs,
		Outputs:  txn.SiacoinOutputs,
		MinerFee: txn.MinerFee,
		Contract: fc,
	}
	if err := rpc.WriteRequest(stream, rhp.RPCFormContractID, req); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to write form contract request: %w", err)
	}

	var resp rhp.RPCFormContractHostAdditions
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to read host additions: %w", err)
	} else if !s.hostKey.VerifyHash(sigHash, resp.ContractSignature) {
		return rhp.Contract{}, types.Transaction{}, errors.New("host signature is invalid")
	}

	// add the host's additions to the transaction.
	renterInputs := txn.SiacoinInputs
	txn.SiacoinInputs = append(txn.SiacoinInputs, resp.Inputs...)
	hostInputs := txn.SiacoinInputs[len(renterInputs):]
	txn.SiacoinOutputs = append(txn.SiacoinOutputs, resp.Outputs...)
	txn.FileContracts[0].HostSignature = resp.ContractSignature

	// sign the transaction and send the signatures to the host.
	if err := s.wallet.SignTransaction(vc, &txn, toSign); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to sign transaction: %w", err)
	}
	renterSigs := rhp.RPCContractSignatures{
		SiacoinInputSignatures: make([][]types.Signature, len(renterInputs)),
	}
	for i := range renterInputs {
		renterSigs.SiacoinInputSignatures[i] = txn.SiacoinInputs[i].Signatures
	}
	if err := rpc.WriteResponse(stream, &renterSigs); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to write renter signatures: %w", err)
	}

	// read the host's signatures and add them to the transaction.
	var hostSigs rhp.RPCContractSignatures
	if err := rpc.ReadResponse(stream, &hostSigs); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to read host signatures: %w", err)
	} else if len(hostSigs.SiacoinInputSignatures) != len(hostInputs) {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("host sent %v signatures, expected %v", len(hostSigs.SiacoinInputSignatures), len(hostInputs))
	}
	for i := range hostInputs {
		hostInputs[i].Signatures = hostSigs.SiacoinInputSignatures[i]
	}

	return rhp.Contract{
		ID:       txn.FileContractID(0),
		Revision: txn.FileContracts[0],
	}, txn, nil
}

// RenewContract renews an existing contract, modifying its funding and/or
// duration. Funds remaining in the old contract will be rolled over into the
// new contract.
func (s *Session) RenewContract(renterKey types.PrivateKey, contract types.FileContractRevision, renterFunds, validHostPayout, missedHostPayout types.Currency, endHeight uint64) (rhp.Contract, types.Transaction, error) {
	// contract renewal should use the registered settings ID.
	settingsID, settings, err := s.currentSettings()
	if err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to use settings: %w", err)
	}
	hostAddr := settings.Address
	vc := s.cm.TipContext()
	startHeight := vc.Index.Height
	if endHeight < startHeight {
		return rhp.Contract{}, types.Transaction{}, errors.New("end height must be greater than start height")
	}
	renterAddr := s.wallet.Address()

	// create the renewal transaction, consisting of the final revision of the
	// old contract and the initial revision of the new contract
	finalRevision := contract.Revision
	finalRevision.RevisionNumber = types.MaxRevisionNumber
	finalRevision.RenterSignature = renterKey.SignHash(vc.ContractSigHash(finalRevision))
	initialRevision := types.FileContract{
		Filesize:           contract.Revision.Filesize,
		FileMerkleRoot:     contract.Revision.FileMerkleRoot,
		WindowStart:        endHeight,
		WindowEnd:          endHeight + settings.WindowSize,
		ValidRenterOutput:  types.SiacoinOutput{Address: renterAddr, Value: renterFunds},
		ValidHostOutput:    types.SiacoinOutput{Address: hostAddr, Value: validHostPayout},
		MissedRenterOutput: types.SiacoinOutput{Address: renterAddr, Value: renterFunds},
		MissedHostOutput:   types.SiacoinOutput{Address: hostAddr, Value: missedHostPayout},
		RenterPublicKey:    contract.Revision.RenterPublicKey,
		HostPublicKey:      contract.Revision.HostPublicKey,
		RevisionNumber:     0,
	}
	initialRevision.RenterSignature = renterKey.SignHash(vc.ContractSigHash(initialRevision))
	renewalTxn := types.Transaction{
		FileContractResolutions: []types.FileContractResolution{{
			Parent: contract.Parent,
			Renewal: types.FileContractRenewal{
				FinalRevision:   finalRevision,
				InitialRevision: initialRevision,
			},
		}},
	}
	renewal := &renewalTxn.FileContractResolutions[0].Renewal
	renewalTxn.MinerFee = s.tpool.RecommendedFee().Mul64(vc.TransactionWeight(renewalTxn))

	totalCost := renterFunds.Add(settings.ContractFee).Add(vc.FileContractTax(initialRevision)).Add(renewalTxn.MinerFee)
	renterRollover := finalRevision.ValidRenterOutput.Value
	if renterRollover.Cmp(totalCost) < 0 {
		// there's isn't enough value left in the old contract to pay for the
		// new one; isn't enough to pay for the new contract; add more inputs
		additionalFunding := totalCost.Sub(renterRollover)
		_, cleanup, err := s.wallet.FundTransaction(&renewalTxn, additionalFunding, nil)
		if err != nil {
			return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to fund renewal transaction: %w", err)
		}
		defer cleanup()
	} else {
		// rollover the exact amount necessary to cover the cost of the new
		// contract (the rest will be returned as timelocked outputs)
		renterRollover = totalCost
	}
	renewal.RenterRollover = renterRollover
	renterInputs := renewalTxn.SiacoinInputs

	// initiate the RPC
	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(2 * time.Minute))

	req := &rhp.RPCRenewContractRequest{
		Inputs:     renewalTxn.SiacoinInputs,
		Outputs:    renewalTxn.SiacoinOutputs,
		MinerFee:   renewalTxn.MinerFee,
		Resolution: renewalTxn.FileContractResolutions[0],
	}
	if err := rpc.WriteRequest(stream, rhp.RPCRenewContractID, &settingsID); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to write RPC id: %w", err)
	} else if err := rpc.WriteObject(stream, req); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to write request: %w", err)
	}

	var resp rhp.RPCRenewContractHostAdditions
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to read host additions: %w", err)
	} else if resp.HostRollover.Cmp(renewal.FinalRevision.ValidHostOutput.Value) > 0 {
		return rhp.Contract{}, types.Transaction{}, errors.New("host rollover is greater than valid host output")
	}

	// validate the host's contract signatures
	if !s.hostKey.VerifyHash(vc.ContractSigHash(finalRevision), resp.FinalizationSignature) {
		return rhp.Contract{}, types.Transaction{}, errors.New("host final revision signature is invalid")
	} else if !s.hostKey.VerifyHash(vc.ContractSigHash(initialRevision), resp.InitialSignature) {
		return rhp.Contract{}, types.Transaction{}, errors.New("host initial contract signature is invalid")
	}

	renewal.HostRollover = resp.HostRollover
	renewal.FinalRevision.HostSignature = resp.FinalizationSignature
	renewal.InitialRevision.HostSignature = resp.InitialSignature
	// verify the host's renewal signature
	renewalSigHash := vc.RenewalSigHash(*renewal)
	if !s.hostKey.VerifyHash(renewalSigHash, resp.RenewalSignature) {
		return rhp.Contract{}, types.Transaction{}, errors.New("host renewal signature is invalid")
	}

	// now that all other signatures are complete the renewal can be signed.
	renewal.HostSignature = resp.RenewalSignature
	renewal.RenterSignature = renterKey.SignHash(renewalSigHash)
	// add the host's inputs to the transaction.
	renewalTxn.SiacoinInputs = append(renewalTxn.SiacoinInputs, resp.Inputs...)
	renewalTxn.SiacoinOutputs = append(renewalTxn.SiacoinOutputs, resp.Outputs...)
	hostInputs := renewalTxn.SiacoinInputs[len(renterInputs):]

	// sign any inputs we added and send the signatures to the host.
	renterSigs := rhp.RPCRenewContractRenterSignatures{
		SiacoinInputSignatures: make([][]types.Signature, len(renterInputs)),
		RenewalSignature:       renewal.RenterSignature,
	}
	if len(renterInputs) > 0 {
		toSign := make([]types.ElementID, len(renterInputs))
		for i, sci := range renterInputs {
			toSign[i] = sci.Parent.ID
		}
		if err := s.wallet.SignTransaction(vc, &renewalTxn, toSign); err != nil {
			return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to sign renewal transaction: %w", err)
		}
		for i := range renterInputs {
			renterSigs.SiacoinInputSignatures[i] = renewalTxn.SiacoinInputs[i].Signatures
		}
	}
	if err := rpc.WriteResponse(stream, &renterSigs); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to write renter signatures: %w", err)
	}

	// add the host signatures to the transaction
	var hostSigs rhp.RPCContractSignatures
	if err := rpc.ReadResponse(stream, &hostSigs); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to read host signatures: %w", err)
	} else if len(hostSigs.SiacoinInputSignatures) != len(hostInputs) {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("host sent %v signatures, expected %v", len(hostSigs.SiacoinInputSignatures), len(hostInputs))
	}
	for i := range hostInputs {
		hostInputs[i].Signatures = hostSigs.SiacoinInputSignatures[i]
	}

	// the transaction should now be valid
	if err := vc.ValidateTransaction(renewalTxn); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("renewal transaction is invalid: %w", err)
	}

	return rhp.Contract{
		ID:       renewalTxn.FileContractID(0),
		Revision: renewalTxn.FileContractResolutions[0].Renewal.InitialRevision,
	}, renewalTxn, nil
}
