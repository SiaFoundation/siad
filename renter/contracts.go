package renter

import (
	"errors"
	"fmt"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

// FormContract negotiates a new contract with the host using the specified
// funds and duration.
func (s *Session) FormContract(renterKey types.PrivateKey, hostFunds, renterFunds types.Currency, endHeight uint64) (rhp.Contract, []types.Transaction, error) {
	vc, err := s.cm.TipContext()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to get validation context: %w", err)
	}
	startHeight := vc.Index.Height
	if endHeight < startHeight {
		return rhp.Contract{}, nil, errors.New("end height must be greater than start height")
	}

	renterAddr := s.wallet.Address()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to generate address: %w", err)
	}

	renterPub := renterKey.PublicKey()

	// retrieve the host's current settings. The host is not expecting
	// payment for forming contracts.
	settings, err := s.ScanSettings()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to get settings: %w", err)
	}
	hostAddr := settings.Address

	// if the host's collateral is more than their max collateral, the contract
	// will be rejected.
	if settings.MaxCollateral.Cmp(hostFunds) < 0 {
		return rhp.Contract{}, nil, errors.New("host payout cannot be greater than max collateral")
	}

	// subtract the contract formation fee from the renter's funds and add it to
	// the host's funds.
	hostPayout := hostFunds.Add(settings.ContractFee)

	// build the contract.
	fc := types.FileContract{
		Filesize:           0,
		WindowStart:        endHeight,
		WindowEnd:          endHeight + settings.WindowSize,
		ValidRenterOutput:  types.SiacoinOutput{Value: renterFunds, Address: renterAddr},
		ValidHostOutput:    types.SiacoinOutput{Value: hostPayout, Address: hostAddr},
		MissedRenterOutput: types.SiacoinOutput{Value: renterFunds, Address: renterAddr},
		MissedHostOutput:   types.SiacoinOutput{Value: hostPayout, Address: hostAddr},
		RenterPublicKey:    renterPub,
		HostPublicKey:      s.hostKey,
	}

	txn := types.Transaction{
		FileContracts: []types.FileContract{fc},
	}
	txn.MinerFee = s.tpool.RecommendedFee().Mul64(vc.TransactionWeight(txn))
	// fund the formation transaction with the renter funds + contract fee +
	// siafund tax + miner fee.
	renterFundAmount := renterFunds.Add(settings.ContractFee).Add(vc.FileContractTax(fc)).Add(txn.MinerFee)

	toSign, cleanup, err := s.wallet.FundTransaction(&txn, renterFundAmount, nil)
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to fund transaction: %w", err)
	}
	defer cleanup()

	req := &rhp.RPCContractRequest{
		Transactions: []types.Transaction{txn},
	}

	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(2 * time.Minute))

	if err := rpc.WriteRequest(stream, rhp.RPCFormContractID, req); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write form contract request: %w", err)
	}

	var resp rhp.RPCContractAdditions
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host additions: %w", err)
	}

	txn.SiacoinInputs = append(txn.SiacoinInputs, resp.Inputs...)
	txn.SiacoinOutputs = append(txn.SiacoinOutputs, resp.Outputs...)

	if err := s.wallet.SignTransaction(vc, &txn, toSign); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	sigHash := vc.ContractSigHash(fc)
	renterSigs := rhp.RPCContractSignatures{
		SiacoinInputSignatures: make([][]types.InputSignature, len(txn.SiacoinInputs)),
		RevisionSignature:      renterKey.SignHash(sigHash),
	}
	for i := range txn.SiacoinInputs {
		renterSigs.SiacoinInputSignatures[i] = append(renterSigs.SiacoinInputSignatures[i], txn.SiacoinInputs[i].Signatures...)
	}

	if err := rpc.WriteResponse(stream, &renterSigs); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write renter signatures: %w", err)
	}

	var hostSigs rhp.RPCContractSignatures
	if err := rpc.ReadResponse(stream, &hostSigs); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host signatures: %w", err)
	}

	// verify the host's signature
	if !fc.HostPublicKey.VerifyHash(sigHash, hostSigs.RevisionSignature) {
		return rhp.Contract{}, nil, errors.New("host revision signature is invalid")
	}

	for i := range hostSigs.SiacoinInputSignatures {
		txn.SiacoinInputs[i].Signatures = append(txn.SiacoinInputs[i].Signatures, hostSigs.SiacoinInputSignatures[i]...)
	}
	return rhp.Contract{
		ID:              txn.FileContractID(0),
		Revision:        fc,
		HostSignature:   hostSigs.RevisionSignature,
		RenterSignature: renterSigs.RevisionSignature,
	}, append(resp.Parents, txn), nil
}

