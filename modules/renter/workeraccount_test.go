package renter

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// newRandomHostKey creates a random key pair and uses it to create a
// SiaPublicKey, this method returns the SiaPublicKey alongisde the secret key
func newRandomHostKey() (types.SiaPublicKey, crypto.SecretKey) {
	sk, pk := crypto.GenerateKeyPair()
	return types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}, sk
}

// TestAccount verifies the functionality of the account
func TestAccount(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		closedRenter := rt.renter
		if err := rt.Close(); err != nil {
			t.Error(err)
		}

		// these test are ran on a renter after Close has been called
		t.Run("Closed", func(t *testing.T) {
			testAccountClosed(t, closedRenter)
		})
		t.Run("CriticalOnDoubleSave", func(t *testing.T) {
			testAccountCriticalOnDoubleSave(t, closedRenter)
		})
	}()

	t.Run("CheckFundAccountGouging", testAccountCheckFundAccountGouging)
	t.Run("Constants", testAccountConstants)
	t.Run("MinMaxExpectedBalance", testAccountMinAndMaxExpectedBalance)
	t.Run("TrackSpending", testAccountTrackSpending)
	t.Run("SyncBalance", testAccountSyncBalance)

	t.Run("Creation", func(t *testing.T) { testAccountCreation(t, rt) })
	t.Run("Tracking", func(t *testing.T) { testAccountTracking(t, rt) })
}

// TestWorkerAccount verifies the functionality of the account related
// functionality of the worker.
func TestWorkerAccount(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	t.Run("HostAccountBalance", func(t *testing.T) {
		testWorkerAccountHostAccountBalance(t, wt)
	})

	t.Run("SyncAccountBalanceToHostCritical", func(t *testing.T) {
		testWorkerAccountSyncAccountBalanceToHostCritical(t, wt)
	})

	t.Run("SpendingDetails", func(t *testing.T) {
		testWorkerAccountSpendingDetails(t, wt)
	})
}

// testAccountCheckFundAccountGouging checks that `checkFundAccountGouging` is
// correctly detecting price gouging from a host.
func testAccountCheckFundAccountGouging(t *testing.T) {
	t.Parallel()

	// allowance contains only the fields necessary to test the price gouging
	allowance := modules.Allowance{
		Funds: types.SiacoinPrecision.Mul64(1e3),
	}

	// set the target balance to 1SC, this is necessary because this decides how
	// frequently we refill the account, which is a required piece of knowledge
	// in order to estimate the total cost of refilling
	targetBalance := types.SiacoinPrecision

	// verify happy case
	pt := newDefaultPriceTable()
	err := checkFundAccountGouging(pt, allowance, targetBalance)
	if err != nil {
		t.Fatal("unexpected price gouging failure")
	}

	// verify gouging case, in order to do so we have to set the fund account
	// cost to an unreasonable amount, empirically we found 75mS to be such a
	// value for the given parameters (1000SC funds and TB of 1SC)
	pt = newDefaultPriceTable()
	pt.FundAccountCost = types.SiacoinPrecision.MulFloat(0.075)
	err = checkFundAccountGouging(pt, allowance, targetBalance)
	if err == nil || !strings.Contains(err.Error(), "fund account cost") {
		t.Fatalf("expected fund account cost gouging error, instead error was '%v'", err)
	}
}

// testAccountConstants makes sure that certain relationships between constants
// exist.
func testAccountConstants(t *testing.T) {
	// Sanity check that the metadata size is not larger than the account size.
	if metadataSize > accountSize {
		t.Fatal("metadata size is larger than account size")
	}
	if accountSize > 4096 {
		t.Fatal("account size must not be larger than a disk sector")
	}
	if 4096%accountSize != 0 {
		t.Fatal("account size must be a factor of 4096")
	}
}

