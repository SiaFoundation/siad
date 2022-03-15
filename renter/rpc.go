package renter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

var (
	// ErrPaymentRequired is returned when a payment method is required but not
	// provided.
	ErrPaymentRequired = errors.New("payment method is required")

	// ErrInsufficientFunds is returned by various RPCs when the renter is
	// unable to provide sufficient payment to the host.
	ErrInsufficientFunds = errors.New("insufficient funds")

	// ErrInvalidMerkleProof is returned by various RPCs when the host supplies
	// an invalid Merkle proof.
	ErrInvalidMerkleProof = errors.New("host supplied invalid Merkle proof")

	// ErrContractLocked is returned when the contract in question is already
	// locked by another party. This is a transient error; the caller should
	// retry later.
	ErrContractLocked = errors.New("contract is locked by another party")

	// ErrNoContractLocked is returned by RPCs that require a locked contract
	// when no contract is locked.
	ErrNoContractLocked = errors.New("no contract locked")

	// ErrContractFinalized is returned when the contract in question has
	// reached its maximum revision number, meaning the contract can no longer
	// be revised.
	ErrContractFinalized = errors.New("contract cannot be revised further")
)

func (s *Session) call(ctx context.Context, rpcID rpc.Specifier, req, resp rpc.Object) error {
	stream, err := s.session.DialStreamContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to open new stream: %w", err)
	}
	defer stream.Close()

	if err := rpc.WriteRequest(stream, rpcID, req); err != nil {
		return err
	}
	return rpc.ReadResponse(stream, resp)
}

