package contractor

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// contractWithSize is a helper function that creates a dummy file contract with a certain size.
func contractWithSize(size uint64) modules.RenterContract {
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{{NewFileSize: size}},
	}
	return modules.RenterContract{Transaction: txn}
}

// TestCanChurnContract tests the functionality of managedCanChurnContract
func TestCanChurnContract(t *testing.T) {
	// This method doesn't use the contractor so this is safe.
	cl := newChurnLimiter(nil)
	cl.maxPeriodChurn = 1000

	// Test: Not enough remainingChurnBudget
	cl.remainingChurnBudget = 499
	cl.aggregateCurrentPeriodChurn = 0
	ok := cl.managedCanChurnContract(contractWithSize(500))
	if ok {
		t.Fatal("Expected not to be able to churn contract")
	}

	// Test: just enough remainingChurnBudget.
	cl.remainingChurnBudget = 500
	cl.aggregateCurrentPeriodChurn = 0
	ok = cl.managedCanChurnContract(contractWithSize(500))
	if !ok {
		t.Fatal("Expected to be able to churn contract")
	}

	// Test: not enough period budget.
	cl.remainingChurnBudget = 500
	cl.aggregateCurrentPeriodChurn = 501
	ok = cl.managedCanChurnContract(contractWithSize(500))
	if ok {
		t.Fatal("Expected not to be able to churn contract")
	}

	// Test: just enough period budget.
	cl.remainingChurnBudget = 500
	cl.aggregateCurrentPeriodChurn = 500
	ok = cl.managedCanChurnContract(contractWithSize(500))
	if !ok {
		t.Fatal("Expected to be able to churn contract")
	}

	// Test: can churn contract bigger than remainingChurnBudget as long as
	// remainingChurnBudget is max and churn fits in period budget.
	cl.remainingChurnBudget = 500
	cl.aggregateCurrentPeriodChurn = 1
	ok = cl.managedCanChurnContract(contractWithSize(999))
	if !ok {
		t.Fatal("Expected to be able to churn contract")
	}

	// Test: can churn any size contract if no aggregateChurn in period and max remainingChurnBudget.
	cl.remainingChurnBudget = 500
	cl.aggregateCurrentPeriodChurn = 0
	ok = cl.managedCanChurnContract(contractWithSize(999999999999999999))
	if !ok {
		t.Fatal("Expected to be able to churn contract")
	}

	// Test: avoid churning big contracts if any aggregate churn in the period was found
	cl.remainingChurnBudget = 1000
	cl.aggregateCurrentPeriodChurn = 1
	ok = cl.managedCanChurnContract(contractWithSize(999999999999999999))
	if ok {
		t.Fatal("Expected not to be able to churn contract")
	}
}
