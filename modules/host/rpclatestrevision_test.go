package host

import (
	"reflect"
	"strings"
	"testing"

	"go.sia.tech/siad/modules"
)

// TestLatestRevision tests fetching the latest revision of a contract from the
// host using RPCLatestRevision.
func TestLatestRevision(t *testing.T) {
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

	// Test the standard flow.
	t.Run("Basic", func(t *testing.T) {
		testLatestRevisionBasic(t, rhp)
	})
	// Test that case that checks for a correct refund.
	t.Run("Refund", func(t *testing.T) {
		testLatestRevisionRefund(t, rhp)
	})
	// Test that case that check for ErrInsufficientBudget.
	t.Run("ErrInsufficientBudget", func(t *testing.T) {
		testLatestRevisionInsufficientBudget(t, rhp)
	})
}

// testLatestRevisionBasic tests the basic case for fetching the latest revision
// from the host.
func testLatestRevisionBasic(t *testing.T, rhp *renterHostPair) {
	host := rhp.staticHT.host

	// fund the account.
	_, err := rhp.managedFundEphemeralAccount(rhp.pt.FundAccountCost.Add(rhp.pt.LatestRevisionCost), false)
	if err != nil {
		t.Fatal(err)
	}

	// get the latest revision.
	so, err := host.managedGetStorageObligationSnapshot(rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}
	rev := so.RecentRevision()

	// fetch the revision and pay for it by EA.
	recentRev, err := rhp.LatestRevision(false)
	if err != nil {
		t.Fatal(err)
	}

	// recentRev should match rev.
	if !reflect.DeepEqual(rev, recentRev) {
		t.Log(rev)
		t.Log(recentRev)
		t.Fatal("revisions don't match")
	}
}

// testLatestRevisionRefund tests the refund a host is expected to grant.
func testLatestRevisionRefund(t *testing.T, rhp *renterHostPair) {
	host := rhp.staticHT.host
	// get balance before test.
	maxBalance := host.managedInternalSettings().MaxEphemeralAccountBalance
	balanceBefore := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	fundingAmt := rhp.pt.LatestRevisionCost.Add(maxBalance).Sub(balanceBefore)
	// fetch the balance and pay for it by contract.
	_, err := rhp.managedLatestRevision(true, fundingAmt, rhp.staticAccountID, rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}

	balance := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	if !balance.Equals(maxBalance) {
		t.Fatalf("expected balance to be %v but was %v", maxBalance, balance)
	}
}

// testLatestRevisionInsufficientBudget tests that the host checks the
// payment for the right amount.
func testLatestRevisionInsufficientBudget(t *testing.T, rhp *renterHostPair) {
	// fundingAmt is insufficient by 1H.
	fundingAmt := rhp.pt.LatestRevisionCost.Sub64(1)
	// fetch the balance and pay for it by contract.
	_, err := rhp.managedLatestRevision(true, fundingAmt, rhp.staticAccountID, rhp.staticFCID)
	if err == nil || !strings.Contains(err.Error(), modules.ErrInsufficientPaymentForRPC.Error()) {
		t.Fatal("expected ErrInsufficientPaymentForRPC but got: ", err)
	}
}
