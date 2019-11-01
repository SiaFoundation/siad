package host

import (
	"crypto/rand"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	accountID = "8e8ed34"
)

// TestAccountCallDeposit verifies we can properly deposit money into an
// ephemeral account
func TestAccountCallDeposit(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester("TestEphemeralAccountDeposit")
	if err != nil {
		t.Fatal(err)
	}

	am := ht.host.staticAccountManager
	diff := types.NewCurrency64(100)
	before := am.accounts[accountID]

	err = am.callDeposit(accountID, diff)
	if err != nil {
		t.Fatal(err)
	}

	after := am.accounts[accountID]
	if !after.Sub(before).Equals(diff) {
		t.Fatal("Deposit was not credited")
	}

	err = am.callDeposit(accountID, accountMaxBalance)
	if err != errMaxBalanceExceeded {
		t.Fatal(err)
	}
}

// TestAccountCallSpend verifies we can spend from an account, but also that
// spending has a blocking behavior with a timeout
func TestAccountCallSpend(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester("TestEphemeralAccountSpend")
	if err != nil {
		t.Fatal(err)
	}

	// Fund the account
	am := ht.host.staticAccountManager
	err = am.callDeposit(accountID, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Spend half of it and verify account balance
	err = am.callSpend(accountID, types.NewCurrency64(5), randomHash())
	if err != nil {
		t.Fatal(err)
	}
	if !am.balanceOf(accountID).Equals(types.NewCurrency64(5)) {
		t.Fatal("Account balance was incorrect after spend")
	}

	// Spend more than the account holds, have it block and then fund it to
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = am.callDeposit(accountID, types.NewCurrency64(3))
	}()
	err = am.callSpend(accountID, types.NewCurrency64(7), randomHash())
	if err != nil {
		t.Fatal(err)
	}

	if !am.balanceOf(accountID).Equals(types.NewCurrency64(1)) {
		t.Fatal("Account balance was incorrect after spend")
	}

	// Spend from an unknown account and verify it timed out
	err = am.callSpend(accountID+"unknown", types.NewCurrency64(5), randomHash())
	if err != errInsufficientBalance {
		t.Fatal(err)
	}
}

// TestAccountExpiry verifies accounts expire and get pruned
func TestAccountExpiry(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankMockHostTester(&dependencies.HostExpireEphemeralAccounts{}, "TestAccountExpiry")
	if err != nil {
		t.Fatal(err)
	}

	// Deposit some money into the account
	am := ht.host.staticAccountManager
	err = am.callDeposit(accountID, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Verify the balance, sleep a bit and verify it is gone
	if !am.balanceOf(accountID).Equals(types.NewCurrency64(10)) {
		t.Fatal("Account balance was incorrect after deposit")
	}
	time.Sleep(pruneExpiredAccountsFrequency)
	if !am.balanceOf(accountID).Equals(types.NewCurrency64(0)) {
		t.Fatal("Account balance was incorrect after expiry")
	}

	// Verify it got pruned from the index list
	_, exists := am.accountIndices[accountID]
	if exists {
		t.Fatal("Account index of pruned account was not removed")
	}
}

// randomHash will return a randomly generated hash
func randomHash() crypto.Hash {
	bytes := make([]byte, 4)
	_, _ = rand.Read(bytes)
	return crypto.HashBytes(bytes)
}
