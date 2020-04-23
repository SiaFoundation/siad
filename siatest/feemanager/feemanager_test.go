package feemanager

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/feemanager"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestFeeManager probes the FeeManager
func TestFeeManager(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new FeeManager
	testDir := feeManagerTestDir(t.Name())
	fm, err := siatest.NewCleanNode(node.FeeManager(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fm.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Check for initial values
	fmg, err := fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	nexPayoutHeight := fmg.Settings.PayoutHeight
	if nexPayoutHeight == 0 {
		t.Fatalf("PayoutHeight is still 0")
	}

	// Define helper checkFees function
	var numPaidFees, numPendingFees int
	checkFees := func() (api.FeeManagerPaidFeesGET, api.FeeManagerPendingFeesGET) {
		fmPaidGet, err := fm.FeeManagerPaidFeesGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(fmPaidGet.PaidFees) != numPaidFees {
			t.Errorf("Expected %v PaidFees but got %v", numPaidFees, len(fmPaidGet.PaidFees))
		}
		fmPendingGet, err := fm.FeeManagerPendingFeesGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(fmPendingGet.PendingFees) != numPendingFees {
			t.Errorf("Expected %v PendingFees but got %v", numPendingFees, len(fmPendingGet.PendingFees))
		}
		return fmPaidGet, fmPendingGet
	}

	// Check initial fees
	_, _ = checkFees()

	// Set a Fee
	amount := types.NewCurrency64(1000)
	address := types.UnlockHash{}
	appUID := modules.AppUID("testapp")
	recurring := fastrand.Intn(2) == 0
	fmap, err := fm.FeeManagerAddPost(address, amount, appUID, recurring)
	if err != nil {
		t.Fatal(err)
	}

	// Check for Fees
	numPendingFees = 1
	_, fmpfg := checkFees()

	fee := fmpfg.PendingFees[0]
	if fee.Address != address {
		t.Errorf("Expected address to be %v but was %v", address, fee.Address)
	}
	if fee.Amount.Cmp(amount) != 0 {
		t.Errorf("Expected amount to be %v but was %v", amount, fee.Amount)
	}
	if fee.AppUID != appUID {
		t.Errorf("Expected AppUID to be %v but was %v", appUID, fee.AppUID)
	}
	if fee.PaymentCompleted {
		t.Error("PaymentCompleted should be false")
	}
	payoutHeight := nexPayoutHeight + feemanager.PayoutInterval
	if fee.PayoutHeight != payoutHeight {
		t.Errorf("Expected PayoutHeight to be %v but was %v", payoutHeight, fee.PayoutHeight)
	}
	if fee.Recurring != recurring {
		t.Errorf("Expected Recurring to be %v but was %v", recurring, fee.Recurring)
	}
	if fee.Timestamp == 0 {
		t.Error("Timestamp is not set")
	}
	if fee.TransactionCreated {
		t.Error("TransactionCreated should be false")
	}
	if fee.UID != fmap.FeeUID {
		t.Errorf("Expected UID to be %v but was %v", fmap.FeeUID, fee.UID)
	}

	// Cancel Fee
	err = fm.FeeManagerCancelPost(fee.UID)
	if err != nil {
		t.Fatal(err)
	}

	// Check for Fees
	numPendingFees = 0
	_, _ = checkFees()
}
