package host

import (
	"testing"
)

// TestAccountBalance verifies the AccountBalance RPC.
func TestAccountBalance(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	// The balance should be 0.
	balance := rhp.ht.host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}

	// fetch the balance and pay for it by contract.
	balance, err = rhp.AccountBalance(true)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}

	// The balance should still be 0 at this point.
	balance = rhp.ht.host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}

	// Fund the account.
	his := rhp.ht.host.managedInternalSettings()
	_, err = rhp.FundEphemeralAccount(his.MaxEphemeralAccountBalance, false)
	if err != nil {
		t.Fatal(err)
	}

	// The balance should be the funded amount minus the cost for funding it.
	expectedBalance := his.MaxEphemeralAccountBalance.Sub(rhp.pt.FundAccountCost)
	balance = rhp.ht.host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	if !balance.Equals(expectedBalance) {
		t.Fatalf("expectd balance to be %v but was %v", expectedBalance.HumanString(), balance.HumanString())
	}

	// Fetch the balance.
	expectedBalance = expectedBalance.Sub(rhp.pt.AccountBalanceCost)
	balance, err = rhp.AccountBalance(false)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.Equals(expectedBalance) {
		t.Fatalf("expectd balance to be %v but was %v", expectedBalance.HumanString(), balance.HumanString())
	}
}