// testAccountMinAndMaxExpectedBalance is a small unit test that verifies the
// functionality of the min and max expected balance functions.
func testAccountMinAndMaxExpectedBalance(t *testing.T) {
	t.Parallel()

	oneCurrency := types.NewCurrency64(1)

	a := new(account)
	a.balance = oneCurrency
	a.negativeBalance = oneCurrency
	if !a.minExpectedBalance().Equals(types.ZeroCurrency) {
		t.Fatal("unexpected min expected balance")
	}
	if !a.maxExpectedBalance().Equals(types.ZeroCurrency) {
		t.Fatal("unexpected max expected balance")
	}

	a = new(account)
	a.balance = oneCurrency.Mul64(2)
	a.negativeBalance = oneCurrency
	a.pendingWithdrawals = oneCurrency
	if !a.minExpectedBalance().Equals(types.ZeroCurrency) {
		t.Fatal("unexpected min expected balance")
	}
	if !a.maxExpectedBalance().Equals(oneCurrency) {
		t.Fatal("unexpected max expected balance")
	}

	a = new(account)
	a.balance = oneCurrency.Mul64(3)
	a.negativeBalance = oneCurrency
	a.pendingWithdrawals = oneCurrency
	if !a.minExpectedBalance().Equals(oneCurrency) {
		t.Fatal("unexpected min expected balance")
	}
	if !a.maxExpectedBalance().Equals(oneCurrency.Mul64(2)) {
		t.Fatal("unexpected max expected balance")
	}

	a = new(account)
	a.balance = oneCurrency.Mul64(3)
	a.negativeBalance = oneCurrency
	a.pendingWithdrawals = oneCurrency
	a.pendingDeposits = oneCurrency
	if !a.minExpectedBalance().Equals(oneCurrency) {
		t.Fatal("unexpected min expected balance")
	}
	if !a.maxExpectedBalance().Equals(oneCurrency.Mul64(3)) {
		t.Fatal("unexpected max expected balance")
	}
}

// testAccountSyncBalance is a small unit test that verifies the functionality
// of the sync balance function.
func testAccountSyncBalance(t *testing.T) {
	t.Parallel()

	// create a mock of the accounts file
	deps := modules.ProductionDependencies{}
	f, err := deps.OpenFile(filepath.Join(t.TempDir(), accountsFilename), os.O_RDWR|os.O_CREATE, defaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = f.Close()
		if err != nil {
			t.Fatal("err")
		}
	}()

	oneCurrency := types.NewCurrency64(1)

	a := new(account)
	a.staticFile = f
	a.balance = types.ZeroCurrency
	a.negativeBalance = oneCurrency
	a.pendingDeposits = oneCurrency
	a.pendingWithdrawals = oneCurrency
	a.managedSyncBalance(oneCurrency)

	if !a.balance.Equals(oneCurrency) {
		t.Fatal("unexpected balance after reset", a.balance)
	}
	if !a.negativeBalance.IsZero() {
		t.Fatal("unexpected negative balance after reset", a.negativeBalance)
	}
	if !a.pendingDeposits.IsZero() {
		t.Fatal("unexpected pending deposits after reset", a.pendingDeposits)
	}
	if !a.pendingWithdrawals.IsZero() {
		t.Fatal("unexpected pending withdrawals after reset", a.pendingWithdrawals)
	}
	if !a.balanceDriftPositive.Equals(oneCurrency) || !a.balanceDriftNegative.IsZero() {
		t.Fatal("unexpected drift")
	}
	if a.syncAt == (time.Time{}) {
		t.Fatal("unexpected sync at")
	}

	// verify negative drift gets updated properly as well
	a.managedSyncBalance(types.ZeroCurrency)
	if !a.balanceDriftPositive.Equals(oneCurrency) || !a.balanceDriftNegative.Equals(oneCurrency) {
		t.Fatal("unexpected drift")
	}
}

