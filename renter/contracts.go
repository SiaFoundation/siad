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

	outputAddr := s.wallet.Address()
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
		ValidRenterOutput: types.SiacoinOutput{
			Value:   renterFunds,
			Address: outputAddr,
		},
		MissedRenterOutput: types.SiacoinOutput{
			Value:   renterFunds,
			Address: outputAddr,
		},
		ValidHostOutput: types.SiacoinOutput{
			Value:   hostPayout,
			Address: settings.Address,
		},
		MissedHostOutput: types.SiacoinOutput{
			Value:   hostPayout,
			Address: settings.Address,
		},
		WindowStart:     endHeight,
		WindowEnd:       endHeight + settings.WindowSize,
		RenterPublicKey: renterPub,
		HostPublicKey:   s.hostKey,
	}

	txn := types.Transaction{
		FileContracts: []types.FileContract{fc},
	}
	// TODO: better fee calculation.
	txn.MinerFee = settings.TxnFeeMaxRecommended.Mul64(vc.TransactionWeight(txn))
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
	stream.SetDeadline(time.Now().Add(time.Minute * 2))

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

	vc, err := s.cm.TipContext()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to get validation context: %w", err)
	}
	startHeight := vc.Index.Height
	if endHeight < startHeight {
		return rhp.Contract{}, nil, errors.New("end height must be greater than start height")
	}

	outputAddr := s.wallet.Address()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to generate address: %w", err)
	}

	renewal := types.FileContract{
		Filesize:        contract.Revision.Filesize,
		FileMerkleRoot:  contract.Revision.FileMerkleRoot,
		WindowStart:     startHeight,
		WindowEnd:       endHeight + settings.WindowSize,
		RenterPublicKey: contract.Revision.RenterPublicKey,
		HostPublicKey:   contract.Revision.HostPublicKey,
		ValidHostOutput: types.SiacoinOutput{
			Address: settings.Address,
		},
		MissedHostOutput: types.SiacoinOutput{
			Address: settings.Address,
			Value:   settings.ContractFee.Add(additionalCollateral),
		},
		ValidRenterOutput: types.SiacoinOutput{
			Address: outputAddr,
			Value:   additionalRenterFunds,
		},
		MissedRenterOutput: types.SiacoinOutput{
			Address: outputAddr,
			Value:   additionalRenterFunds,
		},
	}

	// calculate the "base" storage cost to the renter and risked collateral for
	// the host for the data already in the contract. If the contract height did
	// not increase, base costs are zero.
	var baseStorageCost, baseCollateral types.Currency
	if renewal.WindowEnd > contract.Revision.WindowEnd {
		extension := renewal.WindowEnd - contract.Revision.WindowEnd
		baseStorageCost = settings.StoragePrice.Mul64(renewal.Filesize).Mul64(extension)
		baseCollateral = settings.Collateral.Mul64(renewal.Filesize).Mul64(extension)
	}

	// calculate the total collateral the host is expected to add to the
	// contract.
	totalCollateral := baseCollateral.Add(additionalCollateral)
	if totalCollateral.Cmp(settings.MaxCollateral) > 0 {
		return rhp.Contract{}, nil, errors.New("collateral too large")
	}

	// create the renewal transaction.
	// TODO: better fee calculation
	renewalTxn := types.Transaction{
		FileContracts: []types.FileContract{renewal},
	}
	renewalTxn.MinerFee = settings.TxnFeeMaxRecommended.Mul64(vc.TransactionWeight(renewalTxn))
	// The renter is responsible for the renter funds, contract fee, base
	// storage cost, siafund tax, and miner fee.
	renterFundAmount := additionalRenterFunds.Add(settings.ContractFee).Add(baseStorageCost).Add(vc.FileContractTax(renewal)).Add(renewalTxn.MinerFee)
	// add the contract fee, base storage revenue, and total collateral to the
	// host's valid output. In the event of failure the base collateral and
	// base storage cost will be burned.
	renewal.ValidHostOutput.Value = settings.ContractFee.Add(baseStorageCost).Add(totalCollateral)

	// clear the existing contract.
	clearedContract := rhp.Contract{
		ID:       contract.Parent.ID,
		Revision: contract.Revision,
	}
	clearedContract.Revision = clearedContract.ClearingRevision()
	clearedRevSigHash := vc.ContractSigHash(clearedContract.Revision)
	clearedContract.RenterSignature = renterKey.SignHash(clearedRevSigHash)

	// create the transaction with the clearing revision and renter funding.
	clearingTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{{
			Parent:   contract.Parent,
			Revision: clearedContract.Revision,
		}},
	}

	// calculate the additional funding required to pay for the contract renewal
	// transaction subtracting the renter's early-termination payout.
	var additionalFunding types.Currency
	if renterFundAmount.Cmp(clearedContract.Revision.ValidRenterOutput.Value) > 0 {
		additionalFunding = additionalFunding.Add(renterFundAmount).Sub(clearedContract.Revision.ValidRenterOutput.Value)
		clearingTxn.SiacoinOutputs = append(clearingTxn.SiacoinOutputs, types.SiacoinOutput{
			Address: outputAddr,
			Value:   additionalFunding,
		})
	}

	clearingTxn.MinerFee = settings.TxnFeeMaxRecommended.Mul64(vc.TransactionWeight(clearingTxn))

	// fund the clearing transaction with the total amount needed for both the
	// clearing and renewal transactions. The renewal transaction will use the
	// remaining funds as an ephemeral output.
	clearingToSign, clearingCleanup, err := s.wallet.FundTransaction(&clearingTxn, additionalFunding.Add(clearingTxn.MinerFee), nil)
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to fund clearing transaction: %w", err)
	}
	defer clearingCleanup()

	// create an ephemeral input for the renter payout created by clearing the
	// existing contract.
	policy, ok := s.wallet.SpendPolicy(clearedContract.Revision.ValidRenterOutput.Address)
	if !ok {
		return rhp.Contract{}, nil, fmt.Errorf("failed to get spend policy for renter output %v", clearedContract.Revision.ValidRenterOutput.Address)
	}
	renewalTxn.SiacoinInputs = []types.SiacoinInput{{
		Parent:      clearingTxn.EphemeralSiacoinElement(0),
		SpendPolicy: policy,
	}}

	// if necessary add additional funds to the renewal transaction.
	toSign := []types.ElementID{renewalTxn.SiacoinInputs[0].Parent.ID}
	if !additionalFunding.IsZero() {
		renewalTxn.SiacoinInputs = append(renewalTxn.SiacoinInputs, types.SiacoinInput{
			Parent: types.SiacoinElement{
				StateElement: types.StateElement{
					ID:        clearingTxn.SiacoinOutputID(0),
					LeafIndex: types.EphemeralLeafIndex,
				},
				SiacoinOutput: clearingTxn.SiacoinOutputs[0],
			},
		})
		toSign = append(toSign, renewalTxn.SiacoinInputs[1].Parent.ID)
	}

	req := &rhp.RPCContractRequest{
		Transactions: []types.Transaction{
			clearingTxn,
			renewalTxn,
		},
	}

	stream, err := s.session.DialStream()
	if err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(time.Minute * 2))

	if err := rpc.WriteRequest(stream, rhp.RPCRenewContractID, &settingsID); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write RPC id: %w", err)
	} else if err := rpc.WriteObject(stream, req); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to write request: %w", err)
	}

	var resp rhp.RPCContractAdditions
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to read host additions: %w", err)
	}

	// add the host's inputs and outputs to the renewal transaction.
	renewalTxn.SiacoinInputs = append(renewalTxn.SiacoinInputs, resp.Inputs...)
	renewalTxn.SiacoinOutputs = append(renewalTxn.SiacoinOutputs, resp.Outputs...)

	if err := s.wallet.SignTransaction(vc, &clearingTxn, clearingToSign); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to sign clearing transaction: %w", err)
	} else if err := s.wallet.SignTransaction(vc, &renewalTxn, toSign); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to sign renewal transaction: %w", err)
	}

	// sign the renewal and send the signatures to the host
	renewalHash := vc.ContractSigHash(renewal)
	renterSigs := rhp.RPCRenewContractSignatures{
		ClearingSignature: clearedContract.RenterSignature,
		RenewalSignature:  renterKey.SignHash(renewalHash),
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

	for i := range hostSigs.SiacoinInputSignatures {
		renewalTxn.SiacoinInputs[i].Signatures = append(renewalTxn.SiacoinInputs[i].Signatures, hostSigs.SiacoinInputSignatures[i]...)
	}

	clearedContract.HostSignature = hostSigs.ClearingSignature
	renewedContract := rhp.Contract{
		ID:              renewalTxn.FileContractID(0),
		Revision:        renewal,
		HostSignature:   hostSigs.RenewalSignature,
		RenterSignature: renterSigs.RenewalSignature,
	}

	// verify the clearing and renewal signatures
	if err := clearedContract.ValidateSignatures(vc); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to validate cleared contract signatures: %w", err)
	} else if err := renewedContract.ValidateSignatures(vc); err != nil {
		return rhp.Contract{}, nil, fmt.Errorf("failed to validate renewed contract signatures: %w", err)
	}

	return renewedContract, append(resp.Parents, clearingTxn, renewalTxn), nil
}
