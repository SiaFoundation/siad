package accounting

import (
	"reflect"
	"testing"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
)

// TestAccounting tests the basic functionality of the accounting package
func TestAccounting(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Specific Methods
	t.Run("Accounting", testAccounting)
	t.Run("NewCustomAccounting", testNewCustomAccounting)
}

// testAccounting probes the Accounting method
func testAccounting(t *testing.T) {
	// Create new accounting
	testDir := accountingTestDir(t.Name())
	h, m, r, w, _ := testingParams()
	a, err := NewCustomAccounting(h, m, r, w, testDir, &dependencies.AccountingDisablePersistLoop{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = a.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Initial persistence should be empty
	a.mu.Lock()
	p := a.persistence
	a.mu.Unlock()
	if !reflect.DeepEqual(p, persistence{}) {
		t.Error("initial persistence should be empty")
	}

	// Check accounting
	ai, err := a.Accounting()
	if err != nil {
		t.Fatal(err)
	}
	// Check for a returned value
	expected := modules.AccountingInfo{
		Renter: ai.Renter,
		Wallet: ai.Wallet,
	}
	if !reflect.DeepEqual(ai, expected) {
		t.Error("accounting information is incorrect")
	}
	// Check renter explicitly
	if reflect.DeepEqual(ai.Renter, modules.RenterAccounting{}) {
		t.Error("renter accounting information is empty")
	}
	// Check wallet explicitly
	if reflect.DeepEqual(ai.Wallet, modules.WalletAccounting{}) {
		t.Error("wallet accounting information is empty")
	}

	// Persistence should have been updated
	a.mu.Lock()
	p = a.persistence
	a.mu.Unlock()
	ep := persistence{
		Renter: p.Renter,
		Wallet: p.Wallet,

		Timestamp: p.Timestamp,
	}
	if !reflect.DeepEqual(p, ep) {
		t.Error("persistence information is incorrect")
	}
	if !reflect.DeepEqual(p.Renter, ai.Renter) {
		t.Error("renter accounting persistence not updated")
	}
	if !reflect.DeepEqual(p.Wallet, ai.Wallet) {
		t.Error("wallet accounting persistence not updated")
	}
}

// testNewCustomAccounting probes the NewCustomAccounting function
func testNewCustomAccounting(t *testing.T) {
	// checkNew is a helper function to check NewCustomAccounting
	checkNew := func(h modules.Host, m modules.Miner, r modules.Renter, w modules.Wallet, dir string, deps modules.Dependencies, expectedErr error) {
		a, err := NewCustomAccounting(h, m, r, w, dir, deps)
		if err != expectedErr {
			t.Errorf("Expected %v, got %v", expectedErr, err)
		}
		if a == nil {
			return
		}
		err = a.Close()
		if err != nil {
			t.Error(err)
		}
	}

	// Create testing parameters
	testDir := accountingTestDir(t.Name())
	h, m, r, w, deps := testingParams()

	// Base Case
	checkNew(nil, nil, nil, w, testDir, deps, nil)

	// Check for nil wallet
	checkNew(nil, nil, nil, nil, testDir, deps, errNilWallet)

	// Check for blank persistDir
	checkNew(nil, nil, nil, w, "", deps, errNilPersistDir)

	// Check for nil Dependencies
	checkNew(nil, nil, nil, w, testDir, nil, errNilDeps)

	// Test optional modules
	//
	// Host
	checkNew(h, nil, nil, w, testDir, deps, nil)
	// Miner
	checkNew(nil, m, nil, w, testDir, deps, nil)
	// Renter
	checkNew(nil, nil, r, w, testDir, deps, nil)
}
