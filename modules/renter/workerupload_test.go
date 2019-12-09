package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestCheckUploadExtortion checks that the upload extortion checker is
// correctly detecting extortion from a host.
func TestCheckUploadExtortion(t *testing.T) {
	oneCurrency := types.NewCurrency64(1)

	// minAllowance contians only the fields necessary to test the extortion
	// function. The min allowance doesn't include any of the max prices,
	// because the function will ignore them if they are not set.
	minAllowance := modules.Allowance{
		Funds:  types.SiacoinPrecision.Sub(oneCurrency), // One siacoin, plus a tiny bit for the '>' vs. the '>='
		Period: 1,                                       // 1 block.

		ExpectedStorage: modules.StreamUploadSize, // 1 stream upload operation.
	}
	// minHostSettings contains only the fields necessary to test the extortion
	// function.
	//
	// The cost is set to be exactly equal to the extortion limit, such that
	// slightly decreasing any of the values evades the extortion detector.
	minHostSettings := modules.HostExternalSettings{
		BaseRPCPrice:         types.SiacoinPrecision,
		SectorAccessPrice:    types.SiacoinPrecision,
		UploadBandwidthPrice: types.SiacoinPrecision.Div64(modules.StreamUploadSize),
		StoragePrice:         types.SiacoinPrecision.Div64(modules.StreamUploadSize),
	}

	err := staticCheckUploadExtortion(minAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting extortion check to fail:", err)
	}

	// Drop the host prices one field at a time.
	newHostSettings := minHostSettings
	newHostSettings.BaseRPCPrice = minHostSettings.BaseRPCPrice.Mul64(100).Div64(101)
	err = staticCheckUploadExtortion(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}
	newHostSettings = minHostSettings
	newHostSettings.SectorAccessPrice = minHostSettings.SectorAccessPrice.Mul64(100).Div64(101)
	err = staticCheckUploadExtortion(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}
	newHostSettings = minHostSettings
	newHostSettings.UploadBandwidthPrice = minHostSettings.UploadBandwidthPrice.Mul64(100).Div64(101)
	err = staticCheckUploadExtortion(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}
	newHostSettings = minHostSettings
	newHostSettings.StoragePrice = minHostSettings.StoragePrice.Mul64(100).Div64(101)
	err = staticCheckUploadExtortion(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}

	// Set min settings on the allowance that are just below that should be
	// acceptable.
	maxAllowance := minAllowance
	maxAllowance.Funds = maxAllowance.Funds.Add(oneCurrency)
	maxAllowance.MaxRPCPrice = types.SiacoinPrecision.Add(oneCurrency)
	maxAllowance.MaxContractPrice = oneCurrency
	maxAllowance.MaxDownloadBandwidthPrice = oneCurrency
	maxAllowance.MaxSectorAccessPrice = types.SiacoinPrecision.Add(oneCurrency)
	maxAllowance.MaxStoragePrice = types.SiacoinPrecision.Div64(modules.StreamUploadSize).Add(oneCurrency)
	maxAllowance.MaxUploadBandwidthPrice = types.SiacoinPrecision.Div64(modules.StreamUploadSize).Add(oneCurrency)

	// The max allowance should have no issues with extortion.
	err = staticCheckUploadExtortion(maxAllowance, minHostSettings)
	if err != nil {
		t.Fatal(err)
	}

	// Should fail if the MaxRPCPrice is dropped.
	failAllowance := maxAllowance
	failAllowance.MaxRPCPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = staticCheckUploadExtortion(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting extortion check to fail")
	}

	// Should fail if the MaxSectorAccessPrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxSectorAccessPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = staticCheckUploadExtortion(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting extortion check to fail")
	}

	// Should fail if the MaxStoragePrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxStoragePrice = types.SiacoinPrecision.Div64(modules.StreamUploadSize).Sub(oneCurrency)
	err = staticCheckUploadExtortion(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting extortion check to fail")
	}

	// Should fail if the MaxUploadBandwidthPrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxUploadBandwidthPrice = types.SiacoinPrecision.Div64(modules.StreamUploadSize).Sub(oneCurrency)
	err = staticCheckUploadExtortion(failAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting extortion check to fail")
	}
}
