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

// TestConstants makes sure that certain relationships between constants exist.
func TestConstants(t *testing.T) {
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

// TestAccountTracking unit tests all of the methods on the account that track
// deposits or withdrawals.
func TestAccountTracking(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
	}()
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

// TestNewAccount verifies newAccount returns a valid account object
func TestNewAccount(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
	}()
	r := rt.renter

	// create a random hostKey
	_, pk := crypto.GenerateKeyPair()
	hostKey := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// create an account with a different hostkey to ensure the account we are
	// going to validate has an offset different from 0
	tmpKey := hostKey
	fastrand.Read(tmpKey.Key[:4])
	r.staticAccountManager.managedOpenAccount(tmpKey)

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

// TestNewWithdrawalMessage verifies the newWithdrawalMessage helper
// properly instantiates all required fields on the WithdrawalMessage
func TestNewWithdrawalMessage(t *testing.T) {
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

// TestAccountCriticalOnDoubleSave verifies the critical when
// managedSaveAccounts is called twice.
func TestAccountCriticalOnDoubleSave(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	r := rt.renter

	// close it immediately
	err = rt.Close()
	if err != nil {
		t.Log(err)
	}

	defer func() {
		if r := recover(); r != nil {
			err := fmt.Sprint(r)
			if !strings.Contains(err, "Trying to save accounts twice") {
				t.Fatal("Expected error not returned")
			}
		}
	}()
	err = r.staticAccountManager.managedSaveAndClose()
	if err == nil {
		t.Fatal("Expected build.Critical on double save")
	}
}

// TestAccountClosed verifies accounts can not be opened after the 'closed' flag
// has been set to true by the save.
func TestAccountClosed(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	r := rt.renter

	// close it immediately
	err = rt.Close()
	if err != nil {
		t.Fatal(err)
	}

	hk, _ := newRandomHostKey()
	_, err = r.staticAccountManager.managedOpenAccount(hk)
	if !strings.Contains(err.Error(), "file already closed") {
		t.Fatal("Unexpected error when opening an account, err:", err)
	}
}

// TestAccountResetBalance is a small unit test that verifies the functionality
// of the reset balance function.
func TestAccountResetBalance(t *testing.T) {
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

// TestAccountMinExpectedBalance is a small unit test that verifies the
// functionality of the min expected balance function.
func TestAccountMinExpectedBalance(t *testing.T) {
	t.Parallel()

	oneCurrency := types.NewCurrency64(1)

	a := new(account)
	a.balance = oneCurrency
	a.negativeBalance = oneCurrency
	if !a.minExpectedBalance().Equals(types.ZeroCurrency) {
		t.Fatal("unexpected min expected balance")
	}

	a = new(account)
	a.balance = oneCurrency.Mul64(2)
	a.negativeBalance = oneCurrency
	a.pendingWithdrawals = oneCurrency
	if !a.minExpectedBalance().Equals(types.ZeroCurrency) {
		t.Fatal("unexpected min expected balance")
	}

	a = new(account)
	a.balance = oneCurrency.Mul64(3)
	a.negativeBalance = oneCurrency
	a.pendingWithdrawals = oneCurrency
	if !a.minExpectedBalance().Equals(oneCurrency) {
		t.Fatal("unexpected min expected balance")
	}
}

// TestHostAccountBalance verifies the functionality of staticHostAccountBalance
// that performs the account balance RPC on the host
func TestHostAccountBalance(t *testing.T) {
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
	w := wt.worker

	// check the balance in a retry to allow the worker to run through it's
	// setup, e.g. updating PT, checking balance and refilling. Note we use min
	// expected balance to ensure we're not counting pending deposits
	if err = build.Retry(100, 100*time.Millisecond, func() error {
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

// TestSyncAccountBalanceToHostCritical is a small unit test that verifies the
// sync can not be called when the account delta is not zero
func TestSyncAccountBalanceToHostCritical(t *testing.T) {
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
	w := wt.worker

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
	w.managedSyncAccountBalanceToHost()
}

// openRandomTestAccountsOnRenter is a helper function that creates a random
// number of accounts by calling 'managedOpenAccount' on the given renter
func openRandomTestAccountsOnRenter(r *Renter) []*account {
	accounts := make([]*account, 0)
	for i := 0; i < fastrand.Intn(10)+1; i++ {
		hostKey := types.SiaPublicKey{
			Algorithm: types.SignatureEd25519,
			Key:       fastrand.Bytes(crypto.PublicKeySize),
		}
		account, err := r.staticAccountManager.managedOpenAccount(hostKey)
		if err != nil {
			// TODO: Have this function return an error.
			panic(err)
		}

		// give it a random balance state
		account.balance = types.NewCurrency64(fastrand.Uint64n(1e3))
		account.negativeBalance = types.NewCurrency64(fastrand.Uint64n(1e2))
		account.pendingDeposits = types.NewCurrency64(fastrand.Uint64n(1e2))
		account.pendingWithdrawals = types.NewCurrency64(fastrand.Uint64n(1e2))
		accounts = append(accounts, account)
	}
	return accounts
}
