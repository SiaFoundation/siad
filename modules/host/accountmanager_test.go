package host

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	accountID = "ed25519:8e8ed34020d3c0818c2070c974effbf4ed04d9cdb32a23ce2b869de143473067"
)

// TestEphemeralAccountDeposit checks that the amount deposited gets credited to
// the account owner's balance
func TestEphemeralAccountDeposit(t *testing.T) {
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

	err = am.managedDeposit(accountID, diff)
	if err != nil {
		t.Fatal(err)
	}

	after := am.accounts[accountID]
	if !after.Sub(before).Equals(diff) {
		t.Fatal("Deposit was not credited")
	}

	err = am.managedDeposit(accountID, accountMaxBalance)
	if err != errMaxBalanceExceeded {
		t.Fatal(err)
	}
}
