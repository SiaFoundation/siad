package host

import (
	"bytes"
	"container/heap"
	"encoding/json"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
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
	rpcPriceTableHeap []*hostRPCPriceTable

	// hostRPCPriceTable is a helper struct that wraps a price table alongside
	// its creation timestamp. We need this, in combination with the price
	// table's validity to figure out when to consider the price table to be
	// expired.
	hostRPCPriceTable struct {
		modules.RPCPriceTable
		creation time.Time
	}
)

// Expiry returns the time at which the price table is considered to be expired
func (hpt *hostRPCPriceTable) Expiry() time.Time {
	return hpt.creation.Add(hpt.Validity)
}

// PopExpired returns the UIDs for all rpc price tables that have expired
func (pth *priceTableHeap) PopExpired() (expired []modules.UniqueID) {
	pth.mu.Lock()
	defer pth.mu.Unlock()

	now := time.Now()
	for pth.heap.Len() > 0 {
		pt := heap.Pop(&pth.heap).(*hostRPCPriceTable)
		if now.Before(pt.Expiry()) {
			heap.Push(&pth.heap, pt)
			break
		}
		expired = append(expired, pt.UID)
	}
	return
}

// Push will add a price table to the heap.
func (pth *priceTableHeap) Push(pt *hostRPCPriceTable) {
	pth.mu.Lock()
	defer pth.mu.Unlock()
	heap.Push(&pth.heap, pt)
}

// Implementation of heap.Interface for rpcPriceTableHeap.
func (pth rpcPriceTableHeap) Len() int { return len(pth) }
func (pth rpcPriceTableHeap) Less(i, j int) bool {
	return pth[i].Expiry().Before(pth[j].Expiry())
}
func (pth rpcPriceTableHeap) Swap(i, j int) { pth[i], pth[j] = pth[j], pth[i] }
func (pth *rpcPriceTableHeap) Push(x interface{}) {
	pt := x.(*hostRPCPriceTable)
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

	// set the validity to signal how long these prices are guaranteed for
	pt.Validity = rpcPriceGuaranteePeriod

	// set the host's current blockheight, this allows the renter to create
	// valid withdrawal messages in case it is not synced yet
	pt.HostBlockHeight = h.BlockHeight()

	// json encode the price table
	ptBytes, err := json.Marshal(pt)
	if err != nil {
		return errors.AddContext(err, "Failed to JSON encode the price table")
	}

	// sanity check the price table has a UID
	if bytes.Equal(pt.UID[:], make([]byte, types.SpecifierLen)) {
		build.Critical("PriceTable does not have a UID set")
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

	// Check payment.
	if payment.Amount().Cmp(pt.UpdatePriceTableCost) < 0 {
		return modules.ErrInsufficientPaymentForRPC
	}

	// after payment has been received, track the price table in the host's list
	// of price tables and signal the renter we consider the price table valid
	h.staticPriceTables.managedTrack(&hostRPCPriceTable{pt, time.Now()})
	var tracked modules.RPCTrackedPriceTableResponse
	if err = modules.RPCWrite(stream, tracked); err != nil {
		return errors.AddContext(err, "Failed to signal renter we tracked the price table")
	}

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
	if time.Now().After(pt.Expiry()) {
		return nil, ErrPriceTableExpired
	}
	return &pt.RPCPriceTable, nil
}
