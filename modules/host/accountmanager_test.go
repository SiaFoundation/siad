package host

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestAccountCallDeposit verifies we can deposit into an ephemeral account
func TestAccountCallDeposit(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account
	_, spk := prepareAccount()
	accountID := spk.String()

	// Deposit money into it
	diff := types.NewCurrency64(100)
	before := getAccountBalance(am, accountID)
	err = am.callDeposit(accountID, diff)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the amount was credited
	after := getAccountBalance(am, accountID)
	if !after.Sub(before).Equals(diff) {
		t.Fatal("Deposit was not credited")
	}

	// Verify the deposit can not exceed the max account balance
	maxAccountBalance := am.h.InternalSettings().MaxEphemeralAccountBalance
	err = am.callDeposit(accountID, maxAccountBalance)
	if err != errBalanceMaxExceeded {
		t.Fatal(err)
	}
}

// TestAccountcallWithdraw verifies we can spend from an ephemeral account
func TestAccountcallWithdraw(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account
	sk, spk := prepareAccount()
	accountID := spk.String()

	// Fund the account
	err = am.callDeposit(accountID, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a withdrawal message
	diff := types.NewCurrency64(5)
	msg, sig := prepareWithdrawal(accountID, diff, am.h.blockHeight+10, sk)

	// Spend half of it and verify account balance
	err = am.callWithdraw(msg, sig)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the balance after the spend
	balance := getAccountBalance(am, accountID)
	if !balance.Equals(types.NewCurrency64(5)) {
		t.Fatal("Account balance was incorrect after spend")
	}

	// Spend more than the account holds, have it block and then fund it to
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = am.callDeposit(accountID, types.NewCurrency64(3))
	}()
	overSpend := types.NewCurrency64(7)
	msg, sig = prepareWithdrawal(accountID, overSpend, am.h.blockHeight+10, sk)
	err = am.callWithdraw(msg, sig)
	if err != nil {
		t.Fatal(err)
	}

	balance = getAccountBalance(am, accountID)
	if !balance.Equals(types.NewCurrency64(1)) {
		t.Fatal("Account balance was incorrect after spend")
	}

	// Spend from an unknown account and verify it timed out
	sk, spk = prepareAccount()
	unknown := spk.String()
	msg, sig = prepareWithdrawal(unknown, overSpend, am.h.blockHeight+10, sk)
	err = am.callWithdraw(msg, sig)
	if err != errBalanceInsufficient {
		t.Fatal(err)
	}
}

