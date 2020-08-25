package host

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// managedRPCRenewContract renews an existing contract. This causes the old
// contract to be revised to its maximum revision number and submitted to the
// blockchain in the same transaction which creates the new contract. That way
// contract renewal happens atomically.
func (h *Host) managedRPCRenewContract(stream siamux.Stream) error {
	// read the price table
	pt, err := h.staticReadPriceTableID(stream)
	if err != nil {
		return errors.AddContext(err, "failed to read price table")
	}

	// Get some values for the RPC. Use the ones from the price table if
	// available.
	h.mu.RLock()
	bh := pt.HostBlockHeight
	maxFee := pt.TxnFeeMaxRecommended
	minFee := pt.TxnFeeMinRecommended
	hpk := h.publicKey
	hsk := h.secretKey
	is := h.settings
	es := h.externalSettings(maxFee)
	lockedCollateral := h.financialMetrics.LockedStorageCollateral
	h.mu.RUnlock()

	// Read request
	var req modules.RPCRenewContractRequest
	err = modules.RPCRead(stream, &req)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to read renew contract request")
	}
	txns := req.TSet
	rpk := req.RenterPK

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
	err = managedAcceptRenewal(es.AcceptingContracts, bh, so.expiration())
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: host is not accepting a renewal")
	}

	// Verify the final revision.
	err = verifyClearingRevision(currentRevision, finalRevision, bh, pt.RenewContractCost)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to verify final revision")
	}

	// Verify the new contract.
	err = verifyRenewedContract(so, newContract, currentRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if errors.Contains(err, errCollateralBudgetExceeded) {
		h.staticAlerter.RegisterAlert(modules.AlertIDHostInsufficientCollateral, AlertMSGHostInsufficientCollateral, "", modules.SeverityWarning)
	} else {
		h.staticAlerter.UnregisterAlert(modules.AlertIDHostInsufficientCollateral)
	}
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to verify new contract")
	}

	// Check that the transaction set has enough fees on it to get into the
	// blockchain.
	setFee := modules.CalculateFee(txns)
	if setFee.Cmp(minFee) < 0 {
		return errors.AddContext(ErrLowTransactionFees, fmt.Sprintf("managedRPCRenewContract: insufficient txn fees %v < %v", setFee, minFee))
	}

	// Add the collateral to the contract.
	txnBuilder, newParents, newInputs, newOutputs, err := h.managedAddRenewCollateral(so, es, txns)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to add collateral")
	}

	// TODO: do we need to drop the txnBuilder in case of an error? According to
	// the docstring Drop can only be called before signatures are added.

	// Send the new inputs and outputs to the renter.
	err = modules.RPCWrite(stream, modules.RPCRenewContractCollateralResponse{
		NewParents: newParents,
		NewInputs:  newInputs,
		NewOutputs: newOutputs,
	})
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to send collateral response")
	}

	// Receive the signature for the final revision from the renter.
	var finalRevisionSigRenterResp modules.RPCRenewContractFinalRevisionSig
	err = modules.RPCRead(stream, &finalRevisionSigRenterResp)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to read final revision signatures from renter")
	}
	finalRevRenterSig := finalRevisionSigRenterResp.Signature

	// Manually add the revision signatures.
	finalRevHostSig, err := addRevisionSignatures(txnBuilder, finalRevision, finalRevRenterSig, hsk, rpk.ToPublicKey(), bh)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to add revision signatures to transaction")
	}

	// Send the host's signature back.
	err = modules.RPCWrite(stream, modules.RPCRenewContractFinalRevisionSig{
		Signature: finalRevHostSig,
	})
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to send final revision signatures to renter")
	}

	// Receive the txn signatures from the renter.
	var renterSignatureResp modules.RPCRenewContractRenterSignatures
	err = modules.RPCRead(stream, &renterSignatureResp)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to receive renter signatures")
	}
	renterTxnSigs := renterSignatureResp.RenterTxnSigs
	renterNoOpRevisionSig := renterSignatureResp.RenterNoOpRevisionSig

	// The host adds the renter transaction signatures, then signs the
	// transaction and submits it to the blockchain, creating a storage
	// obligation in the process.
	h.mu.RLock()
	fc := txns[len(txns)-1].FileContracts[0]
	renewRevenue := renewBasePrice(so, es, fc)
	renewRisk := renewBaseCollateral(so, es, fc)
	renewCollateral, err := renewContractCollateral(so, es, fc)
	h.mu.RUnlock()
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to comput contract collateral")
	}

	// Finalize the contract.
	var renterPK crypto.PublicKey
	copy(renterPK[:], rpk.Key)
	fca := finalizeContractArgs{
		builder:                 txnBuilder,
		renewal:                 true,
		renterPK:                renterPK,
		renterSignatures:        renterTxnSigs,
		renterRevisionSignature: renterNoOpRevisionSig,
		initialSectorRoots:      so.SectorRoots,
		hostCollateral:          renewCollateral,
		hostInitialRevenue:      renewRevenue,
		hostInitialRisk:         renewRisk,
		settings:                es,
	}
	hostTxnSignatures, hostRevisionSignature, newSOID, err := h.managedFinalizeContract(fca)
	if err != nil {
		return errors.AddContext(err, "managedRPCRenewContract: failed to finalize contract")
	}

	defer h.managedUnlockStorageObligation(newSOID)

	// Clear the old storage obligatoin.
	so.SectorRoots = []crypto.Hash{}
	so.RevisionTransactionSet = []types.Transaction{txns[len(txns)-1]}

	// we don't count the sectors as being removed since we prevented
	// managedFinalizeContract from incrementing the counters on virtual sectors
	// before
	h.managedModifyStorageObligation(so, nil, nil)

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

