package proto

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/ratelimit"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// dependencyInterruptContractInsertion will interrupt inserting a contract
// after writing the header but before writing the roots.
type dependencyInterruptContractInsertion struct {
	modules.ProductionDependencies
}

// Disrupt returns true if the correct string is provided.
func (d *dependencyInterruptContractInsertion) Disrupt(s string) bool {
	return s == "InterruptContractInsertion"
}

// TestContractUncommittedTxn tests that if a contract revision is left in an
// uncommitted state, either version of the contract can be recovered.
func TestContractUncommittedTxn(t *testing.T) {
	// initial header every subtests starts out with.
	initialHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber:    1,
				NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}

	// Test RecordAppendIntent.
	t.Run("RecordAppendIntent", func(t *testing.T) {
		updateFunc := func(sc *SafeContract) (*unappliedWalTxn, []crypto.Hash, contractHeader, error) {
			revisedHeader := contractHeader{
				Transaction: types.Transaction{
					FileContractRevisions: []types.FileContractRevision{{
						NewRevisionNumber:    2,
						NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
						UnlockConditions: types.UnlockConditions{
							PublicKeys: []types.SiaPublicKey{{}, {}},
						},
					}},
				},
				StorageSpending: types.NewCurrency64(7),
				UploadSpending:  types.NewCurrency64(17),
			}
			revisedRoots := []crypto.Hash{{1}, {2}}
			fcr := revisedHeader.Transaction.FileContractRevisions[0]
			newRoot := revisedRoots[1]
			storageCost := revisedHeader.StorageSpending.Sub(initialHeader.StorageSpending)
			bandwidthCost := revisedHeader.UploadSpending.Sub(initialHeader.UploadSpending)
			txn, err := sc.managedRecordAppendIntent(fcr, newRoot, storageCost, bandwidthCost)
			return txn, revisedRoots, revisedHeader, err
		}
		testContractUncomittedTxn(t, initialHeader, updateFunc)
	})
	// Test RecordDownloadIntent.
	t.Run("RecordDownloadIntent", func(t *testing.T) {
		updateFunc := func(sc *SafeContract) (*unappliedWalTxn, []crypto.Hash, contractHeader, error) {
			revisedHeader := contractHeader{
				Transaction: types.Transaction{
					FileContractRevisions: []types.FileContractRevision{{
						NewRevisionNumber:    2,
						NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
						UnlockConditions: types.UnlockConditions{
							PublicKeys: []types.SiaPublicKey{{}, {}},
						},
					}},
				},
				StorageSpending:  types.ZeroCurrency,
				DownloadSpending: types.NewCurrency64(17),
			}
			revisedRoots := []crypto.Hash{{1}}
			fcr := revisedHeader.Transaction.FileContractRevisions[0]
			bandwidthCost := revisedHeader.DownloadSpending.Sub(initialHeader.DownloadSpending)
			txn, err := sc.managedRecordDownloadIntent(fcr, bandwidthCost)
			return txn, revisedRoots, revisedHeader, err
		}
		testContractUncomittedTxn(t, initialHeader, updateFunc)
	})
	// Test RecordClearContractIntent.
	t.Run("RecordClearContractIntent", func(t *testing.T) {
		updateFunc := func(sc *SafeContract) (*unappliedWalTxn, []crypto.Hash, contractHeader, error) {
			revisedHeader := contractHeader{
				Transaction: types.Transaction{
					FileContractRevisions: []types.FileContractRevision{{
						NewRevisionNumber:    2,
						NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
						UnlockConditions: types.UnlockConditions{
							PublicKeys: []types.SiaPublicKey{{}, {}},
						},
					}},
				},
				StorageSpending: types.ZeroCurrency,
				UploadSpending:  types.NewCurrency64(17),
			}
			revisedRoots := []crypto.Hash{{1}}
			fcr := revisedHeader.Transaction.FileContractRevisions[0]
			bandwidthCost := revisedHeader.UploadSpending.Sub(initialHeader.UploadSpending)
			txn, err := sc.managedRecordClearContractIntent(fcr, bandwidthCost)
			return txn, revisedRoots, revisedHeader, err
		}
		testContractUncomittedTxn(t, initialHeader, updateFunc)
	})
	// Test RecordPaymentIntent.
	t.Run("RecordPaymentIntent", func(t *testing.T) {
		updateFunc := func(sc *SafeContract) (*unappliedWalTxn, []crypto.Hash, contractHeader, error) {
			revisedHeader := contractHeader{
				Transaction: types.Transaction{
					FileContractRevisions: []types.FileContractRevision{{
						NewRevisionNumber:    2,
						NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
						UnlockConditions: types.UnlockConditions{
							PublicKeys: []types.SiaPublicKey{{}, {}},
						},
					}},
				},
				StorageSpending:     types.ZeroCurrency,
				UploadSpending:      types.ZeroCurrency,
				FundAccountSpending: types.NewCurrency64(42),
			}
			revisedRoots := []crypto.Hash{{1}}
			fcr := revisedHeader.Transaction.FileContractRevisions[0]
			amount := revisedHeader.FundAccountSpending
			txn, err := sc.RecordPaymentIntent(fcr, amount, modules.SpendingDetails{
				FundAccountSpending: revisedHeader.FundAccountSpending,
			})
			return txn, revisedRoots, revisedHeader, err
		}
		testContractUncomittedTxn(t, initialHeader, updateFunc)
	})
}

