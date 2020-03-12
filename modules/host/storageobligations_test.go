package host

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
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
	defer ht.Close()

	// Add a storage obligation to the host
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}

	// Add a file contract revision
	sectorRoot, sectorData := randSector()
	so.SectorRoots = []crypto.Hash{sectorRoot}
	validPayouts, missedPayouts := so.payouts()
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{{
			ParentID:          so.id(),
			UnlockConditions:  types.UnlockConditions{},
			NewRevisionNumber: 1,

			NewFileSize:           uint64(len(sectorData)),
			NewFileMerkleRoot:     sectorRoot,
			NewWindowStart:        so.expiration(),
			NewWindowEnd:          so.proofDeadline(),
			NewValidProofOutputs:  validPayouts,
			NewMissedProofOutputs: missedPayouts,
			NewUnlockHash:         types.UnlockConditions{}.UnlockHash(),
		}},
	}}

	// Insert the SO
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedAddStorageObligation(so, false)

	// Get a snapshot & verify its fields
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
	if len(snapshot.SectorRoots()) != 1 {
		t.Fatal("Unexpected number of sector roots")
	}
	if snapshot.SectorRoots()[0] != sectorRoot {
		t.Fatalf("Unexpected sector root, expected %v but received %v", sectorRoot, snapshot.SectorRoots()[0])
	}

	// Update the SO
	sectorRoot2, sectorData := randSector()
	ht.host.managedLockStorageObligation(so.id())
	err = so.Update([]crypto.Hash{sectorRoot, sectorRoot2}, nil, map[crypto.Hash][]byte{sectorRoot2: sectorData})
	if err != nil {
		t.Fatal(err)
	}

	// Note that we purposefully do not unlock the SO before retrieving a
	// snapshot here.
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
	defer ht.Close()

	// add a storage obligation for testing.
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}

	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedAddStorageObligation(so, false)
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
