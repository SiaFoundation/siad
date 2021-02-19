package renter

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// TestUpdatePriceTableHostHeightLeeway verifies the worker will verify the
// HostHeight when updating the price table and will not accept a height that is
// significantly lower than our blockheight.
func TestUpdatePriceTableHostHeightLeeway(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a new worker tester
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// verify recent err is nil
	if w.staticPriceTable().staticRecentErr != nil {
		t.Fatal("unexpected")
	}

	// get the host's blockheight
	hbh := w.staticPriceTable().staticPriceTable.HostBlockHeight

	// corrupt the synced property on the worker's cache
	wc := w.staticCache()
	ptr := unsafe.Pointer(&workerCache{
		staticBlockHeight:     hbh + 2*priceTableHostBlockHeightLeeWay,
		staticContractID:      wc.staticContractID,
		staticContractUtility: wc.staticContractUtility,
		staticHostMuxAddress:  wc.staticHostMuxAddress,
		staticHostVersion:     wc.staticHostVersion,
		staticRenterAllowance: wc.staticRenterAllowance,
		staticSynced:          wc.staticSynced,
		staticLastUpdate:      wc.staticLastUpdate,
	})
	atomic.StorePointer(&w.atomicCache, ptr)

	// corrupt the price table's update time so we are allowed to update
	wpt := w.staticPriceTable()
	wptc := new(workerPriceTable)
	wptc.staticExpiryTime = wpt.staticExpiryTime
	wptc.staticUpdateTime = time.Now().Add(-time.Second)
	wptc.staticPriceTable = wpt.staticPriceTable
	w.staticSetPriceTable(wptc)

	// verify the update errored out and rejected the host's price table due to
	// an invalid blockheight
	err = build.Retry(600, 100*time.Millisecond, func() error {
		w.staticWake()
		err = w.staticPriceTable().staticRecentErr
		if !errors.Contains(err, errHostBlockHeightNotWithinTolerance) {
			return errors.New("pricetable was not rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

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

// TestHostBlockHeightWithinTolerance is a unit test that covers the logic
// contained within the hostBlockHeightWithinTolerance helper.
func TestHostBlockHeightWithinTolerance(t *testing.T) {
	t.Parallel()

	l := priceTableHostBlockHeightLeeWay
	inputs := []struct {
		RenterSynced      bool
		RenterBlockHeight types.BlockHeight
		HostBlockHeight   types.BlockHeight
		ExpectedOutcome   bool
	}{
		{true, l - 1, 0, true},  // renter synced and hbh lower (verify underflow)
		{true, l, 0, true},      // renter synced and hbh lower
		{true, l + 1, 0, false}, // renter synced and hbh too low
		{false, l, 0, false},    // renter not synced and hbh lower
		{true, 1, 1 + l, true},  // renter synced and hbh higher
		{true, l + 1, 1, true},  // renter synced and hbh lower
		{true, 0, l + 1, false}, // renter synced and hbh too high
		{true, l + 2, 1, false}, // renter synced and hbh too low
		{false, 5, 4, false},    // renter not synced and hbh too low
		{false, 5, 5, true},     // renter not synced and hbh equal
		{false, 5, 6, true},     // renter not synced and hbh higher
	}

	for _, input := range inputs {
		if hostBlockHeightWithinTolerance(input.RenterSynced, input.RenterBlockHeight, input.HostBlockHeight) != input.ExpectedOutcome {
			t.Fatal("unexpected outcome", input)
		}
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
		SwapSectorCost:    oneCurrency,

		DownloadBandwidthCost: hes.DownloadBandwidthPrice,
		UploadBandwidthCost:   hes.UploadBandwidthPrice,
	}
}