// testAccountTrackSpending is a small unit test that verifies the functionality
// of the method 'trackSpending' on the account
func testAccountTrackSpending(t *testing.T) {
	t.Parallel()

	// create a mock of the accounts file, tracking a spend needs a file to
	// write to
	deps := modules.ProductionDependencies{}
	f, err := deps.OpenFile(filepath.Join(t.TempDir(), accountsFilename), os.O_RDWR|os.O_CREATE, defaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = f.Close()
		if err != nil {
			t.Fatal("err")
		}
	}()

	// create an account
	a := new(account)
	a.staticFile = f

	// verify initial state
	hasting := types.NewCurrency64(1)
	if !a.spending.downloads.IsZero() ||
		!a.spending.snapshotDownloads.IsZero() ||
		!a.spending.registryReads.IsZero() ||
		!a.spending.registryWrites.IsZero() ||
		!a.spending.repairDownloads.IsZero() ||
		!a.spending.repairUploads.IsZero() ||
		!a.spending.subscriptions.IsZero() ||
		!a.spending.uploads.IsZero() {
		t.Fatal("unexpected")
	}

	// verify every category tracks its own field
	a.trackSpending(categoryDownload, hasting.Mul64(1))
	a.trackSpending(categorySnapshotDownload, hasting.Mul64(2))
	a.trackSpending(categorySnapshotUpload, hasting.Mul64(3))
	a.trackSpending(categoryRegistryRead, hasting.Mul64(4))
	a.trackSpending(categoryRegistryWrite, hasting.Mul64(5))
	a.trackSpending(categoryRepairDownload, hasting.Mul64(6))
	a.trackSpending(categoryRepairUpload, hasting.Mul64(7))
	a.trackSpending(categorySubscription, hasting.Mul64(8))
	a.trackSpending(categoryUpload, hasting.Mul64(9))
	if !a.spending.downloads.Equals(hasting.Mul64(1)) ||
		!a.spending.snapshotDownloads.Equals(hasting.Mul64(2)) ||
		!a.spending.snapshotUploads.Equals(hasting.Mul64(3)) ||
		!a.spending.registryReads.Equals(hasting.Mul64(4)) ||
		!a.spending.registryWrites.Equals(hasting.Mul64(5)) ||
		!a.spending.repairDownloads.Equals(hasting.Mul64(6)) ||
		!a.spending.repairUploads.Equals(hasting.Mul64(7)) ||
		!a.spending.subscriptions.Equals(hasting.Mul64(8)) ||
		!a.spending.uploads.Equals(hasting.Mul64(9)) {
		t.Fatal("unexpected")
	}

	// check category sanity check
	func() {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprintf("%v", r), "uninitialized category") {
				t.Fatalf("expected panic when attempting to track a spend where the category is not initialised, instead we recovered %v", r)
			}
		}()
		a.trackSpending(categoryErr, hasting)
	}()
}

// testAccountCreation verifies newAccount returns a valid account object
func testAccountCreation(t *testing.T, rt *renterTester) {
	r := rt.renter

	// create a random hostKey
	_, pk := crypto.GenerateKeyPair()
	hostKey := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// create an account with a different hostkey to ensure the account we are
	// going to validate has an offset different from 0
	tmpKey, _ := newRandomHostKey()
	_, err := r.staticAccountManager.managedOpenAccount(tmpKey)
	if err != nil {
		t.Fatal(err)
	}

	// create a new account object
	account, err := r.staticAccountManager.managedOpenAccount(hostKey)
	if err != nil {
		t.Fatal(err)
	}

	// validate the account object
	if account.staticID.IsZeroAccount() {
		t.Fatal("Invalid account ID")
	}
	if account.staticOffset == 0 {
		t.Fatal("Invalid offset")
	}
	if !account.staticHostKey.Equals(hostKey) {
		t.Fatal("Invalid host key")
	}

	// validate the account id is built using a valid SiaPublicKey and the
	// account's secret key belongs to the public key used to construct the id
	hash := crypto.HashBytes(fastrand.Bytes(10))
	sig := crypto.SignHash(hash, account.staticSecretKey)
	err = crypto.VerifyHash(hash, account.staticID.SPK().ToPublicKey(), sig)
	if err != nil {
		t.Fatal("Invalid secret key")
	}
}

