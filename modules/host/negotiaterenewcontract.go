package host

import (
	"gitlab.com/NebulousLabs/errors"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// rhp2RenewBaseCollateral returns the base collateral on the storage in the file
// contract, using the host's external settings and the starting file contract.
func rhp2RenewBaseCollateral(so storageObligation, settings modules.HostExternalSettings, fc types.FileContract) types.Currency {
	if fc.WindowEnd <= so.proofDeadline() {
		return types.NewCurrency64(0)
	}
	timeExtension := fc.WindowEnd - so.proofDeadline()
	return settings.Collateral.Mul64(fc.FileSize).Mul64(uint64(timeExtension))
}

// rhp2RenewBasePrice returns the base cost of the storage in the file contract,
// using the host external settings and the starting file contract.
func rhp2RenewBasePrice(so storageObligation, settings modules.HostExternalSettings, fc types.FileContract) types.Currency {
	if fc.WindowEnd <= so.proofDeadline() {
		return types.NewCurrency64(0)
	}
	timeExtension := fc.WindowEnd - so.proofDeadline()
	return settings.StoragePrice.Mul64(fc.FileSize).Mul64(uint64(timeExtension))
}

// rhp2RenewContractCollateral returns the amount of collateral that the host is
// expected to add to the file contract based on the file contract and host
// settings.
func rhp2RenewContractCollateral(so storageObligation, settings modules.HostExternalSettings, fc types.FileContract) (types.Currency, error) {
	if fc.ValidHostPayout().Cmp(settings.ContractPrice) < 0 {
		return types.Currency{}, errors.New("ContractPrice higher than ValidHostOutput")
	}

	diff := fc.ValidHostPayout().Sub(settings.ContractPrice)
	rbp := rhp2RenewBasePrice(so, settings, fc)
	if diff.Cmp(rbp) < 0 {
		return types.Currency{}, errors.New("ValidHostOutput smaller than ContractPrice + RenewBasePrice")
	}
	return diff.Sub(rbp), nil
}

// renewContractCollateral returns the amount of collateral that the host is
// expected to add to the file contract based on the file contract and host
// settings.
func renewContractCollateral(pt *modules.RPCPriceTable, rev types.FileContractRevision, fc types.FileContract) (types.Currency, error) {
	if fc.ValidHostPayout().Cmp(pt.ContractPrice) < 0 {
		return types.Currency{}, errors.New("ContractPrice higher than ValidHostOutput")
	}

	diff := fc.ValidHostPayout().Sub(pt.ContractPrice)
	rbc, _ := modules.RenewBaseCosts(rev, pt, fc.WindowStart)
	if diff.Cmp(rbc) < 0 {
		return types.Currency{}, errors.New("ValidHostOutput smaller than ContractPrice + RenewBasePrice")
	}
	return diff.Sub(rbc), nil
}

// managedAddRenewCollateral adds the host's collateral to the renewed file
// contract.
func (h *Host) managedAddRenewCollateral(hostCollateral types.Currency, so storageObligation, txnSet []types.Transaction) (builder modules.TransactionBuilder, newParents []types.Transaction, newInputs []types.SiacoinInput, newOutputs []types.SiacoinOutput, err error) {
	txn := txnSet[len(txnSet)-1]
	parents := txnSet[:len(txnSet)-1]

	builder, err = h.wallet.RegisterTransaction(txn, parents)
	if err != nil {
		return
	}
	if hostCollateral.IsZero() {
		// We don't need to add anything to the transaction.
		return builder, nil, nil, nil, nil
	}
	err = builder.FundSiacoins(hostCollateral)
	if err != nil {
		builder.Drop()
		return nil, nil, nil, nil, extendErr("could not add collateral: ", ErrorInternal(err.Error()))
	}

	// Return which inputs and outputs have been added by the collateral call.
	newParentIndices, newInputIndices, newOutputIndices, _ := builder.ViewAdded()
	updatedTxn, updatedParents := builder.View()
	for _, parentIndex := range newParentIndices {
		newParents = append(newParents, updatedParents[parentIndex])
	}
	for _, inputIndex := range newInputIndices {
		newInputs = append(newInputs, updatedTxn.SiacoinInputs[inputIndex])
	}
	for _, outputIndex := range newOutputIndices {
		newOutputs = append(newOutputs, updatedTxn.SiacoinOutputs[outputIndex])
	}
	return builder, newParents, newInputs, newOutputs, nil
}

// managedVerifyRenewedContract checks that the contract renewal matches the
// previous contract and makes all of the appropriate payments.
func (h *Host) managedVerifyRenewedContract(so storageObligation, txnSet []types.Transaction, renterPK types.SiaPublicKey) (types.Currency, error) {
	// Register the HostInsufficientCollateral alert if necessary.
	var registerHostInsufficientCollateral bool
	defer func() {
		if registerHostInsufficientCollateral {
			h.staticAlerter.RegisterAlert(modules.AlertIDHostInsufficientCollateral, AlertMSGHostInsufficientCollateral, "", modules.SeverityWarning)
		} else {
			h.staticAlerter.UnregisterAlert(modules.AlertIDHostInsufficientCollateral)
		}
	}()

	// Check that the transaction set is not empty.
	if len(txnSet) < 1 {
		return types.Currency{}, extendErr("zero-length transaction set: ", ErrEmptyObject)
	}
	// Check that the transaction set has a file contract.
	if len(txnSet[len(txnSet)-1].FileContracts) < 1 {
		return types.Currency{}, extendErr("transaction without file contract: ", ErrEmptyObject)
	}

	_, maxFee := h.tpool.FeeEstimation()
	h.mu.Lock()
	blockHeight := h.blockHeight
	externalSettings := h.externalSettings(maxFee)
	internalSettings := h.settings
	lockedStorageCollateral := h.financialMetrics.LockedStorageCollateral
	publicKey := h.publicKey
	unlockHash := h.unlockHash
	h.mu.Unlock()
	fc := txnSet[len(txnSet)-1].FileContracts[0]

	// The file size and merkle root must match the file size and merkle root
	// from the previous file contract.
	if fc.FileSize != so.fileSize() {
		return types.Currency{}, ErrBadFileSize
	}
	if fc.FileMerkleRoot != so.merkleRoot() {
		return types.Currency{}, ErrBadFileMerkleRoot
	}
	// The WindowStart must be at least revisionSubmissionBuffer blocks into
	// the future.
	if fc.WindowStart <= blockHeight+revisionSubmissionBuffer {
		return types.Currency{}, ErrEarlyWindow
	}
	// WindowEnd must be at least settings.WindowSize blocks after WindowStart.
	if fc.WindowEnd < fc.WindowStart+externalSettings.WindowSize {
		return types.Currency{}, ErrSmallWindow
	}
	// WindowStart must not be more than settings.MaxDuration blocks into the
	// future.
	if fc.WindowStart > blockHeight+externalSettings.MaxDuration {
		return types.Currency{}, ErrLongDuration
	}

	// ValidProofOutputs shoud have 2 outputs (renter + host) and missed
	// outputs should have 3 (renter + host + void)
	if len(fc.ValidProofOutputs) != 2 || len(fc.MissedProofOutputs) != 3 {
		return types.Currency{}, ErrBadContractOutputCounts
	}
	// The unlock hashes of the valid and missed proof outputs for the host
	// must match the host's unlock hash. The third missed output should point
	// to the void.
	voidOutput, err := fc.MissedVoidOutput()
	if err != nil {
		return types.Currency{}, err
	}
	if fc.ValidHostOutput().UnlockHash != unlockHash || fc.MissedHostOutput().UnlockHash != unlockHash || voidOutput.UnlockHash != (types.UnlockHash{}) {
		return types.Currency{}, ErrBadPayoutUnlockHashes
	}

	// Check that the collateral does not exceed the maximum amount of
	// collateral allowed.
	expectedCollateral, err := rhp2RenewContractCollateral(so, externalSettings, fc)
	if err != nil {
		return types.Currency{}, errors.AddContext(err, "Failed to compute contract collateral")
	}
	if expectedCollateral.Cmp(externalSettings.MaxCollateral) > 0 {
		return types.Currency{}, errMaxCollateralReached
	}
	// Check that the host has enough room in the collateral budget to add this
	// collateral.
	if lockedStorageCollateral.Add(expectedCollateral).Cmp(internalSettings.CollateralBudget) > 0 {
		registerHostInsufficientCollateral = true
		return types.Currency{}, errCollateralBudgetExceeded
	}

	// Check that the missed proof outputs contain enough money, and that the
	// void output contains enough money.
	basePrice := rhp2RenewBasePrice(so, externalSettings, fc)
	baseCollateral := rhp2RenewBaseCollateral(so, externalSettings, fc)
	if fc.ValidHostPayout().Cmp(basePrice.Add(baseCollateral)) < 0 {
		return types.Currency{}, ErrLowHostValidOutput
	}
	expectedHostMissedOutput := fc.ValidHostPayout().Sub(basePrice).Sub(baseCollateral)
	if fc.MissedHostOutput().Value.Cmp(expectedHostMissedOutput) < 0 {
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
			publicKey,
		},
		SignaturesRequired: 2,
	}.UnlockHash()
	if fc.UnlockHash != expectedUH {
		return types.Currency{}, ErrBadUnlockHash
	}

	// Check that the transaction set has enough fees on it to get into the
	// blockchain.
	setFee := modules.CalculateFee(txnSet)
	minFee, _ := h.tpool.FeeEstimation()
	if setFee.Cmp(minFee) < 0 {
		return types.Currency{}, ErrLowTransactionFees
	}
	return expectedCollateral, nil
}
