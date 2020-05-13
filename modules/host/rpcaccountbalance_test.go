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
	am := rhp.staticHT.host.staticAccountManager
	h := rhp.staticHT.host

	// The balance should be 0.
	balance := am.callAccountBalance(rhp.staticAccountID)
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}

	// fetch the balance and pay for it by contract.
	balance, err = rhp.managedAccountBalance(true)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}

	// The balance should still be 0 at this point.
	balance = am.callAccountBalance(rhp.staticAccountID)
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}

	// Fund the account.
	his := h.managedInternalSettings()
	_, err = rhp.managedFundEphemeralAccount(his.MaxEphemeralAccountBalance, false)
	if err != nil {
		t.Fatal(err)
	}

	// The balance should be the funded amount minus the cost for funding it.
	expectedBalance := his.MaxEphemeralAccountBalance.Sub(rhp.pt.FundAccountCost)
	balance = am.callAccountBalance(rhp.staticAccountID)
	if !balance.Equals(expectedBalance) {
		t.Fatalf("expectd balance to be %v but was %v", expectedBalance.HumanString(), balance.HumanString())
	}

	// Fetch the balance.
	expectedBalance = expectedBalance.Sub(rhp.pt.AccountBalanceCost)
	balance, err = rhp.managedAccountBalance(false)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.Equals(expectedBalance) {
		t.Fatalf("expectd balance to be %v but was %v", expectedBalance.HumanString(), balance.HumanString())
	}
}
