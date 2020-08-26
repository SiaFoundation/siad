package host

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// TestAcceptRenewal is a unit test for managedAcceptRenewal.
func TestAcceptRenewal(t *testing.T) {
	t.Parallel()

	// acceptable
	err := needsRenewal(true, 0, revisionSubmissionBuffer+1)
	if err != nil {
		t.Fatal(err)
	}

	// not accepting new contracts
	err = needsRenewal(false, 0, revisionSubmissionBuffer+1)
	if err == nil || !strings.Contains(err.Error(), "host is not accepting new contracts") {
		t.Fatal(err)
	}

	// too close to submission.
	err = needsRenewal(true, 0, revisionSubmissionBuffer)
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
		t.Fatal(err)
	}

	// No contract.
	noContract := txnSet
	noContract[1].FileContracts = nil
	fcr, fc, err = fetchRevisionAndContract(noContract)
	if err == nil {
		t.Fatal("found contract")
	}

	// No revision.
	noRevision := txnSet
	noRevision[1].FileContractRevisions = nil
	fcr, fc, err = fetchRevisionAndContract(noRevision)
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
	es := modules.HostExternalSettings{
		Collateral:    types.NewCurrency64(1),
		MaxCollateral: types.SiacoinPrecision.Mul64(100),
		ContractPrice: types.SiacoinPrecision,
		MaxDuration:   100,
		UnlockHash:    types.UnlockHash{2},
		StoragePrice:  types.NewCurrency64(1),
		WindowSize:    10,
	}
	is := modules.HostInternalSettings{
		CollateralBudget: es.MaxCollateral,
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
		WindowEnd:          bh + revisionSubmissionBuffer + 1 + es.WindowSize,
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
	basePrice := renewBasePrice(so, es, fc)
	baseCollateral := renewBaseCollateral(so, es, fc)
	oldRevision.NewMissedProofOutputs[2].Value = basePrice.Add(baseCollateral)
	lockedCollateral := types.ZeroCurrency
	expectedCollateral, err := renewContractCollateral(so, es, fc)
	if err != nil {
		t.Fatal(err)
	}

	// Success
	err = verifyRenewedContract(so, fc, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if err != nil {
		t.Fatal(err)
	}

	// Wrong filesize
	badFC := fc
	badFC.FileSize++
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadFileSize) {
		t.Fatal(err)
	}

	// Wrong merkle root
	badFC = fc
	badFC.FileMerkleRoot = crypto.Hash{}
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadFileMerkleRoot) {
		t.Fatal(err)
	}

	// Early window
	badFC = fc
	badFC.WindowStart--
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrEarlyWindow) {
		t.Fatal(err)
	}

	// Small window
	badFC = fc
	badFC.WindowEnd--
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrSmallWindow) {
		t.Fatal(err)
	}

	// Long duration
	badFC = fc
	badFC.WindowStart = bh + es.MaxDuration + 1
	badFC.WindowEnd = badFC.WindowStart + es.WindowSize
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrLongDuration) {
		t.Fatal(err)
	}

	// Bad output count #1
	badFC = fc
	badFC.ValidProofOutputs = nil
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatal(err)
	}

	// Bad output count #2
	badFC = fc
	badFC.MissedProofOutputs = nil
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatal(err)
	}

	// Bad payout unlock hash #1
	badFC = fc
	badFC.ValidProofOutputs = append([]types.SiacoinOutput{}, badFC.ValidProofOutputs...)
	badFC.ValidProofOutputs[1].UnlockHash = types.UnlockHash{}
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal(err)
	}

	// Bad payout unlock hash #2
	badFC = fc
	badFC.MissedProofOutputs = append([]types.SiacoinOutput{}, badFC.MissedProofOutputs...)
	badFC.MissedProofOutputs[1].UnlockHash = types.UnlockHash{}
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal(err)
	}

	// Bad payout unlock hash #3
	badFC = fc
	badFC.MissedProofOutputs = append([]types.SiacoinOutput{}, badFC.MissedProofOutputs...)
	badFC.MissedProofOutputs[2].UnlockHash = types.UnlockHash{1}
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal(err)
	}

	// Max collateral reached
	badES := es
	badES.MaxCollateral = expectedCollateral.Sub64(1)
	err = verifyRenewedContract(so, fc, oldRevision, bh, is, badES, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, errMaxCollateralReached) {
		t.Fatal(err)
	}

	// Collateral budget exceeded.
	badLockedCollateral := is.CollateralBudget.Sub(expectedCollateral).Add64(1)
	err = verifyRenewedContract(so, fc, oldRevision, bh, is, es, rpk, hpk, badLockedCollateral)
	if !errors.Contains(err, errCollateralBudgetExceeded) {
		t.Fatal(err)
	}

	// Low host valid output
	badFC = fc
	badES = es
	badES.ContractPrice = types.ZeroCurrency
	badES.Collateral = types.SiacoinPrecision
	badES.MaxCollateral = types.SiacoinPrecision.Mul64(math.MaxUint64)
	badIS := is
	badIS.CollateralBudget = badES.MaxCollateral
	badBaseCollateral := renewBaseCollateral(so, badES, badFC)
	badFC.ValidProofOutputs = append([]types.SiacoinOutput{}, badFC.ValidProofOutputs...)
	badFC.ValidProofOutputs[1].Value = basePrice.Add(badBaseCollateral).Sub64(1)
	err = verifyRenewedContract(so, badFC, oldRevision, bh, badIS, badES, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrLowHostValidOutput) {
		t.Fatal(err)
	}

	// Low host missed output
	badFC = fc
	badBasePrice := renewBasePrice(so, badES, badFC)
	badBaseCollateral = renewBaseCollateral(so, badES, badFC)
	badFC.ValidProofOutputs = append([]types.SiacoinOutput{}, badFC.ValidProofOutputs...)
	badFC.MissedProofOutputs = append([]types.SiacoinOutput{}, badFC.MissedProofOutputs...)
	badFC.ValidProofOutputs[1].Value = badBasePrice.Add(badBaseCollateral).Add64(1)
	badFC.MissedProofOutputs[1].Value = badFC.ValidProofOutputs[1].Value.Sub(badBaseCollateral).Sub(badBasePrice).Sub64(1)
	err = verifyRenewedContract(so, badFC, oldRevision, bh, badIS, badES, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrLowHostMissedOutput) {
		t.Fatal(err)
	}

	// Bad unlock hash.
	badFC = fc
	badFC.UnlockHash = types.UnlockHash{}
	err = verifyRenewedContract(so, badFC, oldRevision, bh, is, es, rpk, hpk, lockedCollateral)
	if !errors.Contains(err, ErrBadUnlockHash) {
		t.Fatal(err)
	}
}
