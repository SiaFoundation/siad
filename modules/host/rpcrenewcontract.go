package host

import (
	"encoding/json"
	"fmt"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

var (
	// ErrInsufficientRenterFee is the error returned when the renter provided
	// less txn fees than specified in the price table.
	ErrInsufficientRenterFee = errors.New("renter proposed a txn with less fees than specified in the price table")
)

// managedRPCRenewContract renews an existing contract. This causes the old
// contract to be revised to its maximum revision number and submitted to the
// blockchain in the same transaction which creates the new contract. That way
// contract renewal happens atomically.
func (h *Host) managedRPCRenewContract(stream siamux.Stream) error {
	// Disrupt if necessary.
	if h.dependencies.Disrupt("RenewFail") {
		return errors.New("RenewFail")
	}

	// fetch the price table
	pt, err := h.staticReadPriceTableID(stream)
	if errors.Contains(err, errEmptyPriceTableUID) {
		// send a temporary price table.
		pt = h.managedPriceTableForRenter()

		// json encode the price table
		ptBytes, err := json.Marshal(pt)
		if err != nil {
			return errors.AddContext(err, "Failed to JSON encode the price table")
		}

		// send it to the renter
		uptResp := modules.RPCUpdatePriceTableResponse{PriceTableJSON: ptBytes}
		if err = modules.RPCWrite(stream, uptResp); err != nil {
			return errors.AddContext(err, "Failed to write response")
		}
	} else if err != nil {
		return errors.AddContext(err, "failed to fetch price table")
	}

	// Get some values for the RPC. Use the ones from the price table if
	// available.
	h.mu.RLock()
	bh := pt.HostBlockHeight
	minFee := pt.TxnFeeMinRecommended
	hpk := h.publicKey
	hsk := h.secretKey
	contractPrice := pt.ContractPrice
	is := h.settings // internal settings
	ac := is.AcceptingContracts
	lockedCollateral := h.financialMetrics.LockedStorageCollateral
	unlockHash := h.unlockHash
	h.mu.RUnlock()

	// Read request
	var req modules.RPCRenewContractRequest
	err = modules.RPCReadMaxLen(stream, &req, modules.RenewDecodeMaxLen)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to read renew contract request")
	}
	txns := req.TSet
	rpk := req.RenterPK
	finalRevRenterSig := req.FinalRevSig

	// Check that the transaction set has enough fees on it to get into the
	// blockchain. There need to be enough fees to make it into the current pool
	// and also at least as much fees as specified in the price table.
	setFee := modules.CalculateFee(txns)
	if setFee.Cmp(minFee) < 0 {
		return errors.AddContext(ErrInsufficientRenterFee, fmt.Sprintf("managedRPCRenewContract: insufficient txn fees %v < %v", setFee, minFee))
	}
	poolMinFee, _ := h.tpool.FeeEstimation()
	if setFee.Cmp(poolMinFee) < 0 {
		return errors.AddContext(ErrLowTransactionFees, fmt.Sprintf("managedRPCRenewContract: insufficient txn fees to get txn into host tpool %v < %v", setFee, poolMinFee))
	}

	// Fetch the final revision and new contract from the transactionset the
	// renter sent. This also verifies that there are only one contract and
	// revision in the last transaction.
	finalRevision, newContract, err := fetchRevisionAndContract(txns)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to fetch final revision and new contract from txnSet")
	}

	// The contract id of the contract to renew is the parent of the final
	// revision the renter sent.
	fcid := finalRevision.ParentID

	// Lock storage obligation
	h.managedLockStorageObligation(fcid)
	defer h.managedUnlockStorageObligation(fcid)

	// Get storage obligation
	so, err := h.managedGetStorageObligation(fcid)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to get storage obligation")
	}

	// Get latest revision from storage obligation.
	currentRevision, err := so.recentRevision()
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to get current revision")
	}

	// Check if the host wants to accept a renewal for the obligation.
	err = renewAllowed(ac, bh, so.expiration())
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: host is not accepting a renewal")
	}

	// Verify the final revision against the current revision. We use the
	// ZeroCurrency here since the basePrice will cover the renewal rpc cost.
	// That way the new contract pays for the renewal instead of the old one.
	// Which means a contract can even renew if it's out of money.
	err = verifyClearingRevision(currentRevision, finalRevision, bh, types.ZeroCurrency)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to verify final revision")
	}

	// Verify the new contract against the old revision. The final one doesn't
	// have the size set anymore which we need for collateral and base price
	// calculations.
	hostCollateral, err := verifyRenewedContract(so, newContract, currentRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if errors.Contains(err, errCollateralBudgetExceeded) {
		h.staticAlerter.RegisterAlert(modules.AlertIDHostInsufficientCollateral, AlertMSGHostInsufficientCollateral, "", modules.SeverityWarning)
	} else {
		h.staticAlerter.UnregisterAlert(modules.AlertIDHostInsufficientCollateral)
	}
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to verify new contract")
	}

	// Add the collateral to the contract as well as the renter's pre-payment.
	txnBuilder, newParents, newInputs, newOutputs, err := h.managedAddRenewCollateral(hostCollateral, so, txns)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to add collateral")
	}

	// Manually add the revision signatures from the renter.
	finalRevHostSig, err := addRevisionSignatures(txnBuilder, finalRevRenterSig, hsk, rpk.ToPublicKey(), bh)
	if err != nil {
		txnBuilder.Drop()
		return errors.AddContext(err, "managedRPCRenewContract: failed to add revision signatures to transaction")
	}

	// Send the new inputs and outputs to the renter.
	err = modules.RPCWrite(stream, modules.RPCRenewContractCollateralResponse{
		NewParents:  newParents,
		NewInputs:   newInputs,
		NewOutputs:  newOutputs,
		FinalRevSig: finalRevHostSig,
	})
	if err != nil {
		txnBuilder.Drop()
		return errors.AddContext(err, "managedRPCRenewContract: failed to send collateral response")
	}

	// Receive the txn signatures from the renter.
	var renterSignatureResp modules.RPCRenewContractRenterSignatures
	err = modules.RPCReadMaxLen(stream, &renterSignatureResp, modules.RenewDecodeMaxLen)
	if err != nil {
		txnBuilder.Drop()
		return errors.AddContext(err, "managedRPCRenewContract: failed to receive renter signatures")
	}
	renterTxnSigs := renterSignatureResp.RenterTxnSigs
	renterNoOpRevisionSig := renterSignatureResp.RenterNoOpRevisionSig

	// The host adds the renter transaction signatures, then signs the
	// transaction and submits it to the blockchain, creating a storage
	// obligation in the process.
	h.mu.RLock()
	fc := txns[len(txns)-1].FileContracts[0]
	renewRevenue, renewRisk := modules.RenewBaseCosts(finalRevision, pt, fc.WindowStart)
	h.mu.RUnlock()

	// Clear the old storage obligation.
	oldRoots := so.SectorRoots
	so.SectorRoots = []crypto.Hash{}
	so.RevisionTransactionSet = []types.Transaction{txns[len(txns)-1]}

	// Finalize the contract.
	var renterPK crypto.PublicKey
	copy(renterPK[:], rpk.Key)
	fca := finalizeContractArgs{
		builder:                 txnBuilder,
		contractPrice:           contractPrice,
		renewedSO:               &so,
		renterPK:                renterPK,
		renterSignatures:        renterTxnSigs,
		renterRevisionSignature: renterNoOpRevisionSig,
		initialSectorRoots:      oldRoots,
		hostCollateral:          hostCollateral,
		hostInitialRevenue:      renewRevenue,
		hostInitialRisk:         renewRisk,
	}
	hostTxnSignatures, hostRevisionSignature, newSOID, err := h.managedFinalizeContract(fca)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to finalize contract")
	}

	defer h.managedUnlockStorageObligation(newSOID)

	// Send signatures back to renter.
	err = modules.RPCWrite(stream, modules.RPCRenewContractHostSignatures{
		ContractSignatures:    hostTxnSignatures,
		NoOpRevisionSignature: hostRevisionSignature,
	})
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to send host signatures")
	}

	return nil
}

