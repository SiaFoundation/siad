package host

import (
	"reflect"
	"testing"

	"fmt"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestStorageObligationID checks that the return function of the storage
// obligation returns the correct value for the obligaiton id.
func TestStorageObligationID(t *testing.T) {
	t.Parallel()
	// Try a transaction set with just a file contract.
	so1 := &storageObligation{
		OriginTransactionSet: []types.Transaction{{
			FileContracts: []types.FileContract{{
				ValidProofOutputs: []types.SiacoinOutput{
					{
						UnlockHash: types.UnlockHash{2, 1, 3},
						Value:      types.NewCurrency64(35),
					},
					{
						UnlockHash: types.UnlockHash{0, 1, 3},
						Value:      types.NewCurrency64(25),
					},
				},
				MissedProofOutputs: []types.SiacoinOutput{
					{
						UnlockHash: types.UnlockHash{110, 1, 3},
						Value:      types.NewCurrency64(3325),
					},
					{
						UnlockHash: types.UnlockHash{110, 1, 3},
						Value:      types.NewCurrency64(8325),
					},
				},
			}},
		}},
	}
	if so1.id() != so1.OriginTransactionSet[0].FileContractID(0) {
		t.Error("id function of storage obligation is not correct")
	}

	// Try a file contract that includes file contract dependencies.
	so2 := &storageObligation{
		OriginTransactionSet: []types.Transaction{
			{
				SiacoinOutputs: []types.SiacoinOutput{{
					UnlockHash: types.UnlockHash{1, 3, 2},
					Value:      types.NewCurrency64(5),
				}},
			},
			{
				FileContracts: []types.FileContract{{
					ValidProofOutputs: []types.SiacoinOutput{
						{
							UnlockHash: types.UnlockHash{8, 11, 4},
							Value:      types.NewCurrency64(85),
						},
						{
							UnlockHash: types.UnlockHash{8, 11, 14},
							Value:      types.NewCurrency64(859),
						},
					},
					MissedProofOutputs: []types.SiacoinOutput{
						{
							UnlockHash: types.UnlockHash{8, 113, 4},
							Value:      types.NewCurrency64(853),
						},
						{
							UnlockHash: types.UnlockHash{8, 119, 14},
							Value:      types.NewCurrency64(9859),
						},
					},
				}},
			},
		},
	}
	if so2.id() != so2.OriginTransactionSet[1].FileContractID(0) {
		t.Error("id function of storage obligation incorrect for file contracts with dependencies")
	}
}

// TestStorageObligationSnapshot verifies the functionality of the snapshot
// function.
func TestStorageObligationSnapshot(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ht.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a storage obligation & add a revision
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}
	sectorRoot, sectorData := randSector()
	so.SectorRoots = []crypto.Hash{sectorRoot}
	proofDeadline := so.proofDeadline()
	validPayouts, missedPayouts := so.payouts()
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{{
			ParentID:          so.id(),
			UnlockConditions:  types.UnlockConditions{},
			NewRevisionNumber: 1,

			NewFileSize:           uint64(len(sectorData)),
			NewFileMerkleRoot:     sectorRoot,
			NewWindowStart:        so.expiration(),
			NewWindowEnd:          proofDeadline,
			NewValidProofOutputs:  validPayouts,
			NewMissedProofOutputs: missedPayouts,
			NewUnlockHash:         types.UnlockConditions{}.UnlockHash(),
		}},
	}}
	// Set a random missed host payout.
	fcr := so.RevisionTransactionSet[0].FileContractRevisions[0]
	fcr.SetMissedHostPayout(types.NewCurrency64(fastrand.Uint64n(100)))

	// Insert the SO
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedAddStorageObligation(so)
	ht.host.managedUnlockStorageObligation(so.id())

	// Fetch a snapshot & verify its fields
	snapshot, err := ht.host.managedGetStorageObligationSnapshot(so.id())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ContractSize() != uint64(len(sectorData)) {
		t.Fatalf("Unexpected contract size, expected %v but received %v", uint64(len(sectorData)), snapshot.ContractSize())
	}
	if snapshot.MerkleRoot() != sectorRoot {
		t.Fatalf("Unexpected merkle root, expected %v but received %v", sectorRoot, snapshot.MerkleRoot())
	}
	if uint64(snapshot.ProofDeadline()) != uint64(proofDeadline) {
		t.Fatalf("Unexpected proof deadline, expected %v but received %v", proofDeadline, snapshot.ProofDeadline())
	}
	if len(snapshot.SectorRoots()) != 1 {
		t.Fatal("Unexpected number of sector roots")
	}
	if snapshot.SectorRoots()[0] != sectorRoot {
		t.Fatalf("Unexpected sector root, expected %v but received %v", sectorRoot, snapshot.SectorRoots()[0])
	}
	if !snapshot.UnallocatedCollateral().Equals(fcr.MissedHostPayout()) {
		t.Fatalf("Unexpected unallocated collateral, expected %v but was %v", fcr.MissedHostPayout().HumanString(), snapshot.UnallocatedCollateral().HumanString())
	}
	if !reflect.DeepEqual(snapshot.RecentRevision(), fcr) {
		t.Fatal("Revisions don't match")
	}
	// Update the SO with new data
	sectorRoot2, sectorData := randSector()
	ht.host.managedLockStorageObligation(so.id())
	err = so.Update([]crypto.Hash{sectorRoot, sectorRoot2}, nil, map[crypto.Hash][]byte{sectorRoot2: sectorData})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the SO has been updated with the new sector root. Note that we
	// purposefully have not yet unlocked the SO here. Clarifying the snapshot
	// is retrieved from the database.
	snapshot, err = ht.host.managedGetStorageObligationSnapshot(so.id())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.SectorRoots()) != 2 {
		t.Fatal("Unexpected number of sector roots")
	}

	// Verify we can not update the SO if it is not locked
	ht.host.managedUnlockStorageObligation(so.id())
	sectorRoot3, sectorData := randSector()
	err = so.Update([]crypto.Hash{sectorRoot, sectorRoot2, sectorRoot3}, nil, map[crypto.Hash][]byte{sectorRoot3: sectorData})
	if err == nil {
		t.Fatal("Expected Update to fail on unlocked SO")
	}
}

