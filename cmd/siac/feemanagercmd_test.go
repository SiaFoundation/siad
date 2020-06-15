package main

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestParseFees tests the parseFees function to ensure expected return values
func TestParseFees(t *testing.T) {
	// Create fees
	cheapApp := modules.AppUID("cheapApp")
	expensiveApp := modules.AppUID("expensiveApp")
	fees := []modules.AppFee{
		{
			Amount: types.NewCurrency64(200),
			AppUID: cheapApp,
		},
		{
			Amount: types.NewCurrency64(100),
			AppUID: cheapApp,
		},
		{
			Amount: types.NewCurrency64(100),
			AppUID: expensiveApp,
		},
		{
			Amount:       types.NewCurrency64(200),
			AppUID:       expensiveApp,
			PayoutHeight: 100,
		},
		{
			Amount: types.NewCurrency64(200),
			AppUID: expensiveApp,
		},
		{
			Amount: types.NewCurrency64(300),
			AppUID: expensiveApp,
		},
	}

	// Parse the Fees
	parsedFees, totalAmount := parseFees(fees)

	// Check the total
	var sumTotal types.Currency
	for _, fee := range fees {
		sumTotal = sumTotal.Add(fee.Amount)
	}
	if totalAmount.Cmp(sumTotal) != 0 {
		t.Errorf("Expected total to be %v but was %v", sumTotal, totalAmount)
	}

	// Check the sorting of the fees
	for i := 0; i < len(parsedFees); i++ {
		switch i {
		case 0:
			if parsedFees[i].appUID != expensiveApp {
				t.Errorf("FeeInfos not sorted, expected AppUID of %v but got %v", expensiveApp, parsedFees[i].appUID)
			}
		case 1:
			if parsedFees[i].appUID != cheapApp {
				t.Errorf("FeeInfos not sorted, expected AppUID of %v but got %v", cheapApp, parsedFees[i].appUID)
			}
		default:
			t.Errorf("Expected 2 fee infos but got %v", len(parsedFees))
		}

		pfFees := parsedFees[i].fees
		prevAmount := pfFees[0].Amount
		appFeeTotal := prevAmount
		for j := 1; j < len(pfFees); j++ {
			currentAmount := pfFees[j].Amount
			if prevAmount.Cmp(currentAmount) < 0 {
				t.Log("prevAmount", prevAmount)
				t.Log("current amount", currentAmount)
				t.Error("Fees not sorted by amount")
			}
			if prevAmount.Cmp(currentAmount) == 0 && pfFees[j].PayoutHeight > pfFees[j-1].PayoutHeight {
				t.Errorf("Fees not sorted by PayoutHeight when Amounts are equal; Current %v Previous %v", pfFees[j].PayoutHeight, pfFees[j-1].PayoutHeight)
			}
			prevAmount = currentAmount
			appFeeTotal = appFeeTotal.Add(currentAmount)
		}
		if parsedFees[i].totalAmount.Cmp(appFeeTotal) != 0 {
			t.Errorf("Expected total to be %v but was %v", appFeeTotal, parsedFees[i].totalAmount)
		}
	}
}
