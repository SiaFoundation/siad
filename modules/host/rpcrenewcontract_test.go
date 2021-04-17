package host

import (
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestAcceptRenewal is a unit test for managedAcceptRenewal.
func TestAcceptRenewal(t *testing.T) {
	t.Parallel()

	// acceptable
	err := renewAllowed(true, 0, revisionSubmissionBuffer+1)
	if err != nil {
		t.Fatal(err)
	}

	// not accepting new contracts
	err = renewAllowed(false, 0, revisionSubmissionBuffer+1)
	if !errors.Contains(err, ErrNotAcceptingContracts) {
		t.Fatal(err)
	}

	// too close to submission.
	err = renewAllowed(true, 0, revisionSubmissionBuffer)
	if !errors.Contains(err, ErrLateRevision) {
		t.Fatal(err)
	}
}

// TestFetchRevisionAndContract is a unit test for fetchRevisionAndContract.
func TestFetchRevisionAndContract(t *testing.T) {
	t.Parallel()

	txnSet := []types.Transaction{
		{}, // empty parent
		{
			FileContracts: []types.FileContract{
				{
					FileSize: 123,
				},
			},
			FileContractRevisions: []types.FileContractRevision{
				{
					NewFileSize: 321,
				},
			},
		},
	}

	// Success
	fcr, fc, err := fetchRevisionAndContract(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fc, txnSet[1].FileContracts[0]) {
		t.Fatal("wrong contract")
	}
	if !reflect.DeepEqual(fcr, txnSet[1].FileContractRevisions[0]) {
		t.Fatal("wrong revision")
	}

	// Empty set
	_, _, err = fetchRevisionAndContract([]types.Transaction{})
	if err == nil {
		t.Fatal("empty set shouldn't succeed")
	}

	// No contract.
	noContract := txnSet
	noContract[1].FileContracts = nil
	_, _, err = fetchRevisionAndContract(noContract)
	if err == nil {
		t.Fatal("found contract")
	}

	// No revision.
	noRevision := txnSet
	noRevision[1].FileContractRevisions = nil
	_, _, err = fetchRevisionAndContract(noRevision)
	if err == nil {
		t.Fatal("found revision")
	}
}

// TestVerifyRenewedContract is a unit test for verifyRenewedContract.
func TestVerifyRenewedContract(t *testing.T) {
	t.Parallel()

	// Declare vars for success.
	bh := types.BlockHeight(0)
	_, pk := crypto.GenerateKeyPair()
	rpk := types.Ed25519PublicKey(pk)
	_, pk = crypto.GenerateKeyPair()
	hpk := types.Ed25519PublicKey(pk)
	pt := &modules.RPCPriceTable{
		CollateralCost:    types.NewCurrency64(1),
		MaxCollateral:     types.SiacoinPrecision.Mul64(100),
		ContractPrice:     types.SiacoinPrecision,
		MaxDuration:       100,
		WriteStoreCost:    types.NewCurrency64(1),
		WindowSize:        10,
		RenewContractCost: types.SiacoinPrecision,
	}
	unlockHash := types.UnlockHash{2}
	is := modules.HostInternalSettings{
		CollateralBudget: pt.MaxCollateral,
	}
	so := storageObligation{
		RevisionTransactionSet: []types.Transaction{
			{
				FileContractRevisions: []types.FileContractRevision{
					{
						NewFileSize:       123,
						NewFileMerkleRoot: crypto.Hash{1},
					},
				},
			},
		},
	}
	oldRevision := types.FileContractRevision{
		NewFileSize: so.fileSize(),
		NewValidProofOutputs: []types.SiacoinOutput{
			{
				UnlockHash: types.UnlockHash{1},
				Value:      types.SiacoinPrecision.Mul64(10),
			},
			{
				UnlockHash: types.UnlockHash{2},
				Value:      types.SiacoinPrecision.Mul64(20),
			},
		},
		NewMissedProofOutputs: []types.SiacoinOutput{
			{
				UnlockHash: types.UnlockHash{1},
				Value:      types.SiacoinPrecision.Mul64(10),
			},
			{
				UnlockHash: types.UnlockHash{2},
				Value:      types.SiacoinPrecision.Mul64(20).Sub64(1),
			},
			{
				UnlockHash: types.UnlockHash{},
				Value:      types.ZeroCurrency, // set later
			},
		},
	}
	fc := types.FileContract{
		FileSize:           so.fileSize(),
		FileMerkleRoot:     so.merkleRoot(),
		WindowStart:        bh + revisionSubmissionBuffer + 1,
		WindowEnd:          bh + revisionSubmissionBuffer + 1 + pt.WindowSize,
		ValidProofOutputs:  oldRevision.NewValidProofOutputs,
		MissedProofOutputs: oldRevision.NewMissedProofOutputs,
		UnlockHash: types.UnlockConditions{
			PublicKeys: []types.SiaPublicKey{
				rpk,
				hpk,
			},
			SignaturesRequired: 2,
		}.UnlockHash(),
	}
	basePrice, baseCollateral := modules.RenewBaseCosts(oldRevision, pt, fc.WindowStart)
	lockedCollateral := types.ZeroCurrency
	expectedCollateral, err := renewContractCollateral(pt, so.RevisionTransactionSet[0].FileContractRevisions[0], fc)
	if err != nil {
		t.Fatal(err)
	}
	oldRevision.NewMissedProofOutputs[2].Value = basePrice.Add(expectedCollateral)

	// Success
	hostCollateral, err := verifyRenewedContract(so, fc, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if err != nil {
		t.Fatal(err)
	}
	if expectedCollateral.Cmp(hostCollateral) != 0 {
		t.Fatal("wrong collateral returned")
	}

	// Success - Renter expecting less (== 0) collateral
	goodFC := fc
	goodFC.ValidProofOutputs = append([]types.SiacoinOutput{}, goodFC.ValidProofOutputs...)
	goodFC.SetValidHostPayout(basePrice.Add(pt.ContractPrice))
	_, err = verifyRenewedContract(so, goodFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if err != nil {
		t.Fatal(err)
	}
	if expectedCollateral.Cmp(hostCollateral) != 0 {
		t.Fatal("wrong collateral returned")
	}

	// Wrong filesize
	badFC := fc
	badFC.FileSize++
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadFileSize) {
		t.Fatal(err)
	}

	// Wrong merkle root
	badFC = fc
	badFC.FileMerkleRoot = crypto.Hash{}
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadFileMerkleRoot) {
		t.Fatal(err)
	}

	// Early window
	badFC = fc
	badFC.WindowStart--
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrEarlyWindow) {
		t.Fatal(err)
	}

	// Small window
	badFC = fc
	badFC.WindowEnd--
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrSmallWindow) {
		t.Fatal(err)
	}

	// Long duration
	badFC = fc
	badFC.WindowStart = bh + pt.MaxDuration + 1
	badFC.WindowEnd = badFC.WindowStart + pt.WindowSize
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrLongDuration) {
		t.Fatal(err)
	}

	// Bad output count #1
	badFC = fc
	badFC.ValidProofOutputs = nil
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatal(err)
	}

	// Bad output count #2
	badFC = fc
	badFC.MissedProofOutputs = nil
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatal(err)
	}

	// Bad payout unlock hash #1
	badFC = fc
	badFC.ValidProofOutputs = append([]types.SiacoinOutput{}, badFC.ValidProofOutputs...)
	badFC.ValidProofOutputs[1].UnlockHash = types.UnlockHash{}
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal(err)
	}

	// Bad payout unlock hash #2
	badFC = fc
	badFC.MissedProofOutputs = append([]types.SiacoinOutput{}, badFC.MissedProofOutputs...)
	badFC.MissedProofOutputs[1].UnlockHash = types.UnlockHash{}
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal(err)
	}

	// Bad payout unlock hash #3
	badFC = fc
	badFC.MissedProofOutputs = append([]types.SiacoinOutput{}, badFC.MissedProofOutputs...)
	badFC.MissedProofOutputs[2].UnlockHash = types.UnlockHash{1}
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal(err)
	}

	// Max collateral reached
	badPT := *pt
	badPT.MaxCollateral = expectedCollateral.Sub64(1)
	_, err = verifyRenewedContract(so, fc, oldRevision, bh, is, unlockHash, &badPT, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, errMaxCollateralReached) {
		t.Fatal(err)
	}

	// Collateral budget exceeded.
	badLockedCollateral := is.CollateralBudget.Sub(expectedCollateral).Add64(1)
	_, err = verifyRenewedContract(so, fc, oldRevision, bh, is, unlockHash, pt, rpk, hpk, badLockedCollateral)
	if !errors.Contains(err, errCollateralBudgetExceeded) {
		t.Fatal(err)
	}

	// Low host valid output
	badFC = fc
	badFC.ValidProofOutputs = append([]types.SiacoinOutput{}, badFC.ValidProofOutputs...)
	badFC.ValidProofOutputs[1].Value = pt.ContractPrice.Add(basePrice).Sub64(1)
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrLowHostValidOutput) {
		t.Fatal(err)
	}

	// Low host missed output
	badFC = fc
	badFC.ValidProofOutputs = append([]types.SiacoinOutput{}, badFC.ValidProofOutputs...)
	badFC.MissedProofOutputs = append([]types.SiacoinOutput{}, badFC.MissedProofOutputs...)
	badFC.ValidProofOutputs[1].Value = pt.ContractPrice.Add(basePrice)
	badFC.MissedProofOutputs[1].Value = badFC.ValidProofOutputs[1].Value.Sub(baseCollateral).Sub(basePrice).Sub64(1)
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrLowHostMissedOutput) {
		t.Fatal(err)
	}

	// Low void output.
	badFC = fc
	badFC.MissedProofOutputs = append([]types.SiacoinOutput{}, badFC.MissedProofOutputs...)
	_ = badFC.SetMissedVoidPayout(baseCollateral.Add(basePrice).Sub64(1))
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrLowVoidOutput) {
		t.Fatal(err)
	}

	// Bad unlock hash.
	badFC = fc
	badFC.UnlockHash = types.UnlockHash{}
	_, err = verifyRenewedContract(so, badFC, oldRevision, bh, is, unlockHash, pt, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadUnlockHash) {
		t.Fatal(err)
	}

	// Success - whole basePrice is already paid for.
	goodFC = fc
	goodFC.ValidProofOutputs = append([]types.SiacoinOutput{}, goodFC.ValidProofOutputs...)
	goodFC.MissedProofOutputs = append([]types.SiacoinOutput{}, goodFC.MissedProofOutputs...)
	collateral := types.NewCurrency64(1)
	goodFC.SetValidHostPayout(pt.ContractPrice.Add(basePrice).Add(collateral))
	err = goodFC.SetMissedVoidPayout(collateral.Add(basePrice))
	if err != nil {
		t.Fatal(err)
	}
}