// TestAccountFundingTracking verifies the AccountFunding field is properly
// updated when the SOs lifecycle methods get called on the host.
func TestAccountFundingTracking(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ht.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// expectDelta is a helper that asserts the deltas, with regards to the
	// account funding fields, in the host's financial metrics before and after
	// executing the given function f.
	expectDelta := func(pafDelta, afDelta int, action string, f func() error) error {
		bkp := ht.host.FinancialMetrics()
		if err := f(); err != nil {
			return err
		}

		fm := ht.host.FinancialMetrics()
		af := fm.AccountFunding
		paf := fm.PotentialAccountFunding

		// verify potential account funding delta
		if pafDelta >= 0 {
			delta := paf.Sub(bkp.PotentialAccountFunding)
			if !delta.Equals64(uint64(pafDelta)) {
				return fmt.Errorf("Unexpected potential account funding delta after %s, expected '%vH' actual '%vH'", action, pafDelta, delta)
			}
		} else {
			delta := bkp.PotentialAccountFunding.Sub(paf)
			if !delta.Equals64(uint64(pafDelta * -1)) {
				return fmt.Errorf("Unexpected potential account funding delta after %s, expected '%vH' actual '-%vH'", action, pafDelta, delta)
			}
		}

		// verify account funding delta
		if afDelta >= 0 {
			delta := af.Sub(bkp.AccountFunding)
			if !delta.Equals64(uint64(afDelta)) {
				return fmt.Errorf("Unexpected account funding delta after %s, expected '%vH' actual '%vH'", action, afDelta, delta)
			}
		} else {
			delta := bkp.AccountFunding.Sub(af)
			if !delta.Equals64(uint64(afDelta * -1)) {
				return fmt.Errorf("Unexpected account funding delta after %s, expected '%vH' actual '-%vH'", action, afDelta, delta)
			}
		}

		return nil
	}

	// assert account funding is 0 on new host
	af := ht.host.FinancialMetrics().AccountFunding
	if !af.IsZero() {
		t.Fatalf("Expected account funding to be zero but was '%v'", af.HumanString())
	}

	// create a storage obligation
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedLockStorageObligation(so.id())
	defer ht.host.managedUnlockStorageObligation(so.id())

	// add the storage obligation (expect PAF to increase - AF remain same)
	rd1 := fastrand.Intn(10) + 1
	so.PotentialAccountFunding = so.PotentialAccountFunding.Add64(uint64(rd1))
	if err = expectDelta(rd1, 0, "add SO", func() error {
		return ht.host.managedAddStorageObligation(so)
	}); err != nil {
		t.Fatal(err)
	}

	// modify the storage obligation (expect PAF to increase - AF remain same)
	rd2 := fastrand.Intn(10) + 1
	so.PotentialAccountFunding = so.PotentialAccountFunding.Add64(uint64(rd2))
	if err = expectDelta(rd2, 0, "modify SO", func() error {
		return ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, make(map[crypto.Hash][]byte, 0))
	}); err != nil {
		t.Fatal(err)
	}

	// delete the storage obligation (expect PAF to decrease - AF increase)
	total := rd1 + rd2
	if err = expectDelta(-1*total, total, "delete SO", func() error {
		return ht.host.removeStorageObligation(so, obligationSucceeded)
	}); err != nil {
		t.Fatal(err)
	}

	// reset the host's financial metrics (expect PAF and AF to remain the same)
	if err = expectDelta(0, 0, "reset FM", func() error {
		return ht.host.resetFinancialMetrics()
	}); err != nil {
		t.Fatal(err)
	}

	// prune stale obligations - note that we will fake the SO being deleted
	// from the database instead of mocking the conditions for it to be pruned.
	// This to avoid having to manually delete the transaction after it have
	// being confirmed (expect PAF to remain the same, but AF to decrease)
	if err = expectDelta(0, -1*total, "prune stale SOs", func() error {
		return errors.Compose(ht.host.deleteStorageObligations([]types.FileContractID{so.id()}), ht.host.PruneStaleStorageObligations())
	}); err != nil {
		t.Fatal(err)
	}
}