// testContractUncommittedTxn tests that if a contract revision is left in an
// uncommitted state, either version of the contract can be recovered.
func testContractUncomittedTxn(t *testing.T, initialHeader contractHeader, updateFunc func(*SafeContract) (*unappliedWalTxn, []crypto.Hash, contractHeader, error)) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create contract set with one contract
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	initialRoots := []crypto.Hash{{1}}
	c, err := cs.managedInsertContract(initialHeader, initialRoots)
	if err != nil {
		t.Fatal(err)
	}

	// apply an update to the contract, but don't commit it
	sc := cs.managedMustAcquire(t, c.ID)
	walTxn, revisedRoots, revisedHeader, err := updateFunc(sc)
	if err != nil {
		t.Fatal(err)
	}

	// the state of the contract should match the initial state
	// NOTE: can't use reflect.DeepEqual for the header because it contains
	// types.Currency fields
	merkleRoots, err := sc.merkleRoots.merkleRoots()
	if err != nil {
		t.Fatal("failed to get merkle roots", err)
	}
	if !bytes.Equal(encoding.Marshal(sc.header), encoding.Marshal(initialHeader)) {
		t.Fatal("contractHeader should match initial contractHeader")
	} else if !reflect.DeepEqual(merkleRoots, initialRoots) {
		t.Fatal("Merkle roots should match initial Merkle roots")
	}

	// close and reopen the contract set.
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// the uncommitted transaction should be stored in the contract
	sc = cs.managedMustAcquire(t, c.ID)
	if len(sc.unappliedTxns) != 1 {
		t.Fatal("expected 1 unappliedTxn, got", len(sc.unappliedTxns))
	} else if !bytes.Equal(sc.unappliedTxns[0].Updates[0].Instructions, walTxn.Updates[0].Instructions) {
		t.Fatal("WAL transaction changed")
	}
	// the state of the contract should match the initial state
	merkleRoots, err = sc.merkleRoots.merkleRoots()
	if err != nil {
		t.Fatal("failed to get merkle roots:", err)
	}
	if !bytes.Equal(encoding.Marshal(sc.header), encoding.Marshal(initialHeader)) {
		t.Fatal("contractHeader should match initial contractHeader", sc.header, initialHeader)
	} else if !reflect.DeepEqual(merkleRoots, initialRoots) {
		t.Fatal("Merkle roots should match initial Merkle roots")
	}

	// apply the uncommitted transaction
	err = sc.managedCommitTxns()
	if err != nil {
		t.Fatal(err)
	}
	// the uncommitted transaction should be gone now
	if len(sc.unappliedTxns) != 0 {
		t.Fatal("expected 0 unappliedTxns, got", len(sc.unappliedTxns))
	}
	// the state of the contract should now match the revised state
	merkleRoots, err = sc.merkleRoots.merkleRoots()
	if err != nil {
		t.Fatal("failed to get merkle roots:", err)
	}
	if !bytes.Equal(encoding.Marshal(sc.header), encoding.Marshal(revisedHeader)) {
		t.Fatal("contractHeader should match revised contractHeader", sc.header, revisedHeader)
	} else if !reflect.DeepEqual(merkleRoots, revisedRoots) {
		t.Fatal("Merkle roots should match revised Merkle roots", merkleRoots, revisedRoots)
	}
	// close and reopen the contract set.
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// the uncommitted transaction should be gone.
	sc = cs.managedMustAcquire(t, c.ID)
	if len(sc.unappliedTxns) != 0 {
		t.Fatal("expected 0 unappliedTxn, got", len(sc.unappliedTxns))
	}
}