// addRevisionSignatures verifies the revision signature provided by the renter
// and adds it together with the host's own signature to the txnBuilder.
func addRevisionSignatures(txnBuilder modules.TransactionBuilder, renterSigBytes crypto.Signature, sk crypto.SecretKey, rpk crypto.PublicKey, bh types.BlockHeight) (crypto.Signature, error) {
	txn, _ := txnBuilder.View()
	if len(txn.FileContractRevisions) != 1 {
		return crypto.Signature{}, errors.New("addRevisionSignatures: invalid number of revisions")
	}
	if len(txn.FileContracts) != 1 {
		return crypto.Signature{}, errors.New("addRevisionSignatures: invalid number of contracts")
	}
	contract := txn.FileContracts[0]
	revision := txn.FileContractRevisions[0]

	parentID := crypto.Hash(revision.ParentID)
	renterSig := types.TransactionSignature{
		ParentID: parentID,
		CoveredFields: types.CoveredFields{
			FileContracts:         []uint64{0},
			FileContractRevisions: []uint64{0},
		},
		PublicKeyIndex: 0,
		Signature:      renterSigBytes[:],
	}
	hostSig := types.TransactionSignature{
		ParentID: parentID,
		CoveredFields: types.CoveredFields{
			FileContracts:         []uint64{0},
			FileContractRevisions: []uint64{0},
		},
		PublicKeyIndex: 1,
	}
	// Add the signatures to the builder.
	txn.TransactionSignatures = []types.TransactionSignature{renterSig, hostSig}
	sigHash := txn.SigHash(1, bh)
	encodedSig := crypto.SignHash(sigHash, sk)
	hostSig.Signature = encodedSig[:]

	// Add the signatures to the builder.
	txnBuilder.AddTransactionSignature(renterSig)
	txnBuilder.AddTransactionSignature(hostSig)

	// Get the txn with the signatures.
	txn, _ = txnBuilder.View()

	// Verify the signatures.
	err := modules.VerifyRenewalTransactionSignatures(revision, contract, txn.TransactionSignatures, bh)
	if err != nil {
		return crypto.Signature{}, errors.AddContext(err, "addRevisionSignatures: invalid renter signature")
	}
	return encodedSig, nil
}

