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
	h.mu.Lock()
	pt := h.priceTable
	h.mu.Unlock()

	// update the epxiry ensire prices are guaranteed for the
	// 'rpcPriceGuaranteePeriod'
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

	// Note: we have sent the price table before processing payment for this
	// RPC. This allows the renter to check for price gouging and close out the
	// stream if it does not agree with pricing. After this the host processes
	// payment, and the renter will pay for the RPC according to the price it
	// just received. This essentially means the host is optimistically sending
	// over the price table, which is ok.

	// TODO process payment

	// after payment has been received, track the price table in the host's list
	// of price tables
	h.mu.Lock()
	h.priceTableMap[pt.UUID] = &pt
	h.mu.Unlock()

	return nil
}