// TestContractIncompleteWrite tests that if the merkle root section has the wrong
// length due to an incomplete write, it is truncated and the wal transactions
// are applied.
func TestContractIncompleteWrite(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create contract set with one contract
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	initialHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber:    1,
				NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	c, err := cs.managedInsertContract(initialHeader, initialRoots)
	if err != nil {
		t.Fatal(err)
	}

	// apply an update to the contract, but don't commit it
	sc := cs.managedMustAcquire(t, c.ID)
	revisedHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber:    2,
				NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
		StorageSpending: types.NewCurrency64(7),
		UploadSpending:  types.NewCurrency64(17),
	}
	revisedRoots := []crypto.Hash{{1}, {2}}
	fcr := revisedHeader.Transaction.FileContractRevisions[0]
	newRoot := revisedRoots[1]
	storageCost := revisedHeader.StorageSpending.Sub(initialHeader.StorageSpending)
	bandwidthCost := revisedHeader.UploadSpending.Sub(initialHeader.UploadSpending)
	_, err = sc.managedRecordAppendIntent(fcr, newRoot, storageCost, bandwidthCost)
	if err != nil {
		t.Fatal(err)
	}

	// the state of the contract should match the initial state
	// NOTE: can't use reflect.DeepEqual for the header because it contains
	// types.Currency fields
	merkleRoots, err := sc.merkleRoots.merkleRoots()
	if err != nil {
		t.Fatal("failed to get merkle roots", err)
	}
	if !bytes.Equal(encoding.Marshal(sc.header), encoding.Marshal(initialHeader)) {
		t.Fatal("contractHeader should match initial contractHeader")
	} else if !reflect.DeepEqual(merkleRoots, initialRoots) {
		t.Fatal("Merkle roots should match initial Merkle roots")
	}

	// get the size of the merkle roots file.
	size, err := sc.merkleRoots.rootsFile.Size()
	if err != nil {
		t.Fatal(err)
	}
	// the size should be crypto.HashSize since we have exactly one root.
	if size != crypto.HashSize {
		t.Fatal("unexpected merkle root file size", size)
	}
	// truncate the rootsFile to simulate a corruption while writing the second
	// root.
	err = sc.merkleRoots.rootsFile.Truncate(size + crypto.HashSize/2)
	if err != nil {
		t.Fatal(err)
	}

	// close and reopen the contract set.
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// the uncommitted txn should be gone.
	sc = cs.managedMustAcquire(t, c.ID)
	if len(sc.unappliedTxns) != 0 {
		t.Fatal("expected 0 unappliedTxn, got", len(sc.unappliedTxns))
	}
	if sc.merkleRoots.len() != 2 {
		t.Fatal("expected 2 roots, got", sc.merkleRoots.len())
	}
	cs.Return(sc)
	cs.Close()
}

