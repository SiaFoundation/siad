package renter

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// testLimit is a test limit that returns hardcoded bandwidth values
type testLimit struct {
	uploaded   uint64
	downloaded uint64
}

func (tl *testLimit) Downloaded() uint64                { return tl.downloaded }
func (tl *testLimit) Uploaded() uint64                  { return tl.uploaded }
func (tl *testLimit) RecordDownload(bytes uint64) error { return nil }
func (tl *testLimit) RecordUpload(bytes uint64) error   { return nil }

// TestCheckPriceTableGouging checks that the price table gouging is correctly
// detecting price gouging from a host.
func TestCheckPriceTableGouging(t *testing.T) {
	t.Parallel()

	// allowance contains only the fields necessary to test the price gouging,
	// for the first couple of checks the default values suffice
	allowance := modules.Allowance{}

	// limit is a test limit object that returns hardcoded bandwidth values
	limit := new(testLimit)
	limit.uploaded = 1 << 15   // 32KiB
	limit.downloaded = 1 << 15 // 32KiB

	// happy case
	pt := newDefaultPriceTable()
	err := checkPriceTableGouging(pt, limit, allowance)
	if err != nil {
		t.Fatal("unexpected price gouging failure")
	}

	// increase the update price table cost so it's more than twice the cost of
	// bandwidth, should result in gouging error
	bwc := modules.MDMBandwidthCost(pt, limit.Uploaded(), limit.Downloaded())
	pt = newDefaultPriceTable()
	pt.UpdatePriceTableCost = bwc.Mul64(2).Add64(1)
	err = checkPriceTableGouging(pt, limit, allowance)
	if err == nil || !strings.Contains(err.Error(), "update price table cost") {
		t.Fatalf("expected update price table cost gouging error, instead error was '%v'", err)
	}

	// increase the fund account cost so it's more than ten times the cost of
	// bandwidth, should result in gouging error
	pt = newDefaultPriceTable()
	pt.FundAccountCost = bwc.Mul64(10).Add64(1)
	err = checkPriceTableGouging(pt, limit, allowance)
	if err == nil || !strings.Contains(err.Error(), "fund account cost") {
		t.Fatalf("expected fund account cost gouging error, instead error was '%v'", err)
	}

	// set allowance max download cost and exceed it in the price table
	allowanceMaxDL := allowance
	allowanceMaxDL.MaxDownloadBandwidthPrice = types.NewCurrency64(10)
	pt = newDefaultPriceTable()
	pt.DownloadBandwidthCost = allowanceMaxDL.MaxDownloadBandwidthPrice.Add64(1)
	err = checkPriceTableGouging(pt, limit, allowanceMaxDL)
	if err == nil || !strings.Contains(err.Error(), "download bandwidth price") {
		t.Fatalf("expected download bandwidth price gouging error, instead error was '%v'", err)
	}

	// set allowance max upload cost and exceed it in the price table
	allowanceMaxUL := allowance
	allowanceMaxUL.MaxUploadBandwidthPrice = types.NewCurrency64(10)
	pt = newDefaultPriceTable()
	pt.UploadBandwidthCost = allowanceMaxUL.MaxUploadBandwidthPrice.Add64(1)
	err = checkPriceTableGouging(pt, limit, allowanceMaxUL)
	if err == nil || !strings.Contains(err.Error(), "upload bandwidth price") {
		t.Fatalf("expected upload bandwidth price gouging error, instead error was '%v'", err)
	}

	// update the allowance to check the price table in the context of the
	// renter's allowance and funds
	allowance = modules.Allowance{
		ExpectedDownload: 1 << 30, // 1GiB
		Funds:            types.SiacoinPrecision,
	}

	// verify happy case
	pt = newDefaultPriceTable()
	err = checkPriceTableGouging(pt, limit, allowance)
	if err != nil {
		t.Fatal("unexpected gouging error", err)
	}

	// verify insane values for MDM init and base costs results into price
	// gouging
	pt = newDefaultPriceTable()
	pt.InitBaseCost = types.SiacoinPrecision.Div64(100)
	err = checkPriceTableGouging(pt, limit, allowance)
	if err == nil {
		t.Fatal("expected price gouging failure")
	}

	pt = newDefaultPriceTable()
	pt.MemoryTimeCost = types.SiacoinPrecision.Div64(100)
	err = checkPriceTableGouging(pt, limit, allowance)
	if err == nil {
		t.Fatal("expected price gouging failure")
	}

	pt = newDefaultPriceTable()
	pt.ReadBaseCost = types.SiacoinPrecision.Div64(100)
	err = checkPriceTableGouging(pt, limit, allowance)
	if err == nil {
		t.Fatal("expected price gouging failure")
	}

	pt = newDefaultPriceTable()
	pt.ReadLengthCost = types.SiacoinPrecision.Div64(100)
	err = checkPriceTableGouging(pt, limit, allowance)
	if err == nil {
		t.Fatal("expected price gouging failure")
	}

	// verify high download bandwidth cost results in price gouging error
	pt = newDefaultPriceTable()
	pt.DownloadBandwidthCost = modules.DefaultDownloadBandwidthPrice.Mul64(30)
	err = checkPriceTableGouging(pt, limit, allowance)
	if err == nil || !strings.Contains(err.Error(), "read sector pricing") {
		t.Fatalf("expected read sector price gouging error, instead error was '%v'", err)
	}

	// verify it passes if we use the default price table
	err = checkPriceTableGouging(newDefaultPriceTable(), limit, allowance)
	if err != nil {
		t.Fatalf("unexpected read sector price gouging error '%v'", err)
	}

	// check this error gets ignored if funds are 0
	allowance.Funds = types.ZeroCurrency
	err = checkPriceTableGouging(newDefaultPriceTable(), limit, allowance)
	if err != nil {
		t.Fatalf("unexpected price gouging error, err %v", err)
	}
	allowance.Funds = types.SiacoinPrecision

	// tweak the allowance to get passed the read sector price gouging, we have
	// to set PaymentContractInitialFunding to ensure the gouging function
	// checks for has sector price gouging
	allowance = modules.Allowance{
		ExpectedDownload:              1 << 28, // 256 MiB
		Funds:                         types.SiacoinPrecision,
		PaymentContractInitialFunding: types.SiacoinPrecision,
	}

	pt = newDefaultPriceTable()
	pt.DownloadBandwidthCost = modules.DefaultDownloadBandwidthPrice.Mul64(10)
	err = checkPriceTableGouging(pt, limit, allowance)
	if err == nil || !strings.Contains(err.Error(), "has sector pricing") {
		t.Fatalf("expected has sector price gouging error, instead error was '%v'", err)
	}

	// verify it passes if we use the default price table
	err = checkPriceTableGouging(newDefaultPriceTable(), limit, allowance)
	if err != nil {
		t.Fatalf("unexpected has sector price gouging error '%v'", err)
	}
}

// newDefaultPriceTable is a helper function that returns a price table with
// default prices for all fields
func newDefaultPriceTable() modules.RPCPriceTable {
	hes := modules.DefaultHostExternalSettings()
	oneCurrency := types.NewCurrency64(1)
	return modules.RPCPriceTable{
		FundAccountCost:      oneCurrency,
		UpdatePriceTableCost: oneCurrency,

		HasSectorBaseCost: oneCurrency,
		InitBaseCost:      oneCurrency,
		MemoryTimeCost:    oneCurrency,
		ReadBaseCost:      oneCurrency,
		ReadLengthCost:    oneCurrency,

		DownloadBandwidthCost: hes.DownloadBandwidthPrice,
		UploadBandwidthCost:   hes.UploadBandwidthPrice,
	}
}