// AccountBalance returns the current balance of an ephemeral account.
func (s *Session) AccountBalance(ctx context.Context, accountID types.PublicKey, payment PaymentMethod) (types.Currency, error) {
	if payment == nil {
		return types.ZeroCurrency, ErrPaymentRequired
	}

	stream, err := s.session.DialStreamContext(ctx)
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to open new stream: %w", err)
	}
	defer stream.Close()

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
func (s *Session) FundAccount(ctx context.Context, accountID types.PublicKey, amount types.Currency, payment PaymentMethod) (types.Currency, error) {
	if payment == nil {
		return types.ZeroCurrency, ErrPaymentRequired
	} else if _, ok := payment.(*payByContract); !ok {
		return types.ZeroCurrency, errors.New("ephemeral accounts must be funded by a contract")
	}

	stream, err := s.session.DialStreamContext(ctx)
	if err != nil {
		return types.ZeroCurrency, fmt.Errorf("failed to open new stream: %w", err)
	}
	defer stream.Close()

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
func (s *Session) LatestRevision(ctx context.Context, contractID types.ElementID, payment PaymentMethod) (rhp.Contract, error) {
	if payment == nil {
		return rhp.Contract{}, ErrPaymentRequired
	}

	stream, err := s.session.DialStreamContext(ctx)
	if err != nil {
		return rhp.Contract{}, fmt.Errorf("failed to open new stream: %w", err)
	}
	defer stream.Close()

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
func (s *Session) RegisterSettings(ctx context.Context, payment PaymentMethod) (settings rhp.HostSettings, _ error) {
	if payment == nil {
		return rhp.HostSettings{}, ErrPaymentRequired
	}

	stream, err := s.session.DialStreamContext(ctx)
	if err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

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
func (s *Session) ScanSettings(ctx context.Context) (rhp.HostSettings, error) {
	var resp rhp.RPCSettingsResponse
	if err := s.call(ctx, rhp.RPCSettingsID, nil, &resp); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to read settings response: %w", err)
	}
	var settings rhp.HostSettings
	if err := json.Unmarshal(resp.Settings, &settings); err != nil {
		return rhp.HostSettings{}, fmt.Errorf("failed to decode settings: %w", err)
	}
	s.settings = settings
	return settings, nil
}

// Lock calls the Lock RPC, locking the supplied contract and synchronizing its
// state with the host's most recent revision. Where applicable, future RPC
// calls will modify the the locked contract, ending when either the Unlock RPC
// is called or the Session is closed.
//
// Lock returns ErrContractFinalized if the contract can no longer be revised.
// The contract will still be available via the LockedContract method, but
// invoking other RPCs may result in errors or panics.
func (s *Session) Lock(ctx context.Context, id types.ElementID, key types.PrivateKey) (err error) {
	req := &rhp.RPCLockRequest{
		ContractID: id,
		Signature:  s.session.SignChallenge(key),
	}
	if deadline, ok := ctx.Deadline(); ok {
		req.Timeout = uint64(time.Until(deadline).Milliseconds())
	}
	var resp rhp.RPCLockResponse
	if err := s.call(ctx, rhp.RPCLockID, req, &resp); err != nil {
		return err
	}
	s.session.SetChallenge(resp.NewChallenge)

	// verify claimed revision
	if err := rhp.ValidateContractSignatures(s.cm.TipContext(), resp.Revision); err != nil {
		return fmt.Errorf("host returned invalid revision: %w", err)
	}
	if !resp.Acquired {
		return ErrContractLocked
	}
	s.contract = rhp.Contract{
		ID:       id,
		Revision: resp.Revision,
	}
	s.renterKey = key
	if !s.canRevise() {
		return ErrContractFinalized
	}
	return nil
}

// Read calls the Read RPC, writing the requested sections of sector data to w.
// Merkle proofs are always requested.
//
// Note that sector data is streamed to w before it has been validated. Callers
// MUST check the returned error, and discard any data written to w if the error
// is non-nil. Failure to do so may allow an attacker to inject malicious data.
func (s *Session) Read(ctx context.Context, w io.Writer, sections []rhp.RPCReadRequestSection) (err error) {
	if !s.contractLocked() {
		return ErrNoContractLocked
	} else if !s.canRevise() {
		return ErrContractFinalized
	} else if len(sections) == 0 {
		return nil
	}

	price := rhp.RPCReadRenterCost(s.settings, sections)
	if !s.sufficientFunds(price) {
		return ErrInsufficientFunds
	}

	// revise and sign contract
	revision := s.contract.Revision
	revision.RevisionNumber++
	revision.RenterOutput.Value = revision.RenterOutput.Value.Sub(price)
	revision.HostOutput.Value = revision.HostOutput.Value.Add(price)
	revision.MissedHostValue = revision.MissedHostValue.Add(price)
	contractHash := s.cm.TipContext().ContractSigHash(s.contract.Revision)
	revision.RenterSignature = s.renterKey.SignHash(contractHash)

	// initiate RPC
	stream, err := s.session.DialStreamContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	req := &rhp.RPCReadRequest{
		Sections:          sections,
		MerkleProof:       true,
		NewRevisionNumber: revision.RevisionNumber,
		NewOutputs: rhp.ContractOutputs{
			RenterValue:     revision.RenterOutput.Value,
			HostValue:       revision.HostOutput.Value,
			MissedHostValue: revision.MissedHostValue,
		},
		Signature: revision.RenterSignature,
	}
	if err := rpc.WriteRequest(stream, rhp.RPCReadID, req); err != nil {
		return err
	}
	// receive + verify host signature
	var resp rhp.RPCReadResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return fmt.Errorf("couldn't read signature: %w", err)
	} else if s.hostKey.VerifyHash(contractHash, resp.Signature) {
		return errors.New("host's signature is invalid")
	}
	// update contract
	revision.HostSignature = resp.Signature
	s.contract.Revision = revision

	// receive sector data
	for _, sec := range req.Sections {
		// stream into both w and the proof verifier
		proofStart := int(sec.Offset) / rhp.LeafSize
		proofEnd := int(sec.Offset+sec.Length) / rhp.LeafSize
		rpv := rhp.NewRangeProofVerifier(proofStart, proofEnd)
		tee := io.TeeReader(io.LimitReader(stream, int64(sec.Length)), w)
		// the proof verifier Reads one leaf at a time, so bufio is crucial for
		// performance here
		if _, err := rpv.ReadFrom(bufio.NewReaderSize(tee, 1<<16)); err != nil {
			return fmt.Errorf("couldn't stream sector data: %w", err)
		}
		// read + verify the Merkle proof
		proofSize := rhp.RangeProofSize(rhp.LeavesPerSector, proofStart, proofEnd)
		proof := make([]types.Hash256, proofSize)
		for i := range proof {
			if _, err := io.ReadFull(stream, proof[i][:]); err != nil {
				return fmt.Errorf("couldn't read Merkle proof: %w", err)
			}
		}
		if !rpv.Verify(proof, sec.MerkleRoot) {
			return errors.New("invalid Merkle proof")
		}
	}

	return nil
}

// Append calls the Write RPC with a single action, appending the provided
// sector. It returns the Merkle root of the sector.
func (s *Session) Append(ctx context.Context, sector *[rhp.SectorSize]byte) (_ types.Hash256, err error) {
	if !s.contractLocked() {
		return types.Hash256{}, ErrNoContractLocked
	} else if !s.canRevise() {
		return types.Hash256{}, ErrContractFinalized
	}

	actions := []rhp.RPCWriteAction{{
		Type: rhp.RPCWriteActionAppend,
		Data: sector[:],
	}}
	price := rhp.RPCWriteRenterCost(s.settings, s.contract.Revision, actions)
	if !s.sufficientFunds(price) {
		return types.Hash256{}, ErrInsufficientFunds
	}
	collateral := rhp.RPCWriteHostCollateral(s.settings, s.contract.Revision, actions)
	if remainingCollateral := s.contract.Revision.MissedHostValue; collateral.Cmp(remainingCollateral) > 0 {
		collateral = remainingCollateral
	}

	// revise contract
	revision := s.contract.Revision
	revision.RevisionNumber++
	revision.Filesize += rhp.SectorSize
	revision.RenterOutput.Value = revision.RenterOutput.Value.Sub(price)
	revision.HostOutput.Value = revision.HostOutput.Value.Add(price)
	revision.MissedHostValue = revision.MissedHostValue.Add(price)
	revision.MissedHostValue = revision.MissedHostValue.Sub(collateral)

	// compute appended roots in parallel with I/O
	rootChan := make(chan types.Hash256)
	go func() { rootChan <- rhp.SectorRoot(sector) }()

	// initiate RPC
	stream, err := s.session.DialStreamContext(ctx)
	if err != nil {
		<-rootChan
		return types.Hash256{}, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	// send request
	req := &rhp.RPCWriteRequest{
		Actions:     actions,
		MerkleProof: true,

		NewRevisionNumber: s.contract.Revision.RevisionNumber + 1,
		NewOutputs: rhp.ContractOutputs{
			RenterValue:     revision.RenterOutput.Value,
			HostValue:       revision.HostOutput.Value,
			MissedHostValue: revision.MissedHostValue,
		},
	}
	if err := rpc.WriteRequest(stream, rhp.RPCWriteID, req); err != nil {
		<-rootChan
		return types.Hash256{}, err
	}

	// read and verify Merkle proof
	var merkleResp rhp.RPCWriteMerkleProof
	if err := rpc.ReadResponse(stream, &merkleResp); err != nil {
		<-rootChan
		return types.Hash256{}, fmt.Errorf("couldn't read Merkle proof response: %w", err)
	}
	treeHashes := merkleResp.OldSubtreeHashes
	oldRoot, newRoot := revision.FileMerkleRoot, merkleResp.NewMerkleRoot
	sectorRoot := <-rootChan
	if !rhp.VerifyAppendProof(revision.Filesize/rhp.SectorSize, treeHashes, sectorRoot, oldRoot, newRoot) {
		err := ErrInvalidMerkleProof
		rpc.WriteResponseErr(stream, err)
		return types.Hash256{}, err
	}

	// set new Merkle root and exchange signatures
	revision.FileMerkleRoot = newRoot
	contractHash := s.cm.TipContext().ContractSigHash(s.contract.Revision)
	revision.RenterSignature = s.renterKey.SignHash(contractHash)
	renterSig := &rhp.RPCWriteResponse{
		Signature: s.renterKey.SignHash(contractHash),
	}
	if err := rpc.WriteResponse(stream, renterSig); err != nil {
		return types.Hash256{}, fmt.Errorf("couldn't write signature response: %w", err)
	}
	var hostSig rhp.RPCWriteResponse
	if err := rpc.ReadResponse(stream, &hostSig); err != nil {
		return types.Hash256{}, fmt.Errorf("couldn't read signature response: %w", err)
	}
	// verify the host signature
	if !s.hostKey.VerifyHash(contractHash, hostSig.Signature) {
		return types.Hash256{}, errors.New("host's signature is invalid")
	}
	s.contract.Revision = revision
	return sectorRoot, nil
}

// FormContract negotiates a new contract with the host using the specified
// funds and duration.
func (s *Session) FormContract(renterKey types.PrivateKey, renterFunds, hostCollateral types.Currency, endHeight uint64, settings rhp.HostSettings, w Wallet, tp TransactionPool) (rhp.Contract, types.Transaction, error) {
	renterAddr := w.Address()
	hostAddr := settings.Address

	vc := s.cm.TipContext()
	startHeight := vc.Index.Height
	if endHeight < startHeight {
		return rhp.Contract{}, types.Transaction{}, errors.New("end height must be greater than start height")
	}

	hostPayout := settings.ContractFee.Add(hostCollateral)
	fc := types.FileContract{
		Filesize:        0,
		WindowStart:     endHeight,
		WindowEnd:       endHeight + settings.WindowSize,
		RenterOutput:    types.SiacoinOutput{Value: renterFunds, Address: renterAddr},
		HostOutput:      types.SiacoinOutput{Value: hostPayout, Address: hostAddr},
		MissedHostValue: hostPayout,
		TotalCollateral: hostCollateral,
		RenterPublicKey: renterKey.PublicKey(),
		HostPublicKey:   s.hostKey,
	}

	if err := rhp.ValidateContractFormation(fc, startHeight, settings); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("contract formation invalid: %w", err)
	}

	sigHash := vc.ContractSigHash(fc)
	fc.RenterSignature = renterKey.SignHash(sigHash)

	txn := types.Transaction{
		FileContracts: []types.FileContract{fc},
	}
	txn.MinerFee = tp.RecommendedFee().Mul64(vc.TransactionWeight(txn))
	// fund the formation transaction with the renter allowance, contract fee,
	// siafund tax, and miner fee
	totalCost := renterFunds.Add(settings.ContractFee).Add(vc.FileContractTax(fc)).Add(txn.MinerFee)
	toSign, cleanup, err := w.FundTransaction(&txn, totalCost, nil)
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

	// add the host's additions to the transaction
	renterInputs := txn.SiacoinInputs
	txn.SiacoinInputs = append(txn.SiacoinInputs, resp.Inputs...)
	hostInputs := txn.SiacoinInputs[len(renterInputs):]
	txn.SiacoinOutputs = append(txn.SiacoinOutputs, resp.Outputs...)
	txn.FileContracts[0].HostSignature = resp.ContractSignature

	// sign the transaction and send the signatures to the host
	if err := w.SignTransaction(vc, &txn, toSign); err != nil {
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

	// read the host's signatures and add them to the transaction
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
	if err := vc.ValidateTransaction(txn); err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("renewal transaction is invalid: %w", err)
	}

	return rhp.Contract{
		ID:       txn.FileContractID(0),
		Revision: txn.FileContracts[0],
	}, txn, nil
}

// RenewContract renews an existing contract, adding additional funds,
// collateral and/or duration. Funds remaining in the old contract will be
// rolled over into the new contract.
func (s *Session) RenewContract(renterKey types.PrivateKey, contract types.FileContractRevision, renterFunds, hostCollateral types.Currency, extension uint64, w Wallet, tp TransactionPool) (rhp.Contract, types.Transaction, error) {
	// contract renewal should use the registered settings ID
	settingsID, settings, err := s.currentSettings()
	if err != nil {
		return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to use settings: %w", err)
	}
	hostAddr := settings.Address
	renterAddr := w.Address()
	vc := s.cm.TipContext()

	var baseStorageCost, baseCollateral types.Currency
	if extension > 0 {
		baseStorageCost = settings.StoragePrice.Mul64(contract.Revision.Filesize).Mul64(extension)
		baseCollateral = settings.Collateral.Mul64(contract.Revision.Filesize).Mul64(extension)
	}

	totalCollateral := hostCollateral.Add(baseCollateral)
	validHostValue := settings.ContractFee.Add(baseStorageCost).Add(totalCollateral)
	missedHostValue := settings.ContractFee.Add(baseStorageCost).Add(hostCollateral)

	// create the renewal transaction, consisting of the final revision of the
	// old contract and the initial revision of the new contract
	finalRevision := contract.Revision
	finalRevision.RevisionNumber = types.MaxRevisionNumber
	finalRevision.RenterSignature = renterKey.SignHash(vc.ContractSigHash(finalRevision))
	initialRevision := types.FileContract{
		Filesize:        contract.Revision.Filesize,
		FileMerkleRoot:  contract.Revision.FileMerkleRoot,
		WindowStart:     contract.Revision.WindowStart + extension,
		WindowEnd:       contract.Revision.WindowEnd + extension,
		RenterOutput:    types.SiacoinOutput{Address: renterAddr, Value: renterFunds},
		HostOutput:      types.SiacoinOutput{Address: hostAddr, Value: validHostValue},
		MissedHostValue: missedHostValue,
		TotalCollateral: totalCollateral,
		RenterPublicKey: contract.Revision.RenterPublicKey,
		HostPublicKey:   contract.Revision.HostPublicKey,
		RevisionNumber:  0,
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
	renewalTxn.MinerFee = tp.RecommendedFee().Mul64(vc.TransactionWeight(renewalTxn))

	// The renter is responsible for it's funding, the host's initial revenue,
	// siafund tax, and miner fee.
	renterCost := renterFunds.Add(settings.ContractFee).Add(baseStorageCost).Add(vc.FileContractTax(renewal.InitialRevision)).Add(renewalTxn.MinerFee)
	renterRollover := finalRevision.RenterOutput.Value
	var toSign []types.ElementID
	if renterRollover.Cmp(renterCost) < 0 {
		// there isn't enough value left in the old contract to pay for the new
		// one; add more inputs
		additionalFunding := renterCost.Sub(renterRollover)
		added, cleanup, err := w.FundTransaction(&renewalTxn, additionalFunding, nil)
		if err != nil {
			return rhp.Contract{}, types.Transaction{}, fmt.Errorf("failed to fund renewal transaction: %w", err)
		}
		defer cleanup()
		toSign = added
	} else {
		// rollover the exact amount necessary to cover the cost of the new
		// contract (the remainder will be returned as a timelocked output)
		renterRollover = renterCost
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
	} else if resp.HostRollover.Cmp(renewal.FinalRevision.HostOutput.Value) > 0 {
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

	// now that all other signatures are complete, the renewal can be signed
	renewal.HostSignature = resp.RenewalSignature
	renewal.RenterSignature = renterKey.SignHash(renewalSigHash)
	// add the host's inputs to the transaction
	renewalTxn.SiacoinInputs = append(renewalTxn.SiacoinInputs, resp.Inputs...)
	renewalTxn.SiacoinOutputs = append(renewalTxn.SiacoinOutputs, resp.Outputs...)
	hostInputs := renewalTxn.SiacoinInputs[len(renterInputs):]

	// sign any inputs we added and send the signatures to the host
	renterSigs := rhp.RPCRenewContractRenterSignatures{
		SiacoinInputSignatures: make([][]types.Signature, len(renterInputs)),
		RenewalSignature:       renewal.RenterSignature,
	}
	if len(toSign) > 0 {
		if err := w.SignTransaction(vc, &renewalTxn, toSign); err != nil {
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
