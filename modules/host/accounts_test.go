package host

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	accountID = "8e8ed34..."
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

	_, err = am.callDeposit(accountID, diff)
	if err != nil {
		t.Fatal(err)
	}

	after := am.accounts[accountID]
	if !after.Sub(before).Equals(diff) {
		t.Fatal("Deposit was not credited")
	}

	_, err = am.callDeposit(accountID, accountMaxBalance)
	if err != errMaxBalanceExceeded {
		t.Fatal(err)
	}
}

// TestAccountCallSpend verifies we can spend from an account, but also that
// spending has a blocking behaviour with a timeout
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
	_, err = am.callDeposit(accountID, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Spend half of it and verify account balance
	err = am.callSpend(accountID, types.NewCurrency64(5), crypto.Hash{})
	if err != nil {
		t.Fatal(err)
	}
	if !am.balanceOf(accountID).Equals(types.NewCurrency64(5)) {
		t.Fatal("Account balance was incorrect after spend")
	}

	// Spend more than the account holds, have it block and then fund it to
	go am.callDeposit(accountID, types.NewCurrency64(3))
	err = am.callSpend(accountID, types.NewCurrency64(7), crypto.Hash{})
	if err != nil {
		t.Fatal(err)
	}

	if !am.balanceOf(accountID).Equals(types.NewCurrency64(1)) {
		t.Fatal("Account balance was incorrect after spend")
	}

	// Spend from an unknown account and verify it timed out
	err = am.callSpend(accountID+"unknown", types.NewCurrency64(5), crypto.Hash{})
	if err != errBlockedCallTimeout {
		t.Fatal(err)
	}
}