// TestAccountExpiry verifies accounts expire and get pruned
func TestAccountExpiry(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankMockHostTester(&dependencies.HostExpireEphemeralAccounts{}, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account
	_, spk := prepareAccount()
	accountID := spk.String()

	// Deposit some money into the account
	err = am.callDeposit(accountID, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Verify the balance, sleep a bit and verify it is gone
	balance := getAccountBalance(am, accountID)
	if !balance.Equals(types.NewCurrency64(10)) {
		t.Fatal("Account balance was incorrect after deposit")
	}

	time.Sleep(pruneExpiredAccountsFrequency)
	balance = getAccountBalance(am, accountID)
	if !balance.Equals(types.NewCurrency64(0)) {
		t.Fatal("Account balance was incorrect after expiry")
	}
}

// TestAccountWithdrawalSpent verifies a withdrawal can not be spent twice
func TestAccountWithdrawalSpent(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a host
	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account
	sk, spk := prepareAccount()
	accountID := spk.String()

	// Fund the account
	err = am.callDeposit(accountID, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a withdrawal message
	diff := types.NewCurrency64(5)
	msg, sig := prepareWithdrawal(accountID, diff, am.h.blockHeight+10, sk)
	err = am.callWithdraw(msg, sig)
	if err != nil {
		t.Fatal(err)
	}

	err = am.callWithdraw(msg, sig)
	if err != errWithdrawalSpent {
		t.Fatal("Expected withdrawal spent error", err)
	}
}

// TestAccountWithdrawalExpired verifies a withdrawal with an expiry in the past
// is not accepted
func TestAccountWithdrawalExpired(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a host
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account
	sk, spk := prepareAccount()
	accountID := spk.String()

	// Fund the account
	err = am.callDeposit(accountID, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a withdrawal message
	diff := types.NewCurrency64(5)
	msg, sig := prepareWithdrawal(accountID, diff, am.h.blockHeight-1, sk)
	err = am.callWithdraw(msg, sig)
	if err != errWithdrawalExpired {
		t.Fatal("Expected withdrawal expired error", err)
	}
}

// TestAccountWithdrawalExpired verifies a withdrawal with an expiry in the
// extreme future is not accepted
func TestAccountWithdrawalExtremeFuture(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a host
	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account
	sk, spk := prepareAccount()
	accountID := spk.String()

	// Fund the account
	err = am.callDeposit(accountID, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a withdrawal message
	diff := types.NewCurrency64(5)
	msg, sig := prepareWithdrawal(accountID, diff, am.h.blockHeight+(2*bucketBlockRange)+1, sk)
	err = am.callWithdraw(msg, sig)
	if err != errWithdrawalExtremeFuture {
		t.Fatal("Expected withdrawal extreme future error", err)
	}
}

// TestAccountWithdrawalExpired verifies a withdrawal with an invalid signature is not accepted
func TestAccountWithdrawalInvalidSignature(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a host
	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account and fund it
	sk1, spk1 := prepareAccount()
	err = am.callDeposit(spk1.String(), types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a withdrawal message
	diff := types.NewCurrency64(5)
	msg1, _ := prepareWithdrawal(spk1.String(), diff, am.h.blockHeight+5, sk1)

	// Prepare another account and sign the same message using the other account
	sk2, _ := prepareAccount()
	_, sig2 := prepareWithdrawal(spk1.String(), diff, am.h.blockHeight+5, sk2)

	err = am.callWithdraw(msg1, sig2)
	if err != errWithdrawalInvalidSignature {
		t.Fatal("Expected withdrawal invalid signature error", err)
	}
}

// TestAccountWithdrawalMultiple will deposit a large sum and make a lot of
// small withdrawals
func TestAccountWithdrawalMultiple(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a host
	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account and fund it
	sk, spk := prepareAccount()
	account := spk.String()
	err = am.callDeposit(account, types.NewCurrency64(1e3))
	if err != nil {
		t.Fatal(err)
	}

	var errors []error = make([]error, 0)
	for i := 0; i < 1e3; i++ {
		diff := types.NewCurrency64(1)
		msg, sig := prepareWithdrawal(account, diff, am.h.blockHeight+5, sk)

		err = am.callWithdraw(msg, sig)
		if err != nil {
			t.Log(err.Error())
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		t.Fatal("One or multiple withdrawals failed:")
		for _, e := range errors {
			t.Log(e.Error())
		}
	}

	balance := getAccountBalance(am, account)
	if !balance.Equals(types.ZeroCurrency) {
		t.Fatal("Unexpected account balance after withdrawals")
	}
}

// TestAccountWithdrawalBlockMultiple will deposit a large sum in increments,
// meanwhile making a lot of small withdrawals that will block but eventually
// resolve
func TestAccountWithdrawalBlockMultiple(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a host
	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account
	sk, spk := prepareAccount()
	account := spk.String()

	// Deposit money into the account in small increments
	numDeposits := 100
	depositAmount := types.NewCurrency64(10)

	// Add a waitgroup to wait for all deposits and withdrawals that are taking
	// concurrently taking place. Keep track of potential errors using atomics
	var wg sync.WaitGroup
	var atomicDepositErrs, atomicWithdrawalErrs uint64

	wg.Add(1)
	go func() {
		defer wg.Done()
		for d := 0; d < numDeposits; d++ {
			time.Sleep(time.Duration(rand.Intn(5)+5) * time.Millisecond)
			if err := am.callDeposit(account, depositAmount); err != nil {
				atomic.AddUint64(&atomicDepositErrs, 1)
			}
		}
	}()

	// Run the withdrawals in 10 separate buckets (ensure that withdrawals do
	// not exceed numDeposits * depositAmount)
	for b := 0; b < 10; b++ {
		wg.Add(1)
		go func(bucket int) {
			defer wg.Done()
			for i := bucket * 1e2; i < (bucket+1)*1e2; i++ {
				diff := types.NewCurrency64(1)
				msg, sig := prepareWithdrawal(account, diff, am.h.blockHeight+5, sk)
				if wErr := am.callWithdraw(msg, sig); wErr != nil {
					atomic.AddUint64(&atomicWithdrawalErrs, 1)
					t.Log(wErr)
				}
			}
		}(b)
	}
	wg.Wait()

	// Verify all deposits were successful
	depositErrors := atomic.LoadUint64(&atomicDepositErrs)
	if depositErrors != 0 {
		t.Fatal("Unexpected error during deposits")
	}

	// Verify all withdrawals were successful
	withdrawalErrors := atomic.LoadUint64(&atomicWithdrawalErrs)
	if withdrawalErrors != 0 {
		t.Fatal("Unexpected error during withdrawals")
	}

	// Account balance should be zero..
	balance := getAccountBalance(am, account)
	if !balance.Equals(types.ZeroCurrency) {
		t.Log(balance.String())
		t.Fatal("Unexpected account balance")
	}
}

// prepareWithdrawal prepares a withdrawal message, signs it using the provided
// secret key and returns the message and the signature
func prepareWithdrawal(id string, amount types.Currency, expiry types.BlockHeight, sk crypto.SecretKey) (*withdrawalMessage, crypto.Signature) {
	msg := &withdrawalMessage{
		account: id,
		expiry:  expiry,
		amount:  amount,
		nonce:   uint64(rand.Int()),
	}
	hash := crypto.HashObject(*msg)
	sig := crypto.SignHash(hash, sk)
	return msg, sig
}

// prepareAccount will create an account and return its secret key alonside it's
// sia public key
func prepareAccount() (crypto.SecretKey, types.SiaPublicKey) {
	sk, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	return sk, spk
}
