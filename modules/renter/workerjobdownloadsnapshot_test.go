package renter

import (
	"testing"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestCheckDownloadSnapshotGouging checks that the download snapshot price
// gouging checker is correctly detecting price gouging from a host.
func TestCheckDownloadSnapshotGouging(t *testing.T) {
	hes := modules.DefaultHostExternalSettings()

	allowance := modules.DefaultAllowance
	allowance.Funds = types.SiacoinPrecision.Mul64(1e3)
	allowance.MaxDownloadBandwidthPrice = hes.DownloadBandwidthPrice.Mul64(2)
	allowance.MaxUploadBandwidthPrice = hes.UploadBandwidthPrice.Mul64(2)
	allowance.ExpectedDownload = 1 << 30 // 1GiB

	priceTable := modules.RPCPriceTable{
		ReadBaseCost:          hes.SectorAccessPrice,
		ReadLengthCost:        types.NewCurrency64(1),
		InitBaseCost:          hes.BaseRPCPrice,
		DownloadBandwidthCost: hes.DownloadBandwidthPrice,
		UploadBandwidthCost:   hes.UploadBandwidthPrice,
	}

	// verify basic case
	err := checkDownloadSnapshotGouging(allowance, priceTable)
	if err != nil {
		t.Fatal("unexpected failure", err)
	}

	// verify high init costs
	gougingPriceTable := priceTable
	gougingPriceTable.InitBaseCost = types.SiacoinPrecision
	gougingPriceTable.ReadBaseCost = types.SiacoinPrecision
	err = checkDownloadSnapshotGouging(allowance, gougingPriceTable)
	if err == nil {
		t.Fatal("unexpected outcome", err)
	}

	// verify DL bandwidth gouging
	gougingPriceTable = priceTable
	gougingPriceTable.DownloadBandwidthCost = allowance.MaxDownloadBandwidthPrice.Mul64(2)
	err = checkDownloadSnapshotGouging(allowance, gougingPriceTable)
	if err == nil {
		t.Fatal("unexpected outcome", err)
	}
}
