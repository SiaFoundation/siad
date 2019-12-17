package contractor

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestCheckFormContractGouging checks that the upload price gouging checker is
// correctly detecting price gouging from a host.
//
// Test looks a bit funny because it was adapated from the other price gouging
// tests.
func TestCheckFormContractGouging(t *testing.T) {
	oneCurrency := types.NewCurrency64(1)

	// minAllowance contains only the fields necessary to test the price gouging
	// function. The min allowance doesn't include any of the max prices,
	// because the function will ignore them if they are not set.
	minAllowance := modules.Allowance{}
	// minHostSettings contains only the fields necessary to test the price
	// gouging function.
	//
	// The cost is set to be exactly equal to the price gouging limit, such that
	// slightly decreasing any of the values evades the price gouging detector.
	minHostSettings := modules.HostExternalSettings{
		BaseRPCPrice:  types.SiacoinPrecision,
		ContractPrice: types.SiacoinPrecision,
	}

	// Set min settings on the allowance that are just below that should be
	// acceptable.
	maxAllowance := minAllowance
	maxAllowance.Funds = maxAllowance.Funds.Add(oneCurrency)
	maxAllowance.MaxRPCPrice = types.SiacoinPrecision.Add(oneCurrency)
	maxAllowance.MaxContractPrice = types.SiacoinPrecision.Add(oneCurrency)
	maxAllowance.MaxDownloadBandwidthPrice = oneCurrency
	maxAllowance.MaxSectorAccessPrice = oneCurrency
	maxAllowance.MaxStoragePrice = oneCurrency
	maxAllowance.MaxUploadBandwidthPrice = oneCurrency

	// The max allowance should have no issues with price gouging.
	err := checkFormContractGouging(maxAllowance, minHostSettings)
	if err != nil {
		t.Fatal(err)
	}

	// Should fail if the MaxRPCPrice is dropped.
	failAllowance := maxAllowance
	failAllowance.MaxRPCPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = checkFormContractGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting price gouging check to fail")
	}

	// Should fail if the MaxContractPrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxContractPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = checkFormContractGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting price gouging check to fail")
	}
}