// testAccountTracking unit tests all of the methods on the account that track
// deposits or withdrawals.
func testAccountTracking(t *testing.T, rt *renterTester) {
	r := rt.renter

	// create a random account
	hostKey, _ := newRandomHostKey()
	account, err := r.staticAccountManager.managedOpenAccount(hostKey)
	if err != nil {
		t.Fatal(err)
	}

	// verify tracking a deposit properly alters the account state
	deposit := types.SiacoinPrecision
	account.managedTrackDeposit(deposit)
	if !account.pendingDeposits.Equals(deposit) {
		t.Log(account.pendingDeposits)
		t.Fatal("Tracking a deposit did not properly alter the account's state")
	}

	// verify committing a deposit decrements the pendingDeposits and properly
	// adjusts the account balance depending on whether success is true or false
	account.managedCommitDeposit(deposit, false)
	if !account.pendingDeposits.IsZero() {
		t.Fatal("Committing a deposit did not properly alter the  account's state")
	}
	if !account.balance.IsZero() {
		t.Fatal("Committing a failed deposit wrongfully adjusted the account balance")
	}
	account.managedTrackDeposit(deposit) // redo the deposit
	account.managedCommitDeposit(deposit, true)
	if !account.pendingDeposits.IsZero() {
		t.Fatal("Committing a deposit did not properly alter the  account's state")
	}
	if !account.balance.Equals(deposit) {
		t.Fatal("Committing a successful deposit wrongfully adjusted the account balance")
	}

	// verify tracking a withdrawal properly alters the account state
	withdrawal := types.SiacoinPrecision.Div64(100)
	account.managedTrackWithdrawal(withdrawal)
	if !account.pendingWithdrawals.Equals(withdrawal) {
		t.Log(account.pendingWithdrawals)
		t.Fatal("Tracking a withdrawal did not properly alter the account's state")
	}

	// verify committing a withdrawal decrements the pendingWithdrawals and
	// properly adjusts the account balance depending on whether success is true
	// or false
	account.managedCommitWithdrawal(categoryUpload, withdrawal, types.ZeroCurrency, false)
	if !account.pendingWithdrawals.IsZero() {
		t.Fatal("Committing a withdrawal did not properly alter the account's state")
	}
	if !account.balance.Equals(deposit) {
		t.Fatal("Committing a failed withdrawal wrongfully adjusted the account balance")
	}
	account.managedTrackWithdrawal(withdrawal) // redo the withdrawal
	account.managedCommitWithdrawal(categoryUpload, withdrawal, types.ZeroCurrency, true)
	if !account.pendingWithdrawals.IsZero() {
		t.Fatal("Committing a withdrawal did not properly alter the account's state")
	}
	if !account.balance.Equals(deposit.Sub(withdrawal)) {
		t.Fatal("Committing a successful withdrawal wrongfully adjusted the account balance")
	}

	// verify committing a successful withdrawal with a spending category
	// tracks the spend in the spending details, we only verify one category
	// here but the others are covered in the unit test 'trackSpending'
	account.managedTrackWithdrawal(withdrawal)
	account.managedCommitWithdrawal(categoryDownload, withdrawal, types.ZeroCurrency, true)
	if !account.spending.downloads.Equals(withdrawal) {
		t.Fatal("Committing a successful withdrawal with a valid spending category should have update the appropriate field in the spending details")
	}
}

// testAccountClosed verifies accounts can not be opened after the 'closed' flag
// has been set to true by the save.
func testAccountClosed(t *testing.T, closedRenter *Renter) {
	hk, _ := newRandomHostKey()
	_, err := closedRenter.staticAccountManager.managedOpenAccount(hk)
	if !strings.Contains(err.Error(), "file already closed") {
		t.Fatal("Unexpected error when opening an account, err:", err)
	}
}

// testAccountCriticalOnDoubleSave verifies the critical when
// managedSaveAccounts is called twice.
func testAccountCriticalOnDoubleSave(t *testing.T, closedRenter *Renter) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Sprint(r)
			if !strings.Contains(err, "Trying to save accounts twice") {
				t.Fatal("Expected error not returned")
			}
		}
	}()
	err := closedRenter.staticAccountManager.managedSaveAndClose()
	if err == nil {
		t.Fatal("Expected build.Critical on double save")
	}
}

// testWorkerAccountHostAccountBalance verifies the functionality of
// staticHostAccountBalance that performs the account balance RPC on the host
func testWorkerAccountHostAccountBalance(t *testing.T, wt *workerTester) {
	w := wt.worker

	// wait until the worker is done with its maintenance tasks - this basically
	// ensures we have a working worker, with valid PT and funded EA
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.managedMaintenanceSucceeded() {
			return errors.New("worker not ready with maintenance")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// fetch the host account balance and assert it's correct
	balance, err := w.staticHostAccountBalance()
	if err != nil {
		t.Fatal(err)
	}
	if !balance.Equals(w.staticBalanceTarget) {
		t.Fatal(err)
	}
}

// testWorkerAccountSyncAccountBalanceToHostCritical is a small unit test that
// verifies the sync can not be called when the account delta is not zero
func testWorkerAccountSyncAccountBalanceToHostCritical(t *testing.T, wt *workerTester) {
	w := wt.worker

	// wait until the worker is done with its maintenance tasks - this basically
	// ensures we have a working worker, with valid PT and funded EA
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.managedMaintenanceSucceeded() {
			return errors.New("worker not ready with maintenance")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// track a deposit to simulate an ongoing fund
	w.staticAccount.managedTrackDeposit(w.staticBalanceTarget)

	// trigger the account balance sync and expect it to panic
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprintf("%v", r), "managedSyncAccountBalanceToHost is called on a worker with an account that has non-zero deltas") {
			t.Error("Expected build.Critical")
			t.Log(r)
		}
	}()

	w.externSyncAccountBalanceToHost()
}