// TestContractLargeHeader tests if adding or modifying a contract with a large
// header works as expected.
func TestContractLargeHeader(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create contract set with one contract
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	largeHeader := contractHeader{
		Transaction: types.Transaction{
			ArbitraryData: [][]byte{fastrand.Bytes(1 << 20 * 5)}, // excessive 5 MiB Transaction
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber:    1,
				NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	// Inserting a contract with a large header should work.
	c, err := cs.managedInsertContract(largeHeader, initialRoots)
	if err != nil {
		t.Fatal(err)
	}

	sc, ok := cs.Acquire(c.ID)
	if !ok {
		t.Fatal("failed to acquire contract")
	}
	// Applying a large header update should also work.
	if err := sc.applySetHeader(largeHeader); err != nil {
		t.Fatal(err)
	}
}

// TestContractSetInsert checks if inserting contracts into the set is ACID.
func TestContractSetInsertInterrupted(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create contract set with a custom dependency.
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, &dependencyInterruptContractInsertion{})
	if err != nil {
		t.Fatal(err)
	}
	contractHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber:    1,
				NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	// Inserting the contract should fail due to the dependency.
	c, err := cs.managedInsertContract(contractHeader, initialRoots)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatal("insertion should have been interrupted")
	}

	// Reload the contract set. The contract should be there.
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	sc, ok := cs.Acquire(c.ID)
	if !ok {
		t.Fatal("faild to acquire contract")
	}
	if !bytes.Equal(encoding.Marshal(sc.header), encoding.Marshal(contractHeader)) {
		t.Log(sc.header)
		t.Log(contractHeader)
		t.Error("header doesn't match")
	}
	mr, err := sc.merkleRoots.merkleRoots()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mr, initialRoots) {
		t.Error("roots don't match")
	}
}

// TestContractCommitAndRecordPaymentIntent verifies the functionality of the
// RecordPaymentIntent and CommitPaymentIntent methods on the SafeContract
func TestContractRecordAndCommitPaymentIntent(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	blockHeight := types.BlockHeight(fastrand.Intn(100))

	// create contract set
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	// add a contract
	initialHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber: 1,
				NewValidProofOutputs: []types.SiacoinOutput{
					{Value: types.SiacoinPrecision},
					{Value: types.ZeroCurrency},
				},
				NewMissedProofOutputs: []types.SiacoinOutput{
					{Value: types.SiacoinPrecision},
					{Value: types.ZeroCurrency},
					{Value: types.ZeroCurrency},
				},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	contract, err := cs.managedInsertContract(initialHeader, initialRoots)
	if err != nil {
		t.Fatal(err)
	}
	sc := cs.managedMustAcquire(t, contract.ID)

	// create a helper function that records the intent, creates the transaction
	// containing the given revision and then commits the intent depending on
	// whether the given flag was set to true
	processTxnWithRevision := func(rev types.FileContractRevision, amount types.Currency, details modules.SpendingDetails, commit bool) {
		// record the payment intent
		walTxn, err := sc.RecordPaymentIntent(rev, amount, details)
		if err != nil {
			t.Fatal("Failed to record payment intent")
		}

		// create transaction containing the revision
		signedTxn := rev.ToTransaction()
		sig := sc.Sign(signedTxn.SigHash(0, blockHeight))
		signedTxn.TransactionSignatures[0].Signature = sig[:]

		// only commit the intent if the flag is true
		if !commit {
			return
		}
		err = sc.CommitPaymentIntent(walTxn, signedTxn, amount, details)
		if err != nil {
			t.Fatal("Failed to commit payment intent")
		}
	}

	// create a payment revision for a FundAccount RPC
	curr := sc.LastRevision()
	amount := types.NewCurrency64(10)
	rpcCost := types.NewCurrency64(1)
	rev, err := curr.PaymentRevision(amount.Add(rpcCost))
	if err != nil {
		t.Fatal(err)
	}
	processTxnWithRevision(rev, amount, modules.SpendingDetails{
		FundAccountSpending: amount,
		MaintenanceSpending: modules.MaintenanceSpending{FundAccountCost: rpcCost},
	}, true)

	// create another payment revision, this time for an MDM RPC
	curr = sc.LastRevision()
	amount = types.NewCurrency64(20)
	rpcCost = types.ZeroCurrency
	rev, err = curr.PaymentRevision(amount.Add(rpcCost))
	if err != nil {
		t.Fatal(err)
	}
	processTxnWithRevision(rev, amount, modules.SpendingDetails{}, true)

	// create another payment revision, this time for a PT update RPC
	curr = sc.LastRevision()
	amount = types.NewCurrency64(3)
	rpcCost = types.NewCurrency64(3)
	rev, err = curr.PaymentRevision(amount.Add(rpcCost))
	if err != nil {
		t.Fatal(err)
	}
	processTxnWithRevision(rev, amount, modules.SpendingDetails{
		MaintenanceSpending: modules.MaintenanceSpending{UpdatePriceTableCost: rpcCost},
	}, true)
	expectedRevNumber := rev.NewRevisionNumber

	// create another payment revision, for an account balance sync,
	// but this time we don't commit it
	curr = sc.LastRevision()
	amount = types.NewCurrency64(4)
	rpcCost = types.NewCurrency64(4)
	rev, err = curr.PaymentRevision(amount.Add(rpcCost))
	if err != nil {
		t.Fatal(err)
	}
	processTxnWithRevision(rev, amount, modules.SpendingDetails{
		MaintenanceSpending: modules.MaintenanceSpending{AccountBalanceCost: rpcCost},
	}, false)

	// reload the contract set
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	sc = cs.managedMustAcquire(t, contract.ID)

	if sc.LastRevision().NewRevisionNumber != expectedRevNumber {
		t.Fatal("Unexpected revision number after reloading the contract set")
	}

	// we expect the `FundAccount` spending metric to reflect exactly the amount
	// of money that should have made it into the EA
	expectedFundAccountSpending := types.NewCurrency64(10)
	if !sc.header.FundAccountSpending.Equals(expectedFundAccountSpending) {
		t.Fatal("unexpected", sc.header.FundAccountSpending)
	}

	// we expect the `Maintenance` spending metric to reflect the sum of the rpc
	// cost for the fund account, and the amount spent on updating the price
	// table. This means that the cost of the MDM RPC and the non committed
	// account balance sync should not be included
	expectedMaintenanceSpending := types.NewCurrency64(1).Add(types.NewCurrency64(3))
	if !sc.header.MaintenanceSpending.Sum().Equals(expectedMaintenanceSpending) {
		t.Fatal("unexpected", sc.header.MaintenanceSpending)
	}
}