// TestManagedModifyUnlockedStorageObligation checks that the storage obligation
// cannot be modified when unlocked.
func TestManagedModifyUnlockedStorageObligation(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ht.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// add a storage obligation for testing.
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}

	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedAddStorageObligation(so)
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(so.id())

	// Modify the obligation. This should fail.
	if err := ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, nil); err == nil {
		t.Fatal("shouldn't be able to modify unlocked so")
	}

	// Lock obligation.
	ht.host.managedLockStorageObligation(so.id())

	// Modify the obligation. This should work.
	if err := ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, nil); err != nil {
		t.Fatal(err)
	}

	// Unlock obligation.
	ht.host.managedUnlockStorageObligation(so.id())

	// Modify the obligation. This should fail again.
	if err := ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, nil); err == nil {
		t.Fatal("shouldn't be able to modify unlocked so")
	}
}

// TestManagedBuildStorageProof is a unit test for the host's
// managedBuildStorageProof method.
func TestManagedBuildStorageProof(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := ht.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Create a storage obligation without data.
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}
	proofDeadline := so.proofDeadline()
	validPayouts, missedPayouts := so.payouts()
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{{
			ParentID:          so.id(),
			UnlockConditions:  types.UnlockConditions{},
			NewRevisionNumber: 1,

			NewFileSize:           0,
			NewFileMerkleRoot:     crypto.Hash{},
			NewWindowStart:        so.expiration(),
			NewWindowEnd:          proofDeadline,
			NewValidProofOutputs:  validPayouts,
			NewMissedProofOutputs: missedPayouts,
			NewUnlockHash:         types.UnlockConditions{}.UnlockHash(),
		}},
	}}

	// Insert the SO
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedAddStorageObligation(so)
	ht.host.managedUnlockStorageObligation(so.id())

	// Build a proof for the SO.
	sp, err := ht.host.managedBuildStorageProof(so, 0)
	if err != nil {
		t.Fatal("failed to build proof", err)
	}

	// Check the proof.
	if len(sp.HashSet) != 0 {
		t.Fatal("sp should have empty hashset")
	}
	var blank [crypto.SegmentSize]byte
	if sp.Segment != blank {
		t.Fatal("sp should have no segment")
	}
	if sp.ParentID != so.id() {
		t.Fatal("parentID wasn't set correctly")
	}

	// Update the so to have a sector.
	sectorRoot, sectorData := randSector()
	so.SectorRoots = []crypto.Hash{sectorRoot}

	sectorsGained := map[crypto.Hash][]byte{
		sectorRoot: sectorData,
	}
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedModifyStorageObligation(so, nil, sectorsGained)
	ht.host.managedUnlockStorageObligation(so.id())
	if err != nil {
		t.Fatal(err)
	}

	// Build another proof.
	segmentIndex := fastrand.Uint64n(modules.SectorSize / crypto.SegmentSize)
	sp, err = ht.host.managedBuildStorageProof(so, segmentIndex)
	if err != nil {
		t.Fatal("failed to build proof", err)
	}

	// Verify the proof.
	verified := crypto.VerifySegment(
		sp.Segment[:crypto.SegmentSize],
		sp.HashSet,
		crypto.CalculateLeaves(uint64(len(sectorData))),
		segmentIndex,
		sectorRoot,
	)
	if !verified {
		t.Fatal("failed to verify proof")
	}
}

// TestStorageObligationRequiresProof tests the requiresProof method of the
// storageObligation type.
func TestStorageObligationRequiresProof(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := ht.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Create a storage obligation without data.
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}
	proofDeadline := so.proofDeadline()
	validPayouts, missedPayouts := so.payouts()
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{{
			ParentID:          so.id(),
			UnlockConditions:  types.UnlockConditions{},
			NewRevisionNumber: 1,

			NewFileSize:           0,
			NewFileMerkleRoot:     crypto.Hash{},
			NewWindowStart:        so.expiration(),
			NewWindowEnd:          proofDeadline,
			NewValidProofOutputs:  validPayouts,
			NewMissedProofOutputs: missedPayouts,
			NewUnlockHash:         types.UnlockConditions{}.UnlockHash(),
		}},
	}}

	// Obligation should require a proof even though it has never been revised.
	if !so.requiresProof() {
		t.Fatal("obligation should require proof")
	}

	// Increment the revision number. Obligation should now require a proof
	so.RevisionTransactionSet[0].FileContractRevisions[0].NewRevisionNumber++
	if !so.requiresProof() {
		t.Fatal("obligation should require a proof")
	}

	//  Make the outputs match. It should no longer require a proof.
	rev := so.RevisionTransactionSet[0].FileContractRevisions[0]
	so.RevisionTransactionSet[0].FileContractRevisions[0].NewValidProofOutputs = rev.NewMissedProofOutputs
	if so.requiresProof() {
		t.Fatal("obligation shouldn't require proof")
	}
}
