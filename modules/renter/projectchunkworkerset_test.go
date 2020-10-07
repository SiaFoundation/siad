package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestPCWSGouging checks that the gouging check is triggering at the right
// times.
func TestPCWSGouging(t *testing.T) {
	// Create some defaults to get some intuitive ideas for gouging.
	//
	// 100 workers and 1e9 expected download means ~2e6 HasSector queries will
	// be performed.
	pt := modules.RPCPriceTable{
		InitBaseCost:          types.NewCurrency64(1e3),
		DownloadBandwidthCost: types.NewCurrency64(1e3),
		UploadBandwidthCost:   types.NewCurrency64(1e3),
		HasSectorBaseCost:     types.NewCurrency64(1e6),
	}
	allowance := modules.Allowance{
		MaxDownloadBandwidthPrice: types.NewCurrency64(2e3),
		MaxUploadBandwidthPrice:   types.NewCurrency64(2e3),

		Funds: types.NewCurrency64(1e18),

		ExpectedDownload: 1e9, // 1 GiB
	}
	numWorkers := 100
	numRoots := 30

	// Check that the gouging passes for normal values.
	err := checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err != nil {
		t.Error(err)
	}

	// Check with high init base cost.
	pt.InitBaseCost = types.NewCurrency64(1e12)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	pt.InitBaseCost = types.NewCurrency64(1e3)

	// Check with high upload bandwidth cost.
	pt.UploadBandwidthCost = types.NewCurrency64(1e12)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	pt.UploadBandwidthCost = types.NewCurrency64(1e3)

	// Check with high download bandwidth cost.
	pt.DownloadBandwidthCost = types.NewCurrency64(1e12)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	pt.DownloadBandwidthCost = types.NewCurrency64(1e3)

	// Check with high HasSector cost.
	pt.HasSectorBaseCost = types.NewCurrency64(1e12)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	pt.HasSectorBaseCost = types.NewCurrency64(1e6)

	// Check with low MaxDownloadBandwidthPrice.
	allowance.MaxDownloadBandwidthPrice = types.NewCurrency64(100)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	allowance.MaxDownloadBandwidthPrice = types.NewCurrency64(2e3)

	// Check with low MaxUploadBandwidthPrice.
	allowance.MaxUploadBandwidthPrice = types.NewCurrency64(100)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	allowance.MaxUploadBandwidthPrice = types.NewCurrency64(2e3)

	// Check with reduced funds.
	allowance.Funds = types.NewCurrency64(1e15)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	allowance.Funds = types.NewCurrency64(1e18)

	// Check with increased expected download.
	allowance.ExpectedDownload = 1e12
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	allowance.ExpectedDownload = 1e9

	// Check that the base allowanace still passes. (ensures values have been
	// reset correctly)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err != nil {
		t.Error(err)
	}
}
