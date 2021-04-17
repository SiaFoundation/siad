package host

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestVerifyNewContract is a unit test for managedVerifyNewContract. It tests
// every return path of the verification method.
func TestVerifyNewContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a host tester to steal the tpool from.
	ht, err := blankMockHostTester(modules.ProdDependencies, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := ht.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Create a minimal host, contract and renter.
	_, renterPK := crypto.GenerateKeyPair()
	_, hostPK := crypto.GenerateKeyPair()

	h := &Host{
		log:       ht.host.log,
		publicKey: types.Ed25519PublicKey(hostPK),
		settings: modules.HostInternalSettings{
			CollateralBudget: types.SiacoinPrecision,
		},
		staticAlerter: modules.NewAlerter("test"),
		tpool:         ht.tpool,
	}
	curr := []types.Transaction{
		{
			MinerFees: []types.Currency{types.SiacoinPrecision},
			FileContracts: []types.FileContract{
				{
					Payout: types.NewCurrency64(10),
					ValidProofOutputs: []types.SiacoinOutput{
						{Value: types.NewCurrency64(4)},
						{Value: types.NewCurrency64(6)},
					},
					MissedProofOutputs: []types.SiacoinOutput{
						{Value: types.NewCurrency64(4)},
						{Value: types.NewCurrency64(6)},
						{Value: types.ZeroCurrency},
					},
					UnlockHash: types.UnlockConditions{
						PublicKeys: []types.SiaPublicKey{
							types.Ed25519PublicKey(renterPK),
							h.publicKey,
						},
						SignaturesRequired: 2,
					}.UnlockHash(),
					WindowEnd:   100,
					WindowStart: types.BlockHeight(revisionSubmissionBuffer) + 1,
				},
			},
		},
	}
	newSettings := func() modules.HostExternalSettings {
		return modules.HostExternalSettings{
			Collateral:    types.SiacoinPrecision,
			ContractPrice: types.NewCurrency64(1),
			MaxCollateral: types.SiacoinPrecision,
			MaxDuration:   1000,
			WindowSize:    10,
		}
	}

	// verify a properly created payment revision is accepted
	settings := newSettings()
	err = h.managedVerifyNewContract(curr, renterPK, settings)
	if err != nil {
		t.Fatal(err)
	}

	// deepCopy is a helper function that makes a deep copy of a revision
	deepCopy := func(txnSet []types.Transaction) (txnSetCopy []types.Transaction) {
		rBytes := encoding.Marshal(txnSet)
		err := encoding.Unmarshal(rBytes, &txnSetCopy)
		if err != nil {
			panic(err)
		}
		return
	}

	// Empty set.
	badSet := []types.Transaction{}
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if err == nil || !strings.Contains(err.Error(), "zero-length transaction set") {
		t.Fatal("should fail", err)
	}

	// No contracts.
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts = nil
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if err == nil || !strings.Contains(err.Error(), "transaction without file contract") {
		t.Fatal("should fail", err)
	}

	// filesize > 0
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].FileSize = 1
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrBadFileSize) {
		t.Fatal("should fail", err)
	}

	// merkle root not blank
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].FileMerkleRoot = crypto.Hash{1}
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrBadFileMerkleRoot) {
		t.Fatal("should fail", err)
	}

	// early window
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].WindowStart = revisionSubmissionBuffer
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrEarlyWindow) {
		t.Fatal("should fail", err)
	}

	// small window
	badSet = deepCopy(curr)
	fc := &badSet[len(badSet)-1].FileContracts[0]
	fc.WindowEnd = fc.WindowStart + settings.WindowSize - 1
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrSmallWindow) {
		t.Fatal("should fail", err)
	}

	// long duration
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].WindowStart = settings.MaxDuration
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrSmallWindow) {
		t.Fatal("should fail", err)
	}

	// bad output count #1
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].ValidProofOutputs = nil
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatal("should fail", err)
	}

	// bad output count #2
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].MissedProofOutputs = nil
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatal("should fail", err)
	}

	// bad unlock hashes - validhost
	badSet = deepCopy(curr)
	fc = &badSet[len(badSet)-1].FileContracts[0]
	badSet[len(badSet)-1].FileContracts[0].ValidProofOutputs[1].UnlockHash = types.UnlockHash{1}
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal("should fail", err)
	}

	// bad unlock hashes - missedhost
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].MissedProofOutputs[1].UnlockHash = types.UnlockHash{1}
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal("should fail", err)
	}

	// bad unlock hashes - void
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].MissedProofOutputs[2].UnlockHash = types.UnlockHash{1}
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrBadPayoutUnlockHashes) {
		t.Fatal("should fail", err)
	}

	// valid / missed host outputs are not the same.
	badSet = deepCopy(curr)
	fc = &badSet[len(badSet)-1].FileContracts[0]
	fc.SetMissedHostPayout(fc.MissedHostOutput().Value.Add64(1))
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrMismatchedHostPayouts) {
		t.Fatal("should fail", err)
	}

	// low host payout
	badSet = deepCopy(curr)
	fc = &badSet[len(badSet)-1].FileContracts[0]
	fc.ValidProofOutputs[0].Value = settings.ContractPrice.Sub64(1)
	fc.MissedProofOutputs[0].Value = settings.ContractPrice.Sub64(1)
	fc.ValidProofOutputs[1].Value = settings.ContractPrice.Sub64(1)
	fc.MissedProofOutputs[1].Value = settings.ContractPrice.Sub64(1)
	fc.Payout = settings.ContractPrice.Sub64(1)
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrLowHostValidOutput) {
		t.Fatal("should fail", err)
	}

	// maximum collateral exceeded
	badSettings := newSettings()
	badSettings.MaxCollateral = types.ZeroCurrency
	fc = &badSet[len(badSet)-1].FileContracts[0]
	fc.SetMissedHostPayout(fc.MissedHostOutput().Value.Add64(1))
	err = h.managedVerifyNewContract(curr, renterPK, badSettings)
	if !errors.Contains(err, errMaxCollateralReached) {
		t.Fatal("should fail", err)
	}

	// budget exceeded
	backup := h.settings
	h.settings.CollateralBudget = types.ZeroCurrency
	err = h.managedVerifyNewContract(curr, renterPK, settings)
	h.settings = backup
	if !errors.Contains(err, errCollateralBudgetExceeded) {
		t.Fatal("should fail", err)
	}

	// payout mismatch - valid
	badSet = deepCopy(curr)
	fc = &badSet[len(badSet)-1].FileContracts[0]
	fc.SetValidRenterPayout(fc.ValidRenterOutput().Value.Sub64(1))
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrInvalidPayoutSums) {
		t.Fatal("should fail", err)
	}

	// payout mismatch - missed
	badSet = deepCopy(curr)
	fc = &badSet[len(badSet)-1].FileContracts[0]
	fc.SetMissedRenterPayout(fc.MissedRenterOutput().Value.Sub64(1))
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrInvalidPayoutSums) {
		t.Fatal("should fail", err)
	}

	// payout mismatch - total
	badSet = deepCopy(curr)
	fc = &badSet[len(badSet)-1].FileContracts[0]
	fc.SetValidRenterPayout(fc.ValidRenterOutput().Value.Sub64(1))
	fc.SetMissedRenterPayout(fc.MissedRenterOutput().Value.Sub64(1))
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrInvalidPayoutSums) {
		t.Fatal("should fail", err)
	}

	// bad unlock hashes - contract
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].FileContracts[0].UnlockHash = types.UnlockHash{1}
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrBadUnlockHash) {
		t.Fatal("should fail", err)
	}

	// low fee
	badSet = deepCopy(curr)
	badSet[len(badSet)-1].MinerFees = nil
	err = h.managedVerifyNewContract(badSet, renterPK, settings)
	if !errors.Contains(err, ErrLowTransactionFees) {
		t.Fatal("should fail", err)
	}
}
