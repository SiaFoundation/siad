package renter

import (
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// updateTimeInterval defines the amount of time after which we'll update the
// host's prices. This is a temporary variable and will be replaced when we add
// a duration to the host's price table. For now it's just half of the
// rpcPriceGuaranteePeriod set on the host
var updateTimeInterval = build.Select(build.Var{
	Standard: 5 * time.Minute,
	Dev:      3 * time.Minute,
	Testing:  7 * time.Second,
}).(time.Duration)

type (
	// workerPriceTable contains a price table and some information related to
	// retrieveing the next update.
	workerPriceTable struct {
		// The actual price table.
		staticPriceTable modules.RPCPriceTable

		// The next time that the worker should try to update the price table.
		staticUpdateTime time.Time

		// The number of consecutive failures that the worker has experienced in
		// trying to fetch the price table. This number is used to inform
		// staticUpdateTime, a larger number of consecutive failures will result in
		// greater backoff on fetching the price table.
		staticConsecutiveFailures uint64

		// staticRecentErr specifies the most recent error that the price table
		// update has failed with.
		staticRecentErr error
	}
)

// staticNeedsPriceTableUpdate is a helper function that determines whether the
// price table should be updated.
func (w *worker) staticNeedsPriceTableUpdate() bool {
	// Check the version.
	//
	// TODO: '<'
	if build.VersionCmp(w.staticCache().staticHostVersion, minAsyncVersion) != 0 {
		return false
	}
	return time.Now().After(w.staticPriceTable().staticUpdateTime)
}

// newPriceTable will initialize a price table for the worker.
func (w *worker) newPriceTable() {
	if w.staticPriceTable() != nil {
		w.renter.log.Critical("creating a new price table when a new price table already exists")
	}
	w.staticSetPriceTable(new(workerPriceTable))
}

// staticPriceTable will return the most recent price table for the worker's
// host.
func (w *worker) staticPriceTable() *workerPriceTable {
	ptr := atomic.LoadPointer(&w.atomicPriceTable)
	return (*workerPriceTable)(ptr)
}

// staticSetPriceTable will set the price table in the worker to be equal to the
// provided price table.
func (w *worker) staticSetPriceTable(pt *workerPriceTable) {
	atomic.StorePointer(&w.atomicPriceTable, unsafe.Pointer(pt))
}

// staticValid will return true if the latest price table that we have is still
// valid for the host.
//
// The price table is default invalid, because the zero time / empty time is
// before the current time, and the price table expiry defaults to the zero
// time.
func (wpt *workerPriceTable) staticValid() bool {
	return wpt.staticPriceTable.Expiry > time.Now().Unix()
}

// managedUpdatePriceTable performs the UpdatePriceTableRPC on the host.
func (w *worker) staticUpdatePriceTable() {
	// This function should not be running if the price table is not out of date
	// yet.
	updateTime := w.staticPriceTable().staticUpdateTime
	currentTime := time.Now()
	if currentTime.Before(updateTime) {
		build.Critical("price table is being updated prematurely")
	}

	// Create a goroutine to wake the worker when the time has come to check the
	// price table again. Be careful when handling potential underflows.
	defer func() {
		go func() {
			updateTime := w.staticPriceTable().staticUpdateTime
			currentTime := time.Now()
			time.Sleep(updateTime.Sub(currentTime))
			w.staticWake()
		}()
	}()

	// All remaining errors represent short term issues with the host, so the
	// price table should be updated to represent the failure, but should retain
	// the existing price table, which will allow the renter to continue
	// performing tasks even though it's having trouble getting a new price
	// table.
	var err error
	currentPT := w.staticPriceTable()
	defer func() {
		if err != nil {
			// Because of race conditions, can't modify the existing price
			// table, need to make a new one.
			pt := &workerPriceTable{
				staticPriceTable:          currentPT.staticPriceTable,
				staticUpdateTime:          cooldownUntil(currentPT.staticConsecutiveFailures),
				staticConsecutiveFailures: currentPT.staticConsecutiveFailures + 1,
				staticRecentErr:           err,
			}
			w.staticSetPriceTable(pt)
		}
	}()

	// Get a stream.
	stream, err := w.staticNewStream()
	if err != nil {
		return
	}
	defer func() {
		// An error closing the stream is not sufficient reason to reject the
		// price table that the host gave us. Because there is a defer checking
		// for the value of 'err', we use a different variable name here.
		streamCloseErr := stream.Close()
		if streamCloseErr != nil {
			w.renter.log.Println("ERROR: failed to close stream", streamCloseErr)
		}
	}()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
	if err != nil {
		return
	}

	// receive the price table
	var uptr modules.RPCUpdatePriceTableResponse
	err = modules.RPCRead(stream, &uptr)
	if err != nil {
		return
	}

	// decode the JSON
	var pt modules.RPCPriceTable
	err = json.Unmarshal(uptr.PriceTableJSON, &pt)
	if err != nil {
		return
	}

	// TODO: Check for gouging before paying.

	// provide payment
	err = w.renter.hostContractor.ProvidePayment(stream, w.staticHostPubKey, modules.RPCUpdatePriceTable, pt.UpdatePriceTableCost, w.staticAccount.staticID, w.staticCache().staticBlockHeight)
	if err != nil {
		return
	}

	// expect stream to be closed (the host only sees a PT as valid if it
	// successfully managed to process payment, not awaiting the close allows
	// for a race condition where we consider it valid but the host does not
	//
	// We use a different error name here because this error shouldn't
	// invalidate the price table.
	//
	// TODO: Why is this here?
	bogusReadErr := modules.RPCRead(stream, struct{}{})
	if bogusReadErr == nil || !strings.Contains(bogusReadErr.Error(), io.ErrClosedPipe.Error()) {
		w.renter.log.Println("ERROR: expected io.ErrClosedPipe, instead received err:", bogusReadErr)
	}

	// Update the price table.
	//
	// TODO: Do something smarter for the update time than 5 minutes.
	wpt := &workerPriceTable{
		staticPriceTable:          pt,
		staticUpdateTime:          time.Now().Add(updateTimeInterval),
		staticConsecutiveFailures: 0,
		staticRecentErr:           currentPT.staticRecentErr,
	}
	w.staticSetPriceTable(wpt)
}