// TestContractRefCounter checks if refCounter behaves as expected when called
// from Contract
func TestContractRefCounter(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a contract set
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// add a contract
	initialHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber:    1,
				NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	c, err := cs.managedInsertContract(initialHeader, initialRoots)
	if err != nil {
		t.Fatal(err)
	}
	sc := cs.managedMustAcquire(t, c.ID)
	// verify that the refcounter exists and has the correct size
	if sc.staticRC == nil {
		t.Fatal("refCounter was not created with the contract.")
	}
	if sc.staticRC.numSectors != uint64(sc.merkleRoots.numMerkleRoots) {
		t.Fatalf("refCounter has wrong number of sectors. Expected %d, found %d", uint64(sc.merkleRoots.numMerkleRoots), sc.staticRC.numSectors)
	}
	fi, err := os.Stat(sc.staticRC.filepath)
	if err != nil {
		t.Fatal("Failed to read refcounter file from disk:", err)
	}
	rcFileSize := refCounterHeaderSize + int64(sc.merkleRoots.numMerkleRoots)*2
	if fi.Size() != rcFileSize {
		t.Fatalf("refCounter file on disk has wrong size. Expected %d, got %d", rcFileSize, fi.Size())
	}

	// upload a new sector
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{{
			NewRevisionNumber:    2,
			NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
			UnlockConditions: types.UnlockConditions{
				PublicKeys: []types.SiaPublicKey{{}, {}},
			},
		}},
	}
	revisedHeader := contractHeader{
		Transaction:     txn,
		StorageSpending: types.NewCurrency64(7),
		UploadSpending:  types.NewCurrency64(17),
	}
	newRev := revisedHeader.Transaction.FileContractRevisions[0]
	newRoot := crypto.Hash{2}
	storageCost := revisedHeader.StorageSpending.Sub(initialHeader.StorageSpending)
	bandwidthCost := revisedHeader.UploadSpending.Sub(initialHeader.UploadSpending)
	walTxn, err := sc.managedRecordAppendIntent(newRev, newRoot, storageCost, bandwidthCost)
	if err != nil {
		t.Fatal(err)
	}
	// sign the transaction
	txn.TransactionSignatures = []types.TransactionSignature{
		{
			ParentID:       crypto.Hash(newRev.ParentID),
			CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
			PublicKeyIndex: 0, // renter key is always first -- see formContract
		},
		{
			ParentID:       crypto.Hash(newRev.ParentID),
			PublicKeyIndex: 1,
			CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
			Signature:      nil, // to be provided by host
		},
	}
	// commit the change
	err = sc.managedCommitAppend(walTxn, txn, storageCost, bandwidthCost)
	if err != nil {
		t.Fatal(err)
	}
	// verify that the refcounter increased with 1, as expected
	if sc.staticRC.numSectors != uint64(sc.merkleRoots.numMerkleRoots) {
		t.Fatalf("refCounter has wrong number of sectors. Expected %d, found %d", uint64(sc.merkleRoots.numMerkleRoots), sc.staticRC.numSectors)
	}
	fi, err = os.Stat(sc.staticRC.filepath)
	if err != nil {
		t.Fatal("Failed to read refcounter file from disk:", err)
	}
	rcFileSize = refCounterHeaderSize + int64(sc.merkleRoots.numMerkleRoots)*2
	if fi.Size() != rcFileSize {
		t.Fatalf("refCounter file on disk has wrong size. Expected %d, got %d", rcFileSize, fi.Size())
	}
}

