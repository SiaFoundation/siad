package host

import (
	"encoding/json"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// managedRPCUpdatePriceTable returns a copy of the host's current rpc price
// table. These prices are valid for the duration of the
// rpcPriceGuaranteePeriod, which is defined by the price table's Expiry
func (h *Host) managedRPCUpdatePriceTable(stream siamux.Stream) error {
	// copy the host's price table
	h.staticPriceTables.mu.RLock()
	pt := h.staticPriceTables.current
	h.staticPriceTables.mu.RUnlock()

	// update the epxiry to ensure prices are guaranteed for the duration of the
	// rpcPriceGuaranteePeriod
	pt.Expiry = time.Now().Add(rpcPriceGuaranteePeriod).Unix()

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

	// TODO process payment

	// after payment has been received, track the price table in the host's list
	// of price tables
	h.staticPriceTables.mu.Lock()
	h.staticPriceTables.guaranteed[pt.UUID] = &pt
	h.staticPriceTables.mu.Unlock()
	h.staticPriceTables.staticMinHeap.Push(&pt)

	return nil
}
