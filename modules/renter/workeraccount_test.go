package renter

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
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
	t.Run("ResetBalance", testAccountResetBalance)
	t.Run("ResetCoolDown", testAccountResetCoolDown)

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

// testAccountResetBalance is a small unit test that verifies the functionality
// of the reset balance function.
func testAccountResetBalance(t *testing.T) {
	t.Parallel()

	oneCurrency := types.NewCurrency64(1)

	a := new(account)
	a.balance = types.ZeroCurrency
	a.negativeBalance = oneCurrency
	a.pendingDeposits = oneCurrency
	a.pendingWithdrawals = oneCurrency
	a.managedResetBalance(oneCurrency)

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
}

// testAccountResetCoolDown is a small unit test that verifies the functionality
// of the reset cooldown function.
func testAccountResetCoolDown(t *testing.T) {
	t.Parallel()

	a := new(account)
	a.cooldownUntil = cooldownUntil(a.consecutiveFailures)
	a.consecutiveFailures++
	if !a.managedOnCooldown() {
		t.Fatal("expected account to be on cooldown")
	}

	a.managedResetCoolDown()
	if a.consecutiveFailures != 0 {
		t.Fatal("unexpected consecutive failures after cooldown reset", a.consecutiveFailures)
	}
	if a.managedOnCooldown() {
		t.Error("expected account to not be on cooldown")
	}
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
	account.managedCommitWithdrawal(withdrawal, false)
	if !account.pendingWithdrawals.IsZero() {
		t.Fatal("Committing a withdrawal did not properly alter the account's state")
	}
	if !account.balance.Equals(deposit) {
		t.Fatal("Committing a failed withdrawal wrongfully adjusted the account balance")
	}
	account.managedTrackWithdrawal(withdrawal) // redo the withdrawal
	account.managedCommitWithdrawal(withdrawal, true)
	if !account.pendingWithdrawals.IsZero() {
		t.Fatal("Committing a withdrawal did not properly alter the account's state")
	}
	if !account.balance.Equals(deposit.Sub(withdrawal)) {
		t.Fatal("Committing a successful withdrawal wrongfully adjusted the account balance")
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

// TestWorkerAccountCoolDown verifies the functionality of the account cooldown
func TestWorkerAccountCoolDown(t *testing.T) {
	wt, err := newWorkerTesterCustomDependency(t.Name(), &dependencies.DependencyDisableCriticalOnMaxBalance{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// check the balance in a retry to allow the worker to run through it's
	// setup, e.g. updating PT, checking balance and refilling. Note we use min
	// expected balance to ensure we're not counting pending deposits
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.staticAccount.managedMinExpectedBalance().Equals(w.staticBalanceTarget) {
			return errors.New("worker account not funded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// verify the account is not on cooldown
	if w.staticAccount.managedOnCooldown() {
		t.Fatal("Account should not be on cooldown")
	}

	// set a negative balance, tricking the worker into thinking it has to
	// refill
	w.staticAccount.mu.Lock()
	w.staticAccount.negativeBalance = w.staticAccount.balance
	w.staticAccount.mu.Unlock()

	// manually trigger a refill
	w.managedRefillAccount()

	// verify the account is on cooldown
	if !w.staticAccount.managedOnCooldown() {
		t.Fatal("Account should be on cooldown")
	}

	// verify recent error and consecutive failures are set
	w.staticAccount.mu.Lock()
	cf := w.staticAccount.consecutiveFailures
	re := w.staticAccount.recentErr
	w.staticAccount.mu.Unlock()
	if cf == 0 {
		t.Fatal("Consecutive failures should be larger than zero")
	}
	if re == nil {
		t.Fatal("Recent Error should be set")
	}

	// the workerloop should have synced the account balance
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		w.staticAccount.mu.Lock()
		defer w.staticAccount.mu.Unlock()
		if !w.staticAccount.negativeBalance.IsZero() {
			return errors.New("worker account balance not reset")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// run a couple of has sector jobs to spend money
	cc := make(chan struct{})
	rc := make(chan *jobHasSectorResponse)
	jhs := &jobHasSector{
		staticSector:       crypto.Hash{},
		staticResponseChan: rc,
		jobGeneric: &jobGeneric{
			staticCancelChan: cc,
			staticQueue:      w.staticJobHasSectorQueue,
		},
	}
	for i := 0; i < 100; i++ {
		if !w.staticJobHasSectorQueue.callAdd(jhs) {
			t.Fatal("could not add job to queue")
		}
	}

	// manually trigger a refill
	w.managedRefillAccount()

	// check if 'consecutiveFailures' has been reset to 0
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		w.staticAccount.mu.Lock()
		cf := w.staticAccount.consecutiveFailures
		w.staticAccount.mu.Unlock()
		if cf != 0 {
			return errors.New("consecutive failures not reset")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// verify the account is not on cooldown
	if w.staticAccount.managedOnCooldown() {
		t.Fatal("Account should not be on cooldown")
	}
}

// testWorkerAccountHostAccountBalance verifies the functionality of
// staticHostAccountBalance that performs the account balance RPC on the host
func testWorkerAccountHostAccountBalance(t *testing.T, wt *workerTester) {
	w := wt.worker

	// check the balance in a retry to allow the worker to run through it's
	// setup, e.g. updating PT, checking balance and refilling. Note we use min
	// expected balance to ensure we're not counting pending deposits
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.staticAccount.managedMinExpectedBalance().Equals(w.staticBalanceTarget) {
			return errors.New("worker account not funded")
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

	// check the balance in a retry to allow the worker to run through it's
	// setup, e.g. updating PT, checking balance and refilling. Note we use min
	// expected balance to ensure we're not counting pending deposits
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.staticAccount.managedMinExpectedBalance().Equals(w.staticBalanceTarget) {
			return errors.New("worker account not funded")
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
	accounts := make([]*account, 0)
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
		account.balance = types.NewCurrency64(fastrand.Uint64n(1e3))
		account.negativeBalance = types.NewCurrency64(fastrand.Uint64n(1e2))
		account.pendingDeposits = types.NewCurrency64(fastrand.Uint64n(1e2))
		account.pendingWithdrawals = types.NewCurrency64(fastrand.Uint64n(1e2))
		accounts = append(accounts, account)
	}
	return accounts, nil
}