// renewAllowed determines whether it's ok for a contract to be renewed
// according to the host.
func renewAllowed(acceptingContracts bool, blockHeight, soExpiration types.BlockHeight) error {
	// Don't accept a renewal if we don't accept new contracts.
	if !acceptingContracts {
		return ErrNotAcceptingContracts
	}
	// Check that the time to finalize and submit the file contract revision
	// has not already passed.
	if soExpiration-revisionSubmissionBuffer <= blockHeight {
		return ErrLateRevision
	}
	return nil
}

// fetchRevisionAndContract extracts a revision and contract from the provided
// txnSet while also sanity checking the length of the set and the number of
// contracts and revisions in it.
func fetchRevisionAndContract(txnSet []types.Transaction) (types.FileContractRevision, types.FileContract, error) {
	// Check that the transaction set is not empty.
	if len(txnSet) < 1 {
		return types.FileContractRevision{}, types.FileContract{}, errors.AddContext(ErrEmptyObject, "zero-length transaction set")
	}
	// Check that the transaction set has a file contract and revision in the
	// last transaction.
	txn := txnSet[len(txnSet)-1]
	if len(txn.FileContracts) != 1 {
		return types.FileContractRevision{}, types.FileContract{}, errors.New("fetchRevisionAndContract: unexpected number of filecontracts")
	}
	if len(txn.FileContractRevisions) != 1 {
		return types.FileContractRevision{}, types.FileContract{}, errors.New("fetchRevisionAndContract: unexpected number of revisions")
	}
	return txn.FileContractRevisions[0], txn.FileContracts[0], nil
}

