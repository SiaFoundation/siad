package host

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

var (
	// ErrPriceTableNotFound is returned when the price table for a certain UID
	// can not be found in the tracked price tables
	ErrPriceTableNotFound = errors.New("Price table not found")

	// ErrPriceTableExpired is returned when the specified price table has
	// expired
	ErrPriceTableExpired = errors.New("Price table requested is expired")
)

type (
	// priceTableHeap is a helper type that contains a min heap of rpc price
	// tables, sorted on their expiry. The heap is guarded by its own mutex and
	// allows for peeking at the min expiry.
	priceTableHeap struct {
		heap rpcPriceTableHeap
		mu   sync.Mutex
	}

	// rpcPriceTableHeap is a min heap of rpc price tables
	rpcPriceTableHeap []*modules.RPCPriceTable
)

// PopExpired returns the UIDs for all rpc price tables that have expired
func (pth *priceTableHeap) PopExpired() (expired []modules.UniqueID) {
	pth.mu.Lock()
	defer pth.mu.Unlock()

	now := time.Now()
	for {
		if pth.heap.Len() == 0 {
			return
		}

		pt := heap.Pop(&pth.heap).(*modules.RPCPriceTable)
		if now.Before(time.Unix(pt.Timestamp, 0).Add(pt.Expiry)) {
			heap.Push(&pth.heap, pt)
			break
		}
		expired = append(expired, pt.UID)
	}
	return
}

// Push will add a price table to the heap.
func (pth *priceTableHeap) Push(pt *modules.RPCPriceTable) {
	pth.mu.Lock()
	defer pth.mu.Unlock()
	heap.Push(&pth.heap, pt)
}

// Implementation of heap.Interface for rpcPriceTableHeap.
func (pth rpcPriceTableHeap) Len() int { return len(pth) }
func (pth rpcPriceTableHeap) Less(i, j int) bool {
	return time.Unix(pth[i].Timestamp, 0).Add(pth[i].Expiry).Before(time.Unix(pth[j].Timestamp, 0).Add(pth[j].Expiry))
}
func (pth rpcPriceTableHeap) Swap(i, j int) { pth[i], pth[j] = pth[j], pth[i] }
func (pth *rpcPriceTableHeap) Push(x interface{}) {
	pt := x.(*modules.RPCPriceTable)
	*pth = append(*pth, pt)
}
func (pth *rpcPriceTableHeap) Pop() interface{} {
	old := *pth
	n := len(old)
	pt := old[n-1]
	*pth = old[0 : n-1]
	return pt
}

// managedRPCUpdatePriceTable returns a copy of the host's current rpc price
// table. These prices are valid for the duration of the
// rpcPriceGuaranteePeriod, which is defined by the price table's Expiry
func (h *Host) managedRPCUpdatePriceTable(stream siamux.Stream) error {
	// copy the host's price table and give it a random UID
	pt := h.staticPriceTables.managedCurrent()
	fastrand.Read(pt.UID[:])

	// update the epxiry to signal how long these prices are guaranteed and set
	// the timestamp
	pt.Expiry = rpcPriceGuaranteePeriod
	pt.Timestamp = time.Now().Unix()

	// json encode the price table
	ptBytes, err := json.Marshal(pt)
	if err != nil {
		return errors.AddContext(err, "Failed to JSON encode the price table")
	}

	// send it to the renter
	uptResp := modules.RPCUpdatePriceTableResponse{PriceTableJSON: ptBytes}
	if err = modules.RPCWrite(stream, uptResp); err != nil {
		return errors.AddContext(err, "Failed to write response")
	}

	// Note that we have sent the price table before processing payment for this
	// RPC. This allows the renter to check for price gouging and close out the
	// stream if it does not agree with pricing. The price table has not yet
	// been added to the map, which means that the renter has to pay for it in
	// order for it to became active and accepted by the host.

	payment, err := h.ProcessPayment(stream)
	if err != nil {
		return errors.AddContext(err, "Failed to process payment")
	}
	if payment.Amount().Cmp(pt.UpdatePriceTableCost) < 0 {
		return modules.ErrInsufficientPaymentForRPC
	}
	// Don't expect any added collateral.
	if !payment.AddedCollateral().IsZero() {
		return fmt.Errorf("no collateral should be moved but got %v", payment.AddedCollateral().HumanString())
	}

	// after payment has been received, track the price table in the host's list
	// of price tables
	h.staticPriceTables.managedTrack(&pt)

	// refund the money we didn't use.
	refund := payment.Amount().Sub(pt.UpdatePriceTableCost)
	err = h.staticAccountManager.callRefund(payment.AccountID(), refund)
	if err != nil {
		return errors.AddContext(err, "failed to refund client")
	}
	return nil
}

// staticReadPriceTableID receives a stream and reads the price table's UID from
// it, if it's a known UID we return the price table
func (h *Host) staticReadPriceTableID(stream siamux.Stream) (*modules.RPCPriceTable, error) {
	// read the price table uid
	var uid modules.UniqueID
	err := modules.RPCRead(stream, &uid)
	if err != nil {
		return nil, errors.AddContext(err, "Failed to read price table UID")
	}

	// check if we know the uid, if we do return it
	var found bool
	pt, found := h.staticPriceTables.managedGet(uid)
	if !found {
		return nil, ErrPriceTableNotFound
	}

	// make sure the table isn't expired.
	if time.Now().After(time.Unix(pt.Timestamp, 0).Add(pt.Expiry)) {
		return nil, ErrPriceTableExpired
	}
	return pt, nil
}
