package proto

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/ratelimit"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// managedMustAcquire is a convenience function for acquiring contracts that are
// known to be in the set.
func (cs *ContractSet) managedMustAcquire(t *testing.T, id types.FileContractID) *SafeContract {
	t.Helper()
	c, ok := cs.Acquire(id)
	if !ok {
		t.Fatal("no contract with that id")
	}
	return c
}

// TestContractSet tests that the ContractSet type is safe for concurrent use.
func TestContractSet(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create contract set
	testDir := build.TempDir(t.Name())
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(testDir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	header1 := contractHeader{Transaction: types.Transaction{
		FileContractRevisions: []types.FileContractRevision{{
			ParentID:             types.FileContractID{1},
			NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
			UnlockConditions: types.UnlockConditions{
				PublicKeys: []types.SiaPublicKey{{}, {}},
			},
		}},
	}}
	header2 := contractHeader{Transaction: types.Transaction{
		FileContractRevisions: []types.FileContractRevision{{
			ParentID:             types.FileContractID{2},
			NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
			UnlockConditions: types.UnlockConditions{
				PublicKeys: []types.SiaPublicKey{{}, {}},
			},
		}},
	}}
	id1 := header1.ID()
	id2 := header2.ID()

	_, err = cs.managedInsertContract(header1, []crypto.Hash{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cs.managedInsertContract(header2, []crypto.Hash{})
	if err != nil {
		t.Fatal(err)
	}

	// uncontested acquire/release
	c1 := cs.managedMustAcquire(t, id1)
	cs.Return(c1)

	// 100 concurrent serialized mutations
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c1 := cs.managedMustAcquire(t, id1)
			c1.header.Transaction.FileContractRevisions[0].NewRevisionNumber++
			time.Sleep(time.Duration(fastrand.Intn(100)))
			cs.Return(c1)
		}()
	}
	wg.Wait()
	c1 = cs.managedMustAcquire(t, id1)
	cs.Return(c1)
	if c1.header.LastRevision().NewRevisionNumber != 100 {
		t.Fatal("expected exactly 100 increments, got", c1.header.LastRevision().NewRevisionNumber)
	}

	// a blocked acquire shouldn't prevent a return
	c1 = cs.managedMustAcquire(t, id1)
	go func() {
		time.Sleep(time.Millisecond)
		cs.Return(c1)
	}()
	c1 = cs.managedMustAcquire(t, id1)
	cs.Return(c1)

	// delete and reinsert id2
	c2 := cs.managedMustAcquire(t, id2)
	cs.Delete(c2)
	roots, err := c2.merkleRoots.merkleRoots()
	if err != nil {
		t.Fatal(err)
	}
	cs.managedInsertContract(c2.header, roots)

	// call all the methods in parallel haphazardly
	funcs := []func(){
		func() { cs.Len() },
		func() { cs.IDs() },
		func() { cs.View(id1); cs.View(id2) },
		func() { cs.ViewAll() },
		func() { cs.Return(cs.managedMustAcquire(t, id1)) },
		func() { cs.Return(cs.managedMustAcquire(t, id2)) },
		func() {
			header3 := contractHeader{
				Transaction: types.Transaction{
					FileContractRevisions: []types.FileContractRevision{{
						ParentID:             types.FileContractID{3},
						NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
						UnlockConditions: types.UnlockConditions{
							PublicKeys: []types.SiaPublicKey{{}, {}},
						},
					}},
				},
			}
			id3 := header3.ID()
			_, err := cs.managedInsertContract(header3, []crypto.Hash{})
			if err != nil {
				t.Fatal(err)
			}
			c3 := cs.managedMustAcquire(t, id3)
			cs.Delete(c3)
		},
	}
	wg = sync.WaitGroup{}
	for _, fn := range funcs {
		wg.Add(1)
		go func(fn func()) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				time.Sleep(time.Duration(fastrand.Intn(100)))
				fn()
			}
		}(fn)
	}
	wg.Wait()
}

// TestCompatV146SplitContracts tests the compat code for converting single file
// contracts into split contracts.
func TestCompatV146SplitContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// get the dir of the contractset.
	testDir := build.TempDir(t.Name())
	if err := os.MkdirAll(testDir, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	// manually create a legacy contract.
	var id types.FileContractID
	fastrand.Read(id[:])
	contractHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber:    1,
				NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
				ParentID:             id,
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoot := crypto.Hash{1}
	// Place the legacy contract in the dir.
	pathNoExt := filepath.Join(testDir, id.String())
	legacyPath := pathNoExt + v146ContractExtension
	file, err := os.Create(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	headerBytes := encoding.Marshal(contractHeader)
	rootsBytes := initialRoot[:]
	_, err1 := file.WriteAt(headerBytes, 0)
	_, err2 := file.WriteAt(rootsBytes, 4088)
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// load contract set
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(testDir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// The legacy file should be gone.
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatal("legacy contract still exists")
	}
	// The new files should exist.
	if _, err := os.Stat(pathNoExt + contractHeaderExtension); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pathNoExt + contractRootsExtension); err != nil {
		t.Fatal(err)
	}
	// Acquire the contract.
	sc, ok := cs.Acquire(id)
	if !ok {
		t.Fatal("failed to acquire contract")
	}
	// Make sure the header and roots match.
	if !bytes.Equal(encoding.Marshal(sc.header), headerBytes) {
		t.Fatal("header doesn't match")
	}
	roots, err := sc.merkleRoots.merkleRoots()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roots, []crypto.Hash{initialRoot}) {
		t.Fatal("roots don't match")
	}
}