// TestContractRecordCommitDownloadIntent tests recording and committing
// downloads and makes sure they use the wal correctly.
func TestContractRecordCommitDownloadIntent(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	blockHeight := types.BlockHeight(fastrand.Intn(100))

	// create contract set
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	// add a contract
	initialHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber: 1,
				NewValidProofOutputs: []types.SiacoinOutput{
					{Value: types.SiacoinPrecision},
					{Value: types.ZeroCurrency},
				},
				NewMissedProofOutputs: []types.SiacoinOutput{
					{Value: types.SiacoinPrecision},
					{Value: types.ZeroCurrency},
					{Value: types.ZeroCurrency},
				},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	contract, err := cs.managedInsertContract(initialHeader, initialRoots)
	if err != nil {
		t.Fatal(err)
	}
	sc := cs.managedMustAcquire(t, contract.ID)

	// create a download revision
	curr := sc.LastRevision()
	amount := types.NewCurrency64(fastrand.Uint64n(100))
	rev, err := newDownloadRevision(curr, amount)
	if err != nil {
		t.Fatal(err)
	}

	// record the download intent
	walTxn, err := sc.managedRecordDownloadIntent(rev, amount)
	if err != nil {
		t.Fatal("Failed to record payment intent")
	}
	if len(sc.unappliedTxns) != 1 {
		t.Fatalf("expected %v unapplied txns but got %v", 1, len(sc.unappliedTxns))
	}

	// create transaction containing the revision
	signedTxn := rev.ToTransaction()
	sig := sc.Sign(signedTxn.SigHash(0, blockHeight))
	signedTxn.TransactionSignatures[0].Signature = sig[:]

	// don't commit the download. Instead simulate a crash by reloading the
	// contract set.
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	sc = cs.managedMustAcquire(t, contract.ID)

	if sc.LastRevision().NewRevisionNumber != curr.NewRevisionNumber {
		t.Fatal("Unexpected revision number after reloading the contract set")
	}
	if len(sc.unappliedTxns) != 1 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}

	// start a new download
	walTxn, err = sc.managedRecordDownloadIntent(rev, amount)
	if err != nil {
		t.Fatal("Failed to record payment intent")
	}
	if len(sc.unappliedTxns) != 2 {
		t.Fatalf("expected %v unapplied txns but got %v", 2, len(sc.unappliedTxns))
	}

	// commit the download. This should remove all unapplied txns.
	err = sc.managedCommitDownload(walTxn, signedTxn, amount)
	if err != nil {
		t.Fatal("Failed to commit payment intent")
	}
	if len(sc.unappliedTxns) != 0 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}

	// restart again. We still expect 0 unapplied txns.
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	sc = cs.managedMustAcquire(t, contract.ID)

	if sc.LastRevision().NewRevisionNumber != rev.NewRevisionNumber {
		t.Fatal("Unexpected revision number after reloading the contract set")
	}
	if len(sc.unappliedTxns) != 0 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}
}