func addRevisionSignatures(txnBuilder modules.TransactionBuilder, finalRevision types.FileContractRevision, renterSigBytes crypto.Signature, sk crypto.SecretKey, rpk crypto.PublicKey, bh types.BlockHeight) (crypto.Signature, error) {
	txn, _ := txnBuilder.View()
	parentID := crypto.Hash(finalRevision.ParentID)
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
		ParentID:       parentID,
		PublicKeyIndex: 1,
		CoveredFields: types.CoveredFields{
			FileContracts:         []uint64{0},
			FileContractRevisions: []uint64{0},
		},
	}
	// Add the signatures to the builder.
	txn.TransactionSignatures = []types.TransactionSignature{renterSig, hostSig}
	sigHash := txn.SigHash(1, bh)
	encodedSig := crypto.SignHash(sigHash, sk)
	hostSig.Signature = encodedSig[:]

	// Verify the renter's signature.
	renterSigHash := txn.SigHash(0, bh)
	err := crypto.VerifyHash(renterSigHash, rpk, renterSigBytes)
	if err != nil {
		return crypto.Signature{}, errors.AddContext(err, "addRevisionSignatures: invalid renter signature")
	}

	// Add the signatures to the builder.
	txnBuilder.AddTransactionSignature(renterSig)
	txnBuilder.AddTransactionSignature(hostSig)
	return encodedSig, nil
}

func managedAcceptRenewal(acceptingContracts bool, blockHeight, soExpiration types.BlockHeight) error {
	// Don't accept a renewal if we don't accept new contracts.
	if !acceptingContracts {
		return errors.New("host is not accepting new contracts")
	}
	// Check that the time to finalize and submit the file contract revision
	// has not already passed.
	if soExpiration-revisionSubmissionBuffer <= blockHeight {
		return ErrLateRevision
	}
	return nil
}

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
	// TODO: is it safe to check that the whole txnSet only contains a single
	// contract and revision?
	return txn.FileContractRevisions[0], txn.FileContracts[0], nil
}

func verifyRenewedContract(so storageObligation, fc types.FileContract, oldRevision types.FileContractRevision, blockHeight types.BlockHeight, internalSettings modules.HostInternalSettings, externalSettings modules.HostExternalSettings, renterPK, hostPK types.SiaPublicKey, lockedCollateral types.Currency) error {
	unlockHash := externalSettings.UnlockHash

	// The file size and merkle root must match the file size and merkle root
	// from the previous file contract.
	if fc.FileSize != so.fileSize() {
		return ErrBadFileSize
	}
	if fc.FileMerkleRoot != so.merkleRoot() {
		return ErrBadFileMerkleRoot
	}
	// The WindowStart must be at least revisionSubmissionBuffer blocks into
	// the future.
	if fc.WindowStart <= blockHeight+revisionSubmissionBuffer {
		return ErrEarlyWindow
	}
	// WindowEnd must be at least externalSettings.WindowSize blocks after WindowStart.
	if fc.WindowEnd < fc.WindowStart+externalSettings.WindowSize {
		return ErrSmallWindow
	}
	// WindowStart must not be more than externalSettings.MaxDuration blocks into the
	// future.
	if fc.WindowStart > blockHeight+externalSettings.MaxDuration {
		return ErrLongDuration
	}

	// ValidProofOutputs shoud have 2 outputs (renter + host) and missed
	// outputs should have 3 (renter + host + void)
	if len(fc.ValidProofOutputs) != 2 || len(fc.MissedProofOutputs) != 3 {
		return ErrBadContractOutputCounts
	}
	// The unlock hashes of the valid and missed proof outputs for the host
	// must match the host's unlock hash. The third missed output should point
	// to the void.
	voidOutput, err := fc.MissedVoidOutput()
	if err != nil {
		return err
	}
	if fc.ValidHostOutput().UnlockHash != oldRevision.ValidHostOutput().UnlockHash || fc.MissedHostOutput().UnlockHash != unlockHash || voidOutput.UnlockHash != (types.UnlockHash{}) {
		return ErrBadPayoutUnlockHashes
	}

	// Check that the collateral does not exceed the maximum amount of
	// collateral allowed.
	expectedCollateral, err := renewContractCollateral(so, externalSettings, fc)
	if err != nil {
		return errors.AddContext(err, "Failed to compute contract collateral")
	}
	if expectedCollateral.Cmp(externalSettings.MaxCollateral) > 0 {
		return errMaxCollateralReached
	}
	// Check that the host has enough room in the collateral budget to add this
	// collateral.
	if lockedCollateral.Add(expectedCollateral).Cmp(internalSettings.CollateralBudget) > 0 {
		return errCollateralBudgetExceeded
	}

	// Check that the missed proof outputs contain enough money, and that the
	// void output contains enough money.
	basePrice := renewBasePrice(so, externalSettings, fc)
	baseCollateral := renewBaseCollateral(so, externalSettings, fc)
	if fc.ValidHostPayout().Cmp(basePrice.Add(baseCollateral)) < 0 {
		return ErrLowHostValidOutput
	}
	expectedHostMissedOutput := fc.ValidHostPayout().Sub(basePrice).Sub(baseCollateral)
	if fc.MissedHostOutput().Value.Cmp(expectedHostMissedOutput) < 0 {
		return ErrLowHostMissedOutput
	}
	// Check that the void output has the correct value.
	expectedVoidOutput := basePrice.Add(baseCollateral)
	if voidOutput.Value.Cmp(expectedVoidOutput) > 0 {
		return ErrLowVoidOutput
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
	if fc.UnlockHash != expectedUH {
		return ErrBadUnlockHash
	}
	return nil
}
