package main

import (
	"fmt"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestParseFees tests the parseFees function to ensure expected return values
func TestParseFees(t *testing.T) {
	// Create AppUIDs
	cheapApp := modules.AppUID("cheapApp")
	expensiveApp := modules.AppUID("expensiveApp")

	// Create FeeUIDs
	feeUID1 := modules.FeeUID("fee1")
	feeUID2 := modules.FeeUID("fee2")
	feeUID3 := modules.FeeUID("fee3")
	feeUID4 := modules.FeeUID("fee4")

	// Create Fee Amounts
	feeAmount1 := types.NewCurrency64(fastrand.Uint64n(1000))
	feeAmount2 := feeAmount1.Add(types.NewCurrency64(fastrand.Uint64n(1000)))
	feeAmount3 := feeAmount2.Add(types.NewCurrency64(fastrand.Uint64n(1000)))

	// Create Fees
	cheapFee1 := modules.AppFee{
		Amount: feeAmount1,
		AppUID: cheapApp,
		FeeUID: feeUID1,
	}
	cheapFee2 := modules.AppFee{
		Amount: feeAmount2,
		AppUID: cheapApp,
		FeeUID: feeUID2,
	}
	expensiveFee1 := modules.AppFee{
		Amount: feeAmount1,
		AppUID: expensiveApp,
		FeeUID: feeUID1,
	}
	expensiveFee2 := modules.AppFee{
		Amount:       feeAmount2,
		AppUID:       expensiveApp,
		FeeUID:       feeUID2,
		PayoutHeight: 100,
	}
	expensiveFee3 := modules.AppFee{
		Amount: feeAmount2,
		AppUID: expensiveApp,
		FeeUID: feeUID3,
	}
	expensiveFee4 := modules.AppFee{
		Amount: feeAmount3,
		AppUID: expensiveApp,
		FeeUID: feeUID4,
	}

	// Create unsorted list
	fees := []modules.AppFee{cheapFee1, cheapFee2, expensiveFee1, expensiveFee2, expensiveFee3, expensiveFee4}
	// Create expected sorted list
	expectedOrder := []feeInfo{
		{
			AppUID:      expensiveApp,
			Fees:        []modules.AppFee{expensiveFee4, expensiveFee3, expensiveFee2, expensiveFee1},
			TotalAmount: feeAmount1.Add(feeAmount2.Add(feeAmount2.Add(feeAmount3))),
		},
		{

			AppUID:      cheapApp,
			Fees:        []modules.AppFee{cheapFee2, cheapFee1},
			TotalAmount: feeAmount1.Add(feeAmount2),
		},
	}

	// Parse the Fees
	parsedFees, totalAmount := parseFees(fees)

	// Check the total
	expectedTotal := expectedOrder[0].TotalAmount.Add(expectedOrder[1].TotalAmount)
	if totalAmount.Cmp(expectedTotal) != 0 {
		t.Errorf("Expected total to be %v but was %v", expectedTotal.HumanString(), totalAmount.HumanString())
	}

	// Check the sorting of the fees
	if !reflect.DeepEqual(parsedFees, expectedOrder) {
		fmt.Println("Expected Order:")
		siatest.PrintJSON(expectedOrder)
		fmt.Println("Parsed Order:")
		siatest.PrintJSON(parsedFees)
		t.Fatal("Fees not sorted as expected")
	}
}
