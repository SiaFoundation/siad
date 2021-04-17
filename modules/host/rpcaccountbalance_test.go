package host

import (
	"strings"
	"testing"

	"go.sia.tech/siad/modules"
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

	// Test the happy flow.
	t.Run("HappyFlow", func(t *testing.T) {
		testAccountBalanceBasic(t, rhp)
	})
	// Test that a random account has a zero balance.
	t.Run("RandomAccountZeroBalance", func(t *testing.T) {
		testAccountBalanceRandom(t, rhp)
	})
	// Test that case that checks for a correct refund.
	t.Run("Refund", func(t *testing.T) {
		testAccountBalanceRefund(t, rhp)
	})
	// Test that case that check for ErrInsufficientBudget.
	t.Run("ErrInsufficientBudget", func(t *testing.T) {
		testAccountBalanceErrInsufficientBudget(t, rhp)
	})
}

// testAccountBalanceBasic tests the basic happy-flow functionality of the
// AccountBalance RPC.
func testAccountBalanceBasic(t *testing.T, rhp *renterHostPair) {
	host := rhp.staticHT.host
	// The balance should be 0 at this point.
	balance := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0 at beginning of test.")
	}

	// fetch the balance and pay for it by contract.
	balance, err := rhp.AccountBalance(true)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}

	// The balance should still be 0 at this point.
	balance = host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}

	// Fund the account.
	his := host.managedInternalSettings()
	_, err = rhp.managedFundEphemeralAccount(his.MaxEphemeralAccountBalance, false)
	if err != nil {
		t.Fatal(err)
	}

	// The balance should be the funded amount minus the cost for funding it.
	expectedBalance := his.MaxEphemeralAccountBalance.Sub(rhp.pt.FundAccountCost)
	balance = host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	if !balance.Equals(expectedBalance) {
		t.Fatalf("expectd balance to be %v but was %v", expectedBalance.HumanString(), balance.HumanString())
	}

	// Fetch the balance.
	expectedBalance = expectedBalance.Sub(rhp.pt.AccountBalanceCost)
	balance, err = rhp.managedAccountBalance(false, rhp.pt.AccountBalanceCost, rhp.staticAccountID, rhp.staticAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.Equals(expectedBalance) {
		t.Fatalf("expectd balance to be %v but was %v", expectedBalance.HumanString(), balance.HumanString())
	}
}

// testAccountBalanceRandom tests checking the balance for a random account.
func testAccountBalanceRandom(t *testing.T, rhp *renterHostPair) {
	// create random account id.
	_, accountID := prepareAccount()
	// fetch the balance and pay for it by contract.
	balance, err := rhp.managedAccountBalance(true, rhp.pt.AccountBalanceCost, rhp.staticAccountID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.IsZero() {
		t.Fatal("expected balance to be 0")
	}
}

// testAccountBalanceRefund tests the refund a host is expected to grant.
func testAccountBalanceRefund(t *testing.T, rhp *renterHostPair) {
	host := rhp.staticHT.host
	// get balance before test.
	maxBalance := host.managedInternalSettings().MaxEphemeralAccountBalance
	balanceBefore := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	fundingAmt := rhp.pt.AccountBalanceCost.Add(maxBalance).Sub(balanceBefore)
	// fetch the balance and pay for it by contract.
	balance, err := rhp.managedAccountBalance(true, fundingAmt, rhp.staticAccountID, rhp.staticAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if !balance.Equals(maxBalance) {
		t.Fatalf("expected balance to be %v but was %v", maxBalance, balance)
	}
}

// testAccountBalanceErrInsufficientBudget tests that the host checks the
// payment for the right amount.
func testAccountBalanceErrInsufficientBudget(t *testing.T, rhp *renterHostPair) {
	// fundingAmt is insufficient by 1H.
	fundingAmt := rhp.pt.AccountBalanceCost.Sub64(1)
	// fetch the balance and pay for it by contract.
	_, err := rhp.managedAccountBalance(true, fundingAmt, rhp.staticAccountID, rhp.staticAccountID)
	if err == nil || !strings.Contains(err.Error(), modules.ErrInsufficientPaymentForRPC.Error()) {
		t.Fatal("expected ErrInsufficientPaymentForRPC but got: ", err)
	}
}