// RenewContract clears and renews an existing contract with the host adding
// additional funds and duration.
func (s *Session) RenewContract(renterKey types.PrivateKey, contract types.FileContractRevision, additionalCollateral, additionalRenterFunds types.Currency, endHeight uint64) (rhp.Contract, []types.Transaction, error) {
	settingsID, settings, err := s.currentSettings()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to use settings: %w", err)
	}
	hostAddr := settings.Address
	vc, err := s.cm.TipContext()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to get validation context: %w", err)
	}
	startHeight := vc.Index.Height
	if endHeight < startHeight {
		return rhp.Contract{}, nil, errors.New("end height must be greater than start height")
	}
	renterAddr := s.wallet.Address()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to generate address: %w", err)
	}
	current := contract.Revision

	// calculate the "base" storage cost to the renter and risked collateral for
	// the host for the data already in the contract. If the contract height did
	// not increase, base costs are zero.
	var baseStorageCost, baseCollateral types.Currency
	if contractEnd := endHeight + settings.WindowSize; contractEnd > contract.Revision.WindowEnd {
		extension := contractEnd - contract.Revision.WindowEnd
		baseStorageCost = settings.StoragePrice.Mul64(current.Filesize).Mul64(extension)
		baseCollateral = settings.Collateral.Mul64(current.Filesize).Mul64(extension)
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

	// create the clear and renew transaction. No signatures should be present
	// yet.
	renewalTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{{
			Parent:   contract.Parent,
			Revision: rhp.ClearingRevision(contract.Revision),
		}},
		FileContracts: []types.FileContract{renewal},
	}
	// TODO: better fee calculation
	renewalTxn.MinerFee = s.tpool.RecommendedFee().Mul64(vc.TransactionWeight(renewalTxn))
	// Fund the renew and clear transaction. The renter is responsible for the
	// renter funds, contract fee, base storage cost, siafund tax, and miner
	// fee.
	renterFundAmount := additionalRenterFunds.Add(settings.ContractFee).Add(baseStorageCost).Add(vc.FileContractTax(renewal)).Add(renewalTxn.MinerFee)
	toSign, cleanup, err := s.wallet.FundTransaction(&renewalTxn, renterFundAmount, nil)
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to fund clearing transaction: %w", err)
	}
	defer cleanup()

	req := &rhp.RPCContractRequest{
		Transactions: []types.Transaction{renewalTxn},
	}

	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(2 * time.Minute))

	if err := rpc.WriteRequest(stream, rhp.RPCRenewContractID, &settingsID); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write RPC id: %w", err)
	} else if err := rpc.WriteObject(stream, req); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write request: %w", err)
	}

	var resp rhp.RPCContractAdditions
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host additions: %w", err)
	}

	// add the host's inputs and outputs to the renewal transaction and sign the
	// transaction.
	renewalTxn.SiacoinInputs = append(renewalTxn.SiacoinInputs, resp.Inputs...)
	renewalTxn.SiacoinOutputs = append(renewalTxn.SiacoinOutputs, resp.Outputs...)
	if err := s.wallet.SignTransaction(vc, &renewalTxn, toSign); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to sign renewal transaction: %w", err)
	}

	// sign the new renewal and clearing revision.
	clearingHash := vc.ContractSigHash(renewalTxn.FileContractRevisions[0].Revision)
	renewalHash := vc.ContractSigHash(renewalTxn.FileContracts[0])
	renterSigs := rhp.RPCRenewContractSignatures{
		ClearingSignature:      renterKey.SignHash(clearingHash),
		RenewalSignature:       renterKey.SignHash(renewalHash),
		SiacoinInputSignatures: make([][]types.InputSignature, len(renewalTxn.SiacoinInputs)),
	}
	for i := range renewalTxn.SiacoinInputs {
		renterSigs.SiacoinInputSignatures[i] = append(renterSigs.SiacoinInputSignatures[i], renewalTxn.SiacoinInputs[i].Signatures...)
	}

	if err := rpc.WriteResponse(stream, &renterSigs); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write renter signatures: %w", err)
	}

	var hostSigs rhp.RPCRenewContractSignatures
	if err := rpc.ReadResponse(stream, &hostSigs); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host signatures: %w", err)
	}

	// verify the clearing and renewal signatures
	if !renewal.HostPublicKey.VerifyHash(clearingHash, hostSigs.ClearingSignature) {
		return rhp.Contract{}, nil, errors.New("failed to validate host clearing signature")
	} else if !renewal.HostPublicKey.VerifyHash(renewalHash, hostSigs.RenewalSignature) {
		return rhp.Contract{}, nil, errors.New("failed to validate host renewal signature")
	}

	// add the clearing revision signatures to the renewal transaction.
	renewalTxn.FileContractRevisions[0].HostSignature = hostSigs.ClearingSignature
	renewalTxn.FileContractRevisions[0].RenterSignature = renterSigs.ClearingSignature
	for i := range hostSigs.SiacoinInputSignatures {
		renewalTxn.SiacoinInputs[i].Signatures = append(renewalTxn.SiacoinInputs[i].Signatures, hostSigs.SiacoinInputSignatures[i]...)
	}

	return rhp.Contract{
		ID:              renewalTxn.FileContractID(0),
		Revision:        renewal,
		HostSignature:   hostSigs.RenewalSignature,
		RenterSignature: renterSigs.RenewalSignature,
	}, append(resp.Parents, renewalTxn), nil
}