// verifyRenewedContract is a helper method that checks if the proposed renewed
// contract is acceptable.
func verifyRenewedContract(so storageObligation, newContract types.FileContract, oldRevision types.FileContractRevision, blockHeight types.BlockHeight, internalSettings modules.HostInternalSettings, unlockHash types.UnlockHash, pt *modules.RPCPriceTable, renterPK, hostPK types.SiaPublicKey, lockedCollateral types.Currency) (types.Currency, error) {
	// The file size and merkle root must match the file size and merkle root
	// from the previous file contract.
	if newContract.FileSize != so.fileSize() {
		return types.Currency{}, ErrBadFileSize
	}
	if newContract.FileMerkleRoot != so.merkleRoot() {
		return types.Currency{}, ErrBadFileMerkleRoot
	}
	// The WindowStart must be at least revisionSubmissionBuffer blocks into
	// the future.
	if newContract.WindowStart <= blockHeight+revisionSubmissionBuffer {
		return types.Currency{}, ErrEarlyWindow
	}
	// WindowEnd must be at least pt.WindowSize blocks after WindowStart.
	if newContract.WindowEnd < newContract.WindowStart+pt.WindowSize {
		return types.Currency{}, ErrSmallWindow
	}
	// WindowStart must not be more than pt.MaxDuration blocks into the
	// future.
	if newContract.WindowStart > blockHeight+pt.MaxDuration {
		return types.Currency{}, ErrLongDuration
	}

	// ValidProofOutputs should have 2 outputs (renter + host) and missed
	// outputs should have 3 (renter + host + void)
	if len(newContract.ValidProofOutputs) != 2 || len(newContract.MissedProofOutputs) != 3 {
		return types.Currency{}, ErrBadContractOutputCounts
	}
	// The unlock hashes of the valid and missed proof outputs for the host
	// must match the host's unlock hash. The third missed output should point
	// to the void.
	voidOutput, err := newContract.MissedVoidOutput()
	if err != nil {
		return types.Currency{}, err
	}
	if newContract.ValidHostOutput().UnlockHash != unlockHash || newContract.MissedHostOutput().UnlockHash != unlockHash || voidOutput.UnlockHash != (types.UnlockHash{}) {
		return types.Currency{}, ErrBadPayoutUnlockHashes
	}

	// Check that the collateral does not exceed the maximum amount of
	// collateral allowed.
	expectedCollateral, err := renewContractCollateral(pt, oldRevision, newContract)
	if err != nil {
		err = errors.Compose(err, ErrLowHostValidOutput)
		return types.Currency{}, errors.AddContext(err, "Failed to compute contract collateral")
	}
	if expectedCollateral.Cmp(pt.MaxCollateral) > 0 {
		return types.Currency{}, errMaxCollateralReached
	}
	// Check that the host has enough room in the collateral budget to add this
	// collateral.
	if lockedCollateral.Add(expectedCollateral).Cmp(internalSettings.CollateralBudget) > 0 {
		return types.Currency{}, errCollateralBudgetExceeded
	}

	// Compute the basePrice and baseCollateral.
	// NOTE: Since the renter might choose to expect less than the
	// baseCollateral, we need to potentially adjust it.
	basePrice, baseCollateral := modules.RenewBaseCosts(oldRevision, pt, newContract.WindowStart)
	if expectedCollateral.Cmp(baseCollateral) < 0 {
		baseCollateral = expectedCollateral
	}

	// Check that the missed proof outputs contain enough money, and that the
	// void output contains enough money.
	if newContract.ValidHostPayout().Cmp(basePrice.Add(baseCollateral).Add(pt.ContractPrice)) < 0 {
		return types.Currency{}, ErrLowHostValidOutput
	}
	expectedHostMissedOutput := newContract.ValidHostPayout().Sub(basePrice).Sub(baseCollateral)
	if newContract.MissedHostOutput().Value.Cmp(expectedHostMissedOutput) < 0 {
		return types.Currency{}, ErrLowHostMissedOutput
	}
	// Check that the void output has the correct value.
	expectedVoidOutput := basePrice.Add(baseCollateral)
	if voidOutput.Value.Cmp(expectedVoidOutput) < 0 {
		return types.Currency{}, ErrLowVoidOutput
	}

	// The unlock hash for the file contract must match the unlock hash that
	// the host knows how to spend.
	expectedUH := types.UnlockConditions{
		PublicKeys: []types.SiaPublicKey{
			renterPK,
			hostPK,
		},
		SignaturesRequired: 2,
	}.UnlockHash()
	if newContract.UnlockHash != expectedUH {
		return types.Currency{}, ErrBadUnlockHash
	}
	return expectedCollateral, nil
}
