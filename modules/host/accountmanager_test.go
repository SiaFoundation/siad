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
	"gitlab.com/NebulousLabs/errors"
)

type TestAcc struct {
	id string
	sk crypto.SecretKey
}

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
}

// TestAccountMaxBalance verifies we can never deposit more than the account max
// balance into an ephemeral account
func TestAccountMaxBalance(t *testing.T) {
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

	// Verify the deposit can not exceed the max account balance
	maxBalance := am.h.InternalSettings().MaxEphemeralAccountBalance
	exceedingBalance := maxBalance.Add(types.NewCurrency64(1))
	err = am.callDeposit(accountID, exceedingBalance)
	if err != ErrBalanceMaxExceeded {
		t.Fatal(err)
	}
}

// TestAccountCallWithdraw verifies we can spend from an ephemeral account
func TestAccountCallWithdraw(t *testing.T) {
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
	amount := types.NewCurrency64(5)
	msg, sig := prepareWithdrawal(accountID, amount, am.h.blockHeight, sk)

	// Spend half of it and verify account balance
	err = callWithdraw(am, msg, sig)
	if err != nil {
		t.Fatal(err)
	}

	// Verify current balance
	current := types.NewCurrency64(5)
	balance := getAccountBalance(am, accountID)
	if !balance.Equals(current) {
		t.Fatal("Account balance was incorrect after spend")
	}

	overSpend := types.NewCurrency64(7)
	deposit := types.NewCurrency64(3)
	expected := current.Add(deposit).Sub(overSpend)

	var atomicErrs uint64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		msg, sig = prepareWithdrawal(accountID, overSpend, am.h.blockHeight, sk)
		if err := callWithdraw(am, msg, sig); err != nil {
			atomic.AddUint64(&atomicErrs, 1)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond) // ensure withdrawal blocks
		if err := am.callDeposit(accountID, deposit); err != nil {
			atomic.AddUint64(&atomicErrs, 1)
		}
	}()
	wg.Wait()
	if atomic.LoadUint64(&atomicErrs) != 0 {
		t.Fatal("Unexpected error occurred during blocked withdrawal")
	}

	balance = getAccountBalance(am, accountID)
	if !balance.Equals(expected) {
		t.Fatal("Account balance was incorrect after spend", balance.HumanString())
	}
}

