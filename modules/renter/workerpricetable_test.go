package renter

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/errors"
)

// TestUpdatePriceTableHostHeightLeeway verifies the worker will verify the
// HostHeight when updating the price table and will not accept a height that is
// significantly lower than our blockheight.
func TestUpdatePriceTableHostHeightLeeway(t *testing.T) {
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

	// wait until we have a valid pricetable
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if !w.staticPriceTable().staticValid() {
			return errors.New("price table not updated yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.staticPriceTable().staticRecentErr != nil {
		t.Fatal("Expected recent err to be nil")
	}

	// get the host's blockheight
	hbh := w.staticPriceTable().staticPriceTable.HostBlockHeight

	// corrupt the synced property on the worker's cache
	wc := w.staticCache()
	ptr := unsafe.Pointer(&workerCache{
		staticBlockHeight:     hbh + priceTableHostBlockHeightLeeWay + 1,
		staticContractID:      wc.staticContractID,
		staticContractUtility: wc.staticContractUtility,
		staticHostVersion:     wc.staticHostVersion,
		staticSynced:          wc.staticSynced,
		staticLastUpdate:      wc.staticLastUpdate,
	})
	atomic.StorePointer(&w.atomicCache, ptr)

	// corrupt the price table's update time so we are allowed to update
	wpt := w.staticPriceTable()
	wptc := new(workerPriceTable)
	wptc.staticConsecutiveFailures = wpt.staticConsecutiveFailures
	wptc.staticExpiryTime = wpt.staticExpiryTime
	wptc.staticUpdateTime = time.Now().Add(-time.Second)
	wptc.staticPriceTable = wpt.staticPriceTable
	w.staticSetPriceTable(wptc)

	// update the price table, verify the update errored out and rejected the
	// host's price table due to an invalid blockheight
	w.staticUpdatePriceTable()
	err = w.staticPriceTable().staticRecentErr
	if err == nil || !strings.Contains(err.Error(), "host blockheight is considered too far off our own blockheight") {
		t.Fatal("Expected price table to be rejected due to invalid host block height")
	}
}