// testWorkerAccountSpendingDetails verifies that performing actions such as
// downloading and reading, writing and subscribing to the registry properly
// update the spending details in the worker account.
func testWorkerAccountSpendingDetails(t *testing.T, wt *workerTester) {
	w := wt.worker

	// wait until the worker is done with its maintenance tasks - this basically
	// ensures we have a working worker, with valid PT and funded EA
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.managedMaintenanceSucceeded() {
			return errors.New("worker not ready with maintenance")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// verify initial state
	spending := w.staticAccount.callSpendingDetails()
	if !spending.downloads.IsZero() ||
		!spending.registryReads.IsZero() ||
		!spending.registryWrites.IsZero() ||
		!spending.repairDownloads.IsZero() ||
		!spending.repairUploads.IsZero() ||
		!spending.snapshotDownloads.IsZero() ||
		!spending.snapshotUploads.IsZero() ||
		!spending.subscriptions.IsZero() {
		t.Fatal("unexpected")
	}

	// create a registry value
	sk, pk := crypto.GenerateKeyPair()
	var tweak crypto.Hash
	fastrand.Read(tweak[:])
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	rv := modules.NewRegistryValue(tweak, fastrand.Bytes(modules.RegistryDataSize), fastrand.Uint64n(1000), modules.RegistryTypeWithoutPubkey).Sign(sk)

	// update the registry
	err := wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	spending = w.staticAccount.callSpendingDetails()
	if spending.registryWrites.IsZero() {
		t.Fatal("unexpected")
	}

	// read from the registry
	_, err = wt.ReadRegistry(context.Background(), spk, rv.Tweak)
	if err != nil {
		t.Fatal(err)
	}

	spending = w.staticAccount.callSpendingDetails()
	if spending.registryReads.IsZero() {
		t.Fatal("unexpected")
	}

	// upload a snapshot to fill the first sector of the contract.
	backup := modules.UploadedBackup{
		Name:           "foo",
		CreationDate:   types.CurrentTimestamp(),
		Size:           10,
		UploadProgress: 0,
	}
	err = wt.UploadSnapshot(context.Background(), backup, fastrand.Bytes(int(backup.Size)))
	if err != nil {
		t.Fatal(err)
	}

	// download snapshot
	_, err = wt.DownloadSnapshotTable(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	spending = w.staticAccount.callSpendingDetails()
	if spending.snapshotDownloads.IsZero() {
		t.Fatal("unexpected")
	}

	// verify executing a program with specifying the spending category results
	// in an update to the spending details for that category
	pt := wt.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt, 0)
	pb.AddHasSectorInstruction(crypto.Hash{})
	p, data := pb.Program()
	cost, _, _ := pb.Cost(true)

	jhs := new(jobHasSector)
	jhs.staticSectors = []crypto.Hash{{1, 2, 3}}
	ulBandwidth, dlBandwidth := jhs.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// execute it
	_, _, err = w.managedExecuteProgram(p, data, types.FileContractID{}, categoryDownload, cost)
	if err != nil {
		t.Fatal(err)
	}

	spending = w.staticAccount.callSpendingDetails()
	if spending.downloads.IsZero() {
		t.Fatal("unexpected")
	}

	// Subscribe to the random registry value we created earlier.
	// Subscribe to that entry.
	req := modules.RPCRegistrySubscriptionRequest{
		PubKey: spk,
		Tweak:  rv.Tweak,
	}
	_, err = wt.Subscribe(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	// Update the entry on the host.
	rv.Revision++
	rv = rv.Sign(sk)
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	// Unsubscribe
	wt.Unsubscribe(req)

	// Stop the loop by shutting down the worker.
	err = wt.staticTG.Stop()
	if err != nil {
		t.Fatal(err)
	}

	// Check the spending is updated
	time.Sleep(stopSubscriptionGracePeriod)
	spending = w.staticAccount.callSpendingDetails()
	if spending.subscriptions.IsZero() {
		t.Fatal("unexpected")
	}

	// Reload thhe renter and reload the account for that same host
	hostkey := wt.worker.staticHostPubKey
	r, err := wt.rt.reloadRenter(wt.renter)
	if err != nil {
		t.Fatal(err)
	}
	account, err := r.staticAccountManager.managedOpenAccount(hostkey)
	if err != nil {
		t.Fatal(err)
	}

	// Except the account's spending details were reloaded properly. We do not
	// strictly compare but verify it's at least equal to what it was prior to
	// shutdown to avoid NDFs.
	reloaded := account.callSpendingDetails()
	if reloaded.downloads.Cmp(spending.downloads) < 0 ||
		reloaded.registryReads.Cmp(spending.registryReads) < 0 ||
		reloaded.registryWrites.Cmp(spending.registryWrites) < 0 ||
		reloaded.repairDownloads.Cmp(spending.repairDownloads) < 0 ||
		reloaded.repairUploads.Cmp(spending.repairUploads) < 0 ||
		reloaded.snapshotDownloads.Cmp(spending.snapshotDownloads) < 0 ||
		reloaded.snapshotUploads.Cmp(spending.snapshotUploads) < 0 ||
		reloaded.subscriptions.Cmp(spending.subscriptions) < 0 {
		t.Fatal("unexpected")
	}
}

// TestNewWithdrawalMessage verifies the newWithdrawalMessage helper
// properly instantiates all required fields on the WithdrawalMessage
func TestNewWithdrawalMessage(t *testing.T) {
	t.Parallel()
	// create a withdrawal message using random parameters
	aid, _ := modules.NewAccountID()
	amount := types.NewCurrency64(fastrand.Uint64n(100))
	blockHeight := types.BlockHeight(fastrand.Intn(100))
	msg := newWithdrawalMessage(aid, amount, blockHeight)

	// validate the withdrawal message
	if msg.Account != aid {
		t.Fatal("Unexpected account ID")
	}
	if !msg.Amount.Equals(amount) {
		t.Fatal("Unexpected amount")
	}
	if msg.Expiry != blockHeight+withdrawalValidityPeriod {
		t.Fatal("Unexpected expiry")
	}
	if len(msg.Nonce) != modules.WithdrawalNonceSize {
		t.Fatal("Unexpected nonce length")
	}
	var nonce [modules.WithdrawalNonceSize]byte
	if bytes.Equal(msg.Nonce[:], nonce[:]) {
		t.Fatal("Uninitialized nonce")
	}
}

// openRandomTestAccountsOnRenter is a helper function that creates a random
// number of accounts by calling 'managedOpenAccount' on the given renter
func openRandomTestAccountsOnRenter(r *Renter) ([]*account, error) {
	// randomBalance is a small helper function that returns a random
	// types.Currency taking into account the given max value
	randomBalance := func(max uint64) types.Currency {
		return types.NewCurrency64(fastrand.Uint64n(max))
	}

	var accounts []*account
	for i := 0; i < fastrand.Intn(10)+1; i++ {
		hostKey := types.SiaPublicKey{
			Algorithm: types.SignatureEd25519,
			Key:       fastrand.Bytes(crypto.PublicKeySize),
		}
		account, err := r.staticAccountManager.managedOpenAccount(hostKey)
		if err != nil {
			return nil, err
		}

		// give it a random balance state
		account.balance = randomBalance(1e3)
		account.negativeBalance = randomBalance(1e2)
		account.pendingDeposits = randomBalance(1e2)
		account.pendingWithdrawals = randomBalance(1e2)
		account.spending = spendingDetails{
			downloads:         randomBalance(1e1),
			registryReads:     randomBalance(1e1),
			registryWrites:    randomBalance(1e1),
			repairDownloads:   randomBalance(1e1),
			repairUploads:     randomBalance(1e1),
			snapshotDownloads: randomBalance(1e1),
			snapshotUploads:   randomBalance(1e1),
			subscriptions:     randomBalance(1e1),
			uploads:           randomBalance(1e1),
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}
