package renter

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
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

	// prevent cache updates.
	atomic.StoreUint64(&w.atomicCacheUpdating, 1)

	// verify recent err is nil
	if w.staticPriceTable().staticRecentErr != nil {
		t.Fatal("unexpected")
	}

	// get the host's blockheight
	hbh := w.staticPriceTable().staticPriceTable.HostBlockHeight

	// sleep until the cache is supposed to be updated again to flush any
	// goroutines which snuck in after setting the cache update to "in
	// progress".
	wc := w.staticCache()
	time.Sleep(time.Until(wc.staticLastUpdate.Add(2 * workerCacheUpdateFrequency)))

	// corrupt the synced property on the worker's cache
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

// TestSchedulePriceTableUpdate verifies whether scheduling a price table update
// on the worker effectively executes a price table update immediately after it
// being scheduled.
func TestSchedulePriceTableUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a dependency that makes the host refuse a price table
	deps := dependencies.NewDependencyHostLosePriceTable()
	deps.Disable()

	// create a new worker tester
	wt, err := newWorkerTesterCustomDependency(t.Name(), modules.ProdDependencies, deps)
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

	// keep track of the current values
	pt := w.staticPriceTable()
	cUID := pt.staticPriceTable.UID
	cUpdTime := pt.staticUpdateTime

	// schedule an update
	w.staticTryForcePriceTableUpdate()

	// check whether the price table got updated in a retry, although it should
	// update on the very next iteration of the loop
	err = build.Retry(10, 50*time.Millisecond, func() error {
		if time.Now().After(cUpdTime) {
			t.Fatal("the price table updated according to the original schedule")
		}
		pt := w.staticPriceTable()
		if bytes.Equal(pt.staticPriceTable.UID[:], cUID[:]) {
			return errors.New("not updated yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// we have to manually update the pricetable here and reset the time of the
	// last forced update to null to ensure we're allowed to schedule two
	// updates in short succession
	update := *w.staticPriceTable()
	update.staticLastForcedUpdate = time.Time{}
	w.staticSetPriceTable(&update)

	// keep track of the current values
	pt = w.staticPriceTable()
	cUID = pt.staticPriceTable.UID
	cUpdTime = pt.staticUpdateTime

	// enable the dependency
	deps.Enable()

	// create a dummy program
	pb := modules.NewProgramBuilder(&pt.staticPriceTable, 0)
	pb.AddHasSectorInstruction(crypto.Hash{})
	p, data := pb.Program()
	cost, _, _ := pb.Cost(true)
	jhs := new(jobHasSector)
	jhs.staticSectors = []crypto.Hash{{1, 2, 3}}
	ulBandwidth, dlBandwidth := jhs.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt.staticPriceTable, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// execute it
	_, _, err = w.managedExecuteProgram(p, data, types.FileContractID{}, categoryDownload, cost)
	if !modules.IsPriceTableInvalidErr(err) {
		t.Fatal("unexpected")
	}

	// check whether the price table got updated in a retry, the error thrown
	// should have scheduled that
	err = build.Retry(10, 50*time.Millisecond, func() error {
		if time.Now().After(cUpdTime) {
			t.Fatal("the price table updated according to the original schedule")
		}
		pt := w.staticPriceTable()
		if bytes.Equal(pt.staticPriceTable.UID[:], cUID[:]) {
			return errors.New("not updated yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// now disable the dependency
	deps.Disable()

	// execute the same program
	_, _, err = w.managedExecuteProgram(p, data, types.FileContractID{}, categoryDownload, cost)
	if err != nil {
		t.Fatal("unexpected")
	}

	// keep track of the current values
	pt = w.staticPriceTable()
	cUID = pt.staticPriceTable.UID
	cUpdTime = pt.staticUpdateTime

	// check whether the price table got updated in a retry, which should
	// **not** have been the case because not enough time elapsed since the last
	// manually scheduled update
	err = build.Retry(10, 50*time.Millisecond, func() error {
		pt := w.staticPriceTable()
		if !bytes.Equal(pt.staticPriceTable.UID[:], cUID[:]) {
			return errors.New("should not have been scheduled to update")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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
