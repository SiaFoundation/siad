package renter

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
)

// TestUseHostBlockHeight verifies we use the host's blockheight if our worker
// cache indicates the renter is not synced.
func TestUseHostBlockHeight(t *testing.T) {
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

	// wait until the worker has a valid price table
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if !w.staticPriceTable().staticValid() {
			return errors.New("worker has no price table")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// manually corrupt the price table's host blockheight
	wpt := w.staticPriceTable()
	var pt modules.RPCPriceTable
	err = encoding.Unmarshal(encoding.Marshal(wpt.staticPriceTable), &pt)
	if err != nil {
		t.Fatal(err)
	}
	pt.HostBlockHeight += 1e3

	wptc := new(workerPriceTable)
	wptc.staticConsecutiveFailures = wpt.staticConsecutiveFailures
	wptc.staticExpiryTime = wpt.staticExpiryTime
	wptc.staticUpdateTime = wpt.staticUpdateTime
	wptc.staticPriceTable = pt
	w.staticSetPriceTable(wptc)

	// manually corrupt the synced property on the worker's cache
	cache := w.staticCache()
	cache.staticSynced = false
	ptr := unsafe.Pointer(cache)
	atomic.StorePointer(&w.atomicCache, ptr)

	// create a dummy has sector program
	pb := modules.NewProgramBuilder(&pt)
	pb.AddHasSectorInstruction(crypto.Hash{})
	p, data := pb.Program()
	cost, _, _ := pb.Cost(true)
	ulBandwidth, dlBandwidth := new(jobHasSector).callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// execute the program
	_, err = w.managedExecuteProgram(p, data, types.FileContractID{}, cost)
	if err == nil || !strings.Contains(err.Error(), "ephemeral account withdrawal message expires too far into the future") {
		t.Fatal("Unexpected error", err)
	}
}