// TestAccountCallWithdrawTimeout verifies withdrawals timeout eventually
func TestAccountCallWithdrawTimeout(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare a new account
	sk, spk := prepareAccount()
	unknown := spk.String()

	// Withdraw from it
	amount := types.NewCurrency64(1)
	msg, sig := prepareWithdrawal(unknown, amount, am.h.blockHeight, sk)
	if err := callWithdraw(am, msg, sig); err != ErrBalanceInsufficient {
		t.Fatal("Unexpected error: ", err)
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
	err = callWithdraw(am, msg, sig)
	if err != nil {
		t.Fatal(err)
	}

	err = callWithdraw(am, msg, sig)
	if !errors.Contains(err, ErrWithdrawalSpent) {
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
	err = callWithdraw(am, msg, sig)
	if !errors.Contains(err, ErrWithdrawalExpired) {
		t.Fatal("Expected withdrawal expired error", err)
	}
}

// TestAccountWithdrawalExtremeFuture verifies a withdrawal with an expiry in
// the extreme future is not accepted
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
	err = callWithdraw(am, msg, sig)

	if !errors.Contains(err, ErrWithdrawalExtremeFuture) {
		t.Fatal("Expected withdrawal extreme future error", err)
	}
}

// TestAccountWithdrawalInvalidSignature verifies a withdrawal with an invalid
// signature is not accepted
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

	err = callWithdraw(am, msg1, sig2)
	if !errors.Contains(err, ErrWithdrawalInvalidSignature) {
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

	var errors []error
	for i := 0; i < 1e3; i++ {
		diff := types.NewCurrency64(1)
		msg, sig := prepareWithdrawal(account, diff, am.h.blockHeight+5, sk)

		err = callWithdraw(am, msg, sig)
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
	if !balance.IsZero() {
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
	deposits := 20
	depositAmount := 50

	buckets := 10
	withdrawals := deposits * depositAmount
	withdrawalAmount := 1

	// Prepare withdrawals and signatures
	msgs := make([]*withdrawalMessage, withdrawals)
	sigs := make([]crypto.Signature, withdrawals)
	for w := 0; w < withdrawals; w++ {
		msgs[w], sigs[w] = prepareWithdrawal(account, types.NewCurrency64(uint64(withdrawalAmount)), am.h.blockHeight, sk)
	}

	// Add a waitgroup to wait for all deposits and withdrawals that are taking
	// concurrently taking place. Keep track of potential errors using atomics
	var wg sync.WaitGroup
	var atomicDepositErrs, atomicWithdrawalErrs uint64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for d := 0; d < deposits; d++ {
			time.Sleep(time.Duration(10 * time.Millisecond))
			if err := am.callDeposit(account, types.NewCurrency64(uint64(depositAmount))); err != nil {
				atomic.AddUint64(&atomicDepositErrs, 1)
			}
		}
	}()

	// Run the withdrawals in 10 separate buckets (ensure that withdrawals do
	// not exceed numDeposits * depositAmount)
	for b := 0; b < buckets; b++ {
		wg.Add(1)
		go func(bucket int) {
			defer wg.Done()
			for i := bucket * (withdrawals / buckets); i < (bucket+1)*(withdrawals/buckets); i++ {
				if wErr := callWithdraw(am, msgs[i], sigs[i]); wErr != nil {
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
	if !balance.IsZero() {
		t.Log(balance.String())
		t.Fatal("Unexpected account balance")
	}
}

// TestAccountMaxEphemeralAccountRisk tests the behaviour when the amount of
// unsaved ephemeral account balances exceeds the 'maxephemeralaccountrisk'. The
// account manager should wait until the asynchronous persist successfully
// completed before releasing the lock to accept more withdrawals.
func TestAccountMaxEphemeralAccountRisk(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a host that persists the accounts data to disk with a certain
	// latency, this will ensure that we can reach the maxephemeralaccountrisk
	// and the host will effectively block until it drops below the maximum
	deps := dependencies.NewHostMaxEphemeralAccountRiskReached(200 * time.Millisecond)
	ht, err := blankMockHostTester(deps, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	his := ht.host.InternalSettings()
	maxRisk := his.MaxEphemeralAccountRisk
	maxBalance := his.MaxEphemeralAccountBalance

	// Use maxBalance in combination with maxRisk (and multiply by 2 to be sure)
	// to figure out a good amount of parallel accounts necessary to trigger
	// maxRisk to be reached.
	buckets, _ := maxRisk.Div(maxBalance).Mul64(2).Uint64()

	// Prepare the accounts
	accountSKs := make([]crypto.SecretKey, buckets)
	accountPKs := make([]string, buckets)
	for i := 0; i < int(buckets); i++ {
		sk, spk := prepareAccount()
		accountSKs[i] = sk
		accountPKs[i] = spk.String()
	}

	// Fund all acounts to the max
	for _, acc := range accountPKs {
		if err = am.callDeposit(acc, maxBalance); err != nil {
			t.Fatal(err)
		}
	}

	cbh := am.h.blockHeight

	// Keep track of how many times the maxEpheramalAccountRisk was reached. We
	// assume that it works properly when this number exceeds 1, because this
	// means that it was also successful in decreasing the current risk when
	// the persist was successful
	var atomicMaxRiskReached uint64
	var wg sync.WaitGroup

	for i := 0; i < int(buckets); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			accPK := accountPKs[i]
			accSK := accountSKs[i]
			msg, sig := prepareWithdrawal(accPK, maxBalance, cbh, accSK)
			if wErr := callWithdraw(am, msg, sig); wErr == errMaxRiskReached {
				atomic.AddUint64(&atomicMaxRiskReached, 1)
			}
		}(i)
	}
	wg.Wait()

	if atomic.LoadUint64(&atomicMaxRiskReached) == 0 {
		t.Fatal("Max ephemeral account balance risk was not reached")
	}
}

// TestAccountIndexRecycling ensures that the account index of expired accounts
// properly recycle and are re-distributed among new accounts
func TestAccountIndexRecycling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a host & update its settings to expire accounts after 2s
	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	hIS := ht.host.InternalSettings()
	hIS.EphemeralAccountExpiry = 2
	err = ht.host.SetInternalSettings(hIS)
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	numAcc := 100
	accToIndex := make(map[string]uint32, numAcc)

	// deposit is a helper function to deposit 1H into the given account
	deposit := func(id string) {
		err := am.callDeposit(id, types.NewCurrency64(1))
		if err != nil {
			t.Fatal(err)
		}
	}

	// expire is a helper function that decides if an account should expire or
	// not. We expire all accounts where index%10==0
	expire := func(id string) bool {
		index, ok := accToIndex[id]
		if !ok {
			t.Fatal("Unexpected failure, account id unknown")
		}
		return index%10 == 0
	}

	// Prepare a number of accounts
	for i := 0; i < numAcc; i++ {
		_, pk := prepareAccount()
		id := pk.String()
		deposit(id)
		_, accToIndex[id], _, _ = am.managedAccountInfo(id)
	}

	// Keep accounts alive past the expire frequency by periodically depositing
	// into the account
	doneChan := make(chan struct{})
	keepAliveFreq := time.Second
	go func() {
		for {
			select {
			case <-doneChan:
				return
			case <-time.After(keepAliveFreq):
				for id := range accToIndex {
					if !expire(id) {
						deposit(id)
					}
				}
			}
		}
	}()
	// provide ample time for accounts to expire
	time.Sleep(pruneExpiredAccountsFrequency * 3)
	doneChan <- struct{}{}

	// Verify that only accounts which have been inactive for longer than the
	// account expiry threshold are expired
	for id, index := range accToIndex {
		_, _, _, exists := am.managedAccountInfo(id)
		if expire(id) && exists {
			t.Logf("Expected account at index %d to be expired\n", index)
			t.Fatal("PruneExpiredAccount failure")
		} else if !expire(id) && !exists {
			t.Logf("Expected account at index %d to be active\n", index)
			t.Fatal("PruneExpiredAccount failure")
		}
	}

	// For every expired index we want to create a new account, and verify that
	// the new account has recycled the index
	expired := make(map[uint32]bool)
	for id, index := range accToIndex {
		if expire(id) {
			expired[index] = true
			continue
		}
	}
	for i := len(expired); i > 0; i-- {
		_, pk := prepareAccount()
		deposit(pk.String())
		_, newIndex, _, _ := am.managedAccountInfo(pk.String())
		if _, ok := expired[newIndex]; !ok {
			t.Fatal("New account did not reuse a recycled index")
		}
		delete(expired, newIndex)
	}
}

// callWithdraw will perform the withdrawal using a timestamp for the priority
func callWithdraw(am *accountManager, msg *withdrawalMessage, sig crypto.Signature) error {
	return am.callWithdraw(msg, sig, time.Now().UnixNano())
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