// TestContractRecordCommitAppendIntent tests recording and committing
// downloads and makes sure they use the wal correctly.
func TestContractRecordCommitAppendIntent(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	blockHeight := types.BlockHeight(fastrand.Intn(100))

	// create contract set
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	// add a contract
	initialHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber: 1,
				NewValidProofOutputs: []types.SiacoinOutput{
					{Value: types.SiacoinPrecision},
					{Value: types.SiacoinPrecision},
				},
				NewMissedProofOutputs: []types.SiacoinOutput{
					{Value: types.SiacoinPrecision},
					{Value: types.SiacoinPrecision},
					{Value: types.ZeroCurrency},
				},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	contract, err := cs.managedInsertContract(initialHeader, initialRoots)
	if err != nil {
		t.Fatal(err)
	}
	sc := cs.managedMustAcquire(t, contract.ID)

	// create a append revision
	curr := sc.LastRevision()
	bandwidth := types.NewCurrency64(fastrand.Uint64n(100))
	collateral := types.NewCurrency64(fastrand.Uint64n(100))
	storage := types.NewCurrency64(fastrand.Uint64n(100))
	newRoot := crypto.Hash{1}
	rev, err := newUploadRevision(curr, newRoot, bandwidth.Add(storage), collateral)
	if err != nil {
		t.Fatal(err)
	}

	// record the append intent
	walTxn, err := sc.managedRecordAppendIntent(rev, newRoot, storage, bandwidth)
	if err != nil {
		t.Fatal("Failed to record payment intent")
	}
	if len(sc.unappliedTxns) != 1 {
		t.Fatalf("expected %v unapplied txns but got %v", 1, len(sc.unappliedTxns))
	}

	// create transaction containing the revision
	signedTxn := rev.ToTransaction()
	sig := sc.Sign(signedTxn.SigHash(0, blockHeight))
	signedTxn.TransactionSignatures[0].Signature = sig[:]

	// don't commit the download. Instead simulate a crash by reloading the
	// contract set.
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	sc = cs.managedMustAcquire(t, contract.ID)

	if sc.LastRevision().NewRevisionNumber != curr.NewRevisionNumber {
		t.Fatal("Unexpected revision number after reloading the contract set")
	}
	if len(sc.unappliedTxns) != 1 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}

	// start a new append
	walTxn, err = sc.managedRecordAppendIntent(rev, newRoot, storage, bandwidth)
	if err != nil {
		t.Fatal("Failed to record payment intent")
	}
	if len(sc.unappliedTxns) != 2 {
		t.Fatalf("expected %v unapplied txns but got %v", 2, len(sc.unappliedTxns))
	}

	// commit the append. This should remove all unapplied txns.
	err = sc.managedCommitAppend(walTxn, signedTxn, storage, bandwidth)
	if err != nil {
		t.Fatal("Failed to commit payment intent")
	}
	if len(sc.unappliedTxns) != 0 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}

	// restart again. We still expect 0 unapplied txns.
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	sc = cs.managedMustAcquire(t, contract.ID)

	if sc.LastRevision().NewRevisionNumber != rev.NewRevisionNumber {
		t.Fatal("Unexpected revision number after reloading the contract set")
	}
	if len(sc.unappliedTxns) != 0 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}
}

