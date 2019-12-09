package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestCheckFetchBackupsGouging checks that the fetch backups price gouging
// checker is correctly detecting price gouging from a host.
func TestCheckFetchBackupsGouging(t *testing.T) {
	oneCurrency := types.NewCurrency64(1)

	// minAllowance contians only the fields necessary to test the price gouging
	// function. The min allowance doesn't include any of the max prices,
	// because the function will ignore them if they are not set.
	minAllowance := modules.Allowance{
		// Funds is set such that the tests come out to an easy, round number.
		// One siacoin is multiplied by the number of elements that are checked
		// for gouging, and then divided by the gounging denominator.
		Funds: types.SiacoinPrecision.Mul64(3).Div64(fetchBackupsGougingFractionDenom).Sub(oneCurrency),

		ExpectedDownload: modules.StreamDownloadSize, // 1 stream download operation.
	}
	// minHostSettings contains only the fields necessary to test the price
	// gouging function.
	//
	// The cost is set to be exactly equal to the price gouging limit, such that
	// slightly decreasing any of the values evades the price gouging detector.
	minHostSettings := modules.HostExternalSettings{
		BaseRPCPrice:           types.SiacoinPrecision,
		DownloadBandwidthPrice: types.SiacoinPrecision.Div64(modules.StreamDownloadSize),
		SectorAccessPrice:      types.SiacoinPrecision,
	}

	err := checkFetchBackupsGouging(minAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting price gouging check to fail:", err)
	}

	// Drop the host prices one field at a time.
	newHostSettings := minHostSettings
	newHostSettings.BaseRPCPrice = minHostSettings.BaseRPCPrice.Mul64(100).Div64(101)
	err = checkFetchBackupsGouging(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}
	newHostSettings = minHostSettings
	newHostSettings.DownloadBandwidthPrice = minHostSettings.DownloadBandwidthPrice.Mul64(100).Div64(101)
	err = checkFetchBackupsGouging(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}
	newHostSettings = minHostSettings
	newHostSettings.SectorAccessPrice = minHostSettings.SectorAccessPrice.Mul64(100).Div64(101)
	err = checkFetchBackupsGouging(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}

	// Set min settings on the allowance that are just below that should be
	// acceptable.
	maxAllowance := minAllowance
	maxAllowance.Funds = maxAllowance.Funds.Add(oneCurrency)
	maxAllowance.MaxRPCPrice = types.SiacoinPrecision.Add(oneCurrency)
	maxAllowance.MaxContractPrice = oneCurrency
	maxAllowance.MaxDownloadBandwidthPrice = types.SiacoinPrecision.Div64(modules.StreamDownloadSize).Add(oneCurrency)
	maxAllowance.MaxSectorAccessPrice = types.SiacoinPrecision.Add(oneCurrency)
	maxAllowance.MaxStoragePrice = oneCurrency
	maxAllowance.MaxUploadBandwidthPrice = oneCurrency

	// The max allowance should have no issues with price gouging.
	err = checkFetchBackupsGouging(maxAllowance, minHostSettings)
	if err != nil {
		t.Fatal(err)
	}

	// Should fail if the MaxRPCPrice is dropped.
	failAllowance := maxAllowance
	failAllowance.MaxRPCPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = checkFetchBackupsGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting price gouging check to fail")
	}

	// Should fail if the MaxDownloadBandwidthPrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxDownloadBandwidthPrice = types.SiacoinPrecision.Div64(modules.StreamDownloadSize).Sub(oneCurrency)
	err = checkFetchBackupsGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting price gouging check to fail")
	}

	// Should fail if the MaxSectorAccessPrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxSectorAccessPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = checkFetchBackupsGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting price gouging check to fail")
	}
}