// TestContractSetApplyInsertUpdateAtStartup makes sure that a valid insert
// update gets applied at startup and an invalid one won't.
func TestContractSetApplyInsertUpdateAtStartup(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Prepare a header for the test.
	header := contractHeader{Transaction: types.Transaction{
		FileContractRevisions: []types.FileContractRevision{{
			ParentID:             types.FileContractID{1},
			NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
			UnlockConditions: types.UnlockConditions{
				PublicKeys: []types.SiaPublicKey{{}, {}},
			},
		}},
	}}
	initialRoots := []crypto.Hash{{}, {}, {}}
	// Prepare a valid and one invalid update.
	validUpdate, err := makeUpdateInsertContract(header, initialRoots)
	if err != nil {
		t.Fatal(err)
	}
	invalidUpdate, err := makeUpdateInsertContract(header, initialRoots)
	if err != nil {
		t.Fatal(err)
	}
	invalidUpdate.Name = "invalidname"
	// create contract set and close it.
	testDir := build.TempDir(t.Name())
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(testDir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// Prepare the insertion of the invalid contract.
	txn, err := cs.staticWal.NewTransaction([]writeaheadlog.Update{invalidUpdate})
	if err != nil {
		t.Fatal(err)
	}
	err = <-txn.SignalSetupComplete()
	if err != nil {
		t.Fatal(err)
	}
	// Close the contract set.
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	// Load the set again. This should ignore the invalid update and succeed.
	cs, err = NewContractSet(testDir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// Make sure we can't acquire the contract.
	_, ok := cs.Acquire(header.ID())
	if ok {
		t.Fatal("shouldn't be able to acquire the contract")
	}
	// Prepare the insertion of 2 valid contracts within a single txn. This
	// should be ignored at startup.
	txn, err = cs.staticWal.NewTransaction([]writeaheadlog.Update{validUpdate, validUpdate})
	if err != nil {
		t.Fatal(err)
	}
	err = <-txn.SignalSetupComplete()
	if err != nil {
		t.Fatal(err)
	}
	// Close the contract set.
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	// Load the set again. This should apply the invalid update and fail at
	// startup.
	cs, err = NewContractSet(testDir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// Make sure we can't acquire the contract.
	_, ok = cs.Acquire(header.ID())
	if ok {
		t.Fatal("shouldn't be able to acquire the contract")
	}
	// Prepare the insertion of a valid contract by writing the change to the
	// wal but not applying it.
	txn, err = cs.staticWal.NewTransaction([]writeaheadlog.Update{validUpdate})
	if err != nil {
		t.Fatal(err)
	}
	err = <-txn.SignalSetupComplete()
	if err != nil {
		t.Fatal(err)
	}
	// Close the contract set.
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	// Load the set again. This should apply the valid update and not return an
	// error.
	cs, err = NewContractSet(testDir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// Make sure we can acquire the contract.
	_, ok = cs.Acquire(header.ID())
	if !ok {
		t.Fatal("failed to acquire contract after applying valid update")
	}
}

// TestInsertContractTotalCost tests that InsertContrct sets a good estimate for
// TotalCost and TxnFee on recovered contracts.
func TestInsertContractTotalCost(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	renterPayout := types.SiacoinPrecision
	txnFee := types.SiacoinPrecision.Mul64(2)
	fc := types.FileContract{
		ValidProofOutputs: []types.SiacoinOutput{
			{}, {},
		},
		MissedProofOutputs: []types.SiacoinOutput{
			{}, {},
		},
	}
	fc.SetValidRenterPayout(renterPayout)

	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{
			{
				NewValidProofOutputs:  fc.ValidProofOutputs,
				NewMissedProofOutputs: fc.MissedProofOutputs,
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{
						{}, {},
					},
				},
			},
		},
	}

	rc := modules.RecoverableContract{
		FileContract: fc,
		TxnFee:       txnFee,
	}

	// get the dir of the contractset.
	testDir := build.TempDir(t.Name())
	if err := os.MkdirAll(testDir, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(testDir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	// Insert the contract and check its total cost and fee.
	contract, err := cs.InsertContract(rc, txn, []crypto.Hash{}, crypto.SecretKey{})
	if err != nil {
		t.Fatal(err)
	}
	if !contract.TxnFee.Equals(txnFee) {
		t.Fatal("wrong fee", contract.TxnFee, txnFee)
	}
	expectedTotalCost := renterPayout.Add(txnFee)
	if !contract.TotalCost.Equals(expectedTotalCost) {
		t.Fatal("wrong TotalCost", contract.TotalCost, expectedTotalCost)
	}
}