// TestContractRecordCommitRenewAndClearIntent tests recording and committing
// downloads and makes sure they use the wal correctly.
func TestContractRecordCommitRenewAndClearIntent(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	blockHeight := types.BlockHeight(fastrand.Intn(100))

	// create contract set
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	// add a contract
	initialHeader := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber: 1,
				NewValidProofOutputs: []types.SiacoinOutput{
					{Value: types.SiacoinPrecision},
					{Value: types.ZeroCurrency},
				},
				NewMissedProofOutputs: []types.SiacoinOutput{
					{Value: types.SiacoinPrecision},
					{Value: types.ZeroCurrency},
					{Value: types.ZeroCurrency},
				},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	contract, err := cs.managedInsertContract(initialHeader, initialRoots)
	if err != nil {
		t.Fatal(err)
	}
	sc := cs.managedMustAcquire(t, contract.ID)

	// create a renew revision. It's the same as a payment revision with small
	// differences.
	bandwidth := types.NewCurrency64(fastrand.Uint64n(100))
	curr := sc.LastRevision()
	rev, err := curr.PaymentRevision(bandwidth)
	if err != nil {
		t.Fatal(err)
	}
	rev.NewFileSize = 0
	rev.NewFileSize = 0
	rev.NewFileMerkleRoot = crypto.Hash{}
	rev.NewRevisionNumber = math.MaxUint64
	rev.NewMissedProofOutputs = rev.NewValidProofOutputs

	// record the clear contract intent
	walTxn, err := sc.managedRecordClearContractIntent(rev, bandwidth)
	if err != nil {
		t.Fatal("Failed to record payment intent")
	}
	if len(sc.unappliedTxns) != 1 {
		t.Fatalf("expected %v unapplied txns but got %v", 1, len(sc.unappliedTxns))
	}

	// create transaction containing the revision
	signedTxn := rev.ToTransaction()
	sig := sc.Sign(signedTxn.SigHash(0, blockHeight))
	signedTxn.TransactionSignatures[0].Signature = sig[:]

	// don't commit the download. Instead simulate a crash by reloading the
	// contract set.
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	sc = cs.managedMustAcquire(t, contract.ID)

	if sc.LastRevision().NewRevisionNumber != curr.NewRevisionNumber {
		t.Fatal("Unexpected revision number after reloading the contract set")
	}
	if len(sc.unappliedTxns) != 1 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}

	// start a new download
	walTxn, err = sc.managedRecordClearContractIntent(rev, bandwidth)
	if err != nil {
		t.Fatal("Failed to record payment intent")
	}
	if len(sc.unappliedTxns) != 2 {
		t.Fatalf("expected %v unapplied txns but got %v", 2, len(sc.unappliedTxns))
	}

	// commit the download. This should remove all unapplied txns.
	err = sc.managedCommitClearContract(walTxn, signedTxn, bandwidth)
	if err != nil {
		t.Fatal("Failed to commit payment intent")
	}
	if len(sc.unappliedTxns) != 0 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}

	// restart again. We still expect 0 unapplied txns.
	cs, err = NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	sc = cs.managedMustAcquire(t, contract.ID)

	if sc.LastRevision().NewRevisionNumber != rev.NewRevisionNumber {
		t.Fatal("Unexpected revision number after reloading the contract set")
	}
	if len(sc.unappliedTxns) != 0 {
		t.Fatalf("expected %v unapplied txns but got %v", 0, len(sc.unappliedTxns))
	}
	if sc.Utility().GoodForRenew {
		t.Fatal("contract shouldn't be good for renew")
	}
	if sc.Utility().GoodForUpload {
		t.Fatal("contract shouldn't be good for upload")
	}
	if !sc.Utility().Locked {
		t.Fatal("contract should be locked")
	}
}

// TestPanicOnOverwritingNewerRevision tests if attempting to
// overwrite a contract header with an old revision triggers a panic.
func TestPanicOnOverwritingNewerRevision(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create contract set with one contract
	dir := build.TempDir(filepath.Join("proto", t.Name()))
	rl := ratelimit.NewRateLimit(0, 0, 0)
	cs, err := NewContractSet(dir, rl, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	header := contractHeader{
		Transaction: types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				NewRevisionNumber:    2,
				NewValidProofOutputs: []types.SiacoinOutput{{}, {}},
				UnlockConditions: types.UnlockConditions{
					PublicKeys: []types.SiaPublicKey{{}, {}},
				},
			}},
		},
	}
	initialRoots := []crypto.Hash{{1}}
	c, err := cs.managedInsertContract(header, initialRoots)
	if err != nil {
		t.Fatal(err)
	}

	sc, ok := cs.Acquire(c.ID)
	if !ok {
		t.Fatal("failed to acquire contract")
	}
	// Trying to set a header with an older revision should trigger a panic.
	header.Transaction.FileContractRevisions[0].NewRevisionNumber = 1
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic when attempting to overwrite a newer contract header revision")
		}
	}()
	if err := sc.applySetHeader(header); err != nil {
		t.Fatal(err)
	}
}
