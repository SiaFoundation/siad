package renter

import (
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestUpdatePriceTableGouging checks that the price table gouging is correctly
// detecting price gouging from a host.
func TestUpdatePriceTableGouging(t *testing.T) {
	t.Parallel()

	// allowance contains only the fields necessary to test the price gouging
	allowance := modules.Allowance{
		Funds:  types.SiacoinPrecision,
		Period: types.BlockHeight(6),
	}

	// verify happy case
	pt := newDefaultPriceTable()
	err := checkUpdatePriceTableGouging(pt, allowance)
	if err != nil {
		t.Fatal("unexpected price gouging failure")
	}

	// verify gouging case, first calculate how many times we need to update the
	// PT over the duration of the allowance period
	pt = newDefaultPriceTable()
	durationInS := int64(pt.Validity.Seconds())
	periodInS := int64(allowance.Period) * 10 * 60 // period times 10m blocks
	numUpdates := periodInS / durationInS

	// increase the update price table cost so that the total cost of updating
	// it for the entire allowance period exceeds the allowed percentage of the
	// total allowance.
	pt.UpdatePriceTableCost = allowance.Funds.MulFloat(updatePriceTableGougingPercentageThreshold * 2).Div64(uint64(numUpdates))
	err = checkUpdatePriceTableGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "update price table cost") {
		t.Fatalf("expected update price table cost gouging error, instead error was '%v'", err)
	}

	// verify unacceptable validity case
	pt = newDefaultPriceTable()
	pt.Validity = 0
	err = checkUpdatePriceTableGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "update price table validity") {
		t.Fatalf("expected update price table validity gouging error, instead error was '%v'", err)
	}
	pt.Validity = minAcceptedPriceTableValidity
	err = checkUpdatePriceTableGouging(pt, allowance)
	if err != nil {
		t.Fatalf("unexpected update price table validity gouging error: %v", err)
	}
}

// newDefaultPriceTable is a helper function that returns a price table with
// default prices for all fields
func newDefaultPriceTable() modules.RPCPriceTable {
	hes := modules.DefaultHostExternalSettings()
	oneCurrency := types.NewCurrency64(1)
	return modules.RPCPriceTable{
		Validity:             time.Minute,
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
