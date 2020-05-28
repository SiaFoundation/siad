package renter

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux/mux"
)

var (
	// updateTimeInterval defines the amount of time after which we'll update
	// the host's prices. This is a temporary variable and will be replaced when
	// we add a duration to the host's price table. For now it's just half of
	// the rpcPriceGuaranteePeriod set on the host
	//
	// TODO: Need to switch to setting the price table update based on the host
	// timeout instead.
	updateTimeInterval = build.Select(build.Var{
		Standard: 5 * time.Minute,
		Dev:      3 * time.Minute,
		Testing:  7 * time.Second,
	}).(time.Duration)

	// errPriceTableGouging is returned when price gouging is detected
	errPriceTableGouging = errors.New("price table rejected due to price gouging")
)

type (
	// workerPriceTable contains a price table and some information related to
	// retrieving the next update.
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

		// staticRecentErr specifies the most recent error that the worker's
		// price table update has failed with.
		staticRecentErr error
	}
)

// staticNeedsPriceTableUpdate is a helper function that determines whether the
// price table should be updated.
func (w *worker) staticNeedsPriceTableUpdate() bool {
	// Check the version.
	if build.VersionCmp(w.staticCache().staticHostVersion, minAsyncVersion) < 0 {
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
	// Sanity check - This function runs on a fairly strict schedule, the
	// control loop should not have called this function unless the price table
	// is after its updateTime.
	updateTime := w.staticPriceTable().staticUpdateTime
	if time.Now().Before(updateTime) {
		w.renter.log.Critical("price table is being updated prematurely")
	}
	// Sanity check - only one price table update should be running at a time.
	// If multiple are running at a time, there can be a race condition around
	// 'staticConsecutiveFailures'.
	if !atomic.CompareAndSwapUint64(&w.atomicPriceTableUpdateRunning, 0, 1) {
		w.renter.log.Critical("price table is being updated in two threads concurrently")
	}
	defer atomic.StoreUint64(&w.atomicPriceTableUpdateRunning, 0)

	// Create a goroutine to wake the worker when the time has come to check the
	// price table again. Make sure to grab the update time inside of the defer
	// func, after the price table has been updated.
	//
	// This defer needs to run after the defer which updates the price table.
	defer func() {
		updateTime := w.staticPriceTable().staticUpdateTime
		w.renter.tg.AfterFunc(updateTime.Sub(time.Now()), func() {
			w.staticWake()
		})
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

	// check for gouging before paying
	err = checkPriceTableGouging(pt, stream.Limit(), w.staticCache().staticRenterAllowance)
	if err != nil {
		err = errors.Compose(err, errors.AddContext(errPriceTableGouging, fmt.Sprintf("host %v", w.staticHostPubKeyStr)))
		w.renter.log.Println("ERROR: ", err)
		return
	}

	// provide payment
	err = w.renter.hostContractor.ProvidePayment(stream, w.staticHostPubKey, modules.RPCUpdatePriceTable, pt.UpdatePriceTableCost, w.staticAccount.staticID, w.staticCache().staticBlockHeight)
	if err != nil {
		return
	}

	// The price table will not become valid until the host has received and
	// confirmed our payment. The only way for us to know that the payment has
	// been confirmed is to do a read, and we expect that the host has closed
	// the stream.
	//
	// TODO: Since this part is necessary for synchrony reasons, we should make
	// it an explicit part of the protocol and have the host send an actual
	// response.
	expectedReadErr := modules.RPCRead(stream, struct{}{})
	if expectedReadErr == nil || !strings.Contains(expectedReadErr.Error(), io.ErrClosedPipe.Error()) {
		w.renter.log.Println("ERROR: expected io.ErrClosedPipe, instead received err:", expectedReadErr)
	}

	// Update the price table. We preserve the recent error even though there
	// has not been an error for debugging purposes, if there has been an error
	// previously the devs like to be able to see what it was.
	wpt := &workerPriceTable{
		staticPriceTable:          pt,
		staticUpdateTime:          time.Now().Add(updateTimeInterval),
		staticConsecutiveFailures: 0,
		staticRecentErr:           currentPT.staticRecentErr,
	}
	w.staticSetPriceTable(wpt)
}

// checkPriceTableGouging looks at the proposed price table by the host and
// determines whether or not his prices are reasonable. If the renter decides
// its prices are unreasonable, it will reject the price table and this worker
// will be put into cooldown until prices come down to a reasonable level,
// effectively disabling the worker.
func checkPriceTableGouging(pt modules.RPCPriceTable, limit mux.BandwidthLimit, allowance modules.Allowance) error {
	// Check whether the update price table cost is too high
	dlbwc := pt.DownloadBandwidthCost.Mul64(limit.Downloaded())
	ulbwc := pt.UploadBandwidthCost.Mul64(limit.Uploaded())
	if pt.UpdatePriceTableCost.Cmp(dlbwc.Add(ulbwc).Mul64(2)) > 0 {
		return fmt.Errorf("update price table cost %v is considered too high, it is more than twice the cost of bandwidth %v", pt.UpdatePriceTableCost, dlbwc.Add(ulbwc))
	}

	// Check whether the fund account cost is too high
	if pt.FundAccountCost.Cmp(dlbwc.Add(ulbwc).Mul64(10)) > 0 {
		return fmt.Errorf("fund account cost %v is considered too high, it is more than ten times the cost of bandwidth %v", pt.FundAccountCost, dlbwc.Add(ulbwc))
	}

	// Check whether the download bandwidth price is too high.
	if !allowance.MaxDownloadBandwidthPrice.IsZero() && allowance.MaxDownloadBandwidthPrice.Cmp(pt.DownloadBandwidthCost) < 0 {
		return fmt.Errorf("download bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v", pt.DownloadBandwidthCost, allowance.MaxDownloadBandwidthPrice)
	}

	// Check whether the upload bandwidth price is too high.
	if !allowance.MaxUploadBandwidthPrice.IsZero() && allowance.MaxUploadBandwidthPrice.Cmp(pt.UploadBandwidthCost) < 0 {
		return fmt.Errorf("upload bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v", pt.UploadBandwidthCost, allowance.MaxUploadBandwidthPrice)
	}

	// If there is no allowance, general price gouging checks have to be
	// disabled, because there is no baseline for understanding what might count
	// as price gouging.
	if allowance.Funds.IsZero() {
		return nil
	}

	// Check that the prices in the price table make sense in the context of the
	// renter's overall allowance. We do this by calculating the cost of
	// performing the same action repeatedly until a fraction of the desired
	// total resource consumption established by the allowance has been reached.

	// Expected Download Costs

	// we start by calculating the minimum amount of read sector jobs necessary
	// to download the expected download amount
	minNumJobs := allowance.ExpectedDownload / modules.SectorSize

	// calculate the expected cost of a single job
	pb := modules.NewProgramBuilder(&pt)
	pb.AddReadSectorInstruction(modules.SectorSize, 0, crypto.Hash{}, true)
	cost, _, _ := pb.Cost(true)

	jrs := new(jobReadSector)
	jrs.staticLength = modules.SectorSize
	ulbw, dlbw := jrs.callExpectedBandwidth()
	bwc := modules.MDMBandwidthCost(pt, ulbw, dlbw)
	costPerJob := cost.Add(bwc)

	totalCost := costPerJob.Mul64(minNumJobs)
	reducedCost := totalCost.Div64(downloadGougingFractionDenom)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		errStr := fmt.Sprintf("combined read sector of host yields %v, which is more than the renter is willing to pay for downloads: %v - price gouging protection enabled", reducedCost, allowance.Funds)
		return errors.New(errStr)
	}

	if allowance.PaymentContractInitialFunding.IsZero() {
		return nil
	}

	// When PaymentContractInitialFunding is higher than zero, we are dealing
	// with a Skynet portal and have to gouge sector lookups prices.

	// we start by estimating the amount of has sector jobs, we do this in a
	// similar way as we did with the expected download cost, however now we use
	// segment sizes. This is completely arbitrary but tries to reflect the
	// amount of lookups vs the amount of downloads.
	minNumJobs = allowance.ExpectedDownload / crypto.SegmentSize

	// calculate the expected cost of a single job
	pb = modules.NewProgramBuilder(&pt)
	pb.AddHasSectorInstruction(crypto.Hash{})
	cost, _, _ = pb.Cost(true)

	jhs := new(jobHasSector)
	ulbw, dlbw = jhs.callExpectedBandwidth()
	bwc = modules.MDMBandwidthCost(pt, ulbw, dlbw)
	costPerJob = cost.Add(bwc)

	totalCost = costPerJob.Mul64(minNumJobs)
	reducedCost = totalCost.Div64(downloadGougingFractionDenom)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		errStr := fmt.Sprintf("combined has sector pricing of host yields %v, which is more than the renter is willing to pay for sector lookups: %v - price gouging protection enabled", reducedCost, allowance.Funds)
		return errors.New(errStr)
	}

	return nil
}
