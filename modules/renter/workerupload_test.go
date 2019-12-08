package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestCheckUploadExtortion checks that the upload extortion checker is
// correctly detecting extortion from a host.
func TestCheckUploadExtortion(t *testing.T) {
	// minAllowance contians only the fields necessary to test the extortion
	// function. The min allowance doesn't include any of the max prices,
	// because the function will ignore them if they are not set.
	minAllowance := modules.Allowance{
		Funds:  types.SiacoinPrecision, // One siacoin.
		Period: 1,                      // 1 block.

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
		t.Fatal(err)
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
}
