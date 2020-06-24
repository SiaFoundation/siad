package renter

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestPDBRGouging checks that `checkPDBRGouging` is correctly detecting price
// gouging from a host.
func TestPDBRGouging(t *testing.T) {
	t.Parallel()

	// allowance contains only the fields necessary to test the price gouging
	hes := modules.DefaultHostExternalSettings()
	allowance := modules.Allowance{
		Funds:                     types.SiacoinPrecision.Mul64(1e3),
		MaxDownloadBandwidthPrice: hes.DownloadBandwidthPrice.Mul64(10),
		MaxUploadBandwidthPrice:   hes.UploadBandwidthPrice.Mul64(10),
	}

	// verify happy case
	pt := newDefaultPriceTable()
	err := checkPDBRGouging(pt, allowance)
	if err != nil {
		t.Fatal("unexpected price gouging failure", err)
	}

	// verify max download bandwidth price gouging
	pt = newDefaultPriceTable()
	pt.DownloadBandwidthCost = allowance.MaxDownloadBandwidthPrice.Add64(1)
	err = checkPDBRGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "download bandwidth price") {
		t.Fatalf("expected download bandwidth price gouging error, instead error was '%v'", err)
	}

	// verify max upload bandwidth price gouging
	pt = newDefaultPriceTable()
	pt.UploadBandwidthCost = allowance.MaxUploadBandwidthPrice.Add64(1)
	err = checkPDBRGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "upload bandwidth price") {
		t.Fatalf("expected upload bandwidth price gouging error, instead error was '%v'", err)
	}

	// update the expected download to be non zero and verify the default prices
	allowance.ExpectedDownload = 1 << 30 // 1GiB
	pt = newDefaultPriceTable()
	err = checkPDBRGouging(pt, allowance)
	if err != nil {
		t.Fatal("unexpected price gouging failure", err)
	}

	// verify gouging of MDM related costs, in order to verify if gouging
	// detection kicks in we need to ensure the cost of executing enough PDBRs
	// to fulfil the expected download exceeds the allowance

	// we do this by maxing out the upload and bandwidth costs and setting all
	// default cost components to 250 pS

	pt.UploadBandwidthCost = allowance.MaxUploadBandwidthPrice
	pt.DownloadBandwidthCost = allowance.MaxDownloadBandwidthPrice
	pS := types.SiacoinPrecision.MulFloat(1e-12)
	pt.InitBaseCost = pt.InitBaseCost.Add(pS.Mul64(250))
	pt.ReadBaseCost = pt.ReadBaseCost.Add(pS.Mul64(250))
	pt.MemoryTimeCost = pt.MemoryTimeCost.Add(pS.Mul64(250))
	err = checkPDBRGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}

	// verify these checks are ignored if the funds are 0
	allowance.Funds = types.ZeroCurrency
	err = checkPDBRGouging(pt, allowance)
	if err != nil {
		t.Fatal("unexpected price gouging failure", err)
	}

	allowance.Funds = types.SiacoinPrecision.Mul64(1e3) // reset

	// verify bumping every individual cost component to an insane value results
	// in a price gouging error
	pt = newDefaultPriceTable()
	pt.InitBaseCost = types.SiacoinPrecision.Mul64(100)
	err = checkPDBRGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}

	pt = newDefaultPriceTable()
	pt.ReadBaseCost = types.SiacoinPrecision
	err = checkPDBRGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}

	pt = newDefaultPriceTable()
	pt.ReadLengthCost = types.SiacoinPrecision
	err = checkPDBRGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}

	pt = newDefaultPriceTable()
	pt.MemoryTimeCost = types.SiacoinPrecision
	err = checkPDBRGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}
}
