package host

import (
	"container/heap"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

// TestPriceTableMarshaling tests a PriceTable can be marshaled and unmarshaled
func TestPriceTableMarshaling(t *testing.T) {
	pt := modules.RPCPriceTable{
		Expiry:               time.Now().Add(rpcPriceGuaranteePeriod).Unix(),
		UpdatePriceTableCost: types.SiacoinPrecision,
		InitBaseCost:         types.SiacoinPrecision.Mul64(1e2),
		MemoryTimeCost:       types.SiacoinPrecision.Mul64(1e3),
		ReadBaseCost:         types.SiacoinPrecision.Mul64(1e4),
		ReadLengthCost:       types.SiacoinPrecision.Mul64(1e5),
	}
	fastrand.Read(pt.UID[:])

	ptBytes, err := json.Marshal(pt)
	if err != nil {
		t.Fatal(err)
	}
	var ptCopy modules.RPCPriceTable
	err = json.Unmarshal(ptBytes, &ptCopy)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(pt, ptCopy) {
		t.Log(pt.UID[:])
		t.Log(ptCopy.UID[:])
		t.Fatal(errors.New("PriceTable not equal after unmarshaling"))
	}
}

// TestPriceTableMinHeap verifies the working of the min heap
func TestPriceTableMinHeap(t *testing.T) {
	t.Parallel()

	now := time.Now()
	pth := priceTableHeap{heap: make([]*modules.RPCPriceTable, 0)}

	// add 4 price tables (out of order) that expire somewhere in the future
	pt1 := modules.RPCPriceTable{Expiry: now.Add(9 * time.Minute).Unix()}
	pt2 := modules.RPCPriceTable{Expiry: now.Add(-3 * time.Minute).Unix()}
	pt3 := modules.RPCPriceTable{Expiry: now.Add(-6 * time.Minute).Unix()}
	pt4 := modules.RPCPriceTable{Expiry: now.Add(-1 * time.Minute).Unix()}
	pth.Push(&pt1)
	pth.Push(&pt2)
	pth.Push(&pt3)
	pth.Push(&pt4)

	// verify it considers 3 to be expired if we pass it a threshold 7' from now
	expired := pth.PopExpired()
	if len(expired) != 3 {
		t.Fatalf("Expected 3 price tables to be expired, yet managedExpired returned %d price tables", len(expired))
	}

	// verify 'pop' returns the last remaining price table
	pth.mu.Lock()
	expectedPt1 := heap.Pop(&pth.heap)
	pth.mu.Unlock()
	if expectedPt1 != &pt1 {
		t.Fatal("Expected the last price table to be equal to pt1, which is the price table with the highest expiry")
	}
}

// TestPruneExpiredPriceTables verifies the rpc price tables get pruned from the
// host's price table map if they have expired.
func TestPruneExpiredPriceTables(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// track a copy of the host's current price table
	pt := ht.host.staticPriceTables.managedCurrent()
	pt.Expiry = time.Now().Add(rpcPriceGuaranteePeriod).Unix()
	fastrand.Read(pt.UID[:])
	ht.host.staticPriceTables.managedTrack(&pt)

	// verify the price table is being tracked
	ht.host.staticPriceTables.mu.RLock()
	_, tracked := ht.host.staticPriceTables.guaranteed[pt.UID]
	ht.host.staticPriceTables.mu.RUnlock()
	if !tracked {
		t.Fatal("Expected the testing price table to be tracked but isn't")
	}

	// sleep for the duration of the epxiry frequency, seeing as that is greater
	// than the price guarantee period, it is the worst case
	err = build.Retry(3, pruneExpiredRPCPriceTableFrequency, func() error {
		ht.host.staticPriceTables.mu.RLock()
		_, exists := ht.host.staticPriceTables.guaranteed[pt.UID]
		ht.host.staticPriceTables.mu.RUnlock()
		if exists {
			return errors.New("Expected RPC price table to be pruned because it should have expired")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestUpdatePriceTableRPC tests the UpdatePriceTableRPC by manually calling the
// RPC handler.
func TestUpdatePriceTableRPC(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// setup a host and renter pair with an emulated file contract between them
	ht, pair, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	errMockRenterPriceGougingTooLow := errors.New("Cost too low")
	errMockRenterPriceGougingTooHigh := errors.New("Cost too high")

	// renter-side logic
	renterFunc := func(stream siamux.Stream) (pt modules.RPCPriceTable, err error) {
		defer stream.Close()
		var update modules.RPCUpdatePriceTableResponse
		if err = modules.RPCRead(stream, &update); err != nil {
			err = errors.AddContext(err, "Failed to read updated price table from the stream")
			return
		}

		if err = json.Unmarshal(update.PriceTableJSON, &pt); err != nil {
			err = errors.AddContext(err, "Failed to unmarshal the JSON encoded RPC price table")
			return
		}
		ptc := pt.UpdatePriceTableCost

		// mock of what is going to be price gouging on the renter
		if ptc.Equals(types.ZeroCurrency) {
			err = errMockRenterPriceGougingTooLow
			return
		} else if ptc.Cmp(types.SiacoinPrecision) > 0 {
			err = errMockRenterPriceGougingTooHigh
			return
		}

		// prepare an updated revision that pays the host
		rev, sig, err := pair.paymentRevision(ptc)
		if err != nil {
			return
		}

		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		pbcRequest := newPayByContractRequest(rev, sig, "someaccount")
		err = modules.RPCWriteAll(stream, pRequest, pbcRequest)
		if err != nil {
			return
		}

		// receive PayByContractResponse
		var payByResponse modules.PayByContractResponse
		err = modules.RPCRead(stream, &payByResponse)
		if err != nil {
			return
		}
		return
	}

	// host-side logic
	hostFunc := func(stream siamux.Stream, mock modules.RPCPriceTable) error {
		defer stream.Close()
		ht.host.staticPriceTables.managedUpdate(mock)
		err := ht.host.staticRPCUpdatePriceTable(stream)
		if err != nil {
			modules.RPCWriteError(stream, err)
		}
		return nil
	}

	runRPC := func(mockedHostPriceTable modules.RPCPriceTable) (pt modules.RPCPriceTable, rErr, hErr error) {
		rStream, hStream := NewTestStreams()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			pt, rErr = renterFunc(rStream)
			wg.Done()
		}()
		wg.Add(1)
		go func() {
			hErr = hostFunc(hStream, mockedHostPriceTable)
			wg.Done()
		}()
		wg.Wait()
		return
	}

	// verify happy flow
	current := ht.host.staticPriceTables.managedCurrent()
	fastrand.Read(current.UID[:]) // overwrite to avoid critical during prune
	update, rErr, hErr := runRPC(current)
	if err := errors.Compose(rErr, hErr); err != nil {
		t.Fatal("Update price table failed")
	}

	// verify the price table is tracked
	ht.host.staticPriceTables.mu.Lock()
	_, tracked := ht.host.staticPriceTables.guaranteed[update.UID]
	ht.host.staticPriceTables.mu.Unlock()
	if !tracked {
		t.Fatalf("Expected price table with.UID %v to be tracked after successful update", update.UID[:])
	}

	// expect error if the rpc costs nothing
	invalidPT := current
	fastrand.Read(invalidPT.UID[:]) // overwrite to avoid critical during prune
	invalidPT.UpdatePriceTableCost = types.ZeroCurrency
	_, rErr, hErr = runRPC(invalidPT)
	if rErr != errMockRenterPriceGougingTooLow {
		t.Fatalf("Expected err '%v', received '%v'", errMockRenterPriceGougingTooLow, err)
	}
	if hErr != nil {
		t.Fatalf("Expected no err, received '%v'", hErr)
	}

	// expect error if the rpc costs an insane amount
	invalidPT.UpdatePriceTableCost = types.SiacoinPrecision.Add64(1)
	_, rErr, hErr = runRPC(invalidPT)
	if rErr != errMockRenterPriceGougingTooHigh {
		t.Fatalf("Expected err '%v', received '%v'", errMockRenterPriceGougingTooHigh, err)
	}
	if hErr != nil {
		t.Fatalf("Expected no err, received '%v'", hErr)
	}
}
